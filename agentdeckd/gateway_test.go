package main

import (
	"strings"
	"testing"
)

func TestNormalizeTakeover(t *testing.T) {
	if got := normalizeTakeover(nil); got != nil {
		t.Fatalf("空列表应返回 nil，实际 %#v", got)
	}
	if got := normalizeTakeover([]string{"nope", "", "  "}); got != nil {
		t.Fatalf("未知 CLI 应被过滤掉，实际 %#v", got)
	}
	got := normalizeTakeover([]string{" claude-code ", "claude-code"})
	if len(got) != 1 || got[0] != "claude-code" {
		t.Fatalf("应去重并去掉空白，实际 %#v", got)
	}
}

func TestGatewayRouteFor(t *testing.T) {
	route, ok := gatewayRouteFor("claude-code")
	if !ok {
		t.Fatal("claude-code 应该在白名单里")
	}
	if len(route.Env) != 1 || route.Env[0].Name != "ANTHROPIC_BASE_URL" || route.Via != viaLLM || route.Flag == "" {
		t.Fatalf("Claude Code 的接管方式不对: %#v", route)
	}
	// Codex 的 /v1/responses 转不了 Anthropic Messages，只能走代理端口 + 启动参数。
	codex, ok := gatewayRouteFor("codex")
	if !ok {
		t.Fatal("codex 应该在白名单里")
	}
	if codex.Via != viaProxy || len(codex.Args) == 0 || codex.Env != nil {
		t.Fatalf("Codex 应该走代理端口并追加参数: %#v", codex)
	}
	// Gemini 的原生 generateContent 也要走代理口，环境变量里得有鉴权方式覆盖。
	gem, ok := gatewayRouteFor("gemini")
	if !ok || gem.Via != viaProxy || len(gem.Env) < 3 {
		t.Fatalf("Gemini 的接管方式不对: %#v", gem)
	}
	if _, ok := gatewayRouteFor("nope"); ok {
		t.Fatal("未知 CLI 不该命中白名单")
	}
}

func TestBuildArgs(t *testing.T) {
	// 只测展开逻辑：配置给零值时 normalize 会补上默认上游 key。
	g := &Gateway{app: &App{config: &Config{}}}

	codex, _ := gatewayRouteFor("codex")
	_, args := g.buildArgs(codex, "http://127.0.0.1:4001/backend-api/codex")
	if len(args) != 2 || args[0] != "-c" {
		t.Fatalf("Codex 参数不对: %#v", args)
	}
	if want := `openai_base_url="http://127.0.0.1:4001/backend-api/codex"`; args[1] != want {
		t.Fatalf("Codex 参数值不对: %q != %q", args[1], want)
	}

	gem, _ := gatewayRouteFor("gemini")
	env, _ := g.buildArgs(gem, "http://127.0.0.1:4001")
	want := map[string]string{
		"GOOGLE_GEMINI_BASE_URL":   "http://127.0.0.1:4001",
		"GEMINI_CLI_AUTH_OVERRIDE": "gemini-api-key",
		"GEMINI_API_KEY":           defaultGatewaySettings().UpstreamKey,
	}
	if len(env) != len(want) {
		t.Fatalf("Gemini 环境变量数量不对: %#v", env)
	}
	for _, kv := range env {
		name, value, _ := strings.Cut(kv, "=")
		if want[name] != value {
			t.Fatalf("%s 的值不对: %q != %q", name, value, want[name])
		}
	}
}

func TestRenderConfig(t *testing.T) {
	s := GatewaySettings{
		Port:        4000,
		ProxyPort:   4001,
		AdminPort:   15000,
		Upstream:    "http://127.0.0.1:3425",
		UpstreamKey: "magpie",
	}
	yml := RenderConfig(s, "")
	for _, want := range []string{
		`name: "*"`,
		"provider: anthropic",
		`apiKey: "magpie"`,
		`baseUrl: "http://127.0.0.1:3425"`,
		// 代理端口：Codex / Gemini 这类解析不了的协议原样转发。
		"binds:",
		"  - port: 4001",
		"      - protocol: HTTP",
		`              - host: "127.0.0.1:3425"`,
	} {
		if !strings.Contains(yml, want) {
			t.Fatalf("生成的配置缺少 %q:\n%s", want, yml)
		}
	}
	if strings.Contains(yml, "session:") {
		t.Fatalf("没有 session key 时不该写 config.session:\n%s", yml)
	}

	withKey := RenderConfig(s, "0123456789abcdef0123456789abcdef")
	if !strings.Contains(withKey, "    key: 0123456789abcdef0123456789abcdef") {
		t.Fatalf("已有 session key 应被保留:\n%s", withKey)
	}

	// 上游带路径时要在配置里提醒：代理端口只转发 host，不带前缀。
	s.Upstream = "http://127.0.0.1:3425/v1"
	if yml := RenderConfig(s, ""); !strings.Contains(yml, "代理端口转发时不带前缀") {
		t.Fatalf("上游带路径时应该提示:\n%s", yml)
	}
}

func TestUpstreamHost(t *testing.T) {
	cases := map[string]string{
		"http://127.0.0.1:3425":      "127.0.0.1:3425",
		"https://api.example.com/v1": "api.example.com",
		"127.0.0.1:3425":             "",
		"":                           "",
	}
	for in, want := range cases {
		if got := upstreamHost(in); got != want {
			t.Fatalf("upstreamHost(%q) = %q, 期望 %q", in, got, want)
		}
	}
}

func TestParseSamplesSummarize(t *testing.T) {
	text := `# HELP agentgateway_requests_total 请求数
# TYPE agentgateway_requests_total counter
agentgateway_requests_total{status="200",bind="bind/4000"} 2
agentgateway_requests_total{status="502"} 1
agentgateway_requests_total{status="101",method="GET"} 1
agentgateway_gen_ai_client_token_usage_sum{gen_ai_token_type="input"} 10
agentgateway_gen_ai_client_token_usage_sum{gen_ai_token_type="output"} 7
agentgateway_gen_ai_client_token_usage_sum{gen_ai_token_type="input_cache_read"} 99
agentgateway_gen_ai_client_token_usage_bucket{le="1"} 4
agentgateway_gen_ai_client_token_usage_count 4
`
	m := summarizeMetrics(parseSamples(text))
	// 101 是 WebSocket 升级成功，算请求不算错误。
	if m.Requests != 4 || m.Errors != 1 {
		t.Fatalf("请求/错误统计不对: %+v", m)
	}
	// cache_read 和直方图桶都不能混进输入 token。
	if m.InputTokens != 10 || m.OutputTokens != 7 {
		t.Fatalf("token 统计不对: %+v", m)
	}
}

func TestParseLabels(t *testing.T) {
	labels := parseLabels(`{k="a,b",n="x"}`)
	if labels["k"] != "a,b" || labels["n"] != "x" {
		t.Fatalf("引号内的逗号不该切开: %#v", labels)
	}
}

func TestMergeEnv(t *testing.T) {
	base := []string{"A=1", "B=2", "PATH=/bin"}
	// Windows 环境变量名不区分大小写，path 和 PATH 视为同一个。
	out := mergeEnv(base, []string{"b=9", "C=3"})
	got := strings.Join(out, ";")
	if got != "A=1;PATH=/bin;b=9;C=3" {
		t.Fatalf("覆盖逻辑不对: %s", got)
	}
	if same := mergeEnv(base, nil); len(same) != len(base) {
		t.Fatalf("没有覆盖项时应原样返回: %#v", same)
	}
}

func TestVersionLike(t *testing.T) {
	cases := map[string]bool{
		"1.7.0":       true,
		"1.7.0-rc.1":  true,
		"True":        false, // 1.7.0 的构建里 -V 打出来就是这个
		"":            false,
		"agentgatewa": false,
	}
	for in, want := range cases {
		if got := versionLike(in); got != want {
			t.Fatalf("versionLike(%q) = %v, 期望 %v", in, got, want)
		}
	}
}
