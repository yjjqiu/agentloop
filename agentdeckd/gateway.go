package main

// 内置 agentgateway 的托管入口：设置、运行状态、日志。
//
// 拆成几个文件，按关注点分工：
//   - gateway.go          设置与状态（GatewaySettings / Gateway / Status / Logs）
//   - gateway_install.go  下载安装、镜像回退、sha256 校验、版本探测
//   - gateway_config.go   生成 config.yaml（唯一真相源是 config.json）
//   - gateway_process.go  进程托管：启动、认领、停止、日志泵
//   - gateway_route.go    终端接管：把选中 CLI 的上游地址指到网关
//   - gateway_metrics.go  指标抓取与归并
//
// 设计原则：
//   - 接管是按 CLI 选的，默认不接管；只有勾选的 CLI 新开的终端才把上游地址改到网关。
//   - 接管只改「指向上游的那一项」（环境变量或 CLI 自己的 --settings），不动用户配置。
//   - 不启用就完全不启动进程，删掉这个目录也不会影响 AgentDeck 其它功能。

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// GatewaySettings 是内置 agentgateway 的托管设置，随 config.json 持久化。
type GatewaySettings struct {
	AutoStart   bool     `json:"autoStart"`          // AgentDeck 启动时自动拉起网关
	Port        int      `json:"port"`               // 业务端口，能解析协议的 CLI 对接它
	ProxyPort   int      `json:"proxyPort"`          // 代理端口，原样转发的 CLI 对接它
	AdminPort   int      `json:"adminPort"`          // 管理台/指标端口
	Upstream    string   `json:"upstream"`           // 上游 LLM 网关，默认指向本机 magpie
	UpstreamKey string   `json:"upstreamKey"`        // 上游鉴权 key，回环短串
	BinPath     string   `json:"binPath"`            // 自定义二进制路径（留空用托管目录里的那份）
	Takeover    []string `json:"takeover,omitempty"` // 经网关转一手的 CLI id，空表示都不接管
}

// gatewayDefaultVersion 是内置安装默认拉的版本。
// agentgateway 迭代很快、配置结构还在变动，所以固定版本而不是跟 latest 漂。
const gatewayDefaultVersion = "1.7.0"

// gatewayRepo 是发布源。
const gatewayRepo = "agentgateway/agentgateway"

// gatewayMaxLogs 是内存里保留的日志行数。
const gatewayMaxLogs = 300

// defaultGatewaySettings 给出可用的默认值：端口避开 magpie 的 3425/3430。
func defaultGatewaySettings() GatewaySettings {
	return GatewaySettings{
		Port:        4000,
		ProxyPort:   4001,
		AdminPort:   15000,
		Upstream:    "http://127.0.0.1:3425",
		UpstreamKey: "magpie",
	}
}

// normalizeGatewaySettings 补齐零值，保证老配置文件也能用。
func normalizeGatewaySettings(s GatewaySettings) GatewaySettings {
	def := defaultGatewaySettings()
	if s.Port <= 0 || s.Port > 65535 {
		s.Port = def.Port
	}
	if s.ProxyPort <= 0 || s.ProxyPort > 65535 {
		s.ProxyPort = def.ProxyPort
	}
	if s.AdminPort <= 0 || s.AdminPort > 65535 {
		s.AdminPort = def.AdminPort
	}
	if strings.TrimSpace(s.Upstream) == "" {
		s.Upstream = def.Upstream
	}
	s.Upstream = strings.TrimRight(strings.TrimSpace(s.Upstream), "/")
	s.UpstreamKey = strings.TrimSpace(s.UpstreamKey)
	if s.UpstreamKey == "" {
		// 默认上游是本机 magpie，回环请求用短串即可；换别家端点时在界面里改。
		s.UpstreamKey = def.UpstreamKey
	}
	s.Takeover = normalizeTakeover(s.Takeover)
	return s
}

// GatewayJob 是二进制下载任务的进度快照。
type GatewayJob struct {
	Running   bool   `json:"running"`
	Phase     string `json:"phase,omitempty"`
	Done      int64  `json:"done"`
	Total     int64  `json:"total"`
	Error     string `json:"error,omitempty"`
	StartedAt int64  `json:"startedAt,omitempty"`
	DoneAt    int64  `json:"doneAt,omitempty"`
}

// GatewayStatus 是给界面用的运行时视图。
type GatewayStatus struct {
	Installed  bool     `json:"installed"`
	Running    bool     `json:"running"`
	PID        int      `json:"pid,omitempty"`
	Version    string   `json:"version,omitempty"`
	Port       int      `json:"port"`
	ProxyPort  int      `json:"proxyPort"`
	AdminPort  int      `json:"adminPort"`
	URL        string   `json:"url"`
	ProxyURL   string   `json:"proxyUrl"`
	AdminURL   string   `json:"adminUrl"`
	Upstream   string   `json:"upstream"`
	BinPath    string   `json:"binPath"`
	ConfigPath string   `json:"configPath"`
	DataDir    string   `json:"dataDir"`
	StartedAt  int64    `json:"startedAt,omitempty"`
	Error      string   `json:"error,omitempty"`
	Takeover   []string `json:"takeover,omitempty"` // 当前被接管的 CLI
}

// Gateway 托管一个 agentgateway 子进程。
type Gateway struct {
	app *App

	mu        sync.Mutex
	cmd       *exec.Cmd
	pid       int
	logs      []string
	startErr  string
	startedAt time.Time
	ver       string
	verAt     time.Time

	jobMu sync.Mutex
	job   GatewayJob
}

// NewGateway 创建网关托管器。
func NewGateway(app *App) *Gateway {
	return &Gateway{app: app}
}

// Settings 返回补齐默认值后的设置。
func (g *Gateway) Settings() GatewaySettings {
	return normalizeGatewaySettings(g.app.config.GatewaySettingsValue())
}

// SaveSettings 校验并持久化设置。
func (g *Gateway) SaveSettings(next GatewaySettings) (GatewaySettings, error) {
	next = normalizeGatewaySettings(next)
	if next.Port == next.AdminPort {
		return next, fmt.Errorf("业务端口和管理端口不能相同")
	}
	if next.Port == next.ProxyPort || next.AdminPort == next.ProxyPort {
		return next, fmt.Errorf("代理端口要和业务端口、管理端口分开")
	}
	if next.ProxyPort != 0 && portOpen(next.ProxyPort) && next.ProxyPort != g.Settings().ProxyPort {
		return next, fmt.Errorf("代理端口 %d 已被别的程序占用", next.ProxyPort)
	}
	if !strings.HasPrefix(next.Upstream, "http://") && !strings.HasPrefix(next.Upstream, "https://") {
		return next, fmt.Errorf("上游地址要以 http:// 或 https:// 开头")
	}
	g.app.config.SetGatewaySettings(next)
	return next, nil
}

// Dir 是托管的运行目录（二进制、配置、日志、pid 都放这）。
func (g *Gateway) Dir() string {
	return filepath.Join(g.app.dataDir, "gateway")
}

// ConfigPath 是生成出来的 agentgateway 配置文件。
func (g *Gateway) ConfigPath() string {
	return filepath.Join(g.Dir(), "config.yaml")
}

// pidPath 记录最近一次拉起的进程号，用来识别“上次没退干净”。
func (g *Gateway) pidPath() string {
	return filepath.Join(g.Dir(), "agentgateway.pid")
}

// BinPath 返回实际使用的二进制路径：用户指定 > 托管目录。
func (g *Gateway) BinPath() string {
	if s := strings.TrimSpace(g.Settings().BinPath); s != "" {
		return s
	}
	name := "agentgateway"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(g.Dir(), name)
}

// Installed 判断二进制是否就位。
func (g *Gateway) Installed() bool {
	info, err := os.Stat(g.BinPath())
	return err == nil && !info.IsDir()
}

// Status 汇总状态给界面。
func (g *Gateway) Status() GatewayStatus {
	s := g.Settings()
	g.mu.Lock()
	if g.pid == 0 {
		g.adoptLocked()
	}
	if g.pid > 0 && !processAlive(g.pid) {
		g.pid, g.cmd = 0, nil
	}
	pid, startedAt, startErr, logs := g.pid, g.startedAt, g.startErr, append([]string(nil), g.logs...)
	g.mu.Unlock()

	st := GatewayStatus{
		Installed:  g.Installed(),
		Running:    pid > 0,
		PID:        pid,
		Port:       s.Port,
		ProxyPort:  s.ProxyPort,
		AdminPort:  s.AdminPort,
		URL:        fmt.Sprintf("http://127.0.0.1:%d", s.Port),
		ProxyURL:   fmt.Sprintf("http://127.0.0.1:%d", s.ProxyPort),
		AdminURL:   fmt.Sprintf("http://127.0.0.1:%d", s.AdminPort),
		Upstream:   s.Upstream,
		BinPath:    g.BinPath(),
		ConfigPath: g.ConfigPath(),
		DataDir:    g.Dir(),
		Error:      startErr,
		Takeover:   s.Takeover,
	}
	if pid > 0 && !startedAt.IsZero() {
		st.StartedAt = startedAt.UnixMilli()
	}
	if st.Installed {
		st.Version = g.Version()
	}
	if !st.Running && len(logs) > 0 {
		st.Error = strings.Join(lastLines(logs, 3), " ⏎ ")
	}
	return st
}

// Logs 返回最近日志（给界面的日志面板）。
func (g *Gateway) Logs(limit int) []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if limit <= 0 || limit > len(g.logs) {
		limit = len(g.logs)
	}
	out := make([]string, 0, limit)
	return append(out, g.logs[len(g.logs)-limit:]...)
}

func lastLines(lines []string, n int) []string {
	if n <= 0 || n > len(lines) {
		n = len(lines)
	}
	return lines[len(lines)-n:]
}
