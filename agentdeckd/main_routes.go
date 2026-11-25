package main

// HTTP 路由注册：按板块拆开，接口一目了然，新增接口时知道该放哪。

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// routes 注册所有 HTTP 接口，按板块委托给下面的注册函数。
func (a *App) routes(mux *http.ServeMux) {
	a.registerSystemRoutes(mux)
	a.registerProjectRoutes(mux)
	a.registerOverviewRoutes(mux)
	a.registerCliRoutes(mux)
	a.registerPipelineRoutes(mux)
	a.registerSessionRoutes(mux)
	a.registerFsRoutes(mux)
}

// registerSystemRoutes 系统：探活与配置。
func (a *App) registerSystemRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"status":   "ok",
			"pid":      os.Getpid(),
			"sessions": a.sessions.Count(),
			"version":  version,
			"repo":     a.updates.Repo(),
			"platform": runtime.GOOS + "/" + runtime.GOARCH,
			"auth":     a.auth.Enabled(),
		})
	})

	mux.HandleFunc("GET /api/v1/update/check", func(w http.ResponseWriter, r *http.Request) {
		force := r.URL.Query().Get("force") == "1"
		writeJSON(w, http.StatusOK, a.updates.Check(force))
	})

	mux.HandleFunc("GET /api/v1/config", func(w http.ResponseWriter, r *http.Request) {
		a.config.mu.RLock()
		defer a.config.mu.RUnlock()
		writeJSON(w, http.StatusOK, map[string]any{
			"lastCwd":        a.config.LastCwd,
			"home":           homeDir(),
			"localBin":       a.config.LocalBin,
			"githubTokenSet": a.config.GitHubToken != "",
			"githubTokenEnv": strings.TrimSpace(os.Getenv("GITHUB_TOKEN")) != "",
		})
	})

	mux.HandleFunc("POST /api/v1/config", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			LastCwd     string  `json:"lastCwd"`
			LocalBin    string  `json:"localBin"`
			GitHubToken *string `json:"githubToken"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "请求体不是 JSON")
			return
		}
		if body.LastCwd != "" {
			a.config.SetLastCwd(body.LastCwd)
		}
		if body.LocalBin != "" {
			a.config.SetLocalBin(body.LocalBin)
		}
		if body.GitHubToken != nil {
			a.config.SetGitHubToken(*body.GitHubToken)
			a.updates.Invalidate()
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "githubTokenSet": a.config.GitHubTokenValue() != ""})
	})
}

// registerProjectRoutes 项目与工作流扫描。
func (a *App) registerProjectRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		a.config.mu.RLock()
		defer a.config.mu.RUnlock()
		paths := a.config.existsProjects()
		list := make([]map[string]string, 0, len(paths))
		for _, p := range paths {
			list = append(list, map[string]string{"path": p, "name": filepath.Base(p)})
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"projects": list,
			"active":   a.config.Active,
		})
	})

	mux.HandleFunc("POST /api/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Path == "" {
			writeError(w, http.StatusBadRequest, "缺少 path")
			return
		}
		if err := a.config.AddProject(body.Path); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		a.config.SetActive(body.Path)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	mux.HandleFunc("POST /api/v1/projects/remove", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Path == "" {
			writeError(w, http.StatusBadRequest, "缺少 path")
			return
		}
		a.config.RemoveProject(body.Path)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	mux.HandleFunc("POST /api/v1/projects/active", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Path == "" {
			writeError(w, http.StatusBadRequest, "缺少 path")
			return
		}
		a.config.SetActive(body.Path)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	mux.HandleFunc("GET /api/v1/workflows", func(w http.ResponseWriter, r *http.Request) {
		root := r.URL.Query().Get("root")
		writeJSON(w, http.StatusOK, map[string]any{
			"root":      root,
			"workflows": ListWorkflows(root),
			"installed": LocalStatus(root),
		})
	})
}

// registerCliRoutes CLI 注册表、版本、安装任务与阶段适配。
func (a *App) registerCliRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/clis", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"clis": a.cliList()})
	})

	mux.HandleFunc("GET /api/v1/clis/versions", func(w http.ResponseWriter, r *http.Request) {
		force := r.URL.Query().Get("refresh") == "1"
		overrides := a.config.CliOverridesSnapshot()
		agents := DetectAgentsWith(overrides)
		packages := make(map[string]string, len(agents))
		for _, agent := range agents {
			if pkg := a.resolvePackage(agent, overrides[agent.ID]); pkg != "" {
				packages[agent.ID] = pkg
			}
		}
		versions, checkedAt := a.versions.Versions(agents, packages, force)
		writeJSON(w, http.StatusOK, map[string]any{"versions": versions, "checkedAt": checkedAt})
	})

	mux.HandleFunc("GET /api/v1/clis/release", a.releases.Handler)

	mux.HandleFunc("GET /api/v1/clis/job", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"job": a.install.Snapshot()})
	})

	mux.HandleFunc("POST /api/v1/clis/install", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ID == "" {
			writeError(w, http.StatusBadRequest, "缺少 id")
			return
		}
		if err := a.install.Start(body.ID); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "job": a.install.Snapshot()})
	})

	mux.HandleFunc("POST /api/v1/clis/job/cancel", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": a.install.Cancel()})
	})

	mux.HandleFunc("POST /api/v1/clis/config", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ID      string   `json:"id"`
			Path    string   `json:"path"`
			Args    []string `json:"args"`
			Mode    string   `json:"mode"`
			Package string   `json:"package"`
			Enabled *bool    `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ID == "" {
			writeError(w, http.StatusBadRequest, "缺少 id")
			return
		}
		def, ok := agentByID(body.ID)
		if !ok {
			writeError(w, http.StatusBadRequest, "未知的 CLI: "+body.ID)
			return
		}
		args := normalizeArgs(body.Args)
		mode := strings.TrimSpace(body.Mode)
		if mode != "" {
			if _, ok := pickMode(def, mode); !ok {
				writeError(w, http.StatusBadRequest, "该 CLI 没有启动模式: "+mode)
				return
			}
		}
		a.config.SetCliOverride(body.ID, CliOverride{
			Path:    strings.TrimSpace(body.Path),
			Args:    args,
			Mode:    mode,
			Package: strings.TrimSpace(body.Package),
			Enabled: body.Enabled,
		})
		a.versions.Invalidate()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "clis": a.cliList()})
	})

	mux.HandleFunc("POST /api/v1/clis/reset", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ID == "" {
			writeError(w, http.StatusBadRequest, "缺少 id")
			return
		}
		a.config.ResetCliOverride(body.ID)
		a.versions.Invalidate()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "clis": a.cliList()})
	})

	mux.HandleFunc("GET /api/v1/agents", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"agents": DetectAgentsWith(a.config.CliOverridesSnapshot())})
	})
}

// registerPipelineRoutes 流水线调度与 LoopForge 安装。
func (a *App) registerPipelineRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/pipeline", func(w http.ResponseWriter, r *http.Request) {
		root := r.URL.Query().Get("root")
		writeJSON(w, http.StatusOK, map[string]any{
			"root":     root,
			"config":   a.pipes.configFor(root),
			"runs":     a.pipes.Snapshot(root),
			"stages":   stageOrder,
			"editions": detectEditions(root),
		})
	})

	mux.HandleFunc("POST /api/v1/pipeline/config", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Root         string                `json:"root"`
			StageAgents  map[string]StageAgent `json:"stageAgents"`
			Auto         *bool                 `json:"auto"`
			MaxReworks   *int                  `json:"maxReworks"`
			KeepSessions *bool                 `json:"keepSessions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Root == "" {
			writeError(w, http.StatusBadRequest, "缺少 root")
			return
		}
		cfg := a.pipes.configFor(body.Root)
		if body.StageAgents != nil {
			cfg.StageAgents = body.StageAgents
		}
		if body.Auto != nil {
			cfg.Auto = *body.Auto
		}
		if body.MaxReworks != nil {
			cfg.MaxReworks = *body.MaxReworks
		}
		if body.KeepSessions != nil {
			cfg.KeepSessions = *body.KeepSessions
		}
		a.pipes.SetConfig(body.Root, cfg)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "config": cfg})
	})

	mux.HandleFunc("POST /api/v1/pipeline/start", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Root        string `json:"root"`
			Slug        string `json:"slug"`
			Requirement string `json:"requirement"`
			Force       bool   `json:"force"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Root == "" {
			writeError(w, http.StatusBadRequest, "缺少 root")
			return
		}
		run, err := a.pipes.Start(body.Root, body.Slug, body.Requirement, body.Force)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "run": run})
	})

	mux.HandleFunc("POST /api/v1/pipeline/stop", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Root string `json:"root"`
			Slug string `json:"slug"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Root == "" || body.Slug == "" {
			writeError(w, http.StatusBadRequest, "缺少 root 或 slug")
			return
		}
		a.pipes.Stop(body.Root, body.Slug)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	mux.HandleFunc("POST /api/v1/pipeline/resume", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Root string `json:"root"`
			Slug string `json:"slug"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Root == "" || body.Slug == "" {
			writeError(w, http.StatusBadRequest, "缺少 root 或 slug")
			return
		}
		a.pipes.Resume(body.Root, body.Slug)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	mux.HandleFunc("POST /api/v1/pipeline/reject", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Root   string `json:"root"`
			Slug   string `json:"slug"`
			Stage  string `json:"stage"`
			Target string `json:"target"`
			Reason string `json:"reason"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Root == "" || body.Slug == "" {
			writeError(w, http.StatusBadRequest, "缺少 root 或 slug")
			return
		}
		run, err := a.pipes.Reject(body.Root, body.Slug, body.Stage, body.Target, body.Reason)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "run": run})
	})

	mux.HandleFunc("POST /api/v1/install", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Root string `json:"root"`
			Host string `json:"host"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Root == "" || body.Host == "" {
			writeError(w, http.StatusBadRequest, "缺少 root 或 host")
			return
		}
		logs, err := InstallLocal(a.config.LocalBin, body.Root, body.Host)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "log": logs, "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "log": logs})
	})
}

// registerSessionRoutes 终端会话：列表、创建、输入、尺寸、SSE、tail。
func (a *App) registerSessionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/sessions", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"sessions": a.sessions.List()})
	})

	mux.HandleFunc("POST /api/v1/sessions", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Command string `json:"command"`
			Cwd     string `json:"cwd"`
			Agent   string `json:"agent"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Command == "" {
			writeError(w, http.StatusBadRequest, "缺少 command")
			return
		}
		session, err := a.sessions.Start(body.Command, body.Cwd)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if body.Agent != "" {
			session.SetAgent(body.Agent)
		}
		a.config.SetLastCwd(body.Cwd)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "session": session.Snapshot()})
	})

	mux.HandleFunc("POST /api/v1/sessions/{id}/input", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Data string `json:"data"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "请求体不是 JSON")
			return
		}
		if err := a.sessions.Write(r.PathValue("id"), []byte(body.Data)); err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("POST /api/v1/sessions/{id}/resize", func(w http.ResponseWriter, r *http.Request) {
		cols, _ := strconv.Atoi(r.URL.Query().Get("cols"))
		rows, _ := strconv.Atoi(r.URL.Query().Get("rows"))
		a.sessions.Resize(r.PathValue("id"), cols, rows)
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("DELETE /api/v1/sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
		a.sessions.Close(r.PathValue("id"))
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("GET /api/v1/sessions/{id}/stream", func(w http.ResponseWriter, r *http.Request) {
		a.sessions.Stream(w, r, r.PathValue("id"))
	})

	mux.HandleFunc("GET /api/v1/sessions/{id}/tail", func(w http.ResponseWriter, r *http.Request) {
		session := a.sessions.get(r.PathValue("id"))
		if session == nil {
			writeError(w, http.StatusNotFound, "会话不存在")
			return
		}
		max, _ := strconv.Atoi(r.URL.Query().Get("max"))
		if max <= 0 || max > 4000 {
			max = 400
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"id": session.ID, "state": session.State(), "tail": session.TailText(max),
		})
	})
}
