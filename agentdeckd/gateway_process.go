package main

// agentgateway 的进程托管：启动、认领上次遗留的进程、停止、日志泵。

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// —— 进程托管 ——

// Start 拉起 agentgateway；已经在跑就报错。
func (g *Gateway) Start() error {
	g.mu.Lock()
	if g.runningLocked() {
		g.mu.Unlock()
		return fmt.Errorf("网关已经在运行")
	}
	if !g.Installed() {
		g.mu.Unlock()
		return fmt.Errorf("还没安装 agentgateway，先点「安装」")
	}
	s := g.Settings()
	g.mu.Unlock()

	if err := g.writeConfig(s); err != nil {
		return err
	}

	cmd := exec.Command(g.BinPath(), "-f", g.ConfigPath())
	cmd.Dir = g.Dir()
	cmd.Env = append(os.Environ(), "NO_COLOR=1")
	hideWindow(cmd)
	detachProcess(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("启动失败: %w", err)
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动失败: %w", err)
	}

	g.mu.Lock()
	g.cmd = cmd
	g.pid = cmd.Process.Pid
	g.logs = nil
	g.startErr = ""
	g.startedAt = time.Now()
	g.mu.Unlock()

	_ = os.WriteFile(g.pidPath(), []byte(strconv.Itoa(cmd.Process.Pid)), 0o600)

	go g.pump(cmd, stdout)
	go func(pid int) {
		err := cmd.Wait()
		g.mu.Lock()
		if g.pid == pid {
			g.cmd = nil
			if err != nil {
				g.startErr = err.Error()
			}
		}
		g.mu.Unlock()
	}(cmd.Process.Pid)

	// 进程起来不等于端口通了。等业务端口真正开始监听再返回，否则紧跟着的
	// 终端/请求会撞上「connection refused」，看起来像接管失败。
	if !waitPort(s.Port, 5*time.Second) {
		_ = killProcessTree(cmd.Process.Pid)
		_ = os.Remove(g.pidPath())
		return fmt.Errorf("网关没有在 5 秒内监听端口 %d，看看日志", s.Port)
	}
	return nil
}

// pump 把子进程输出收进环形缓冲，界面可以看最近日志。
func (g *Gateway) pump(cmd *exec.Cmd, r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 512*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(cliAnsiPattern.ReplaceAllString(scanner.Text(), ""))
		if line == "" {
			continue
		}
		g.mu.Lock()
		g.logs = append(g.logs, line)
		if len(g.logs) > gatewayMaxLogs {
			g.logs = append([]string(nil), g.logs[len(g.logs)-gatewayMaxLogs:]...)
		}
		g.mu.Unlock()
	}
}

// Stop 结束网关进程（连同子进程）。
func (g *Gateway) Stop() error {
	g.mu.Lock()
	pid := g.pid
	proc := g.cmd
	g.mu.Unlock()

	if pid == 0 {
		// 不是本进程拉起的：读 pid 文件兜底。
		if raw, err := os.ReadFile(g.pidPath()); err == nil {
			if n, _ := strconv.Atoi(strings.TrimSpace(string(raw))); n > 0 && processAlive(n) {
				pid = n
			}
		}
	}
	if pid == 0 || !processAlive(pid) {
		_ = os.Remove(g.pidPath())
		g.mu.Lock()
		g.pid, g.cmd = 0, nil
		g.mu.Unlock()
		return fmt.Errorf("网关没在运行")
	}
	if proc != nil {
		_ = proc.Process.Kill()
	}
	_ = killProcessTree(pid)
	_ = os.Remove(g.pidPath())
	g.mu.Lock()
	g.pid, g.cmd = 0, nil
	g.mu.Unlock()
	// 等端口真正释放，避免紧接着重启时撞 address in use。
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !portOpen(g.Settings().Port) && !portOpen(g.Settings().AdminPort) {
			break
		}
		time.Sleep(80 * time.Millisecond)
	}
	return nil
}

// runningLocked 判断进程是否还在（调用方持锁）。
func (g *Gateway) runningLocked() bool {
	if g.pid == 0 {
		g.adoptLocked()
	}
	if g.pid > 0 && processAlive(g.pid) {
		return true
	}
	if g.pid > 0 {
		g.pid, g.cmd = 0, nil
	}
	return false
}

// adoptLocked 认领上一次运行留下的进程（daemon 重启后子进程还在跑）。
// 只信 pid 文件里那个 pid，并且要求它真的活着。
func (g *Gateway) adoptLocked() {
	raw, err := os.ReadFile(g.pidPath())
	if err != nil {
		return
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	if pid <= 0 || !processAlive(pid) {
		return
	}
	g.pid = pid
	if info, err := os.Stat(g.pidPath()); err == nil {
		g.startedAt = info.ModTime()
	}
}

// Running 返回托管进程是否在跑。
func (g *Gateway) Running() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.runningLocked()
}

// portOpen 判断本机端口是否有人监听。
func portOpen(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 300*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// waitPort 等端口开始监听；超时返回 false。
func waitPort(port int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if portOpen(port) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(80 * time.Millisecond)
	}
}
