package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestClaudeSessionIDPattern(t *testing.T) {
	valid := []string{
		"11111111-2222-3333-4444-555555555555",
		"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
	}
	invalid := []string{
		"", "notes", "11111111-2222-3333-4444-55555555555",
		"../../etc/passwd", "11111111-2222-3333-4444-55555555555z",
		"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee; rm -rf /",
	}
	for _, id := range valid {
		if !claudeSessionIDPattern.MatchString(id) {
			t.Fatalf("合法 id 被判为非法: %q", id)
		}
	}
	for _, id := range invalid {
		if claudeSessionIDPattern.MatchString(id) {
			t.Fatalf("非法 id 通过了校验: %q", id)
		}
	}
}

// TestNewestClaudeSession 只认启动之后还在写的最新 UUID 会话文件。
func TestNewestClaudeSession(t *testing.T) {
	dir := t.TempDir()
	started := time.Now()
	fresh := "11111111-2222-3333-4444-555555555555"
	old := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	for _, name := range []string{fresh + ".jsonl", old + ".jsonl", "notes.jsonl"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	past := started.Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(dir, old+".jsonl"), past, past); err != nil {
		t.Fatal(err)
	}
	if got := newestClaudeSession(dir, started); got != fresh {
		t.Fatalf("拿到 %q，期望 %q", got, fresh)
	}
	if got := newestClaudeSession(filepath.Join(dir, "missing"), started); got != "" {
		t.Fatalf("目录不存在时应返回空，实际 %q", got)
	}
}

func TestCanResumeClaudeRejectsOtherAgents(t *testing.T) {
	if canResumeClaude("codex", "11111111-2222-3333-4444-555555555555", "/tmp") {
		t.Fatal("非 claude-code 不应尝试恢复")
	}
	if canResumeClaude("claude-code", "not-a-uuid", "/tmp") {
		t.Fatal("非法 id 不应尝试恢复")
	}
}

// TestCanResumeClaudeFindsTranscript 按项目目录拼出 Claude 的会话文件路径。
func TestCanResumeClaudeFindsTranscript(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	root := t.TempDir()
	id := "11111111-2222-3333-4444-555555555555"
	dir := filepath.Join(home, ".claude", "projects", mungeProjectDir(root))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !canResumeClaude("claude-code", id, root) {
		t.Fatal("会话文件存在时应允许恢复")
	}
	if canResumeClaude("claude-code", "22222222-3333-4444-5555-666666666666", root) {
		t.Fatal("会话文件不存在时不应恢复")
	}
}
