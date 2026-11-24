package main

// 阶段门禁：产物、交接包和产物新鲜度校验。

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// checkGate 校验阶段真的产出了黑板上的文件，并且留下了可用的交接包。
func (m *PipelineManager) checkGate(run *pipelineRun, state wfState, stage string) error {
	if err := m.checkArtifact(run, state, stage); err != nil {
		return err
	}
	if err := checkHandoff(run, stage); err != nil {
		return err
	}
	// 返工后的阶段必须写本轮的新产物，避免拿上一轮的旧文件冒充完成。
	if run.reworkCount(stage) > 0 {
		if at, ok := run.dispatchedAt(stage); ok {
			newest := newestArtifact(run, state, stage)
			if !newest.IsZero() && newest.Before(at.Add(-2*time.Second)) {
				return fmt.Errorf("产物是本轮打回之前写的（%s），确认后重跑本阶段", newest.Format("15:04:05"))
			}
		}
	}
	return nil
}

// checkArtifact 校验阶段上报的产物文件真实存在且非空。
func (m *PipelineManager) checkArtifact(run *pipelineRun, state wfState, stage string) error {
	dir := filepath.Join(run.root, "artifacts", run.slug)
	check := func(p string) error {
		abs := p
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(dir, abs)
		}
		info, err := os.Stat(abs)
		if err != nil || info.IsDir() || info.Size() == 0 {
			return fmt.Errorf("产物不存在或为空：%s", p)
		}
		return nil
	}
	if p := stageArtifactPath(state, stage); p != "" {
		return check(p)
	}
	if raw, ok := state.Stages[stage]; ok {
		var info struct {
			Artifacts []string `json:"artifacts"`
		}
		if json.Unmarshal(raw, &info) == nil {
			for _, p := range info.Artifacts {
				if err := check(p); err != nil {
					return err
				}
			}
			if len(info.Artifacts) > 0 {
				return nil
			}
		}
	}
	expected := filepath.Join(dir, stageMeta[stage].Dir)
	found := false
	_ = filepath.WalkDir(expected, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || found {
			return nil
		}
		if strings.HasSuffix(strings.ToLower(p), ".md") {
			if info, e := d.Info(); e == nil && info.Size() > 0 {
				found = true
			}
		}
		return nil
	})
	if !found {
		return fmt.Errorf("%s 里没有非空 Markdown 产物", stageMeta[stage].Dir)
	}
	return nil
}

// handoffSections 是交接包必须包含的六节，下一阶段靠它接住上下文。
var handoffSections = []string{
	"inputs_used", "outputs", "decisions", "assumptions", "open_questions", "next_focus",
}

// checkHandoff 校验交接包存在且六节齐全；独立终端方案里，交接包就是阶段之间的接口。
func checkHandoff(run *pipelineRun, stage string) error {
	path := filepath.Join(run.root, "artifacts", run.slug, ".agentdeck", "handoff", stage+".md")
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("交接包缺失（.agentdeck/handoff/%s.md）", stage)
	}
	text := strings.ToLower(string(raw))
	missing := make([]string, 0, len(handoffSections))
	for _, section := range handoffSections {
		if !strings.Contains(text, section) {
			missing = append(missing, section)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("交接包缺少 %s", strings.Join(missing, " / "))
	}
	return nil
}
