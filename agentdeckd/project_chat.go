package main

// project_chat.go：「项目对话」入口。在项目根目录直接开一个 CLI 终端，只读项目现状，
// 不写 artifacts、不碰流水线状态，和阶段开发互不干扰。

import (
	"encoding/json"
	"net/http"
	"strings"
)

// projectChatPrompt 是项目对话的第一条指令：先看再答，明确禁止直接改代码。
const projectChatPrompt = "先不要修改任何代码。请通读这个项目的现状并汇报：" +
	"1) artifacts/ 下每个任务的 workflow-state.json 现在做到哪个阶段、卡在哪里；" +
	"2) README 和 docs 里的功能清单，整理成功能树（已完成 / 进行中 / 计划）；" +
	"3) git 最近提交和未提交改动说明当前在做什么；" +
	"4) 你建议的下一步。用简洁中文分条回答，不要贴大段代码。"

// registerChatRoutes 注册项目对话入口。
func (a *App) registerChatRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/projects/chat", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Root  string `json:"root"`
			Agent string `json:"agent"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Root) == "" {
			writeError(w, http.StatusBadRequest, "缺少 root")
			return
		}
		root, err := a.projectRoot(body.Root)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		agent, ok := a.pickChatAgent(root, strings.TrimSpace(body.Agent))
		if !ok {
			writeError(w, http.StatusBadRequest, "没有可用的 CLI，请先在设置里安装或指定路径")
			return
		}
		command := buildAgentCommand(agent.AgentDef, agent.Path, projectChatPrompt)
		session, err := a.sessions.Start(command, root)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		session.SetAgent(agent.ID)
		session.SetTitle("项目对话 · " + baseName(root))
		// 位置参数型 CLI 直接把提示词带进命令行；其余等 TUI 起来后再输入。
		if agent.Prompt != "positional" {
			go a.pipes.injectPrompt(session.ID, projectChatPrompt, true)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":      true,
			"agent":   agent.ID,
			"session": session.Snapshot(),
		})
	})
}

// pickChatAgent 选项目对话用的 CLI：指定 > 该项目 TASK-01 配置 > Claude Code > 第一个可用。
func (a *App) pickChatAgent(root, want string) (AgentStatus, bool) {
	agents := DetectAgentsWith(a.config.CliOverridesSnapshot())
	byID := make(map[string]AgentStatus, len(agents))
	for _, agent := range agents {
		byID[agent.ID] = agent
	}
	usable := func(id string) (AgentStatus, bool) {
		agent, exists := byID[id]
		if !exists || !agent.Installed || !agent.Enabled {
			return AgentStatus{}, false
		}
		return agent, true
	}
	if want != "" {
		if agent, ok := usable(want); ok {
			return agent, true
		}
	}
	if stage, ok := a.pipes.configFor(root).StageAgents["TASK-01"]; ok {
		if agent, ok := usable(stage.Agent); ok {
			return agent, true
		}
	}
	if agent, ok := usable("claude-code"); ok {
		return agent, true
	}
	for _, agent := range agents {
		if agent.Installed && agent.Enabled {
			return agent, true
		}
	}
	return AgentStatus{}, false
}

// baseName 取目录名，空路径返回「项目」。
func baseName(path string) string {
	trimmed := strings.TrimRight(strings.TrimRight(path, "/"), "\\")
	if idx := strings.LastIndexAny(trimmed, "/\\"); idx >= 0 {
		trimmed = trimmed[idx+1:]
	}
	if trimmed == "" {
		return "项目"
	}
	return trimmed
}
