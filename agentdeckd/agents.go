package main

import (
	_ "embed"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed agents.json
var agentsJSON []byte

// AgentMode 是一套预设启动参数，例如「全自动」「只读」。
type AgentMode struct {
	ID    string   `json:"id"`
	Label string   `json:"label"`
	Note  string   `json:"note,omitempty"`
	Args  []string `json:"args"`
}

// AgentDef 是一款 coding agent CLI 的静态描述。
type AgentDef struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Bin         string      `json:"bin"`
	AltBins     []string    `json:"altBins,omitempty"`
	Args        []string    `json:"args,omitempty"`
	Modes       []AgentMode `json:"modes,omitempty"`       // 预设启动模式；为空表示只有默认一种
	DefaultMode string      `json:"defaultMode,omitempty"` // 默认使用的模式 id
	// ContextWindow 是上下文窗口大小，界面算「上下文用了几成」时用；
	// 会话记录里能读到真实窗口的（如 Codex）优先用真实值。
	ContextWindow int64    `json:"contextWindow,omitempty"`
	Prompt        string   `json:"prompt,omitempty"`    // positional / after-start
	PromptArg     string   `json:"promptArg,omitempty"` // positional 时的分隔参数，如 "--"
	Note          string   `json:"note,omitempty"`
	Npm           string   `json:"npm,omitempty"`    // 默认 npm 包名，用于安装 / 更新
	Update        []string `json:"update,omitempty"` // 自更新命令参数，例如 pi 的 ["update"]
	Repo          string   `json:"repo,omitempty"`   // GitHub 仓库（owner/name），用于拉取最新发布说明
	// Stages 是这款 CLI 在 6 个阶段上的适配度（0-5，5 最顶尖），键与流水线阶段一致。
	Stages  map[string]int `json:"stages,omitempty"`
	FitNote string         `json:"fitNote,omitempty"` // 适配说明（一句话）
}

// AgentStatus 是探测后的运行时状态。
type AgentStatus struct {
	AgentDef
	Path        string      `json:"path"`
	Installed   bool        `json:"installed"`
	Enabled     bool        `json:"enabled"`
	DefaultArgs []string    `json:"defaultArgs,omitempty"`
	Mode        string      `json:"mode,omitempty"`  // 当前生效的启动模式 id；空表示自定义参数或没有模式
	Modes       []AgentMode `json:"modes,omitempty"` // 该 CLI 支持的启动模式
}

// pickMode 选出当前生效的启动模式：优先生效用户选的，其次内置默认，最后第一个。
func pickMode(def AgentDef, want string) (AgentMode, bool) {
	if len(def.Modes) == 0 {
		return AgentMode{}, false
	}
	want = strings.TrimSpace(want)
	for _, m := range def.Modes {
		if m.ID == want && want != "" {
			return m, true
		}
	}
	for _, m := range def.Modes {
		if m.ID == def.DefaultMode {
			return m, true
		}
	}
	return def.Modes[0], true
}

var agentDefs = mustParseAgents()

func mustParseAgents() []AgentDef {
	var defs []AgentDef
	if err := json.Unmarshal(agentsJSON, &defs); err != nil {
		return []AgentDef{{
			ID: "claude-code", Name: "Claude Code", Bin: "claude",
			Args: []string{"--dangerously-skip-permissions"}, Prompt: "after-start",
		}}
	}
	return defs
}

func agentByID(id string) (AgentDef, bool) {
	for _, def := range agentDefs {
		if def.ID == id {
			return def, true
		}
	}
	return AgentDef{}, false
}

// pathIndex 把 PATH 与常见安装目录一次性读成“目录 → 文件名表”。
// 逐个 agent 调 exec.LookPath 在 Windows 上要做上万次 stat，44 款 CLI 探测一次要 2 秒以上；
// 这里每个目录只读一次，探测能降到毫秒级。
type pathIndex struct {
	dirs []indexedDir
}

type indexedDir struct {
	names map[string]string // 小写文件名 → 完整路径
}

func newPathIndex() *pathIndex {
	idx := &pathIndex{}
	seen := make(map[string]bool)
	addDir := func(dir string) {
		dir = strings.Trim(strings.TrimSpace(dir), `"`)
		if dir == "" {
			return
		}
		key := strings.ToLower(filepath.Clean(dir))
		if seen[key] {
			return
		}
		seen[key] = true
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		names := make(map[string]string, len(entries))
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			names[strings.ToLower(entry.Name())] = filepath.Join(dir, entry.Name())
		}
		idx.dirs = append(idx.dirs, indexedDir{names: names})
	}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		addDir(dir)
	}
	// PATH 之外再兜底几个常见目录（Windows 的 npm/bun，Linux 的 ~/.local/bin 等）。
	for _, dir := range extraBinDirs() {
		addDir(dir)
	}
	return idx
}

// find 按 PATH 顺序找第一个匹配的可执行文件。
// Windows 上优先按 PATHEXT 找（npm 目录里的 claude 是无扩展名的 sh 脚本，
// 真正能执行的是 claude.cmd，不能只认裸文件名）。
func (idx *pathIndex) find(names []string) string {
	exts := pathExtensions()
	for _, name := range names {
		if name == "" {
			continue
		}
		lower := strings.ToLower(name)
		candidates := []string{lower}
		if filepath.Ext(name) == "" {
			candidates = candidates[:0]
			for _, ext := range exts {
				candidates = append(candidates, lower+ext)
			}
		}
		for _, dir := range idx.dirs {
			for _, candidate := range candidates {
				if full, ok := dir.names[candidate]; ok {
					return full
				}
			}
		}
	}
	return ""
}

// resolveAgentIn 在给定索引里解析 agent 的可执行文件。
func resolveAgentIn(idx *pathIndex, def AgentDef) string {
	return idx.find(append([]string{def.Bin}, def.AltBins...))
}

// resolveAgentWith 在自动探测的基础上应用用户指定的可执行文件路径。
func resolveAgentWith(def AgentDef, ov CliOverride) string {
	return resolveAgentWithIndex(newPathIndex(), def, ov)
}

// resolveAgentWithIndex 复用同一个路径索引解析（批量探测时用）。
func resolveAgentWithIndex(idx *pathIndex, def AgentDef, ov CliOverride) string {
	if ov.Path != "" {
		if info, err := os.Stat(ov.Path); err == nil && !info.IsDir() {
			return ov.Path
		}
		return ""
	}
	return resolveAgentIn(idx, def)
}

// DetectAgentsWith 返回所有 agent 及安装状态，已安装的排前面，并应用本地覆盖设置。
func DetectAgentsWith(overrides map[string]CliOverride) []AgentStatus {
	out := make([]AgentStatus, 0, len(agentDefs))
	idx := newPathIndex()
	for _, def := range agentDefs {
		ov := overrides[def.ID]
		base := def
		path := resolveAgentWithIndex(idx, base, ov)
		mode := ""
		if len(ov.Args) > 0 {
			// 用户手工填了参数：整组覆盖，模式不再生效。
			base.Args = ov.Args
		} else if m, ok := pickMode(def, ov.Mode); ok {
			base.Args = m.Args
			mode = m.ID
		}
		enabled := true
		if ov.Enabled != nil {
			enabled = *ov.Enabled
		}
		out = append(out, AgentStatus{
			AgentDef:    base,
			Path:        path,
			Installed:   path != "",
			Enabled:     enabled,
			DefaultArgs: def.Args,
			Mode:        mode,
			Modes:       def.Modes,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Installed != out[j].Installed {
			return out[i].Installed
		}
		return false
	})
	return out
}

// CliCommand 拼出新终端里要执行的完整命令（含用户覆盖的参数）。
func CliCommand(agent AgentStatus) string {
	bin := agent.Path
	if bin == "" {
		bin = agent.Bin
	}
	parts := []string{quoteArg(bin)}
	for _, arg := range agent.Args {
		parts = append(parts, quoteArg(arg))
	}
	return strings.Join(parts, " ")
}

// normalizeArgs 去掉空白参数，空切片表示“沿用内置参数”。
func normalizeArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for _, arg := range args {
		if arg = strings.TrimSpace(arg); arg != "" {
			out = append(out, arg)
		}
	}
	return out
}

// buildAgentCommand 拼出交给 ConPTY 的命令行。prompt 只在 positional 模式下追加。
func buildAgentCommand(def AgentDef, path string, prompt string) string {
	parts := []string{quoteArg(path)}
	for _, arg := range def.Args {
		parts = append(parts, quoteArg(arg))
	}
	if prompt != "" && def.Prompt == "positional" {
		text := quoteArg(oneLine(prompt))
		switch {
		case strings.HasSuffix(def.PromptArg, "="):
			// --prompt=<prompt> 这种形态，prompt 必须紧贴等号，不能加空格。
			parts = append(parts, def.PromptArg+text)
		case def.PromptArg != "":
			parts = append(parts, def.PromptArg, text)
		default:
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, " ")
}

func quoteArg(s string) string {
	if s == "" {
		return `""`
	}
	if !strings.ContainsAny(s, " \t\n\"&|<>^") {
		return s
	}
	escaped := strings.ReplaceAll(s, `"`, `\"`)
	return `"` + escaped + `"`
}

// oneLine 把多行文本压成一行，避免 TUI 提前提交。
func oneLine(s string) string {
	fields := strings.Fields(strings.ReplaceAll(s, "\n", " "))
	return strings.Join(fields, " ")
}
