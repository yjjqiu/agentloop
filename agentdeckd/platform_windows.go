//go:build windows

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/UserExistsError/conpty"
)

// —— Windows 平台实现：ConPTY、cmd.exe 包装、npm 全局目录 ——

// startPTY 用 ConPTY 启动命令。extraEnv 覆盖继承环境里的同名变量
// （网关接管时用来改上游地址）；为空时不传，子进程直接继承 daemon 的环境。
func startPTY(command, workdir string, cols, rows int, extraEnv []string) (ptyHost, error) {
	opts := []conpty.ConPtyOption{
		conpty.ConPtyDimensions(cols, rows),
		conpty.ConPtyWorkDir(workdir),
	}
	if len(extraEnv) > 0 {
		opts = append(opts, conpty.ConPtyEnv(mergeEnv(os.Environ(), extraEnv)))
	}
	handle, err := conpty.Start(wrapCommand(command), opts...)
	if err != nil {
		return nil, err
	}
	return handle, nil
}

// ptyAvailable 检查系统是否支持 ConPTY。
func ptyAvailable() error {
	if !conpty.IsConPtyAvailable() {
		return fmt.Errorf("当前系统不支持 ConPTY（需要 Windows 10 1809+）")
	}
	return nil
}

// wrapCommand 决定怎么把命令交给 ConPTY：
// .exe 直接跑，其余（.cmd/.bat/裸命令）用 cmd.exe /c 包一层。
func wrapCommand(command string) string {
	lower := strings.ToLower(command)
	if strings.HasSuffix(lower, ".exe") || strings.HasSuffix(lower, ".com") {
		return command
	}
	comspec := os.Getenv("ComSpec")
	if comspec == "" {
		comspec = `C:\Windows\System32\cmd.exe`
	}
	return comspec + ` /c ` + command
}

// hideWindow 让后台命令不弹控制台窗口。
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}

// versionProbeCommand 构造 <cli> --version 的探测命令。
func versionProbeCommand(ctx context.Context, path string) *exec.Cmd {
	lower := strings.ToLower(path)
	if strings.HasSuffix(lower, ".exe") || strings.HasSuffix(lower, ".com") {
		cmd := exec.CommandContext(ctx, path, "--version")
		hideWindow(cmd)
		return cmd
	}
	comspec := os.Getenv("ComSpec")
	if comspec == "" {
		comspec = `C:\Windows\System32\cmd.exe`
	}
	cmd := exec.CommandContext(ctx, comspec, "/c", filepath.Base(path), "--version")
	// 用文件名 + 切目录调用，避开 cmd /c 剥引号导致带空格路径失败。
	cmd.Dir = filepath.Dir(path)
	hideWindow(cmd)
	return cmd
}

// installCommand 构造 CLI 安装 / 更新命令。
func installCommand(ctx context.Context, task jobTask) *exec.Cmd {
	comspec := os.Getenv("ComSpec")
	if comspec == "" {
		comspec = `C:\Windows\System32\cmd.exe`
	}
	// cmd /c 会把整条命令里的首个引号配对剥掉，路径带空格（Program Files）就会失败；
	// 先把工作目录切到可执行文件所在目录，用文件名调用，命令里就不需要引号。
	var cmd *exec.Cmd
	if task.self {
		if isExeFile(task.exe) {
			cmd = exec.CommandContext(ctx, task.exe, task.updateArgs...)
		} else {
			args := append([]string{"/c", filepath.Base(task.exe)}, task.updateArgs...)
			cmd = exec.CommandContext(ctx, comspec, args...)
			cmd.Dir = filepath.Dir(task.exe)
		}
	} else {
		args := []string{"/c", filepath.Base(task.npm), "install", "-g", task.pkg + "@latest", "--no-fund", "--no-audit"}
		cmd = exec.CommandContext(ctx, comspec, args...)
		cmd.Dir = filepath.Dir(task.npm)
	}
	hideWindow(cmd)
	return cmd
}

// isExeFile 判断能否直接启动（.exe/.com），否则要经 cmd /c。
func isExeFile(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, ".exe") || strings.HasSuffix(lower, ".com")
}

// processAlive 判断 pid 对应的进程是否还在（用 tasklist，不引额外依赖）。
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	cmd := exec.Command("tasklist", "/FI", "PID eq "+strconv.Itoa(pid), "/NH")
	hideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	want := strconv.Itoa(pid)
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == want {
			return true
		}
	}
	return false
}

// killProcessTree 结束进程及其子进程。
func killProcessTree(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("无效的进程号")
	}
	cmd := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid))
	hideWindow(cmd)
	return cmd.Run()
}

// detachProcess 在 Windows 下不需要额外处理，隐藏窗口由 hideWindow 负责。
func detachProcess(_ *exec.Cmd) {}

// pathExtensions 返回按优先级排列的候选扩展名（小写）。
func pathExtensions() []string {
	if raw := strings.TrimSpace(os.Getenv("PATHEXT")); raw != "" {
		exts := make([]string, 0, 4)
		for _, ext := range strings.Split(raw, ";") {
			if ext = strings.ToLower(strings.TrimSpace(ext)); ext != "" {
				exts = append(exts, ext)
			}
		}
		return exts
	}
	return []string{"", ".exe", ".cmd", ".bat"}
}

// extraBinDirs 返回 PATH 之外还要扫的目录（npm 全局、bun 等）。
func extraBinDirs() []string {
	var dirs []string
	if appData := os.Getenv("APPDATA"); appData != "" {
		dirs = append(dirs, filepath.Join(appData, "npm"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs,
			filepath.Join(home, "AppData", "Roaming", "npm"),
			filepath.Join(home, ".bun", "bin"),
			filepath.Join(home, ".local", "bin"),
		)
	}
	return dirs
}

// npmGlobalRoot 返回 Windows 上 npm 全局 node_modules 目录。
func npmGlobalRoot() string {
	if appData := os.Getenv("APPDATA"); appData != "" {
		root := filepath.Join(appData, "npm", "node_modules")
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			return root
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		root := filepath.Join(home, "AppData", "Roaming", "npm", "node_modules")
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			return root
		}
	}
	return ""
}

// npmBinDir 把 npm 全局 node_modules 目录换算成全局可执行文件目录。
// Windows 下可执行文件和 node_modules 同级（%APPDATA%\npm）。
func npmBinDir(root string) string {
	return filepath.Dir(root)
}
