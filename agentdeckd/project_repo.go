package main

// project_repo.go：「项目对话」页上的 GitHub / AtomGit 一级入口。
// 只读项目根下的 git 远端，判断它接在哪个托管平台、对应哪个仓库；不联网、不写文件。

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// 平台标识，界面按它选图标和文案。
const (
	repoPlatformGitHub  = "github"
	repoPlatformAtomGit = "atomgit"
	repoPlatformOther   = "other"
)

// projectRepo 是解析出来的一个远端仓库。
type projectRepo struct {
	Platform   string `json:"platform"` // github / atomgit / other
	RemoteName string `json:"remoteName"`
	Host       string `json:"host"`
	Owner      string `json:"owner"`
	Name       string `json:"name"`
	Slug       string `json:"slug"`   // owner/repo
	URL        string `json:"url"`    // 网页地址
	Remote     string `json:"remote"` // 原始远端地址，给 tooltip 用
}

// projectGitInfo 是「项目对话」页入口需要的仓库信息。
type projectGitInfo struct {
	IsRepo bool          `json:"isRepo"`
	Branch string        `json:"branch,omitempty"`
	Repos  []projectRepo `json:"repos"`
	Error  string        `json:"error,omitempty"` // 有 git 但读不出来时的原因
}

// registerRepoRoutes 注册项目仓库信息接口（项目对话页的 GitHub / AtomGit 入口）。
func (a *App) registerRepoRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/projects/git", func(w http.ResponseWriter, r *http.Request) {
		root, err := a.projectRoot(r.URL.Query().Get("root"))
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, readProjectGit(root))
	})
	// 本地 git 同步（领先/落后、fetch/pull/push）与平台 API（仓库详情、提交、PR、令牌）。
	a.registerGitSyncRoutes(mux)
	a.registerRepoAPIRoutes(mux)
}

// readProjectGit 读远列表与当前分支；不是 git 仓库时 IsRepo 为 false。
func readProjectGit(root string) projectGitInfo {
	info := projectGitInfo{Repos: []projectRepo{}}
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		return info
	}
	info.IsRepo = true
	run := func(args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		// 和 project_overview 一致：强制 UTF-8、不加引号转义，中文路径才不会乱码。
		base := []string{"-C", root, "-c", "i18n.logOutputEncoding=UTF-8", "-c", "core.quotepath=false"}
		out, err := exec.CommandContext(ctx, "git", append(base, args...)...).Output()
		return strings.TrimSpace(string(out)), err
	}

	// remote -v 每行形如：origin\thttps://github.com/o/r.git (fetch)
	out, err := run("remote", "-v")
	if err != nil {
		info.Error = "读不到 git 远端（git 可能没装）"
		return info
	}
	seen := make(map[string]bool, 4)
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		// 只要 fetch 那一行，push 行会和它重复。
		if len(fields) > 2 && !strings.Contains(fields[2], "fetch") {
			continue
		}
		repo, ok := parseRepoRemote(fields[1])
		if !ok {
			continue
		}
		// fields[0] 是远端名（origin / github …），同步时按它指定远端。
		repo.RemoteName = fields[0]
		key := repo.Platform + "|" + repo.Host + "|" + repo.Slug
		if seen[key] {
			continue
		}
		seen[key] = true
		info.Repos = append(info.Repos, repo)
	}
	if branch, err := run("rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		info.Branch = branch
	}
	return info
}

// scpRemote 匹配 git 的 scp 风格远端（git@github.com:owner/repo.git）。
var scpRemote = regexp.MustCompile(`^[A-Za-z0-9._-]+@([^:/]+):(.+)$`)

// parseRepoRemote 把任意远端地址解析成「平台 + owner/repo + 网页地址」。
// 支持 https://、ssh://、git@host:path 三种常见写法。
func parseRepoRemote(raw string) (projectRepo, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return projectRepo{}, false
	}
	host, path := "", ""
	if m := scpRemote.FindStringSubmatch(raw); m != nil {
		host, path = m[1], m[2]
	} else {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return projectRepo{}, false
		}
		host, path = u.Hostname(), strings.TrimPrefix(u.Path, "/")
	}
	path = strings.TrimSuffix(strings.TrimRight(path, "/"), ".git")
	parts := make([]string, 0, 3)
	for _, part := range strings.Split(path, "/") {
		if part != "" {
			parts = append(parts, part)
		}
	}
	if len(parts) < 2 {
		return projectRepo{}, false
	}
	owner, name := parts[len(parts)-2], parts[len(parts)-1]
	host = strings.ToLower(strings.TrimPrefix(host, "www."))

	platform := repoPlatformOther
	switch {
	case host == "github.com" || strings.HasSuffix(host, ".github.com"):
		platform = repoPlatformGitHub
	case host == "atomgit.com" || strings.HasSuffix(host, ".atomgit.com"):
		platform = repoPlatformAtomGit
	}
	return projectRepo{
		Platform: platform,
		Host:     host,
		Owner:    owner,
		Name:     name,
		Slug:     owner + "/" + name,
		URL:      "https://" + host + "/" + owner + "/" + name,
		Remote:   raw,
	}, true
}
