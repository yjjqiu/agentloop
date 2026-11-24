package main

// 流水线核心：阶段定义、运行态模型、调度循环和阶段推进（step）。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// stageOrder 是 LoopForge classic 流程里 runner 负责的阶段顺序。
var stageOrder = []string{"TASK-01", "TASK-02", "TASK-03", "CODE-REVIEW", "TASK-04", "TASK-05"}

// stageMeta 描述阶段的展示信息、执行角色和对应的命令文件。
var stageMeta = map[string]struct {
	Label    string
	Executor string
	Command  string
	Dir      string
}{
	"TASK-01":     {"需求", "analyst", "analyze-requirement", "01-requirement"},
	"TASK-02":     {"方案", "architect", "design-solution", "02-design"},
	"TASK-03":     {"开发", "developer", "develop-code", "03-code"},
	"CODE-REVIEW": {"审查", "code-reviewer", "review-code", "03-code"},
	"TASK-04":     {"测试", "test-engineer", "run-tests", "04-e2e"},
	"TASK-05":     {"沉淀", "knowledge-engineer", "distill-knowledge", "05-knowledge"},
}

// gateStages 是默认的人工确认点。
var gateStages = map[string]bool{"TASK-02": true, "CODE-REVIEW": true, "TASK-04": true}

// StageAgent 是某个阶段用哪款 CLI 执行，以及只属于这个阶段的额外参数。
// 旧配置里直接写字符串（"claude-code"），StageAgent 的 UnmarshalJSON 会兼容。
type StageAgent struct {
	Agent string   `json:"agent"`
	Args  []string `json:"args,omitempty"` // 阶段级启停参数，例如审查阶段的只读 / sandbox 参数
}

// UnmarshalJSON 兼容旧格式（字符串）和新格式（{agent,args}）。
func (s *StageAgent) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) > 0 && data[0] == '"' {
		var id string
		if err := json.Unmarshal(data, &id); err != nil {
			return err
		}
		s.Agent = id
		s.Args = nil
		return nil
	}
	type plain struct {
		Agent string   `json:"agent"`
		Args  []string `json:"args"`
	}
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	s.Agent = p.Agent
	s.Args = p.Args
	return nil
}

// PipelineConfig 是某个项目配置的阶段→CLI 映射。
type PipelineConfig struct {
	StageAgents  map[string]StageAgent `json:"stageAgents"`
	Auto         bool                  `json:"auto"`
	MaxReworks   int                   `json:"maxReworks,omitempty"`   // 审查 / 测试打回的上限，默认 2
	KeepSessions bool                  `json:"keepSessions,omitempty"` // 阶段结束后保留终端进程，默认归档后关闭
}

func (c PipelineConfig) stageAgent(stage string) StageAgent {
	a := c.StageAgents[stage]
	if a.Agent == "" {
		a.Agent = "claude-code"
	}
	return a
}

func (c PipelineConfig) maxReworks() int {
	if c.MaxReworks <= 0 {
		return 2
	}
	return c.MaxReworks
}

// PipelineRunView 是返回给前端的运行状态。
type PipelineRunView struct {
	Root      string            `json:"root"`
	Slug      string            `json:"slug"`
	Status    string            `json:"status"`
	Message   string            `json:"message"`
	Stage     string            `json:"stage"`
	Sessions  map[string]string `json:"sessions"`
	StartedAt string            `json:"startedAt"`
	UpdatedAt string            `json:"updatedAt"`
	Mode      string            `json:"mode"`               // driven：每次阶段派发独立进程
	Editions  map[string]string `json:"editions,omitempty"` // 宿主 → classic / portable
	Reworks   map[string]int    `json:"reworks,omitempty"`  // 被退回的阶段 → 轮次
}

// pipelineRun 是内存里的调度状态，不持久化。
type pipelineRun struct {
	mu           sync.Mutex
	root         string
	slug         string
	status       string
	message      string
	stage        string
	sessions     map[string]string
	attempts     map[string]int
	dispatched   map[string]time.Time
	confirmed    map[string]bool
	reworks      map[string]int
	editions     map[string]string
	stateMtime   time.Time
	stateChanged time.Time
	leaseAt      time.Time
	startedAt    time.Time
	cancel       chan struct{}
	cancelOnce   sync.Once
}

func (r *pipelineRun) view() PipelineRunView {
	r.mu.Lock()
	defer r.mu.Unlock()
	sessions := make(map[string]string, len(r.sessions))
	for k, v := range r.sessions {
		sessions[k] = v
	}
	reworks := make(map[string]int, len(r.reworks))
	for k, v := range r.reworks {
		reworks[k] = v
	}
	editions := make(map[string]string, len(r.editions))
	for k, v := range r.editions {
		editions[k] = v
	}
	return PipelineRunView{
		Root:      r.root,
		Slug:      r.slug,
		Status:    r.status,
		Message:   r.message,
		Stage:     r.stage,
		Sessions:  sessions,
		StartedAt: r.startedAt.Format(time.RFC3339),
		UpdatedAt: time.Now().Format(time.RFC3339),
		Mode:      "driven",
		Editions:  editions,
		Reworks:   reworks,
	}
}

func (r *pipelineRun) set(status, message string) {
	r.mu.Lock()
	r.status = status
	r.message = message
	r.mu.Unlock()
}

func (r *pipelineRun) setStage(stage string) {
	r.mu.Lock()
	r.stage = stage
	r.mu.Unlock()
}

func (r *pipelineRun) setSession(stage, id string) {
	r.mu.Lock()
	r.sessions[stage] = id
	r.dispatched[stage] = time.Now()
	r.attempts[stage]++
	r.stateChanged = time.Now()
	r.mu.Unlock()
}

func (r *pipelineRun) attempt(stage string) (int, time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.attempts[stage], r.dispatched[stage]
}

// dispatchedAt 返回本轮阶段终端的启动时间，用于校验产物是不是本轮新写的。
func (r *pipelineRun) dispatchedAt(stage string) (time.Time, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	at, ok := r.dispatched[stage]
	return at, ok && !at.IsZero()
}

// clearSession 断开阶段与会话的绑定，供归档并关闭终端后使用。
func (r *pipelineRun) clearSession(stage string) {
	r.mu.Lock()
	delete(r.sessions, stage)
	r.mu.Unlock()
}

// resetAttempt 让返工后的阶段重新拥有正常的启动重试额度。
func (r *pipelineRun) resetAttempt(stage string) {
	r.mu.Lock()
	delete(r.attempts, stage)
	delete(r.dispatched, stage)
	r.mu.Unlock()
}

// bumpRework 记录某个阶段被打回的轮次，返回本轮序号。
func (r *pipelineRun) bumpRework(stage string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reworks[stage]++
	return r.reworks[stage]
}

func (r *pipelineRun) reworkCount(stage string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reworks[stage]
}

// editionFor 返回该阶段所用宿主安装的 LoopForge edition。
func (r *pipelineRun) editionFor(stage string, agentID string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.editions[agentHost[agentID]]
}

func (r *pipelineRun) session(stage string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sessions[stage]
}

func (r *pipelineRun) state() (status string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.status
}

// noteState 记录 workflow-state.json 的修改时间，返回距上次变化过了多久。
func (r *pipelineRun) noteState(mtime time.Time) time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stateChanged.IsZero() || !mtime.Equal(r.stateMtime) {
		r.stateMtime = mtime
		r.stateChanged = time.Now()
	}
	return time.Since(r.stateChanged)
}

// PipelineManager 负责按阶段把 CLI 拉起来并推进 workflow-state.json。
type PipelineManager struct {
	app        *App
	mu         sync.Mutex
	runs       map[string]*pipelineRun
	instanceID string
}

// NewPipelineManager 创建流水线管理器。
func NewPipelineManager(app *App) *PipelineManager {
	host, _ := os.Hostname()
	return &PipelineManager{
		app:        app,
		runs:       make(map[string]*pipelineRun),
		instanceID: fmt.Sprintf("%s:%d:%d", host, os.Getpid(), time.Now().UnixNano()),
	}
}

func pipeKey(root, slug string) string {
	return filepath.Clean(root) + "|" + slug
}

// —— 运行租约 ——

type leaseDoc struct {
	Owner     string `json:"owner"`
	Host      string `json:"host"`
	PID       int    `json:"pid"`
	Slug      string `json:"slug"`
	UpdatedAt string `json:"updated_at"`
	ExpiresAt string `json:"expires_at"`
}

func (m *PipelineManager) configFor(root string) PipelineConfig {
	m.app.config.mu.RLock()
	defer m.app.config.mu.RUnlock()
	cfg, ok := m.app.config.Pipelines[filepath.Clean(root)]
	if !ok {
		return PipelineConfig{StageAgents: map[string]StageAgent{}, Auto: true}
	}
	if cfg.StageAgents == nil {
		cfg.StageAgents = map[string]StageAgent{}
	}
	return cfg
}

// SetConfig 保存某个项目的阶段 → CLI 映射。
func (m *PipelineManager) SetConfig(root string, cfg PipelineConfig) {
	m.app.config.SetPipeline(root, cfg)
}

// Snapshot 返回该项目所有正在跑（或跑过）的流水线。
func (m *PipelineManager) Snapshot(root string) []PipelineRunView {
	m.mu.Lock()
	runs := make([]*pipelineRun, 0, len(m.runs))
	for _, r := range m.runs {
		if filepath.Clean(r.root) == filepath.Clean(root) {
			runs = append(runs, r)
		}
	}
	m.mu.Unlock()

	out := make([]PipelineRunView, 0, len(runs))
	for _, r := range runs {
		out = append(out, r.view())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out
}

func (m *PipelineManager) loop(run *pipelineRun) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-run.cancelCh():
			return
		case <-ticker.C:
			m.renewLease(run)
			status := run.state()
			if status != "running" && status != "waiting" {
				continue
			}
			if err := m.step(run); err != nil {
				run.set("error", err.Error())
			}
		}
	}
}

func (m *PipelineManager) step(run *pipelineRun) error {
	state, path, err := readState(run.root, run.slug)
	if err != nil {
		return fmt.Errorf("读取 workflow-state.json 失败: %w", err)
	}
	idle := run.noteState(fileMtime(path))
	if state.LastEvent == "workflow_completed" || state.CurrentStage == "FINAL" {
		m.releaseLease(run)
		run.set("done", "工作流已完成")
		return nil
	}

	stage := state.CurrentStage
	if stage == "" || stage == "SOLO" {
		stage = nextPending(state)
	}
	if stage == "" {
		m.releaseLease(run)
		run.set("done", "所有阶段已完成")
		return nil
	}
	run.setStage(stage)

	switch state.status(stage) {
	case "completed", "passed":
		cfg := m.configFor(run.root)
		if !cfg.Auto && gateStages[stage] && !run.isConfirmed(stage) {
			run.set("paused", fmt.Sprintf("%s 已完成，等待人工确认", stage))
			return nil
		}
		// 产物门禁：阶段声称完成但黑板上的文件不存在 / 为空时，不推进。
		if err := m.checkGate(run, state, stage); err != nil {
			// 用户已经点过“继续”：确认只消费一次，直接把本阶段打回待执行重跑。
			if run.isConfirmed(stage) {
				run.clearConfirm(stage)
				run.resetAttempt(stage)
				if perr := patchState(path, func(mp map[string]any) {
					setStageStatus(mp, stage, "pending")
					mp["updated_at"] = time.Now().Format(time.RFC3339)
				}); perr != nil {
					return perr
				}
				run.set("running", fmt.Sprintf("已确认，重新执行 %s", stageMeta[stage].Label))
				// 主动重派一次：原会话可能还活着，不能让 sessionAlive 把重跑挡在门外。
				return m.dispatch(run, stage)
			}
			run.set("paused", fmt.Sprintf("%s 声称完成，但%s", stage, err.Error()))
			return nil
		}
		m.archiveStage(run, stage)
		next := nextStage(stage)
		if next == "" {
			if err := patchState(path, func(mp map[string]any) {
				mp["current_stage"] = "FINAL"
				mp["last_event"] = "workflow_completed"
				mp["next_target"] = ""
				mp["updated_at"] = time.Now().Format(time.RFC3339)
			}); err != nil {
				return err
			}
			m.releaseLease(run)
			run.set("done", "所有阶段已完成")
			return nil
		}
		if err := patchState(path, func(mp map[string]any) {
			mp["current_stage"] = next
			mp["last_event"] = stage + "_completed"
			mp["next_target"] = stageMeta[next].Executor
			mp["updated_at"] = time.Now().Format(time.RFC3339)
		}); err != nil {
			return err
		}
		run.setStage(next)
		// 上一阶段的结论提示不能带到下一阶段，否则界面会把“已打回/重新执行”挂在错误阶段上。
		run.set("running", "")
		return nil

	case "failed", "blocked":
		// 审查 / 测试失败时沿回退边回到开发阶段，而不是直接停下。
		cfg := m.configFor(run.root)
		target := reworkTarget(stage)
		if target != "" && run.reworkCount(target) < cfg.maxReworks() {
			round := run.bumpRework(target)
			m.archiveStage(run, stage)
			run.resetAttempt(target)
			if err := patchState(path, func(mp map[string]any) {
				mp["current_stage"] = target
				mp["last_event"] = stage + "_failed"
				mp["next_target"] = stageMeta[target].Executor
				setStageStatus(mp, target, "pending")
				// 打回后本阶段的失败结论不能再留成终态，否则返工完成后回到这里会被旧状态立刻判失败。
				setStageStatus(mp, stage, "pending")
				mp["updated_at"] = time.Now().Format(time.RFC3339)
			}); err != nil {
				return err
			}
			run.setStage(target)
			run.set("running", fmt.Sprintf("%s 未通过，已打回 %s（第 %d 轮）",
				stageMeta[stage].Label, stageMeta[target].Label, round))
			return nil
		}
		run.set("paused", fmt.Sprintf("%s 失败，需要人工处理", stage))
		return nil
	}

	if run.sessionAlive(m, stage) {
		// CLI 还在跑，但状态文件长时间没动，多半是 TUI 在等用户回答。
		if idle > 4*time.Minute {
			run.set("waiting", fmt.Sprintf("%s 已 %d 分钟没有进展，可能在等你输入", stage, int(idle.Minutes())))
		} else if run.state() == "waiting" {
			run.set("running", "")
		}
		return nil
	}
	if run.state() == "waiting" {
		run.set("running", "")
	}
	if attempts, at := run.attempt(stage); attempts >= 3 && !at.IsZero() {
		run.set("error", fmt.Sprintf("%s 连续 %d 次启动失败", stage, attempts))
		return nil
	}
	if attempts, at := run.attempt(stage); attempts > 0 && time.Since(at) < 6*time.Second {
		return nil
	}
	return m.dispatch(run, stage)
}
