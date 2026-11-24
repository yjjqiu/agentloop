package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Stage 是一个阶段在 workflow-state.json 里的快照。
type Stage struct {
	Status       string          `json:"status"`
	AgentType    string          `json:"agent_type"`
	StartedAt    string          `json:"started_at"`
	CompletedAt  string          `json:"completed_at"`
	ArtifactPath string          `json:"artifact_path"`
	SelfCheck    json.RawMessage `json:"self_check"` // 旧版是字符串，新版是对象，原样保留
	Conclusion   string          `json:"conclusion"`
	ReworkRound  int             `json:"rework_round"`
}

type workflowState struct {
	TaskID       string           `json:"task_id"`
	CurrentStage string           `json:"current_stage"`
	LastEvent    string           `json:"last_event"`
	NextTarget   string           `json:"next_target"`
	UpdatedAt    string           `json:"updated_at"`
	SizeClass    string           `json:"size_class"`
	RunMode      string           `json:"run_mode"`
	Stages       map[string]Stage `json:"stages"`
}

// Workflow 是渲染窗口需要的扁平结构。
type Workflow struct {
	Slug         string           `json:"slug"`
	Dir          string           `json:"dir"`
	TaskID       string           `json:"taskId"`
	CurrentStage string           `json:"currentStage"`
	LastEvent    string           `json:"lastEvent"`
	NextTarget   string           `json:"nextTarget"`
	UpdatedAt    string           `json:"updatedAt"`
	SizeClass    string           `json:"sizeClass"`
	RunMode      string           `json:"runMode"`
	Status       string           `json:"status"`
	Completed    int              `json:"completed"`
	Total        int              `json:"total"`
	Stages       map[string]Stage `json:"stages"`
}

// ListWorkflows 扫描 <root>/artifacts/*/workflow-state.json。
func ListWorkflows(root string) []Workflow {
	out := []Workflow{}
	if root == "" {
		return out
	}
	base := filepath.Join(root, "artifacts")
	entries, err := os.ReadDir(base)
	if err != nil {
		return out
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		file := filepath.Join(base, entry.Name(), "workflow-state.json")
		raw, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		var state workflowState
		if err := json.Unmarshal(raw, &state); err != nil {
			continue
		}
		completed, status := summarize(state)
		taskID := state.TaskID
		if taskID == "" {
			taskID = entry.Name()
		}
		out = append(out, Workflow{
			Slug:         entry.Name(),
			Dir:          filepath.Join(base, entry.Name()),
			TaskID:       taskID,
			CurrentStage: state.CurrentStage,
			LastEvent:    state.LastEvent,
			NextTarget:   state.NextTarget,
			UpdatedAt:    state.UpdatedAt,
			SizeClass:    state.SizeClass,
			RunMode:      state.RunMode,
			Status:       status,
			Completed:    completed,
			Total:        len(state.Stages),
			Stages:       state.Stages,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out
}

func summarize(state workflowState) (int, string) {
	completed := 0
	failed := false
	for _, stage := range state.Stages {
		switch strings.ToLower(stage.Status) {
		case "completed", "passed":
			completed++
		case "failed", "blocked":
			failed = true
		}
	}
	switch {
	case state.LastEvent == "workflow_completed" || state.CurrentStage == "FINAL":
		return completed, "completed"
	case failed || strings.HasSuffix(state.LastEvent, "_failed"):
		return completed, "failed"
	case state.CurrentStage != "":
		return completed, "running"
	default:
		return completed, "pending"
	}
}

// LocalStatus 判断项目里装了哪些宿主的 LoopForge Local。
func LocalStatus(root string) map[string]bool {
	status := map[string]bool{"claude": false, "codex": false, "cursor": false}
	if root == "" {
		return status
	}
	for host := range status {
		if info, err := os.Stat(filepath.Join(root, "."+host)); err == nil && info.IsDir() {
			status[host] = true
		}
	}
	return status
}

// InstallLocal 调用 loopforge-cli 把工作流写进项目。
func InstallLocal(localBin, root, host string) (string, error) {
	if localBin == "" {
		return "", fmt.Errorf("没有配置 LoopForge Local 入口")
	}
	if _, err := os.Stat(localBin); err != nil {
		return "", fmt.Errorf("找不到 LoopForge Local: %s", localBin)
	}
	cmd := exec.Command("node", localBin, "install", host, "--project-root", root)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	return string(output), err
}
