package main

// repo_api_test.go：用本地 httptest 服务模拟 GitHub / AtomGit 的返回，验证解析与鉴权头。
// 不访问真实平台，也不涉及任何真实令牌。

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchGitHubRepo(t *testing.T) {
	var authHeader string
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/owner/repo", func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"description":       "示例仓库",
			"private":           true,
			"default_branch":    "main",
			"stargazers_count":  7,
			"open_issues_count": 3,
			"language":          "Go",
			"pushed_at":         "2026-11-20T10:00:00Z",
			"html_url":          "https://github.com/owner/repo",
		})
	})
	mux.HandleFunc("/repos/owner/repo/commits", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{
				"sha":      "abcdef1234567890",
				"html_url": "https://github.com/owner/repo/commit/abcdef1",
				"commit": map[string]any{
					"message": "fix: 第一行\n\n第二行不显示",
					"author":  map[string]any{"name": "Alice", "date": "2026-11-19T09:00:00Z"},
				},
			},
		})
	})
	mux.HandleFunc("/repos/owner/repo/pulls", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{
				"number":     12,
				"title":      "加一个入口",
				"html_url":   "https://github.com/owner/repo/pull/12",
				"updated_at": "2026-11-21T08:00:00Z",
				"user":       map[string]any{"login": "bob"},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	restore := setAPIBase(&githubAPIBase, srv.URL)
	defer restore()

	info := fetchGitHubRepo(projectRepo{
		Platform: repoPlatformGitHub, Owner: "owner", Name: "repo",
		Slug: "owner/repo", URL: "https://github.com/owner/repo",
	}, "test-token")

	if authHeader != "Bearer test-token" {
		t.Errorf("Authorization = %q, 期望 Bearer test-token", authHeader)
	}
	if info.Private != true || info.DefaultBranch != "main" || info.Stars != 7 || info.OpenIssues != 3 {
		t.Errorf("详情解析不对: %#v", info)
	}
	if len(info.Commits) != 1 || info.Commits[0].Message != "fix: 第一行" || info.Commits[0].Author != "Alice" {
		t.Errorf("提交解析不对: %#v", info.Commits)
	}
	if len(info.Pulls) != 1 || info.Pulls[0].Number != 12 || info.Pulls[0].Author != "bob" {
		t.Errorf("PR 解析不对: %#v", info.Pulls)
	}
	if !info.TokenSet || info.Error != "" {
		t.Errorf("令牌状态不对: %#v", info)
	}
}

func TestFetchGitHubPrivateWithoutToken(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/owner/repo", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	defer setAPIBase(&githubAPIBase, srv.URL)()

	info := fetchGitHubRepo(projectRepo{
		Platform: repoPlatformGitHub, Owner: "owner", Name: "repo", Slug: "owner/repo",
	}, "")
	if !info.NeedToken {
		t.Fatalf("404 时应提示需要令牌: %#v", info)
	}
	if info.TokenSet {
		t.Error("没有令牌时 TokenSet 应为 false")
	}
}

func TestFetchAtomGitRepo(t *testing.T) {
	var tokenHeader string
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/owner/repo", func(w http.ResponseWriter, r *http.Request) {
		tokenHeader = r.Header.Get("private-token")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"description":       "原子仓库",
			"private":           true,
			"default_branch":    "master",
			"stargazers_count":  2,
			"open_issues_count": 1,
			"language":          "C++",
			"updated_at":        "2026-11-22T12:00:00+08:00",
		})
	})
	mux.HandleFunc("/repos/owner/repo/commits", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{
				"sha":      "1234567890abcdef",
				"html_url": "https://atomgit.com/owner/repo/commits/detail/1234567",
				"commit": map[string]any{
					"message": "feat: 新增仓库入口\n\n正文",
					"author":  map[string]any{"name": "Carol", "date": "2026-11-22T11:00:00+08:00"},
				},
			},
		})
	})
	mux.HandleFunc("/repos/owner/repo/pulls", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{
				"number":     88,
				"title":      "合并请求标题",
				"html_url":   "https://atomgit.com/owner/repo/merge_requests/88",
				"updated_at": "2026-11-23T09:00:00+08:00",
				"user":       map[string]any{"login": "dave"},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	defer setAPIBase(&atomGitAPIBase, srv.URL)()

	info := fetchAtomGitRepo(projectRepo{
		Platform: repoPlatformAtomGit, Owner: "owner", Name: "repo",
		Slug: "owner/repo", URL: "https://atomgit.com/owner/repo",
	}, "ag-token")

	if tokenHeader != "ag-token" {
		t.Errorf("private-token = %q, 期望 ag-token", tokenHeader)
	}
	if !info.Private || info.DefaultBranch != "master" || info.Stars != 2 {
		t.Errorf("详情解析不对: %#v", info)
	}
	if len(info.Commits) != 1 || info.Commits[0].Message != "feat: 新增仓库入口" || info.Commits[0].Author != "Carol" {
		t.Errorf("提交解析不对: %#v", info.Commits)
	}
	if len(info.Pulls) != 1 || info.Pulls[0].Number != 88 || info.Pulls[0].Author != "dave" {
		t.Errorf("PR 解析不对: %#v", info.Pulls)
	}
}

func TestFetchAtomGitPrivateWithoutToken(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/owner/repo/commits", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error_message":"403 Forbidden - Unauthorized access"}`, http.StatusForbidden)
	})
	mux.HandleFunc("/repos/owner/repo/pulls", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error_message":"403 Forbidden"}`, http.StatusForbidden)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	defer setAPIBase(&atomGitAPIBase, srv.URL)()

	info := fetchAtomGitRepo(projectRepo{
		Platform: repoPlatformAtomGit, Owner: "owner", Name: "repo", Slug: "owner/repo",
	}, "")
	if !info.NeedToken {
		t.Fatalf("403 时应提示需要令牌: %#v", info)
	}
	// 已经因为缺令牌报错时，不再叠一条「先显示提交」的提示。
	if info.Warning != "" {
		t.Errorf("有错误时不该再给警告，Warning=%q", info.Warning)
	}
	if !strings.Contains(info.Error, "令牌") {
		t.Errorf("错误信息应说明需要令牌，Error=%q", info.Error)
	}
}

func TestRepoAPIClientNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := repoAPIClient{base: srv.URL}
	status, err := client.get("/x", &struct{}{})
	if status != http.StatusInternalServerError || err == nil {
		t.Fatalf("应返回 500 和错误，得到 %d / %v", status, err)
	}
}

// setAPIBase 临时改平台地址（测试专用），返回恢复函数。
func setAPIBase(target *string, value string) func() {
	old := *target
	*target = value
	return func() { *target = old }
}
