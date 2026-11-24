package main

// 阶段提示词与交接包：角色规则、上游输入、返工打回说明。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// stagePrompt 是本阶段给 CLI 的唯一指令：角色约束 + 交接包 + 只做这一阶段。
func (m *PipelineManager) stagePrompt(run *pipelineRun, stage string, state wfState) string {
	meta := stageMeta[stage]
	dir := filepath.Join(run.root, "artifacts", run.slug)
	outDir := filepath.Join(dir, meta.Dir)
	handoffDir := filepath.Join(dir, ".agentdeck", "handoff")
	statePath := filepath.Join(dir, "workflow-state.json")

	var b strings.Builder
	fmt.Fprintf(&b, "执行 LoopForge devflow 的 %s 阶段（%s，角色 %s）。", stage, meta.Label, meta.Executor)
	if brief := readRoleBrief(run.root, meta.Executor); brief != "" {
		fmt.Fprintf(&b, "【角色约束】%s", brief)
	}
	fmt.Fprintf(&b, "项目根目录 %s；artifacts 目录 %s。", run.root, dir)

	switch run.editionFor(stage, m.configFor(run.root).stageAgent(stage).Agent) {
	case "classic":
		fmt.Fprintf(&b, "本项目是 LoopForge Classic edition：先读 %s 和 .claude/commands/%s.md（或 .codex / .cursor 下的同名文件）。", statePath, meta.Command)
	case "portable":
		fmt.Fprintf(&b, "本项目是 LoopForge Portable edition：先读 %s 和项目里的 devflow 技能（.claude/skills/devflow/SKILL.md、.agents/skills/devflow/SKILL.md 或 .dsh/skills/devflow/SKILL.md）。", statePath)
	default:
		fmt.Fprintf(&b, "先读 %s。项目没有安装 LoopForge 时，按上面的角色约束和本阶段任务执行。", statePath)
	}

	b.WriteString("【输入】只读以下文件，不要依赖其他终端的历史对话：")
	for _, p := range upstreamInputs(run, state, stage) {
		fmt.Fprintf(&b, " %s；", p)
	}
	if prev := previousStage(stage); prev != "" {
		if text := readSnippet(filepath.Join(handoffDir, prev+".md"), 1200); text != "" {
			fmt.Fprintf(&b, "【上一阶段交接】%s", text)
		}
	}
	if run.reworkCount(stage) > 0 {
		fmt.Fprintf(&b, "【打回】%s", rejectPacket(run, state, stage))
	}

	fmt.Fprintf(&b, "只做本阶段：产物写入 %s；完成后只更新 stages.%s 的 status、artifact_path、completed_at、conclusion，", outDir, stage)
	b.WriteString("不要修改 current_stage、last_event、next_target，也不要开始下一阶段。")
	fmt.Fprintf(&b, "结束前把交接写到 %s，必须包含六节：inputs_used / outputs / decisions / assumptions / open_questions / next_focus；没有未决问题时 open_questions 留空。", filepath.Join(handoffDir, stage+".md"))
	b.WriteString("全程自主执行：不要向用户提问或等待确认，信息不足就按最小可行假设继续，并把假设写进结论。")
	fmt.Fprintf(&b, "完成时输出一行 STAGE_DONE %s。", stage)

	if stage == "TASK-01" {
		if raw, err := os.ReadFile(filepath.Join(dir, "requirement.txt")); err == nil {
			if req := strings.TrimSpace(string(raw)); req != "" {
				fmt.Fprintf(&b, "需求原文：%s", oneLine(req))
			}
		}
	}
	return b.String()
}

// agentHost 把 AgentDeck 的 CLI 标识映射到 LoopForge 的宿主名。
var agentHost = map[string]string{
	"claude-code":      "claude",
	"codex":            "codex",
	"cursor":           "cursor",
	"codebuddy":        "codebuddy",
	"pi":               "pi",
	"deepseek-harness": "dsh",
	"hermes":           "hermes",
}

// detectEditions 读 .devflow/install-state.json，返回宿主 → classic / portable。
func detectEditions(root string) map[string]string {
	raw, err := os.ReadFile(filepath.Join(root, ".devflow", "install-state.json"))
	if err != nil {
		return nil
	}
	var doc struct {
		Installations map[string]struct {
			Host    string `json:"host"`
			Edition string `json:"edition"`
		} `json:"installations"`
	}
	if json.Unmarshal(raw, &doc) != nil || len(doc.Installations) == 0 {
		return nil
	}
	out := map[string]string{}
	for _, item := range doc.Installations {
		if item.Host != "" && item.Edition != "" {
			out[item.Host] = item.Edition
		}
	}
	return out
}

// reworkTarget 返回该阶段失败时应打回的上游阶段。
func reworkTarget(stage string) string {
	switch stage {
	case "CODE-REVIEW", "TASK-04":
		return "TASK-03"
	}
	return ""
}

// upstreamInputs 列出本阶段应当阅读的上游产物，避免依赖别人的对话历史。
func upstreamInputs(run *pipelineRun, state wfState, stage string) []string {
	dir := filepath.Join(run.root, "artifacts", run.slug)
	out := make([]string, 0, len(stageOrder))
	for _, key := range stageOrder {
		if key == stage {
			break
		}
		switch state.status(key) {
		case "completed", "passed":
		default:
			continue
		}
		path := stageArtifactPath(state, key)
		if path == "" {
			path = filepath.Join(dir, stageMeta[key].Dir)
		} else if !filepath.IsAbs(path) {
			path = filepath.Join(dir, path)
		}
		out = append(out, path)
	}
	if len(out) == 0 {
		out = append(out, filepath.Join(dir, "workflow-state.json"))
	}
	return out
}

// rejectNote 是人工打回的原因，下一轮返工的阶段提示会读它。
type rejectNote struct {
	From   string `json:"from"`
	Target string `json:"target"`
	Reason string `json:"reason"`
	Round  int    `json:"round"`
	At     string `json:"at"`
}

func writeRejectReason(root, slug, target, from, reason string, round int) error {
	path := filepath.Join(root, "artifacts", slug, ".agentdeck", "reject", target+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, _ := json.MarshalIndent(rejectNote{
		From:   from,
		Target: target,
		Reason: reason,
		Round:  round,
		At:     time.Now().Format(time.RFC3339),
	}, "", "  ")
	return os.WriteFile(path, raw, 0o644)
}

// rejectPacket 把下游的失败报告打包回上游，作为返工的唯一依据。
func rejectPacket(run *pipelineRun, state wfState, target string) string {
	dir := filepath.Join(run.root, "artifacts", run.slug)
	// 人工打回优先：原因由人写，范围由人定。
	if raw, err := os.ReadFile(filepath.Join(dir, ".agentdeck", "reject", target+".json")); err == nil {
		var note rejectNote
		if json.Unmarshal(raw, &note) == nil && strings.TrimSpace(note.Reason) != "" {
			fromLabel := note.From
			if meta, ok := stageMeta[note.From]; ok {
				fromLabel = meta.Label
			}
			return fmt.Sprintf("人类在第 %d 轮把 %s 打回到本阶段：%s。只修复这些问题，不要扩大范围。",
				note.Round, fromLabel, note.Reason)
		}
	}
	from := ""
	for _, key := range []string{"CODE-REVIEW", "TASK-04"} {
		switch state.status(key) {
		case "failed", "blocked":
			from = key
		}
	}
	if from == "" {
		return "上一轮下游阶段未通过，按报告修复后重新交付。"
	}
	path := stageArtifactPath(state, from)
	if path == "" {
		candidates := []string{
			filepath.Join(dir, "03-code", "review-report.md"),
			filepath.Join(dir, "04-e2e", "test-report.md"),
		}
		for _, p := range candidates {
			if text := readSnippet(p, 2000); text != "" {
				return fmt.Sprintf("上一轮 %s 未通过。只修复下面报告里的问题，不要扩大范围：%s", stageMeta[from].Label, text)
			}
		}
		return fmt.Sprintf("上一轮 %s 未通过，按它的报告修复后再交付。", stageMeta[from].Label)
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	text := readSnippet(path, 2000)
	if text == "" {
		return fmt.Sprintf("上一轮 %s 未通过，按它的报告修复后再交付。", stageMeta[from].Label)
	}
	return fmt.Sprintf("上一轮 %s 未通过。只修复下面报告里的问题，不要扩大范围：%s", stageMeta[from].Label, text)
}

// roleRuleFiles 是 executor → 项目 / 用户规则文件的映射。
var roleRuleFiles = map[string]string{
	"architect":          "architect.md",
	"developer":          "developer.md",
	"code-reviewer":      "code-review.md",
	"test-engineer":      "tester.md",
	"knowledge-engineer": "knowledge-engineer.md",
}

// roleFallback 在项目里找不到规则文件时，用内置的角色边界兜底。
var roleFallback = map[string]string{
	"analyst":            "只做需求分析与澄清：写清目标、范围、排除项和验收标准，产出 01-requirement/requirement-report.md；不写代码。",
	"architect":          "只做方案：读需求报告，产出 02-design/tech-design.md 与 execution-plan.md；不写代码、不执行测试。",
	"developer":          "只按 execution-plan 改代码，遵守最小修改和白名单；产出 change-report.md 与代码变更；不审查、不测试。",
	"code-reviewer":      "只做审查：独立判断，按严重等级列出问题，产出 review-report.md；不修改代码。",
	"test-engineer":      "只补测试并执行：按风险选择测试层级，报告区分 passed / failed / blocked，产出 test-report.md；不改生产代码。",
	"knowledge-engineer": "只做沉淀：从本次交付提炼可复用结论，追加写入 05-knowledge；不改阶段产物。",
}

// readRoleBrief 读项目或用户级的角色规则，截断后放进本阶段提示词。
func readRoleBrief(root, executor string) string {
	if file := roleRuleFiles[executor]; file != "" {
		candidates := []string{
			filepath.Join(root, ".claude", "rules", file),
			filepath.Join(root, ".agentdeck", "rules", file),
		}
		if home, err := os.UserHomeDir(); err == nil {
			candidates = append(candidates, filepath.Join(home, ".claude", "rules", file))
		}
		for _, p := range candidates {
			if text := readSnippet(p, 2200); text != "" {
				return text
			}
		}
	}
	return roleFallback[executor]
}

// readSnippet 读一段文本并压成单行，避免提示词里出现换行把 TUI 打断。
func readSnippet(path string, max int) string {
	if path == "" {
		return ""
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return ""
	}
	runes := []rune(text)
	if len(runes) > max {
		text = string(runes[:max]) + "…"
	}
	return oneLine(text)
}
