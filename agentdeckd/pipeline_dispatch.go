package main

// 阶段派发：为每个阶段开独立 PTY、注入阶段指令、判断能否复用原终端。

import (
	"fmt"
	"time"
)

func (r *pipelineRun) sessionAlive(m *PipelineManager, stage string) bool {
	id := r.session(stage)
	if id == "" {
		return false
	}
	session := m.app.sessions.get(id)
	return session != nil && session.State() == "running"
}

func (m *PipelineManager) dispatch(run *pipelineRun, stage string) error {
	cfg := m.configFor(run.root)
	sa := cfg.stageAgent(stage)
	agentID := sa.Agent
	def, ok := agentByID(agentID)
	if !ok {
		run.set("error", fmt.Sprintf("未知的 agent: %s", agentID))
		return nil
	}
	ov := m.app.config.GetCliOverride(agentID)
	if ov.Enabled != nil && !*ov.Enabled {
		run.set("paused", fmt.Sprintf("%s 已在 CLI 设置里停用", def.Name))
		return nil
	}
	if len(ov.Args) > 0 {
		def.Args = append([]string{}, ov.Args...)
	}
	if len(sa.Args) > 0 {
		def.Args = append(append([]string{}, def.Args...), sa.Args...)
	}
	binPath := resolveAgentWith(def, ov)
	if binPath == "" {
		if ov.Path != "" {
			run.set("paused", fmt.Sprintf("%s 的路径不可用：%s", def.Name, ov.Path))
		} else {
			run.set("paused", fmt.Sprintf("%s 没有安装（%s）", def.Name, def.Bin))
		}
		return nil
	}

	state, statePath, _ := readState(run.root, run.slug)
	prompt := m.stagePrompt(run, stage, state)
	title := fmt.Sprintf("%s · %s", stageMeta[stage].Label, run.slug)

	// 派发前把阶段标成 running：既清掉上一次失败留下的终态，也让黑板反映真实进度。
	if statePath != "" {
		_ = patchState(statePath, func(mp map[string]any) {
			setStageStatus(mp, stage, "running")
			mp["updated_at"] = time.Now().Format(time.RFC3339)
		})
	}

	// 驱动模式：每次阶段派发都是独立进程，角色身份靠启动参数和交接文件成立。
	// 唯一的复用场景是同一阶段被打回后还在跑的终端（继续同一角色的修复）。
	if id, ok := m.reuseSession(run, stage); ok {
		run.setSession(stage, id)
		m.app.sessions.SetTitle(id, title)
		m.app.sessions.SetAgent(id, agentID)
		go m.injectPrompt(id, prompt, false)
		return nil
	}

	// 流水线终端和手动开的终端走同一套接管规则：勾了就走网关，没勾就原样。
	routeEnv, routeArgs := m.app.gateway.Takeover(agentID)
	command := buildAgentCommand(def, binPath, prompt, routeArgs)
	session, err := m.app.sessions.Start(command, run.root, routeEnv, len(routeEnv) > 0 || len(routeArgs) > 0)
	if err != nil {
		return fmt.Errorf("启动 %s 失败: %w", def.Name, err)
	}
	run.setSession(stage, session.ID)
	session.SetTitle(title)
	session.SetAgent(agentID)

	if def.Prompt != "positional" {
		go m.injectPrompt(session.ID, prompt, true)
	}
	return nil
}

// reuseSession 只复用「同一阶段返工」的原终端；跨角色一律新开进程。
func (m *PipelineManager) reuseSession(run *pipelineRun, stage string) (string, bool) {
	if run.reworkCount(stage) == 0 {
		return "", false
	}
	id := run.session(stage)
	if id == "" || !m.sessionRunning(id) {
		return "", false
	}
	return id, true
}

// injectPrompt 把阶段指令打进 CLI 的 TUI。文本和回车必须分开发，
// 一起写时正在初始化的 TUI 会把回车吞掉。TUI 的转圈动画让“等待静止”
// 不可靠，所以新会话按固定启动时间等，复用会话直接输入。
func (m *PipelineManager) injectPrompt(id, prompt string, fresh bool) {
	text := oneLine(prompt)
	if fresh {
		if !m.waitStarted(id, 45*time.Second) {
			return
		}
		time.Sleep(3 * time.Second)
	} else {
		time.Sleep(500 * time.Millisecond)
	}
	if !m.sessionRunning(id) {
		return
	}
	_ = m.app.sessions.Write(id, []byte(text))
	time.Sleep(700 * time.Millisecond)
	_ = m.app.sessions.Write(id, []byte("\r"))
	// 再补一次回车：空输入时回车是无害的，能兜住第一次被吞的情况。
	time.Sleep(1500 * time.Millisecond)
	if m.sessionRunning(id) {
		_ = m.app.sessions.Write(id, []byte("\r"))
	}
}

// waitStarted 等 CLI 画出第一屏。
func (m *PipelineManager) waitStarted(id string, limit time.Duration) bool {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if size, _ := m.outputSig(id); size > 0 {
			return true
		}
		if !m.sessionRunning(id) {
			return false
		}
		time.Sleep(300 * time.Millisecond)
	}
	return false
}

func (m *PipelineManager) outputSig(id string) (int, time.Time) {
	if s := m.app.sessions.get(id); s != nil {
		return s.OutputSig()
	}
	return 0, time.Time{}
}

func (m *PipelineManager) sessionRunning(id string) bool {
	s := m.app.sessions.get(id)
	return s != nil && s.State() == "running"
}
