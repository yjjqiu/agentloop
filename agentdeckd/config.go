package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// 项目默认指向仓库里的 LoopForge Local 入口（Windows），可在配置里覆盖；
// 其它平台没有约定路径，默认留空，见 localbin_unix.go。

// CliOverride 是某个 CLI 的本地覆盖设置（路径 / 默认参数 / npm 包名 / 是否启用）。
type CliOverride struct {
	Path    string   `json:"path,omitempty"`
	Args    []string `json:"args,omitempty"`
	Package string   `json:"package,omitempty"` // npm 包名，未安装时用于一键安装
	Enabled *bool    `json:"enabled,omitempty"`
}

// Config 是可持久化的本地配置。
type Config struct {
	mu           sync.RWMutex
	path         string
	LastCwd      string                    `json:"lastCwd"`
	Projects     []string                  `json:"projects"`
	Active       string                    `json:"activeProject"`
	LocalBin     string                    `json:"localBin"`
	Pipelines    map[string]PipelineConfig `json:"pipelines,omitempty"`
	CliOverrides map[string]CliOverride    `json:"cliOverrides,omitempty"`
	AuthToken    string                    `json:"authToken,omitempty"`   // 对外监听时的访问令牌
	GitHubToken  string                    `json:"githubToken,omitempty"` // 私有仓库查更新用的只读 token
}

// LoadConfig 读配置，文件不存在就用默认值。
func LoadConfig(dir string) *Config {
	cfg := &Config{
		path:         filepath.Join(dir, "config.json"),
		LocalBin:     defaultLocalBin,
		Pipelines:    map[string]PipelineConfig{},
		CliOverrides: map[string]CliOverride{},
	}
	raw, err := os.ReadFile(cfg.path)
	if err != nil {
		return cfg
	}
	if err := json.Unmarshal(raw, cfg); err != nil {
		log.Printf("配置格式错误，使用默认值: %v", err)
		return cfg
	}
	if cfg.LocalBin == "" && defaultLocalBin != "" {
		cfg.LocalBin = defaultLocalBin
	}
	cfg.LocalBin = normalizeLocalBin(cfg.LocalBin)
	if cfg.Pipelines == nil {
		cfg.Pipelines = map[string]PipelineConfig{}
	}
	if cfg.CliOverrides == nil {
		cfg.CliOverrides = map[string]CliOverride{}
	}
	return cfg
}

func (c *Config) saveLocked() {
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return
	}
	// 配置里有访问令牌和 GitHub token，只给当前用户读写。
	if err := os.WriteFile(c.path, raw, 0o600); err != nil {
		return
	}
	_ = os.Chmod(c.path, 0o600)
}

// GitHubTokenValue 返回私有仓库更新检查用的只读 token（可能为空）。
func (c *Config) GitHubTokenValue() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.GitHubToken
}

// SetGitHubToken 写入只读 token 并立即落盘；空字符串表示清除。
func (c *Config) SetGitHubToken(token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.GitHubToken = strings.TrimSpace(token)
	c.saveLocked()
}

// AuthTokenValue 返回持久化的访问令牌（可能为空）。
func (c *Config) AuthTokenValue() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.AuthToken
}

// SetAuthToken 写入访问令牌并立即落盘。
func (c *Config) SetAuthToken(token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.AuthToken = token
	c.saveLocked()
}

// existsProjects 过滤掉已经不存在的目录。
func (c *Config) existsProjects() []string {
	out := make([]string, 0, len(c.Projects))
	for _, p := range c.Projects {
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			out = append(out, p)
		}
	}
	return out
}

// AddProject 去重后追加。
func (c *Config) AddProject(path string) error {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("目录不存在: %s", path)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, p := range c.Projects {
		if samePath(p, path) {
			return nil
		}
	}
	c.Projects = append(c.Projects, path)
	c.saveLocked()
	return nil
}

// RemoveProject 从列表移除，必要时把 active 切走。
func (c *Config) RemoveProject(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	next := make([]string, 0, len(c.Projects))
	for _, p := range c.Projects {
		if !samePath(p, path) {
			next = append(next, p)
		}
	}
	c.Projects = next
	// 移除项目时顺手清掉它的流水线配置，避免重新添加同名目录时带出旧映射。
	if c.Pipelines != nil {
		delete(c.Pipelines, filepath.Clean(path))
	}
	if samePath(c.Active, path) {
		c.Active = ""
		if len(next) > 0 {
			c.Active = next[0]
		}
	}
	c.saveLocked()
}

// SetActive 切换当前项目，并持久化到配置文件。
func (c *Config) SetActive(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if path != "" {
		// 统一成平台路径分隔符，避免客户端传正斜杠后和项目列表里的反斜杠路径对不上。
		path = filepath.Clean(path)
	}
	c.Active = path
	c.saveLocked()
}

// SetLastCwd 记住最近一次新建终端的工作目录。
func (c *Config) SetLastCwd(path string) {
	if path == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if samePath(c.LastCwd, path) {
		return
	}
	c.LastCwd = path
	c.saveLocked()
}

// SetLocalBin 记录 LoopForge Local 的入口脚本路径。
func (c *Config) SetLocalBin(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.LocalBin = path
	c.saveLocked()
}

// GetCliOverride 读取某个 CLI 的覆盖设置。
func (c *Config) GetCliOverride(id string) CliOverride {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.CliOverrides[id]
}

// CliOverridesSnapshot 返回覆盖设置副本，交给探测逻辑使用。
func (c *Config) CliOverridesSnapshot() map[string]CliOverride {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string]CliOverride, len(c.CliOverrides))
	for id, ov := range c.CliOverrides {
		out[id] = ov
	}
	return out
}

// SetCliOverride 保存覆盖设置；清空且启用时删除条目，回到默认。
func (c *Config) SetCliOverride(id string, ov CliOverride) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.CliOverrides == nil {
		c.CliOverrides = map[string]CliOverride{}
	}
	if ov.Path == "" && ov.Package == "" && len(ov.Args) == 0 && (ov.Enabled == nil || *ov.Enabled) {
		delete(c.CliOverrides, id)
	} else {
		c.CliOverrides[id] = ov
	}
	c.saveLocked()
}

// ResetCliOverride 恢复某个 CLI 的默认设置。
func (c *Config) ResetCliOverride(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.CliOverrides, id)
	c.saveLocked()
}

// SetPipeline 保存某个项目的阶段→CLI 映射。
func (c *Config) SetPipeline(root string, cfg PipelineConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Pipelines == nil {
		c.Pipelines = map[string]PipelineConfig{}
	}
	c.Pipelines[filepath.Clean(root)] = cfg
	c.saveLocked()
}

func samePath(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b) || filepath.Clean(a) == filepath.Clean(filepath.FromSlash(b))
}

// —— HTTP 小工具 ——

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeSSE(w http.ResponseWriter, event Event) {
	raw, err := json.Marshal(event)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "data: %s\n\n", raw)
}

// withCORS 只处理预检，不再给跨域来源发 ACAO。
// 界面始终和 API 同源（Electron / 浏览器 / 反代都是），放开通配会让任意网页读写本机接口。
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// noStore 关掉缓存，改完界面刷新就能看到最新文件。
func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		next.ServeHTTP(w, r)
	})
}

func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.URL.Path) >= 5 && r.URL.Path[:5] == "/api/" {
			start := time.Now()
			next.ServeHTTP(w, r)
			log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
			return
		}
		next.ServeHTTP(w, r)
	})
}
