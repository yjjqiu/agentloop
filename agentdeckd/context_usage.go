package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// context_usage.go 估算一个终端占用的上下文额度，供终端标题栏显示。
// 取数分三层：已知 CLI 读它自己的会话记录（准确），读不到就扫终端最后几屏文字里的
// 「context left / 已用 %」，都没有就如实返回未知；以后接入新 CLI 只需要加一个适配函数。

// ContextUsage 是一个终端当前的上下文占用。Limit 为 0 表示不知道窗口大小，只能给已用 token 数。
type ContextUsage struct {
	Known     bool    `json:"known"`
	Used      int64   `json:"used,omitempty"`
	Limit     int64   `json:"limit,omitempty"`
	Percent   float64 `json:"percent,omitempty"`
	Model     string  `json:"model,omitempty"`
	Source    string  `json:"source,omitempty"`    // claude-transcript / codex-rollout / screen
	Estimated bool    `json:"estimated,omitempty"` // 上限来自 agents.json 默认值而非会话记录
	UpdatedAt int64   `json:"updatedAt,omitempty"`
}

// contextTTL 是同一个会话的取数缓存；终端每几秒轮询一次，没必要每次重读大文件。
const contextTTL = 4 * time.Second

var ctxCache = struct {
	mu sync.Mutex
	m  map[string]ctxCacheEntry
}{m: map[string]ctxCacheEntry{}}

type ctxCacheEntry struct {
	usage ContextUsage
	at    time.Time
}

// SessionContext 返回某个终端当前的上下文占用（带 4 秒缓存）。
func (a *App) SessionContext(s *Session) ContextUsage {
	if s == nil {
		return ContextUsage{}
	}
	ctxCache.mu.Lock()
	if hit, ok := ctxCache.m[s.ID]; ok && time.Since(hit.at) < contextTTL {
		ctxCache.mu.Unlock()
		return hit.usage
	}
	ctxCache.mu.Unlock()

	usage := computeContext(s)
	ctxCache.mu.Lock()
	ctxCache.m[s.ID] = ctxCacheEntry{usage: usage, at: time.Now()}
	ctxCache.mu.Unlock()
	return usage
}

// computeContext 按 CLI 类型选适配器，失败时退回扫描终端文字。
func computeContext(s *Session) ContextUsage {
	var usage ContextUsage
	switch s.AgentID() {
	case "claude-code":
		usage = claudeContext(s.Cwd, s.Started)
	case "codex":
		usage = codexContext(s.Started)
	}
	if !usage.Known {
		usage = screenContext(s)
	}
	// 会话记录里没写窗口大小（常见于走代理的模型名）时，退回 agents.json 的默认窗口，
	// 并标记成估算值，界面上会注明「按 xxx 估算」。
	if usage.Known && usage.Limit == 0 {
		if def, ok := agentByID(s.AgentID()); ok && def.ContextWindow > 0 {
			usage.Limit = def.ContextWindow
			usage.Estimated = true
		}
	}
	if usage.Percent == 0 && usage.Limit > 0 {
		usage.Percent = percentOf(usage)
	}
	return usage
}

// —— Claude Code：读 ~/.claude/projects/<munged-cwd>/*.jsonl 里最后一条带 usage 的消息 ——

type claudeTranscriptLine struct {
	Message *struct {
		Model string `json:"model"`
		Usage *struct {
			InputTokens         int64 `json:"input_tokens"`
			CacheReadTokens     int64 `json:"cache_read_input_tokens"`
			CacheCreationTokens int64 `json:"cache_creation_input_tokens"`
			OutputTokens        int64 `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

func claudeContext(cwd string, started time.Time) ContextUsage {
	dir := filepath.Join(homeDir(), ".claude", "projects", mungeProjectDir(cwd))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ContextUsage{}
	}
	// 同一个项目目录下可能有多个历史会话，取「启动之后还在写」的最新一个。
	var newest string
	var newestMod time.Time
	floor := started.Add(-2 * time.Minute)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.ModTime().Before(floor) {
			continue
		}
		if info.ModTime().After(newestMod) {
			newest = filepath.Join(dir, entry.Name())
			newestMod = info.ModTime()
		}
	}
	if newest == "" {
		return ContextUsage{}
	}
	for _, size := range []int64{256 * 1024, 4 * 1024 * 1024} {
		lines := readTailLines(newest, size)
		for i := len(lines) - 1; i >= 0; i-- {
			var line claudeTranscriptLine
			if err := json.Unmarshal([]byte(lines[i]), &line); err != nil || line.Message == nil || line.Message.Usage == nil {
				continue
			}
			u := line.Message.Usage
			used := u.InputTokens + u.CacheReadTokens + u.CacheCreationTokens + u.OutputTokens
			if used <= 0 {
				continue
			}
			return ContextUsage{
				Known:     true,
				Used:      used,
				Limit:     claudeContextLimit(line.Message.Model),
				Model:     line.Message.Model,
				Source:    "claude-transcript",
				UpdatedAt: newestMod.UnixMilli(),
			}
		}
	}
	return ContextUsage{}
}

// mungeProjectDir 复刻 Claude Code 的项目目录命名：非字母数字一律替换成 '-'。
func mungeProjectDir(path string) string {
	var b strings.Builder
	for _, r := range path {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

// claudeContextLimit 按模型名粗略给出上下文窗口；认不出来返回 0（只显示已用 token）。
func claudeContextLimit(model string) int64 {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "[1m]"), strings.Contains(m, "-1m"):
		return 1_000_000
	case strings.Contains(m, "claude"):
		return 200_000
	case strings.Contains(m, "gemini"):
		return 1_000_000
	case strings.Contains(m, "gpt-5"), strings.Contains(m, "codex"):
		return 400_000
	}
	return 0
}

// —— Codex：读 ~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl 里最后一条 token_count ——

type codexRolloutLine struct {
	Payload *struct {
		Type string `json:"type"`
		Info *struct {
			LastUsage *struct {
				TotalTokens int64 `json:"total_tokens"`
			} `json:"last_token_usage"`
			ContextWindow int64 `json:"model_context_window"`
		} `json:"info"`
	} `json:"payload"`
}

func codexContext(started time.Time) ContextUsage {
	root := filepath.Join(homeDir(), ".codex", "sessions")
	floor := started.Add(-2 * time.Minute)
	var newest string
	var newestMod time.Time
	for day := 0; day < 3; day++ {
		d := started.AddDate(0, 0, -day)
		dir := filepath.Join(root, d.Format("2006"), d.Format("01"), d.Format("02"))
		matches, _ := filepath.Glob(filepath.Join(dir, "rollout-*.jsonl"))
		for _, path := range matches {
			info, err := os.Stat(path)
			if err != nil || info.ModTime().Before(floor) {
				continue
			}
			if info.ModTime().After(newestMod) {
				newest = path
				newestMod = info.ModTime()
			}
		}
	}
	if newest == "" {
		return ContextUsage{}
	}
	for _, size := range []int64{256 * 1024, 4 * 1024 * 1024} {
		lines := readTailLines(newest, size)
		for i := len(lines) - 1; i >= 0; i-- {
			var line codexRolloutLine
			if err := json.Unmarshal([]byte(lines[i]), &line); err != nil || line.Payload == nil {
				continue
			}
			if line.Payload.Type != "token_count" || line.Payload.Info == nil || line.Payload.Info.LastUsage == nil {
				continue
			}
			used := line.Payload.Info.LastUsage.TotalTokens
			if used <= 0 {
				continue
			}
			return ContextUsage{
				Known:     true,
				Used:      used,
				Limit:     line.Payload.Info.ContextWindow,
				Source:    "codex-rollout",
				UpdatedAt: newestMod.UnixMilli(),
			}
		}
	}
	return ContextUsage{}
}

// —— 通用兜底：扫终端最近的文字，找「还剩 x%」或「已用 x%」 ——

type screenUsagePattern struct {
	re     *regexp.Regexp
	remain bool // true 表示匹配到的是「还剩多少」
}

var screenUsagePatterns = []screenUsagePattern{
	{regexp.MustCompile(`(?i)context left until auto-compact:\s*(\d+(?:\.\d+)?)\s*%`), true},
	{regexp.MustCompile(`(?i)context left:\s*(\d+(?:\.\d+)?)\s*%`), true},
	{regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*%\s*(?:of\s*)?context\s*(?:left|remaining)`), true},
	{regexp.MustCompile(`(?i)context\s*(?:window\s*)?(?:used|usage)?:?\s*(\d+(?:\.\d+)?)\s*%`), false},
	{regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*%\s*(?:of\s*)?context\s*(?:used|usage)`), false},
}

func screenContext(s *Session) ContextUsage {
	text := s.PlainText(8000)
	if strings.TrimSpace(text) == "" {
		return ContextUsage{}
	}
	best := -1
	var usage ContextUsage
	for _, pattern := range screenUsagePatterns {
		locs := pattern.re.FindAllStringSubmatchIndex(text, -1)
		if len(locs) == 0 {
			continue
		}
		// 同一屏里出现多次时，位置最靠后的才是最新的一帧。
		last := locs[len(locs)-1]
		if last[0] < best {
			continue
		}
		value, err := strconv.ParseFloat(text[last[2]:last[3]], 64)
		if err != nil {
			continue
		}
		percent := value
		if pattern.remain {
			percent = 100 - value
		}
		if percent < 0 {
			percent = 0
		}
		if percent > 100 {
			percent = 100
		}
		best = last[0]
		usage = ContextUsage{Known: true, Percent: percent, Source: "screen", UpdatedAt: time.Now().UnixMilli()}
	}
	return usage
}

// readTailLines 从文件尾部读一段并按行切开；start 不是行首时丢掉第一条半行。
func readTailLines(path string, maxBytes int64) []string {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() == 0 {
		return nil
	}
	start := info.Size() - maxBytes
	if start < 0 {
		start = 0
	}
	buf := make([]byte, info.Size()-start)
	n, err := file.ReadAt(buf, start)
	if err != nil && n == 0 {
		return nil
	}
	lines := strings.Split(string(buf[:n]), "\n")
	if start > 0 && len(lines) > 0 {
		lines = lines[1:]
	}
	return lines
}

// percentOf 用已用 / 上限算出百分比；上限未知时返回 0。
func percentOf(usage ContextUsage) float64 {
	if usage.Limit <= 0 || usage.Used <= 0 {
		return 0
	}
	return float64(usage.Used) * 100 / float64(usage.Limit)
}
