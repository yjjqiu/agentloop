package main

// repo_api.go：查 GitHub / AtomGit 平台上的仓库信息（联网，只读）。
// GitHub 走 REST v3（Authorization: Bearer），AtomGit 走 Gitee 兼容的 v5（private-token 头）。
// 令牌只从本机配置读、只发给对应平台，不进日志也不回显。

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// 两个平台的基础地址；测试里可以替换成本地 httptest 服务。
var (
	githubAPIBase  = "https://api.github.com"
	atomGitAPIBase = "https://atomgit.com/api/v5"
)

const repoAPIHTTPTimeout = 15 * time.Second

// repoCommit 是仓库面板里的一条提交。
type repoCommit struct {
	SHA     string `json:"sha"`
	Message string `json:"message"`
	Author  string `json:"author"`
	Date    string `json:"date"`
	URL     string `json:"url"`
}

// repoPull 是仓库面板里的一个合并请求。
type repoPull struct {
	Number    int    `json:"number"`
	Title     string `json:"title"`
	Author    string `json:"author"`
	URL       string `json:"url"`
	UpdatedAt string `json:"updatedAt"`
}

// repoPlatformInfo 是「这个项目的仓库现在什么样」的快照。
type repoPlatformInfo struct {
	Platform      string       `json:"platform"`
	Slug          string       `json:"slug"`
	URL           string       `json:"url"`
	Description   string       `json:"description,omitempty"`
	Private       bool         `json:"private"`
	DefaultBranch string       `json:"defaultBranch,omitempty"`
	Stars         int          `json:"stars"`
	OpenIssues    int          `json:"openIssues"`
	Language      string       `json:"language,omitempty"`
	PushedAt      string       `json:"pushedAt,omitempty"`
	Commits       []repoCommit `json:"commits"`
	Pulls         []repoPull   `json:"pulls"`
	TokenSet      bool         `json:"tokenSet"`
	TokenSealed   bool         `json:"tokenSealed"` // 令牌是否加密落盘
	NeedToken     bool         `json:"needToken"`   // 私有仓库缺令牌
	Error         string       `json:"error,omitempty"`
	Warning       string       `json:"warning,omitempty"`
	FetchedAt     int64        `json:"fetchedAt"`
}

// repoAPIClient 是只读 JSON 请求的小工具，令牌只放在请求头里。
type repoAPIClient struct {
	base        string
	token       string
	tokenHeader string // Authorization / private-token
	tokenPrefix string // GitHub 要 "Bearer "
}

func (c repoAPIClient) get(path string, out any) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), repoAPIHTTPTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "agentdeckd")
	if c.token != "" {
		req.Header.Set(c.tokenHeader, c.tokenPrefix+c.token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// 响应体只读掉不解析，避免把平台返回的细节（可能含令牌提示）带进界面。
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 8<<10))
		return resp.StatusCode, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return resp.StatusCode, json.NewDecoder(resp.Body).Decode(out)
}

// platformLabel 给界面用的平台名。
func platformLabel(platform string) string {
	switch platform {
	case repoPlatformGitHub:
		return "GitHub"
	case repoPlatformAtomGit:
		return "AtomGit"
	}
	return platform
}

// repoPlatformInfoFor 读项目远端 → 调对应平台 API，返回仓库快照。
func (a *App) repoPlatformInfoFor(root, platform string) repoPlatformInfo {
	info := repoPlatformInfo{
		Platform:    platform,
		Commits:     []repoCommit{},
		Pulls:       []repoPull{},
		TokenSealed: secretProtected(),
		FetchedAt:   time.Now().UnixMilli(),
	}
	if platform != repoPlatformGitHub && platform != repoPlatformAtomGit {
		info.Error = "不支持的平台: " + platform
		return info
	}
	var repo projectRepo
	found := false
	for _, item := range readProjectGit(root).Repos {
		if item.Platform == platform {
			repo, found = item, true
			break
		}
	}
	if !found {
		info.Error = "这个项目没有接 " + platformLabel(platform)
		return info
	}
	info.Slug, info.URL = repo.Slug, repo.URL
	if platform == repoPlatformGitHub {
		return fetchGitHubRepo(repo, a.config.GitHubTokenValue())
	}
	return fetchAtomGitRepo(repo, a.config.AtomGitTokenValue())
}

// fetchGitHubRepo 查 GitHub：仓库详情 + 最近 5 条提交 + 未合并 PR。
func fetchGitHubRepo(repo projectRepo, token string) repoPlatformInfo {
	info := repoPlatformInfo{
		Platform:    repo.Platform,
		Slug:        repo.Slug,
		URL:         repo.URL,
		Commits:     []repoCommit{},
		Pulls:       []repoPull{},
		TokenSet:    token != "",
		TokenSealed: secretProtected(),
		FetchedAt:   time.Now().UnixMilli(),
	}
	client := repoAPIClient{base: githubAPIBase, token: token, tokenHeader: "Authorization", tokenPrefix: "Bearer "}
	path := "/repos/" + url.PathEscape(repo.Owner) + "/" + url.PathEscape(repo.Name)

	var detail struct {
		FullName      string `json:"full_name"`
		Description   string `json:"description"`
		Private       bool   `json:"private"`
		DefaultBranch string `json:"default_branch"`
		Stars         int    `json:"stargazers_count"`
		OpenIssues    int    `json:"open_issues_count"`
		Language      string `json:"language"`
		PushedAt      string `json:"pushed_at"`
		HTMLURL       string `json:"html_url"`
	}
	status, err := client.get(path, &detail)
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		info.NeedToken = true
		info.Error = "GitHub 拒绝访问（401/403），请填一个有仓库读取权限的令牌"
		return info
	case status == http.StatusNotFound:
		// GitHub 对私有仓库统一回 404，分不清「没有」还是「没权限」。
		info.NeedToken = true
		info.Error = "GitHub 上找不到这个仓库；如果它是私有的，需要填令牌"
		return info
	case err != nil:
		info.Error = "读 GitHub 仓库信息失败：" + err.Error()
		return info
	}
	info.Private = detail.Private
	info.Description = detail.Description
	info.DefaultBranch = detail.DefaultBranch
	info.Stars = detail.Stars
	info.OpenIssues = detail.OpenIssues
	info.Language = detail.Language
	info.PushedAt = detail.PushedAt
	if detail.HTMLURL != "" {
		info.URL = detail.HTMLURL
	}

	var commits []struct {
		SHA     string `json:"sha"`
		HTMLURL string `json:"html_url"`
		Commit  struct {
			Message string `json:"message"`
			Author  struct {
				Name string `json:"name"`
				Date string `json:"date"`
			} `json:"author"`
		} `json:"commit"`
	}
	if _, err := client.get(path+"/commits?per_page=5", &commits); err != nil {
		info.Warning = "读最近提交失败：" + err.Error()
	} else {
		for _, item := range commits {
			info.Commits = append(info.Commits, repoCommit{
				SHA:     item.SHA,
				Message: firstLine(item.Commit.Message),
				Author:  item.Commit.Author.Name,
				Date:    item.Commit.Author.Date,
				URL:     item.HTMLURL,
			})
		}
	}

	var pulls []struct {
		Number    int    `json:"number"`
		Title     string `json:"title"`
		HTMLURL   string `json:"html_url"`
		UpdatedAt string `json:"updated_at"`
		User      struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	if _, err := client.get(path+"/pulls?state=open&per_page=5", &pulls); err != nil {
		if info.Warning == "" {
			info.Warning = "读合并请求失败：" + err.Error()
		}
	} else {
		for _, item := range pulls {
			info.Pulls = append(info.Pulls, repoPull{
				Number:    item.Number,
				Title:     item.Title,
				Author:    item.User.Login,
				URL:       item.HTMLURL,
				UpdatedAt: item.UpdatedAt,
			})
		}
	}
	return info
}

// fetchAtomGitRepo 查 AtomGit：仓库详情（要令牌）+ 最近提交 + 未合并的合并请求。
func fetchAtomGitRepo(repo projectRepo, token string) repoPlatformInfo {
	info := repoPlatformInfo{
		Platform:    repo.Platform,
		Slug:        repo.Slug,
		URL:         repo.URL,
		Commits:     []repoCommit{},
		Pulls:       []repoPull{},
		TokenSet:    token != "",
		TokenSealed: secretProtected(),
		FetchedAt:   time.Now().UnixMilli(),
	}
	client := repoAPIClient{base: atomGitAPIBase, token: token, tokenHeader: "private-token"}
	path := "/repos/" + url.PathEscape(repo.Owner) + "/" + url.PathEscape(repo.Name)

	if token != "" {
		var detail struct {
			FullName      string `json:"full_name"`
			Description   string `json:"description"`
			Private       bool   `json:"private"`
			DefaultBranch string `json:"default_branch"`
			Stars         int    `json:"stargazers_count"`
			OpenIssues    int    `json:"open_issues_count"`
			Language      string `json:"language"`
			UpdatedAt     string `json:"updated_at"`
			HTMLURL       string `json:"html_url"`
		}
		status, err := client.get(path, &detail)
		switch {
		case status == http.StatusUnauthorized || status == http.StatusForbidden:
			info.NeedToken = true
			info.Error = "AtomGit 拒绝了令牌（401/403），换一个试试"
			return info
		case err != nil:
			info.Warning = "读 AtomGit 仓库详情失败：" + err.Error()
		default:
			info.Private = detail.Private
			info.Description = detail.Description
			info.DefaultBranch = detail.DefaultBranch
			info.Stars = detail.Stars
			info.OpenIssues = detail.OpenIssues
			info.Language = detail.Language
			info.PushedAt = detail.UpdatedAt
			if detail.HTMLURL != "" {
				info.URL = detail.HTMLURL
			}
		}
	} else {
		info.Warning = "AtomGit 的仓库详情接口要用令牌，先只显示最近提交和合并请求"
	}

	var commits []struct {
		SHA     string `json:"sha"`
		HTMLURL string `json:"html_url"`
		Commit  struct {
			Message string `json:"message"`
			Author  struct {
				Name string `json:"name"`
				Date string `json:"date"`
			} `json:"author"`
		} `json:"commit"`
	}
	if status, err := client.get(path+"/commits?per_page=5", &commits); err != nil {
		switch {
		case status == http.StatusUnauthorized || status == http.StatusForbidden || status == http.StatusNotFound:
			info.NeedToken = true
			info.Error = "AtomGit 拒绝访问：私有仓库需要令牌"
		default:
			if info.Warning == "" {
				info.Warning = "读 AtomGit 最近提交失败：" + err.Error()
			}
		}
	} else {
		for _, item := range commits {
			info.Commits = append(info.Commits, repoCommit{
				SHA:     item.SHA,
				Message: firstLine(item.Commit.Message),
				Author:  item.Commit.Author.Name,
				Date:    item.Commit.Author.Date,
				URL:     item.HTMLURL,
			})
		}
	}
	if info.Error != "" {
		// 已经因为缺令牌失败了，就别再提「先显示提交」。
		info.Warning = ""
		return info
	}

	var pulls []struct {
		Number    int    `json:"number"`
		Title     string `json:"title"`
		HTMLURL   string `json:"html_url"`
		UpdatedAt string `json:"updated_at"`
		User      struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	if _, err := client.get(path+"/pulls?state=open&per_page=5", &pulls); err != nil {
		if info.Warning == "" {
			info.Warning = "读 AtomGit 合并请求失败：" + err.Error()
		}
	} else {
		for _, item := range pulls {
			info.Pulls = append(info.Pulls, repoPull{
				Number:    item.Number,
				Title:     item.Title,
				Author:    item.User.Login,
				URL:       item.HTMLURL,
				UpdatedAt: item.UpdatedAt,
			})
		}
	}
	return info
}

// firstLine 提交信息只留第一行，列表里不撑开。
func firstLine(message string) string {
	message = strings.TrimSpace(message)
	if idx := strings.IndexByte(message, '\n'); idx >= 0 {
		message = message[:idx]
	}
	return strings.TrimSpace(message)
}

// registerRepoAPIRoutes 注册平台仓库信息与令牌保存接口。
func (a *App) registerRepoAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/projects/repo", func(w http.ResponseWriter, r *http.Request) {
		root, err := a.projectRoot(r.URL.Query().Get("root"))
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		platform := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("platform")))
		if platform != repoPlatformGitHub && platform != repoPlatformAtomGit {
			writeError(w, http.StatusBadRequest, "platform 只能是 github 或 atomgit")
			return
		}
		writeJSON(w, http.StatusOK, a.repoPlatformInfoFor(root, platform))
	})

	// 令牌只在这一次请求里进来，随后加密落盘；响应只回「有没有设置」，不回显内容。
	mux.HandleFunc("POST /api/v1/repo-token", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Platform string `json:"platform"`
			Token    string `json:"token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "请求体不是 JSON")
			return
		}
		platform := strings.ToLower(strings.TrimSpace(body.Platform))
		token := strings.TrimSpace(body.Token)
		// 令牌只可能是单行短字符串，挡住粘贴整段文件或恶意超长内容。
		if len(token) > 4096 || strings.ContainsAny(token, "\r\n\x00") {
			writeError(w, http.StatusBadRequest, "令牌格式不对（应为单行、不超过 4096 字符）")
			return
		}
		switch platform {
		case repoPlatformGitHub:
			a.config.SetGitHubToken(token)
			a.updates.Invalidate()
		case repoPlatformAtomGit:
			a.config.SetAtomGitToken(token)
		default:
			writeError(w, http.StatusBadRequest, "platform 只能是 github 或 atomgit")
			return
		}
		tokenSet := a.config.GitHubTokenValue() != ""
		if platform == repoPlatformAtomGit {
			tokenSet = a.config.AtomGitTokenValue() != ""
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":        true,
			"platform":  platform,
			"tokenSet":  tokenSet,
			"protected": secretProtected(),
		})
	})
}
