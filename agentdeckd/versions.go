package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// CliVersion 是一款 CLI 的版本探测结果。
type CliVersion struct {
	Installed   string `json:"installed,omitempty"`   // 本机版本
	Latest      string `json:"latest,omitempty"`      // npm 上的最新版本
	Package     string `json:"package,omitempty"`     // 对应的 npm 包名
	Outdated    bool   `json:"outdated,omitempty"`    // 有更新
	ProbeError  string `json:"probeError,omitempty"`  // --version 探测失败原因
	LatestError string `json:"latestError,omitempty"` // 查询最新版失败原因
	CheckedAt   int64  `json:"checkedAt,omitempty"`   // 毫秒时间戳
}

// VersionChecker 缓存 CLI 版本，避免每次开设置页都拉一遍进程和网络。
type VersionChecker struct {
	mu      sync.Mutex
	cache   map[string]CliVersion
	expires time.Time
	ttl     time.Duration

	npmMu     sync.Mutex
	npmBins   map[string]npmPackage
	npmExpire time.Time
}

// npmPackage 是全局 node_modules 里的一款包。
type npmPackage struct {
	Name    string
	Version string
}

const (
	versionProbeTimeout = 4 * time.Second
	versionFetchTimeout = 6 * time.Second
	versionCacheTTL     = 10 * time.Minute
	versionConcurrency  = 8
)

// NewVersionChecker 创建带缓存的版本探测器。
func NewVersionChecker() *VersionChecker {
	return &VersionChecker{ttl: versionCacheTTL}
}

// PackageFor 返回 bin 对应的 npm 包名（没有映射时 ok 为 false）。
func (c *VersionChecker) PackageFor(bin string) (string, bool) {
	pkg, ok := c.scanNpmBins()[strings.ToLower(bin)]
	if !ok {
		return "", false
	}
	return pkg.Name, true
}

// IsNpmManaged 判断二进制是否来自 npm 全局安装；自管理安装的 CLI（如 pi 的 ~/.pi）用 npm 更新不会生效。
// 注意：可执行文件目录与 node_modules 目录的层级关系分平台不同，见 npmBinDir。
func (c *VersionChecker) IsNpmManaged(path string) bool {
	root := npmGlobalRoot()
	if path == "" || root == "" {
		return false
	}
	binDir := strings.ToLower(npmBinDir(root))
	return strings.HasPrefix(strings.ToLower(filepath.Clean(path)), binDir+string(filepath.Separator))
}

// Invalidate 清空版本缓存（改了 CLI 路径 / 参数之后调用）。
func (c *VersionChecker) Invalidate() {
	c.mu.Lock()
	c.cache = nil
	c.expires = time.Time{}
	c.mu.Unlock()
	c.npmMu.Lock()
	c.npmBins = nil
	c.npmExpire = time.Time{}
	c.npmMu.Unlock()
}

// Versions 返回 CLI 的版本信息（含未安装但在设置里填了 npm 包名的）。
// packages 是 id → npm 包名；force 为 true 时忽略缓存。
func (c *VersionChecker) Versions(agents []AgentStatus, packages map[string]string, force bool) (map[string]CliVersion, int64) {
	c.mu.Lock()
	if !force && c.cache != nil && time.Now().Before(c.expires) {
		out := cloneVersions(c.cache)
		checked := c.expires.Add(-c.ttl).UnixMilli()
		c.mu.Unlock()
		return out, checked
	}
	c.mu.Unlock()

	npmBins := c.scanNpmBins()
	out := make(map[string]CliVersion)
	var outMu sync.Mutex
	sem := make(chan struct{}, versionConcurrency)
	var wg sync.WaitGroup
	now := time.Now()

	for _, agent := range agents {
		pkg := packages[agent.ID]
		if agent.Path == "" && pkg == "" {
			continue
		}
		wg.Add(1)
		go func(agent AgentStatus, pkg string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			result := CliVersion{CheckedAt: now.UnixMilli(), Package: pkg}
			if agent.Path != "" {
				if installed, err := probeCliVersion(agent.Path); err != nil {
					result.ProbeError = err.Error()
				} else {
					result.Installed = installed
				}
			}
			// 探测不到时用全局 node_modules 里的版本兜底。
			if result.Installed == "" && pkg != "" {
				if meta, ok := npmBins[strings.ToLower(agent.Bin)]; ok && meta.Version != "" {
					result.Installed = meta.Version
				}
			}
			if result.Package != "" {
				if latest, err := fetchLatestVersion(result.Package); err != nil {
					result.LatestError = err.Error()
				} else {
					result.Latest = latest
					result.Outdated = result.Installed != "" && compareVersions(result.Installed, latest) < 0
				}
			}
			outMu.Lock()
			out[agent.ID] = result
			outMu.Unlock()
		}(agent, pkg)
	}
	wg.Wait()

	c.mu.Lock()
	c.cache = out
	c.expires = time.Now().Add(c.ttl)
	c.mu.Unlock()
	return cloneVersions(out), now.UnixMilli()
}

// scanNpmBins 扫描全局 node_modules，建立 bin 名 → npm 包 的映射（带缓存）。
func (c *VersionChecker) scanNpmBins() map[string]npmPackage {
	c.npmMu.Lock()
	defer c.npmMu.Unlock()
	if c.npmBins != nil && time.Now().Before(c.npmExpire) {
		return c.npmBins
	}
	bins := make(map[string]npmPackage)
	root := npmGlobalRoot()
	if root != "" {
		for _, name := range readPackageDirs(root) {
			if pkg, ok := readNpmPackage(filepath.Join(root, name)); ok {
				for _, bin := range pkg.bins {
					bins[strings.ToLower(bin)] = npmPackage{Name: pkg.name, Version: pkg.version}
				}
			}
		}
	}
	c.npmBins = bins
	c.npmExpire = time.Now().Add(versionCacheTTL)
	return bins
}

type npmPackageJSON struct {
	name    string
	version string
	bins    []string
}

// readPackageDirs 返回 node_modules 下的包目录（含 @scope/xxx 一层）。
func readPackageDirs(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	dirs := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if strings.HasPrefix(entry.Name(), "@") {
			scoped, err := os.ReadDir(filepath.Join(root, entry.Name()))
			if err != nil {
				continue
			}
			for _, sub := range scoped {
				if sub.IsDir() {
					dirs = append(dirs, filepath.Join(entry.Name(), sub.Name()))
				}
			}
			continue
		}
		dirs = append(dirs, entry.Name())
	}
	return dirs
}

func readNpmPackage(dir string) (npmPackageJSON, bool) {
	raw, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return npmPackageJSON{}, false
	}
	var meta struct {
		Name    string          `json:"name"`
		Version string          `json:"version"`
		Bin     json.RawMessage `json:"bin"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil || meta.Name == "" {
		return npmPackageJSON{}, false
	}
	pkg := npmPackageJSON{name: meta.Name, version: meta.Version}
	if len(meta.Bin) > 0 {
		var single string
		var many map[string]string
		if json.Unmarshal(meta.Bin, &single) == nil && single != "" {
			pkg.bins = []string{packageBaseName(meta.Name)}
		} else if json.Unmarshal(meta.Bin, &many) == nil {
			for bin := range many {
				pkg.bins = append(pkg.bins, bin)
			}
		}
	}
	return pkg, true
}

// packageBaseName 取出 @scope/name 里的 name。
func packageBaseName(name string) string {
	if idx := strings.LastIndex(name, "/"); idx >= 0 {
		return name[idx+1:]
	}
	return name
}

// versionPattern 从 --version 输出里抓版本号。
var versionPattern = regexp.MustCompile(`\d+\.\d+\.\d+(?:-[0-9A-Za-z.\-]+)?`)

// probeCliVersion 执行 <cli> --version 并解析版本号，超时直接杀掉。
func probeCliVersion(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), versionProbeTimeout)
	defer cancel()

	cmd := versionProbeCommand(ctx, path)
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("--version 超时（%s）", versionProbeTimeout)
	}
	if match := versionPattern.FindString(string(out)); match != "" {
		return match, nil
	}
	if err != nil {
		return "", fmt.Errorf("--version 执行失败")
	}
	return "", fmt.Errorf("输出里没有版本号")
}

// fetchLatestVersion 查询 npm 上的最新版本，国内镜像优先。
func fetchLatestVersion(pkg string) (string, error) {
	client := &http.Client{Timeout: versionFetchTimeout}
	var lastErr error
	for _, base := range []string{"https://registry.npmmirror.com/", "https://registry.npmjs.org/"} {
		latest, err := getDistLatest(client, base+pkg+"/latest")
		if err == nil {
			return latest, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("查询失败")
	}
	return "", lastErr
}

func getDistLatest(client *http.Client, url string) (string, error) {
	res, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", res.StatusCode)
	}
	var meta struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(res.Body).Decode(&meta); err != nil {
		return "", err
	}
	if meta.Version == "" {
		return "", fmt.Errorf("响应里没有 version")
	}
	return meta.Version, nil
}

// compareVersions 比较两段版本号，返回 -1 / 0 / 1；无法比较时按字符串比较。
func compareVersions(a, b string) int {
	pa, oka := parseVersion(a)
	pb, okb := parseVersion(b)
	if !oka || !okb {
		return strings.Compare(a, b)
	}
	for i := 0; i < 3; i++ {
		if pa.nums[i] != pb.nums[i] {
			if pa.nums[i] < pb.nums[i] {
				return -1
			}
			return 1
		}
	}
	// 预发布版本（-rc.1）小于正式版本。
	switch {
	case pa.pre == pb.pre:
		return 0
	case pa.pre == "":
		return 1
	case pb.pre == "":
		return -1
	default:
		return strings.Compare(pa.pre, pb.pre)
	}
}

type parsedVersion struct {
	nums [3]int
	pre  string
}

func parseVersion(v string) (parsedVersion, bool) {
	var p parsedVersion
	core, pre := v, ""
	if idx := strings.IndexAny(core, "-+"); idx >= 0 {
		pre = core[idx+1:]
		core = core[:idx]
	}
	parts := strings.Split(core, ".")
	if len(parts) < 2 {
		return p, false
	}
	for i := 0; i < 3 && i < len(parts); i++ {
		num := 0
		for _, ch := range parts[i] {
			if ch < '0' || ch > '9' {
				return p, false
			}
			num = num*10 + int(ch-'0')
		}
		p.nums[i] = num
	}
	p.pre = pre
	return p, true
}

func cloneVersions(in map[string]CliVersion) map[string]CliVersion {
	out := make(map[string]CliVersion, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
