package main

// 指标抓取：读 stats 端口的 Prometheus 文本，归并成界面要的几个数字。

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// —— 指标 ——

// gatewayMetricsPort 是 agentgateway 的 stats 端口（1.7 固定在 15020，绑在 [::]）。
// 管理台 15000 上是 UI，没有 /metrics。
const gatewayMetricsPort = 15020

// GatewayMetrics 是抓下来的指标汇总。
type GatewayMetrics struct {
	OK           bool    `json:"ok"`
	Error        string  `json:"error,omitempty"`
	Requests     float64 `json:"requests"`
	Errors       float64 `json:"errors"`
	InputTokens  float64 `json:"inputTokens"`
	OutputTokens float64 `json:"outputTokens"`
	ScrapedAt    int64   `json:"scrapedAt,omitempty"`
}

// Metrics 抓 stats 端口的 /metrics 并归并成四个数字。
// 端口先用 15020（stats），失败再退回管理端口，兼容以后版本挪位置。
func (g *Gateway) Metrics() GatewayMetrics {
	if !g.Running() {
		return GatewayMetrics{OK: false, Error: "网关没在运行"}
	}
	var lastErr = "抓取指标失败"
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for _, url := range []string{
		fmt.Sprintf("http://[::1]:%d/metrics", gatewayMetricsPort),
		fmt.Sprintf("http://127.0.0.1:%d/metrics", gatewayMetricsPort),
		fmt.Sprintf("http://[::1]:%d/metrics", g.Settings().AdminPort),
	} {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			lastErr = err.Error()
			continue
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			lastErr = err.Error()
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if readErr != nil {
			lastErr = readErr.Error()
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Sprintf("指标接口返回 %d", resp.StatusCode)
			continue
		}
		out := summarizeMetrics(parseSamples(string(body)))
		out.OK = true
		out.ScrapedAt = time.Now().UnixMilli()
		return out
	}
	return GatewayMetrics{OK: false, Error: lastErr}
}

// promSample 是 Prometheus 文本里的一条样本。
type promSample struct {
	name   string
	labels map[string]string
	value  float64
}

// parseSamples 解析 Prometheus 文本格式：`name{labels} value [ts]`。
// 只保留原始样本，怎么归并交给 summarizeMetrics，避免把直方图的桶也算进去。
func parseSamples(text string) []promSample {
	out := make([]promSample, 0, 256)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		space := strings.LastIndexByte(line, ' ')
		if space <= 0 {
			continue
		}
		value, err := strconv.ParseFloat(strings.TrimSpace(strings.Fields(line[space:])[0]), 64)
		if err != nil {
			continue
		}
		head := strings.TrimSpace(line[:space])
		name, labels := head, map[string]string{}
		if open := strings.IndexByte(head, '{'); open >= 0 {
			name = head[:open]
			labels = parseLabels(head[open:])
		}
		if name == "" {
			continue
		}
		out = append(out, promSample{name: name, labels: labels, value: value})
	}
	return out
}

// parseLabels 解析 `{k="v",k2="v2"}`；值里可能有逗号，按引号状态切。
func parseLabels(raw string) map[string]string {
	out := map[string]string{}
	raw = strings.TrimSuffix(strings.TrimPrefix(raw, "{"), "}")
	for _, part := range splitLabels(raw) {
		eq := strings.IndexByte(part, '=')
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(part[:eq])
		val := strings.Trim(strings.TrimSpace(part[eq+1:]), `"`)
		out[key] = val
	}
	return out
}

// splitLabels 按逗号切分，跳过引号内的逗号。
func splitLabels(raw string) []string {
	var parts []string
	var b strings.Builder
	inQuote := false
	for _, r := range raw {
		switch {
		case r == '"':
			inQuote = !inQuote
			b.WriteRune(r)
		case r == ',' && !inQuote:
			parts = append(parts, b.String())
			b.Reset()
		default:
			b.WriteRune(r)
		}
	}
	if b.Len() > 0 {
		parts = append(parts, b.String())
	}
	return parts
}

// summarizeMetrics 把样本归并成界面要的四个数字。
func summarizeMetrics(samples []promSample) GatewayMetrics {
	var out GatewayMetrics
	for _, s := range samples {
		switch {
		case s.name == "agentgateway_requests_total":
			out.Requests += s.value
			// 4xx 也算「没走通」，和 5xx 一起计入错误数；
			// 101 是 WebSocket 升级成功（Codex 走的就是它），不能算错。
			if status := s.labels["status"]; status != "" && !strings.HasPrefix(status, "2") && status != "101" {
				out.Errors += s.value
			}
		case s.name == "agentgateway_gen_ai_client_token_usage_sum":
			switch s.labels["gen_ai_token_type"] {
			case "input":
				out.InputTokens += s.value
			case "output":
				out.OutputTokens += s.value
			}
		}
	}
	return out
}
