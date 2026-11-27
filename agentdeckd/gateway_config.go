package main

// agentgateway 的配置生成：config.yaml 是生成物，真相源是 config.json 的 gateway 段。

import (
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
)

// —— 配置生成 ——

// RenderConfig 按设置生成 agentgateway 的 YAML。
// 上游用 custom provider：它能直接吃 Anthropic Messages 格式（magpie 就是这种），
// 又不会被当成官方 Anthropic 端点去拼路径。
func RenderConfig(s GatewaySettings, sessionKey string) string {
	var b strings.Builder
	b.WriteString("# 由 AgentDeck 生成，改这里会在下次启动时被覆盖。\n")
	b.WriteString("# 要改上游/端口：AgentDeck → 设置 → 网关。\n")
	b.WriteString("# yaml-language-server: $schema=https://agentgateway.dev/schema/config\n")
	b.WriteString("llm:\n")
	b.WriteString("  models:\n")
	b.WriteString("    - name: \"*\"\n")
	b.WriteString("      provider: anthropic\n")
	b.WriteString("      params:\n")
	// provider 用 anthropic：1.7 的 llm.models 里 custom 过不了 schema 校验，
	// 而内置 anthropic provider 认 baseUrl。magpie 说的就是 Anthropic Messages，路径天然对得上。
	if s.UpstreamKey != "" {
		b.WriteString(fmt.Sprintf("        apiKey: %q\n", s.UpstreamKey))
	}
	b.WriteString(fmt.Sprintf("        baseUrl: %q\n", s.Upstream))
	if s.ProxyPort > 0 {
		writeProxyBind(&b, s)
	}
	if sessionKey != "" {
		// agentgateway 首次启动会自己往配置里写一把 session key；重建文件时带上它，
		// 免得每次启动都换 key、把已有会话顶掉。
		b.WriteString("config:\n")
		b.WriteString("  session:\n")
		b.WriteString("    key: " + sessionKey + "\n")
	}
	return b.String()
}

// writeProxyBind 生成代理端口的配置：不解析协议，原样转发给上游。
// Codex 的 Responses、Gemini 的 generateContent 都靠它过网关。
func writeProxyBind(b *strings.Builder, s GatewaySettings) {
	host := upstreamHost(s.Upstream)
	if host == "" {
		return
	}
	b.WriteString("\n# 代理端口：agentgateway 解析不了的协议（Codex / Gemini）原样转发给上游。\n")
	b.WriteString("binds:\n")
	b.WriteString(fmt.Sprintf("  - port: %d\n", s.ProxyPort))
	b.WriteString("    listeners:\n")
	b.WriteString("      - protocol: HTTP\n")
	b.WriteString("        routes:\n")
	b.WriteString("          - matches:\n")
	b.WriteString("              - path:\n")
	b.WriteString("                  pathPrefix: /\n")
	b.WriteString("            backends:\n")
	b.WriteString(fmt.Sprintf("              - host: %q\n", host))
	if u, err := url.Parse(s.Upstream); err == nil && strings.Trim(u.Path, "/") != "" {
		b.WriteString(fmt.Sprintf("# 注意：上游地址带路径前缀 %q，代理端口转发时不带前缀（业务端口会带）。\n", u.Path))
	}
}

// upstreamHost 取上游的 host:port，代理端口只往这里转发。
func upstreamHost(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Host
}

// writeConfig 把生成结果落盘（配置里有上游 key，只给当前用户读写）。
func (g *Gateway) writeConfig(s GatewaySettings) error {
	if err := os.MkdirAll(g.Dir(), 0o755); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}
	yaml := RenderConfig(s, g.existingSessionKey())
	if err := os.WriteFile(g.ConfigPath(), []byte(yaml), 0o600); err != nil {
		return fmt.Errorf("写配置失败: %w", err)
	}
	_ = os.Chmod(g.ConfigPath(), 0o600)
	return nil
}

// sessionKeyPattern 匹配 agentgateway 自己写进配置的 session key。
var sessionKeyPattern = regexp.MustCompile(`(?m)^\s*key:\s*([0-9a-f]{16,})\s*$`)

// existingSessionKey 读出磁盘上已有的 session key（没有就返回空）。
func (g *Gateway) existingSessionKey() string {
	raw, err := os.ReadFile(g.ConfigPath())
	if err != nil {
		return ""
	}
	if m := sessionKeyPattern.FindStringSubmatch(string(raw)); m != nil {
		return m[1]
	}
	return ""
}

// ConfigYAML 返回当前会生成的配置，供界面预览。
func (g *Gateway) ConfigYAML() string {
	return RenderConfig(g.Settings(), g.existingSessionKey())
}
