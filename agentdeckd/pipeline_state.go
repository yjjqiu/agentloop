package main

// workflow-state.json 读写、阶段状态与产物路径工具。

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type wfState struct {
	CurrentStage string                     `json:"current_stage"`
	LastEvent    string                     `json:"last_event"`
	RuntimeMode  string                     `json:"runtime_mode"`
	Stages       map[string]json.RawMessage `json:"stages"`
	stageStatus  map[string]string
}

func (s wfState) status(key string) string {
	raw, ok := s.Stages[key]
	if !ok {
		return ""
	}
	var stage struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(raw, &stage); err != nil {
		return ""
	}
	return strings.ToLower(stage.Status)
}

func readState(root, slug string) (wfState, string, error) {
	path := filepath.Join(root, "artifacts", slug, "workflow-state.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return wfState{}, path, err
	}
	var state wfState
	if err := json.Unmarshal(raw, &state); err != nil {
		return wfState{}, path, err
	}
	return state, path, nil
}

func fileMtime(path string) time.Time {
	if info, err := os.Stat(path); err == nil {
		return info.ModTime()
	}
	return time.Time{}
}

func previousStage(stage string) string {
	for i, key := range stageOrder {
		if key == stage && i > 0 {
			return stageOrder[i-1]
		}
	}
	return ""
}

// setStageStatus 只改某个阶段的 status，保留其余字段。
func setStageStatus(doc map[string]any, stage, status string) {
	stages, _ := doc["stages"].(map[string]any)
	if stages == nil {
		stages = map[string]any{}
		doc["stages"] = stages
	}
	entry, _ := stages[stage].(map[string]any)
	if entry == nil {
		entry = map[string]any{}
		stages[stage] = entry
	}
	entry["status"] = status
}

// stageArtifactPath 取出阶段上报的产物路径。
func stageArtifactPath(state wfState, stage string) string {
	raw, ok := state.Stages[stage]
	if !ok {
		return ""
	}
	var info struct {
		ArtifactPath string   `json:"artifact_path"`
		Artifacts    []string `json:"artifacts"`
	}
	if json.Unmarshal(raw, &info) != nil {
		return ""
	}
	if info.ArtifactPath != "" {
		return info.ArtifactPath
	}
	if len(info.Artifacts) > 0 {
		return info.Artifacts[0]
	}
	return ""
}

func stageIndex(stage string) int {
	for i, key := range stageOrder {
		if key == stage {
			return i
		}
	}
	return -1
}

// newestArtifact 返回阶段产物里最新的修改时间。
func newestArtifact(run *pipelineRun, state wfState, stage string) time.Time {
	dir := filepath.Join(run.root, "artifacts", run.slug)
	var newest time.Time
	touch := func(p string) {
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, p)
		}
		if info, err := os.Stat(p); err == nil && !info.IsDir() && info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}
	if p := stageArtifactPath(state, stage); p != "" {
		touch(p)
	}
	if raw, ok := state.Stages[stage]; ok {
		var info struct {
			Artifacts []string `json:"artifacts"`
		}
		if json.Unmarshal(raw, &info) == nil {
			for _, p := range info.Artifacts {
				touch(p)
			}
		}
	}
	if newest.IsZero() {
		expected := filepath.Join(dir, stageMeta[stage].Dir)
		_ = filepath.WalkDir(expected, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if info, e := d.Info(); e == nil && info.ModTime().After(newest) {
				newest = info.ModTime()
			}
			return nil
		})
	}
	return newest
}

// archiveStage 把阶段终端输出落到 artifacts，再关掉进程释放内存和租约。
func (m *PipelineManager) archiveStage(run *pipelineRun, stage string) {
	id := run.session(stage)
	if id == "" {
		return
	}
	session := m.app.sessions.get(id)
	if session != nil {
		dir := filepath.Join(run.root, "artifacts", run.slug, ".agentdeck", "stages")
		if err := os.MkdirAll(dir, 0o755); err == nil {
			_ = os.WriteFile(filepath.Join(dir, stage+".log"), []byte(session.PlainText(512*1024)), 0o644)
		}
	}
	if m.configFor(run.root).KeepSessions {
		return
	}
	m.app.sessions.Close(id)
	run.clearSession(stage)
}

func nextStage(stage string) string {
	for i, key := range stageOrder {
		if key == stage && i+1 < len(stageOrder) {
			return stageOrder[i+1]
		}
	}
	return ""
}

func nextPending(state wfState) string {
	for _, key := range stageOrder {
		switch state.status(key) {
		case "completed", "passed":
			continue
		}
		return key
	}
	return ""
}

// patchState 读改写 workflow-state.json，保留未知字段。
func patchState(path string, mutate func(map[string]any)) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return err
	}
	mutate(doc)
	next, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".agentdeck.tmp"
	if err := os.WriteFile(tmp, append(next, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// createRunWorkspace 从需求创建 artifacts 目录和初始 workflow-state.json。
func createRunWorkspace(root, requirement string) (string, error) {
	root = filepath.Clean(root)
	slug := slugFromRequirement(requirement)
	dir := filepath.Join(root, "artifacts", slug)
	for _, sub := range []string{"01-requirement", "02-design", "03-code", "04-e2e", "05-knowledge", "01-solo"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return "", err
		}
	}
	now := time.Now()
	taskID := strings.TrimSpace(strings.SplitN(requirement, "\n", 2)[0])
	if taskID == "" {
		taskID = slug
	}
	if len([]rune(taskID)) > 40 {
		taskID = string([]rune(taskID)[:40])
	}

	stages := map[string]any{}
	for _, key := range stageOrder {
		stages[key] = map[string]any{"status": "pending", "executor": stageMeta[key].Executor}
	}
	stages["SOLO"] = map[string]any{"status": "pending", "executor": "solo-developer"}

	state := map[string]any{
		"version":        "1.3",
		"task_id":        taskID,
		"task_slug":      slug,
		"run_mode":       "auto",
		"runtime_mode":   "agentdeck",
		"size_class":     "medium",
		"current_stage":  "TASK-01",
		"last_event":     "workflow_initialized",
		"next_target":    "analyst",
		"workspace_root": root,
		"artifacts_dir":  dir,
		"created_at":     now.Format(time.RFC3339),
		"updated_at":     now.Format(time.RFC3339),
		"stages":         stages,
		"decisions":      []any{},
	}
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "workflow-state.json"), append(raw, '\n'), 0o644); err != nil {
		return "", err
	}
	return slug, nil
}

func slugFromRequirement(requirement string) string {
	base := strings.TrimSpace(requirement)
	var b strings.Builder
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + 32)
		case strings.ContainsRune(" -_/", r):
			b.WriteByte('-')
		}
		if b.Len() >= 32 {
			break
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		slug = "task"
	}
	return fmt.Sprintf("%s-%s", slug, time.Now().Format("0102-1504"))
}
