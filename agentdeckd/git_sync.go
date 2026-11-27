package main

// git_sync.go：项目对话页仓库入口的本地 git 操作。
// 只做本机 git 会做的事：查领先 / 落后、fetch、pull --ff-only、push。
// 凭据完全交给系统 git（SSH key / Git Credential Manager），daemon 不读也不存任何 token。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	gitStatusTimeout = 8 * time.Second
	gitSyncTimeout   = 180 * time.Second
)

// gitSyncStatus 是仓库行要显示的本地状态。
type gitSyncStatus struct {
	IsRepo   bool   `json:"isRepo"`
	Branch   string `json:"branch,omitempty"`
	Upstream string `json:"upstream,omitempty"`
	Ahead    int    `json:"ahead"`
	Behind   int    `json:"behind"`
	Dirty    int    `json:"dirty"` // 未提交的改动条数
}

// gitSyncActions 是允许从界面触发的操作，其它一律拒绝。
var gitSyncActions = map[string][]string{
	"pull": {"pull", "--ff-only"}, // 只做快进，冲突留给终端手动处理
	"push": {"push"},
}

// validRemote 只放行项目里真实存在的远端名，避免把任意字符串拼进 git 命令。
func validRemote(root, remote string) bool {
	if remote == "" {
		return true
	}
	if strings.ContainsAny(remote, " \t\r\n/\\") {
		return false
	}
	out, err := gitRun(root, gitStatusTimeout, "remote")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == remote {
			return true
		}
	}
	return false
}

// gitEnv 让后台 git 缺凭据时立刻失败，不在服务里卡着等输入或弹窗。
func gitEnv() []string {
	return append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GCM_INTERACTIVE=never",
	)
}

// gitRun 在项目目录跑一条 git 命令，返回合并后的输出。
func gitRun(root string, timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	base := []string{"-C", root, "-c", "i18n.logOutputEncoding=UTF-8", "-c", "core.quotepath=false", "-c", "color.ui=false"}
	cmd := exec.CommandContext(ctx, "git", append(base, args...)...)
	cmd.Env = gitEnv()
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if ctx.Err() == context.DeadlineExceeded {
		return text, fmt.Errorf("git %s 超时（%s）", args[0], timeout)
	}
	if err != nil {
		if text != "" {
			return text, fmt.Errorf("%s", firstLines(text, 3))
		}
		return text, err
	}
	return text, nil
}

// firstLines 截断多行输出，错误提示只要前几行。
func firstLines(text string, n int) string {
	lines := strings.Split(text, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

// readGitSyncStatus 读分支、上游、领先 / 落后、未提交条数，纯本地、不联网。
func readGitSyncStatus(root string) gitSyncStatus {
	st := gitSyncStatus{}
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		return st
	}
	st.IsRepo = true
	if branch, err := gitRun(root, gitStatusTimeout, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		st.Branch = branch
	}
	if upstream, err := gitRun(root, gitStatusTimeout, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}"); err == nil {
		st.Upstream = upstream
	}
	if st.Upstream != "" {
		// 输出是「落后 领先」两个数，顺序不能反。
		if out, err := gitRun(root, gitStatusTimeout, "rev-list", "--left-right", "--count", "@{u}...HEAD"); err == nil {
			if fields := strings.Fields(out); len(fields) == 2 {
				st.Behind, _ = strconv.Atoi(fields[0])
				st.Ahead, _ = strconv.Atoi(fields[1])
			}
		}
	}
	if out, err := gitRun(root, gitStatusTimeout, "status", "--porcelain"); err == nil && out != "" {
		st.Dirty = len(strings.Split(out, "\n"))
	}
	return st
}

// runGitSync 执行一次联网操作，返回 git 的输出（成功和失败都带上，界面直接展示）。
// remote 只对 fetch 生效：按平台只抓对应的那个远端，别因为另一个平台没凭据一起失败。
func runGitSync(root, action, remote string) (string, error) {
	if action == "fetch" {
		if !validRemote(root, remote) {
			return "", fmt.Errorf("未知远端: %s", remote)
		}
		args := []string{"fetch", "--prune"}
		if remote != "" {
			args = append(args, remote)
		}
		return gitRun(root, gitSyncTimeout, args...)
	}
	args, ok := gitSyncActions[action]
	if !ok {
		return "", fmt.Errorf("不支持的操作: %s", action)
	}
	return gitRun(root, gitSyncTimeout, args...)
}

// registerGitSyncRoutes 注册仓库同步接口（本地 git，不碰平台 API）。
func (a *App) registerGitSyncRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/projects/git/sync", func(w http.ResponseWriter, r *http.Request) {
		root, err := a.projectRoot(r.URL.Query().Get("root"))
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, readGitSyncStatus(root))
	})

	mux.HandleFunc("POST /api/v1/projects/git/sync", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Root   string `json:"root"`
			Action string `json:"action"`
			Remote string `json:"remote"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "请求体不是 JSON")
			return
		}
		root, err := a.projectRoot(body.Root)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if body.Action == "" {
			writeError(w, http.StatusBadRequest, "缺少 action")
			return
		}
		output, syncErr := runGitSync(root, body.Action, strings.TrimSpace(body.Remote))
		resp := map[string]any{
			"ok":     syncErr == nil,
			"action": body.Action,
			"output": output,
			"status": readGitSyncStatus(root),
		}
		if syncErr != nil {
			resp["error"] = syncErr.Error()
		}
		// 操作失败也回 200：这是可预期的结果（冲突、没上游、缺凭据），界面照常展示输出。
		writeJSON(w, http.StatusOK, resp)
	})
}
