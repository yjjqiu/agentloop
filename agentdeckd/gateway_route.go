package main

// 终端接管：把勾选的 CLI 的上游地址指到内置网关。
// 只改启动方式（环境变量 / 启动参数），不碰 CLI 自己的配置文件。
//
// agentgateway 对客户端暴露两个口：
//   - 业务端口（ViaLLM）：agentgateway 自己解析请求，能统计 token；
//   - 代理端口（ViaProxy）：原样转发，客户端说什么协议就是什么协议，只统计请求数。
//
// Claude Code 说 Anthropic Messages，和上游 magpie 一致，走业务端口；
// Codex 说 Responses、Gemini 说原生 generateContent，agentgateway 1.7 转不了，
// 走代理端口最稳。加一款新 CLI 只需要在 gatewayRoutes 里补一行。

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// viaLLM 走业务端口：能解析协议，指标里有 token。
	viaLLM = "llm"
	// viaProxy 走代理端口：原样转发，指标里只有请求数。
	viaProxy = "proxy"
)

// gatewayEnv 是一个要注入的环境变量。
// Value 里可以写 {url}（网关地址，含该 CLI 的路径前缀）和 {key}（上游 Key）。
type gatewayEnv struct {
	Name  string
	Value string
}

// gatewayRoute 描述一款 CLI 怎么接进网关：
// 注入哪些环境变量、要不要追加启动参数、从哪个口出。
type gatewayRoute struct {
	Agent string
	Label string
	Via   string
	// Env 是要注入的环境变量；为空表示这款 CLI 只认启动参数。
	Env []gatewayEnv
	// Path 是拼在网关地址后面的路径前缀。
	Path string
	// Args 是追加的启动参数，{url} 会替换成网关地址。
	Args []string
	// Flag 非空表示这款 CLI 的设置文件优先级更高（Claude Code 的
	// settings.json 里 env 会覆盖进程环境），得额外传一个高优先级设置文件。
	Flag string
	// Note 给界面的一句话说明。
	Note string
	// Verified 表示这套接管在本机实测跑通过。
	Verified bool
}

// gatewayRoutes 是支持接管的 CLI 白名单。加一款只需要在这里补一行。
var gatewayRoutes = []gatewayRoute{
	{
		Agent: "claude-code", Label: "Claude Code", Via: viaLLM,
		Env:      []gatewayEnv{{Name: "ANTHROPIC_BASE_URL", Value: "{url}"}},
		Flag:     "--settings",
		Note:     "Anthropic Messages 直通",
		Verified: true,
	},
	{
		Agent: "codex", Label: "Codex", Via: viaProxy,
		Path:     "/backend-api/codex",
		Args:     []string{"-c", `openai_base_url="{url}"`},
		Note:     "Responses 原样转发；只加启动参数，不碰 ~/.codex/config.toml",
		Verified: true,
	},
	{
		Agent: "gemini", Label: "Gemini CLI", Via: viaProxy,
		Env: []gatewayEnv{
			{Name: "GOOGLE_GEMINI_BASE_URL", Value: "{url}"},
			// 0.71 起把鉴权方式钉成 API Key：它的 gateway 鉴权在非交互模式下
			// 会被自己的校验拒掉（Invalid auth method selected），但地址照样跟 env 走。
			{Name: "GEMINI_CLI_AUTH_OVERRIDE", Value: "gemini-api-key"},
			{Name: "GEMINI_API_KEY", Value: "{key}"},
		},
		Note:     "原生 generateContent 原样转发，顺带把鉴权方式钉成 API Key",
		Verified: true,
	},
	{
		Agent: "qwen", Label: "Qwen Code", Via: viaProxy,
		Env: []gatewayEnv{
			{Name: "OPENAI_BASE_URL", Value: "{url}/v1"},
			{Name: "OPENAI_API_KEY", Value: "{key}"},
		},
		Note:     "按 OpenAI 兼容接口转发；本机没装，未实测",
		Verified: false,
	},
	{
		Agent: "aider", Label: "Aider", Via: viaProxy,
		Env: []gatewayEnv{
			{Name: "OPENAI_API_BASE", Value: "{url}/v1"},
			{Name: "OPENAI_API_KEY", Value: "{key}"},
		},
		Note:     "按 OpenAI 兼容接口转发；启动时选 OpenAI 兼容模型，未实测",
		Verified: false,
	},
}

// gatewayRouteFor 查一款 CLI 怎么接管。
func gatewayRouteFor(agent string) (gatewayRoute, bool) {
	agent = strings.TrimSpace(agent)
	for _, r := range gatewayRoutes {
		if r.Agent == agent {
			return r, true
		}
	}
	return gatewayRoute{}, false
}

// normalizeTakeover 只保留白名单里的 CLI，去重并按白名单顺序排好。
func normalizeTakeover(list []string) []string {
	if len(list) == 0 {
		return nil
	}
	want := make(map[string]bool, len(list))
	for _, id := range list {
		want[strings.TrimSpace(id)] = true
	}
	out := make([]string, 0, len(list))
	for _, r := range gatewayRoutes {
		if want[r.Agent] {
			out = append(out, r.Agent)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// RouteRows 返回可接管的 CLI 与当前开关，界面直接渲染成勾选框。
func (g *Gateway) RouteRows() []map[string]any {
	settings := g.Settings()
	on := make(map[string]bool, len(settings.Takeover))
	for _, id := range settings.Takeover {
		on[id] = true
	}
	rows := make([]map[string]any, 0, len(gatewayRoutes))
	for _, r := range gatewayRoutes {
		names := make([]string, 0, len(r.Env))
		for _, e := range r.Env {
			names = append(names, e.Name)
		}
		rows = append(rows, map[string]any{
			"agent":    r.Agent,
			"label":    r.Label,
			"via":      r.Via,
			"env":      strings.Join(names, " + "),
			"arg":      r.Flag,
			"note":     r.Note,
			"verified": r.Verified,
			"enabled":  on[r.Agent],
		})
	}
	return rows
}

// RouteArgs 返回启动这款 CLI 时要追加的参数（只用于拼命令，不负责拉起网关）。
// 界面列 CLI、生成命令时用它，避免「只是看一眼列表」就把网关拉起来。
func (g *Gateway) RouteArgs(agent string) []string {
	route, ok := g.routedFor(agent)
	if !ok {
		return nil
	}
	_, args := g.buildArgs(route, g.routeURL(route))
	return args
}

// Takeover 返回接管这款 CLI 需要的环境变量和启动参数。
// 没勾选、不在白名单、二进制没装、网关起不来，一律返回 nil——终端按原方式启动，
// 接管失败绝不拦着用户开终端。
func (g *Gateway) Takeover(agent string) (env, args []string) {
	route, ok := g.routedFor(agent)
	if !ok || !g.ensureRunning() {
		return nil, nil
	}
	// 走代理口的 CLI 还要确认那个口真的在听：AgentDeck 升级后老进程用的配置里
	// 可能还没有代理口，重启一次让它带上新配置。
	if route.Via == viaProxy && !g.ensureProxyPort() {
		return nil, nil
	}
	return g.buildArgs(route, g.routeURL(route))
}

// buildArgs 把路由展开成真实的环境变量和参数；写设置文件失败就整体放弃接管。
func (g *Gateway) buildArgs(route gatewayRoute, url string) (env, args []string) {
	for _, e := range route.Env {
		env = append(env, e.Name+"="+g.expandEnv(e.Value, url))
	}
	for _, a := range route.Args {
		args = append(args, strings.ReplaceAll(a, "{url}", url))
	}
	if route.Flag == "" {
		return env, args
	}
	// Claude Code 的 settings.json 里有 env.ANTHROPIC_BASE_URL 时，光改进程环境会被
	// 设置文件覆盖（实测无效），额外传一个高优先级设置文件把上游指到网关。
	path, err := g.writeTakeoverFile(route, url)
	if err != nil {
		log.Printf("终端接管：写设置文件失败，%s 按原方式启动: %v", route.Agent, err)
		return nil, nil
	}
	return env, append(args, route.Flag, path)
}

// TakeoverEnabled 判断这款 CLI 是否在接管名单里（不管网关此刻能不能用）。
func (g *Gateway) TakeoverEnabled(agent string) bool {
	route, ok := gatewayRouteFor(agent)
	if !ok {
		return false
	}
	for _, id := range g.Settings().Takeover {
		if id == route.Agent {
			return true
		}
	}
	return false
}

// routedFor 判断这款 CLI 是否勾了接管，并保证二进制在位。
func (g *Gateway) routedFor(agent string) (gatewayRoute, bool) {
	route, ok := gatewayRouteFor(agent)
	if !ok || !g.TakeoverEnabled(agent) {
		return gatewayRoute{}, false
	}
	if !g.Installed() {
		log.Printf("终端接管：agentgateway 还没装，%s 按原方式启动", route.Agent)
		return gatewayRoute{}, false
	}
	return route, true
}

// ensureRunning 保证网关在跑；起不来就返回 false，调用方按原样启动终端。
func (g *Gateway) ensureRunning() bool {
	if g.Running() {
		return true
	}
	if err := g.Start(); err != nil {
		log.Printf("终端接管：自动拉起 agentgateway 失败，终端按原方式启动: %v", err)
		return false
	}
	log.Printf("终端接管：已自动拉起 agentgateway（业务端口 %d）", g.Settings().Port)
	return true
}

// ensureProxyPort 确认代理口在听；没有就重启网关一次（重启时按当前设置重写配置）。
func (g *Gateway) ensureProxyPort() bool {
	port := g.Settings().ProxyPort
	if waitPort(port, 200*time.Millisecond) {
		return true
	}
	log.Printf("终端接管：代理端口 %d 没在听，重启 agentgateway 让配置生效", port)
	_ = g.Stop() // 进程本来就没跑时这里会报错，忽略
	if err := g.Start(); err != nil {
		log.Printf("终端接管：重启 agentgateway 失败，终端按原方式启动: %v", err)
		return false
	}
	if !waitPort(port, 3*time.Second) {
		log.Printf("终端接管：代理端口 %d 还是没起来，终端按原方式启动", port)
		return false
	}
	return true
}

// routeBase 是这条路由从哪个口出：业务端口还是代理端口。
func (g *Gateway) routeBase(route gatewayRoute) string {
	s := g.Settings()
	port := s.Port
	if route.Via == viaProxy {
		port = s.ProxyPort
	}
	return fmt.Sprintf("http://127.0.0.1:%d", port)
}

// routeURL 是终端要连的网关地址（含该 CLI 需要的路径前缀）。
func (g *Gateway) routeURL(route gatewayRoute) string {
	return g.routeBase(route) + route.Path
}

// expandEnv 把 {url} / {key} 换成真实值。
func (g *Gateway) expandEnv(value, url string) string {
	return strings.NewReplacer("{url}", url, "{key}", g.Settings().UpstreamKey).Replace(value)
}

// takeoverPath 是传给 CLI 的高优先级设置文件位置。
func (g *Gateway) takeoverPath(agent string) string {
	return filepath.Join(g.Dir(), "takeover-"+agent+".json")
}

// writeTakeoverFile 生成只含上游地址的设置文件（0600），每次开终端前刷新，
// 改了端口也能立刻生效。
func (g *Gateway) writeTakeoverFile(route gatewayRoute, url string) (string, error) {
	if err := os.MkdirAll(g.Dir(), 0o755); err != nil {
		return "", err
	}
	envBlock := make(map[string]string, len(route.Env))
	for _, e := range route.Env {
		envBlock[e.Name] = g.expandEnv(e.Value, url)
	}
	body, err := json.MarshalIndent(map[string]any{"env": envBlock}, "", "  ")
	if err != nil {
		return "", err
	}
	path := g.takeoverPath(route.Agent)
	if err := os.WriteFile(path, append(body, '\n'), 0o600); err != nil {
		return "", err
	}
	return path, nil
}
