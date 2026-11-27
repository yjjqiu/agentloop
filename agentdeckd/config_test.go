package main

import "testing"

// TestChatPromptChoice 验证「要不要发提示词」的选择能落盘、能清除，且不丢其它偏好。
func TestChatPromptChoice(t *testing.T) {
	dir := t.TempDir()
	root := dir
	cfg := LoadConfig(dir)

	cfg.SetChatPref(root, ChatPref{Agent: "claude-code", Mode: "safe"})
	if pref := cfg.ChatPrefFor(root); pref.SendPrompt != nil {
		t.Fatalf("没选过时 SendPrompt 应为 nil: %#v", pref)
	}

	yes := true
	cfg.SetChatPref(root, ChatPref{Agent: "claude-code", Mode: "safe", SendPrompt: &yes})

	// 重新读盘，确认 null / true 能正确往返。
	reloaded := LoadConfig(dir)
	pref := reloaded.ChatPrefFor(root)
	if pref.SendPrompt == nil || !*pref.SendPrompt {
		t.Fatalf("选择没有持久化: %#v", pref)
	}
	if pref.Agent != "claude-code" || pref.Mode != "safe" {
		t.Fatalf("其它偏好被弄丢了: %#v", pref)
	}

	reloaded.ClearChatPromptChoice()
	cleared := LoadConfig(dir).ChatPrefFor(root)
	if cleared.SendPrompt != nil {
		t.Fatalf("清除后 SendPrompt 应为 nil: %#v", cleared)
	}
	if cleared.Agent != "claude-code" || cleared.Mode != "safe" {
		t.Fatalf("清除选择时不该动 CLI / 模式: %#v", cleared)
	}
}

func TestChatPromptChoiceFalse(t *testing.T) {
	dir := t.TempDir()
	cfg := LoadConfig(dir)
	no := false
	cfg.SetChatPref(dir, ChatPref{Agent: "codex", SendPrompt: &no})
	pref := cfg.ChatPrefFor(dir)
	if pref.SendPrompt == nil || *pref.SendPrompt {
		t.Fatalf("false 也应能保存: %#v", pref)
	}
}
