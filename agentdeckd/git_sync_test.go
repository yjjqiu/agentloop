package main

// git_sync_test.go：用本地 bare 仓库当远端，验证领先 / 落后统计和 fetch / pull / push。
// 全程不联网、不碰用户凭据。

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func needGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("本机没有 git，跳过")
	}
}

// gitExec 在 dir 里跑 git，失败直接结束测试。
func gitExec(t *testing.T, dir string, args ...string) string {
	t.Helper()
	base := append([]string{"-C", dir, "-c", "user.name=test", "-c", "user.email=test@example.com"}, args...)
	out, err := exec.Command("git", base...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v 失败: %v\n%s", args, err, out)
	}
	return string(out)
}

// setupGitSandbox 建一个 bare 远端 + 工作副本，工作副本已经推过一次。
func setupGitSandbox(t *testing.T) (work, remote string) {
	t.Helper()
	needGit(t)
	root := t.TempDir()
	remote = filepath.Join(root, "remote.git")
	work = filepath.Join(root, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	gitExec(t, root, "init", "--bare", remote)
	gitExec(t, work, "init")
	gitExec(t, work, "symbolic-ref", "HEAD", "refs/heads/main")
	if err := os.WriteFile(filepath.Join(work, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitExec(t, work, "add", "a.txt")
	gitExec(t, work, "commit", "-m", "first")
	gitExec(t, work, "remote", "add", "origin", remote)
	gitExec(t, work, "push", "-u", "origin", "main")
	return work, remote
}

func TestGitSyncStatusClean(t *testing.T) {
	work, _ := setupGitSandbox(t)
	st := readGitSyncStatus(work)
	if !st.IsRepo || st.Branch != "main" || st.Upstream != "origin/main" {
		t.Fatalf("状态不对: %#v", st)
	}
	if st.Ahead != 0 || st.Behind != 0 || st.Dirty != 0 {
		t.Fatalf("刚推送过应全为 0: %#v", st)
	}
}

func TestGitSyncStatusNotRepo(t *testing.T) {
	if st := readGitSyncStatus(t.TempDir()); st.IsRepo {
		t.Fatalf("普通目录不该认成 git 仓库: %#v", st)
	}
}

func TestGitSyncPushPull(t *testing.T) {
	work, remote := setupGitSandbox(t)

	// 本地提交 → 领先 1 → push 后归零。
	if err := os.WriteFile(filepath.Join(work, "a.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitExec(t, work, "commit", "-am", "second")
	if st := readGitSyncStatus(work); st.Ahead != 1 {
		t.Fatalf("本地提交后应领先 1: %#v", st)
	}
	if _, err := runGitSync(work, "push", ""); err != nil {
		t.Fatalf("push 失败: %v", err)
	}
	if st := readGitSyncStatus(work); st.Ahead != 0 {
		t.Fatalf("push 后应不领先: %#v", st)
	}

	// 另开一个副本推新提交 → 原副本 fetch 后落后 1 → pull 后归零且内容更新。
	other := filepath.Join(filepath.Dir(work), "other")
	// 显式指定分支：bare 远端的 HEAD 默认指向 master，不指定会克隆到空分支。
	gitExec(t, filepath.Dir(work), "clone", "-b", "main", remote, other)
	if err := os.WriteFile(filepath.Join(other, "b.txt"), []byte("three\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitExec(t, other, "add", "b.txt")
	gitExec(t, other, "commit", "-m", "third")
	gitExec(t, other, "push", "origin", "main")

	if _, err := runGitSync(work, "fetch", "origin"); err != nil {
		t.Fatalf("fetch 失败: %v", err)
	}
	if st := readGitSyncStatus(work); st.Behind != 1 {
		t.Fatalf("fetch 后应落后 1: %#v", st)
	}
	if _, err := runGitSync(work, "pull", ""); err != nil {
		t.Fatalf("pull 失败: %v", err)
	}
	if st := readGitSyncStatus(work); st.Behind != 0 {
		t.Fatalf("pull 后应不落后: %#v", st)
	}
	if _, err := os.Stat(filepath.Join(work, "b.txt")); err != nil {
		t.Fatalf("pull 后应有 b.txt: %v", err)
	}
}

func TestGitSyncRejectsUnknownAction(t *testing.T) {
	work, _ := setupGitSandbox(t)
	if _, err := runGitSync(work, "reset --hard", ""); err == nil {
		t.Fatal("未知操作应当被拒绝")
	}
}

func TestGitSyncRejectsUnknownRemote(t *testing.T) {
	work, _ := setupGitSandbox(t)
	if _, err := runGitSync(work, "fetch", "no-such-remote"); err == nil {
		t.Fatal("未知远端应当被拒绝")
	}
}

func TestFirstLines(t *testing.T) {
	if got := firstLines("a\nb\nc\nd", 2); got != "a\nb" {
		t.Errorf("firstLines = %q", got)
	}
	if got := firstLines("only", 3); got != "only" {
		t.Errorf("firstLines = %q", got)
	}
}
