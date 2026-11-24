package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// UpdateAsset 是发布里的一个下载文件。
type UpdateAsset struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Size int64  `json:"size,omitempty"`
}

// UpdateInfo 是版本检查结果，直接给设置页的「关于」使用。
type UpdateInfo struct {
	Current     string        `json:"current"`
	Latest      string        `json:"latest,omitempty"`
	Available   bool          `json:"available"`
	URL         string        `json:"url,omitempty"` // 发布页
	Notes       string        `json:"notes,omitempty"`
	PublishedAt string        `json:"publishedAt,omitempty"`
	Assets      []UpdateAsset `json:"assets,omitempty"`
	CheckedAt   int64         `json:"checkedAt,omitempty"`
	Error       string        `json:"error,omitempty"`
}

// UpdateService 从 GitHub Releases 检查新版本，并缓存结果避免频繁请求。
type UpdateService struct {
	mu        sync.Mutex
	repo      string
	token     func() string
	client    *http.Client
	cache     *UpdateInfo
	expiresAt time.Time
}

// NewUpdateService 创建检查器；repo 形如 owner/name，token 返回只读访问令牌（可为空）。
func NewUpdateService(repo string, token func() string) *UpdateService {
	return &UpdateService{
		repo:   strings.TrimSpace(repo),
		token:  token,
		client: &http.Client{Timeout: 12 * time.Second},
	}
}

// Repo 返回发布仓库标识。
func (s *UpdateService) Repo() string { return s.repo }

// Invalidate 清缓存，令牌变更后让下一次检查立刻重新请求。
func (s *UpdateService) Invalidate() {
	s.mu.Lock()
	s.cache, s.expiresAt = nil, time.Time{}
	s.mu.Unlock()
}

// githubToken 返回可用的只读令牌：环境变量优先（服务器部署），其次配置文件（设置页填写）。
func (s *UpdateService) githubToken() string {
	if token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); token != "" {
		return token
	}
	if s.token != nil {
		return strings.TrimSpace(s.token())
	}
	return ""
}

// Check 返回最新版本；force 为 true 时忽略缓存。
func (s *UpdateService) Check(force bool) UpdateInfo {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !force && s.cache != nil && time.Now().Before(s.expiresAt) {
		return *s.cache
	}
	info := UpdateInfo{Current: version}
	if s.repo == "" {
		info.Error = "没有配置发布仓库（AGENTDECK_REPO）"
		return info
	}

	release, err := s.latestRelease()
	if err != nil {
		info.Error = err.Error()
		info.CheckedAt = time.Now().UnixMilli()
		// 失败也短暂缓存，避免每次打开设置都打一次网络。
		s.cache, s.expiresAt = &info, time.Now().Add(2*time.Minute)
		return info
	}

	latest := normalizeVersion(release.TagName)
	info.Latest = latest
	info.URL = release.HTMLURL
	info.Notes = release.Body
	info.PublishedAt = release.PublishedAt
	info.CheckedAt = time.Now().UnixMilli()
	info.Available = latest != "" && compareVersion(latest, version) > 0
	for _, asset := range release.Assets {
		info.Assets = append(info.Assets, UpdateAsset{Name: asset.Name, URL: asset.BrowserDownloadURL, Size: asset.Size})
	}
	s.cache, s.expiresAt = &info, time.Now().Add(30*time.Minute)
	return info
}

// githubRelease 是 GitHub Releases API 的子集。
type githubRelease struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	Body        string `json:"body"`
	HTMLURL     string `json:"html_url"`
	PublishedAt string `json:"published_at"`
	Assets      []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
		Size               int64  `json:"size"`
	} `json:"assets"`
}

// latestRelease 拉取最新正式发布。
func (s *UpdateService) latestRelease() (*githubRelease, error) {
	req, err := http.NewRequest(http.MethodGet, "https://api.github.com/repos/"+s.repo+"/releases/latest", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "AgentDeck/"+version)
	token := s.githubToken()
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("连接 GitHub 失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		if token == "" {
			return nil, fmt.Errorf("仓库 %s 不存在或无权访问（私有仓库请在「设置 → 关于」填只读 Token）", s.repo)
		}
		return nil, fmt.Errorf("仓库 %s 没有正式发布，或 Token 无权访问此仓库", s.repo)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("GitHub 返回 %s：Token 无效、过期或没有 Contents 读权限", resp.Status)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub 返回 %s（可能触发了限流）", resp.Status)
	}
	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("解析发布信息失败: %w", err)
	}
	if strings.TrimSpace(release.TagName) == "" {
		return nil, fmt.Errorf("发布信息里没有版本号")
	}
	return &release, nil
}

// compareVersion 比较点分版本号，返回 a 相对 b 的大小。
func compareVersion(a, b string) int {
	as := strings.Split(normalizeVersion(a), ".")
	bs := strings.Split(normalizeVersion(b), ".")
	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		var av, bv int
		if i < len(as) {
			av, _ = strconv.Atoi(strings.SplitN(as[i], "-", 2)[0])
		}
		if i < len(bs) {
			bv, _ = strconv.Atoi(strings.SplitN(bs[i], "-", 2)[0])
		}
		if av != bv {
			if av > bv {
				return 1
			}
			return -1
		}
	}
	return 0
}
