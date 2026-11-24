package main

// 流水线生命周期：启动 / 暂停 / 继续 / 人工打回，以及驱动租约。

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// 同一个任务同一时刻只允许一个 daemon 实例驱动。租约写在 artifacts 里，
// 这样另一个端口 / 另一个外壳打开同一份任务时能发现冲突，而不是两边同时派发。
const (
	leaseTTL   = 90 * time.Second
	leaseRenew = 25 * time.Second
)

func leasePath(root, slug string) string {
	return filepath.Join(root, "artifacts", slug, ".agentdeck", "lease.json")
}

// activeLease 返回仍然有效的租约；过期租约视为无人持有。
func activeLease(root, slug string) leaseDoc {
	raw, err := os.ReadFile(leasePath(root, slug))
	if err != nil {
		return leaseDoc{}
	}
	var doc leaseDoc
	if json.Unmarshal(raw, &doc) != nil || doc.Owner == "" {
		return leaseDoc{}
	}
	exp, err := time.Parse(time.RFC3339, doc.ExpiresAt)
	if err != nil || time.Now().After(exp) {
		return leaseDoc{}
	}
	return doc
}

func (m *PipelineManager) writeLease(run *pipelineRun) {
	host, _ := os.Hostname()
	now := time.Now()
	doc := leaseDoc{
		Owner:     m.instanceID,
		Host:      host,
		PID:       os.Getpid(),
		Slug:      run.slug,
		UpdatedAt: now.Format(time.RFC3339),
		ExpiresAt: now.Add(leaseTTL).Format(time.RFC3339),
	}
	raw, _ := json.MarshalIndent(doc, "", "  ")
	p := leasePath(run.root, run.slug)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err == nil {
		_ = os.WriteFile(p, raw, 0o644)
	}
	run.mu.Lock()
	run.leaseAt = now
	run.mu.Unlock()
}

// renewLease 定期续租；进程意外退出后租约会在 leaseTTL 后自动失效。
func (m *PipelineManager) renewLease(run *pipelineRun) {
	run.mu.Lock()
	recent := time.Since(run.leaseAt) < leaseRenew
	run.mu.Unlock()
	if recent {
		return
	}
	m.writeLease(run)
}

// releaseLease 只释放自己持有的租约，避免误删接管者的租约。
func (m *PipelineManager) releaseLease(run *pipelineRun) {
	p := leasePath(run.root, run.slug)
	raw, err := os.ReadFile(p)
	if err != nil {
		return
	}
	var doc leaseDoc
	if json.Unmarshal(raw, &doc) != nil || doc.Owner != m.instanceID {
		return
	}
	_ = os.Remove(p)
}

// Start 启动（或重启）一个 slug 的调度。force 用于明确接管 LoopForge 宿主驱动的任务。
func (m *PipelineManager) Start(root, slug, requirement string, force bool) (PipelineRunView, error) {
	if root == "" {
		return PipelineRunView{}, errors.New("缺少 root")
	}
	if slug == "" {
		var err error
		slug, err = createRunWorkspace(root, requirement)
		if err != nil {
			return PipelineRunView{}, err
		}
	}
	statePath := filepath.Join(root, "artifacts", slug, "workflow-state.json")
	if _, err := os.Stat(statePath); err != nil {
		return PipelineRunView{}, fmt.Errorf("找不到 %s", statePath)
	}
	// 同一个任务同一时刻只能有一个驱动者。LoopForge 宿主（runtime_mode=ide）
	// 自己在跑的时候，AgentDeck 不默默插手。
	if state, _, err := readState(root, slug); err == nil && !force {
		if state.RuntimeMode != "" && state.RuntimeMode != "agentdeck" {
			return PipelineRunView{}, fmt.Errorf(
				"该任务由 LoopForge 宿主（runtime_mode=%s）驱动；确认由 AgentDeck 接管后再强制启动", state.RuntimeMode)
		}
	}
	if !force {
		if lease := activeLease(root, slug); lease.Owner != "" && lease.Owner != m.instanceID {
			return PipelineRunView{}, fmt.Errorf(
				"该任务正被另一个 AgentDeck 实例驱动（%s，租约到 %s）；确认接管后再强制启动", lease.Host, lease.ExpiresAt)
		}
	}

	key := pipeKey(root, slug)
	m.mu.Lock()
	old := m.runs[key]
	run := &pipelineRun{
		root:       filepath.Clean(root),
		slug:       slug,
		status:     "running",
		sessions:   map[string]string{},
		attempts:   map[string]int{},
		dispatched: map[string]time.Time{},
		confirmed:  map[string]bool{},
		reworks:    map[string]int{},
		editions:   detectEditions(root),
		startedAt:  time.Now(),
		cancel:     make(chan struct{}),
	}
	m.runs[key] = run
	m.mu.Unlock()
	if old != nil {
		old.stop()
	}

	if requirement != "" {
		_ = os.WriteFile(filepath.Join(root, "artifacts", slug, "requirement.txt"), []byte(requirement), 0o644)
	}
	m.writeLease(run)

	go m.loop(run)
	return run.view(), nil
}

// Stop 停止调度，不关闭已经开着的终端。
func (m *PipelineManager) Stop(root, slug string) {
	m.mu.Lock()
	run, ok := m.runs[pipeKey(root, slug)]
	m.mu.Unlock()
	if !ok {
		return
	}
	run.set("stopped", "已停止")
	run.stop()
	m.releaseLease(run)
}

// stop 幂等地结束调度循环。
func (r *pipelineRun) stop() {
	r.mu.Lock()
	ch := r.cancel
	once := &r.cancelOnce
	r.mu.Unlock()
	once.Do(func() { close(ch) })
}

// resetCancel 给已停止的 run 换一个新取消通道，供 Resume 重启循环。
func (r *pipelineRun) resetCancel() {
	r.mu.Lock()
	r.cancel = make(chan struct{})
	r.cancelOnce = sync.Once{}
	r.mu.Unlock()
}

func (r *pipelineRun) cancelCh() chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cancel
}

// Resume 从暂停态继续驱动当前阶段。
func (m *PipelineManager) Resume(root, slug string) {
	m.mu.Lock()
	run := m.runs[pipeKey(root, slug)]
	m.mu.Unlock()
	if run == nil {
		return
	}
	if run.state() == "stopped" {
		run.resetCancel()
		go m.loop(run)
	}
	run.confirm(run.currentStage())
	run.set("running", "")
}

// confirm 记住用户已经确认过某个门禁阶段，下一次检查直接放行。
func (r *pipelineRun) confirm(stage string) {
	if stage == "" {
		return
	}
	r.mu.Lock()
	r.confirmed[stage] = true
	r.mu.Unlock()
}

func (r *pipelineRun) isConfirmed(stage string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.confirmed[stage]
}

// clearConfirm 消费掉一次人工确认。重跑后如果门禁再次失败，需要重新暂停等人确认，
// 不能因为一次点击就自动无限重跑。
func (r *pipelineRun) clearConfirm(stage string) {
	r.mu.Lock()
	delete(r.confirmed, stage)
	r.mu.Unlock()
}

func (r *pipelineRun) currentStage() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stage
}

// Reject 是人工打回：把当前阶段退回指定上游阶段，并把原因交给下一轮返工。
func (m *PipelineManager) Reject(root, slug, stage, target, reason string) (PipelineRunView, error) {
	m.mu.Lock()
	run := m.runs[pipeKey(root, slug)]
	m.mu.Unlock()
	if run == nil {
		return PipelineRunView{}, errors.New("这个任务没有正在调度的流水线")
	}
	state, path, err := readState(root, slug)
	if err != nil {
		return PipelineRunView{}, fmt.Errorf("读取 workflow-state.json 失败: %w", err)
	}
	if stage == "" {
		stage = state.CurrentStage
		if stage == "" || stage == "FINAL" {
			stage = run.currentStage()
		}
	}
	if stageIndex(stage) < 0 {
		return PipelineRunView{}, fmt.Errorf("未知阶段：%s", stage)
	}
	if target == "" {
		target = reworkTarget(stage)
	}
	if target == "" {
		target = previousStage(stage)
	}
	if stageIndex(target) < 0 || stageIndex(target) >= stageIndex(stage) {
		return PipelineRunView{}, errors.New("只能打回到当前阶段之前的上游阶段")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "人工打回：按当前报告重新处理"
	}

	wasStopped := run.state() == "stopped"
	round := run.bumpRework(target)
	m.archiveStage(run, stage)
	run.resetAttempt(target)
	if err := writeRejectReason(root, slug, target, stage, reason, round); err != nil {
		return PipelineRunView{}, err
	}
	if err := patchState(path, func(mp map[string]any) {
		mp["current_stage"] = target
		mp["last_event"] = stage + "_rejected"
		mp["next_target"] = stageMeta[target].Executor
		setStageStatus(mp, target, "pending")
		// 被打回的一方也回到 pending，返工完成后不会带着旧结论立刻再判一次。
		setStageStatus(mp, stage, "pending")
		mp["updated_at"] = time.Now().Format(time.RFC3339)
	}); err != nil {
		return PipelineRunView{}, err
	}
	run.setStage(target)
	run.set("running", fmt.Sprintf("人工打回 %s → %s（第 %d 轮）",
		stageMeta[stage].Label, stageMeta[target].Label, round))
	if wasStopped {
		run.resetCancel()
		go m.loop(run)
	}
	return run.view(), nil
}
