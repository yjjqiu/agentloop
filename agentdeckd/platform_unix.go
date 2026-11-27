//go:build !windows

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"syscall"

	"github.com/creack/pty"
)

// —— Linux / macOS 平台实现：POSIX PTY、sh 包装、npm 全局目录 ——

// unixPTY 把 creack/pty 的句柄 + 子进程包装成统一的 ptyHost。
type unixPTY struct {
	cmd  *exec.Cmd
	file *os.File
}

func (p *unixPTY) Read(b []byte) (int, error)  { return p.file.Read(b) }
func (p *unixPTY) Write(b []byte) (int, error) { return p.file.Write(b) }

// Resize 调整 PTY 窗口大小。
func (p *unixPTY) Resize(cols, rows int) error {
	return pty.Setsize(p.file, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
}

// Wait 等待子进程退出，返回退出码。
func (p *unixPTY) Wait(ctx context.Context) (uint32, error) {
	done := make(chan error, 1)
	go func() { done <- p.cmd.Wait() }()
	select {
	case err := <-done:
		if err == nil {
			return 0, nil
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return uint32(exit.ExitCode()), err
		}
		return 1, err
	case <-ctx.Done():
		return 1, ctx.Err()
	}
}

// Close 关闭 PTY 并结束子进程。
func (p *unixPTY) Close() error {
	err := p.file.Close()
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	return err
}

// startPTY 用 /bin/sh 启动命令，并把 stdio 接到伪终端。
// extraEnv 覆盖继承环境里的同名变量（网关接管时用来改上游地址）。
func startPTY(command, workdir string, cols, rows int, extraEnv []string) (ptyHost, error) {
	cmd := exec.Command("/bin/sh", "-c", "exec "+command)
	cmd.Dir = workdir
	cmd.Env = mergeEnv(append(os.Environ(), "TERM=xterm-256color"), extraEnv)
	file, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
	if err != nil {
		return nil, err
	}
	return &unixPTY{cmd: cmd, file: file}, nil
}

// ptyAvailable 检查系统是否有可用的伪终端。
func ptyAvailable() error {
	if _, err := os.Stat("/dev/ptmx"); err != nil {
		return fmt.Errorf("当前系统没有可用的 PTY（/dev/ptmx）")
	}
	return nil
}

// wrapCommand 在 POSIX 下不需要改命令，保留给 startPTY 的对称调用。
func wrapCommand(command string) string { return command }

// hideWindow 在 POSIX 下没有意义。
func hideWindow(_ *exec.Cmd) {}

// versionProbeCommand 构造 <cli> --version 的探测命令。
func versionProbeCommand(ctx context.Context, path string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, path, "--version")
	cmd.Dir = filepath.Dir(path)
	return cmd
}

// installCommand 构造 CLI 安装 / 更新命令。
func installCommand(ctx context.Context, task jobTask) *exec.Cmd {
	if task.self {
		cmd := exec.CommandContext(ctx, task.exe, task.updateArgs...)
		cmd.Dir = filepath.Dir(task.exe)
		return cmd
	}
	cmd := exec.CommandContext(ctx, task.npm, "install", "-g", task.pkg+"@latest", "--no-fund", "--no-audit")
	cmd.Dir = filepath.Dir(task.npm)
	return cmd
}

// isExeFile 在 POSIX 下按执行权限判断。
func isExeFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
}

// processAlive 判断 pid 对应的进程是否还在。
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// killProcessTree 结束进程组（启动时用 detachProcess 设了 Setpgid）。
func killProcessTree(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("无效的进程号")
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err == nil {
		return nil
	}
	return syscall.Kill(pid, syscall.SIGKILL)
}

// detachProcess 让子进程独立成组，便于连同子进程一起结束。
func detachProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// pathExtensions 在 POSIX 下不靠扩展名找可执行文件。
func pathExtensions() []string {
	return []string{""}
}

// extraBinDirs 返回 PATH 之外还要扫的目录。
func extraBinDirs() []string {
	dirs := []string{"/usr/local/bin", "/usr/bin"}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs,
			filepath.Join(home, ".local", "bin"),
			filepath.Join(home, ".npm-global", "bin"),
			filepath.Join(home, ".bun", "bin"),
		)
	}
	if nvmBin := os.Getenv("NVM_BIN"); nvmBin != "" {
		dirs = append(dirs, nvmBin)
	}
	return dirs
}

// npmGlobalRoot 返回 npm 全局 node_modules 目录（取第一个存在的）。
func npmGlobalRoot() string {
	var roots []string
	if prefix := os.Getenv("NPM_CONFIG_PREFIX"); prefix != "" {
		roots = append(roots, filepath.Join(prefix, "lib", "node_modules"))
	}
	roots = append(roots, "/usr/local/lib/node_modules", "/usr/lib/node_modules")
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots,
			filepath.Join(home, ".npm-global", "lib", "node_modules"),
			filepath.Join(home, ".local", "lib", "node_modules"),
		)
		if matches, err := filepath.Glob(filepath.Join(home, ".nvm", "versions", "node", "*", "lib", "node_modules")); err == nil {
			sort.Sort(sort.Reverse(sort.StringSlice(matches)))
			roots = append(roots, matches...)
		}
	}
	for _, root := range roots {
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			return root
		}
	}
	return ""
}

// npmBinDir 把 npm 全局 node_modules 目录换算成全局可执行文件目录。
// POSIX 下 node_modules 在 <prefix>/lib/node_modules，可执行文件在 <prefix>/bin，差两级。
func npmBinDir(root string) string {
	return filepath.Clean(filepath.Join(filepath.Dir(root), "..", "bin"))
}
