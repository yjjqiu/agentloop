package main

// agentgateway 的下载安装：发布产物名、镜像回退、sha256 校验、版本探测。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// —— 安装 ——

// assetName 拼出当前平台对应的发布产物名，例如 agentgateway-linux-amd64。
func assetName() string {
	arch := runtime.GOARCH
	name := fmt.Sprintf("agentgateway-%s-%s", runtime.GOOS, arch)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

// releasePath 是发布产物相对 GitHub 的路径，镜像前缀拼在它前面。
func releasePath(ver string) string {
	return fmt.Sprintf("%s/releases/download/v%s/%s", gatewayRepo, ver, assetName())
}

// downloadSources 是候选下载前缀：国内直连 GitHub 经常 0 速，换镜像基本能跑满。
// 顺序按"最可信"排，失败自动往下试。
func downloadSources() []string {
	return []string{
		"https://github.com/",
		"https://gh-proxy.com/https://github.com/",
		"https://ghfast.top/https://github.com/",
	}
}

// Install 异步下载并安装 agentgateway（一次只跑一个任务）。
func (g *Gateway) Install() error {
	g.jobMu.Lock()
	if g.job.Running {
		g.jobMu.Unlock()
		return fmt.Errorf("安装任务已经在跑")
	}
	g.job = GatewayJob{Running: true, Phase: "准备下载", StartedAt: time.Now().UnixMilli()}
	g.jobMu.Unlock()

	go g.runInstall()
	return nil
}

// Job 返回安装任务快照。
func (g *Gateway) Job() GatewayJob {
	g.jobMu.Lock()
	defer g.jobMu.Unlock()
	return g.job
}

// setJob 更新任务进度。
func (g *Gateway) setJob(fn func(*GatewayJob)) {
	g.jobMu.Lock()
	fn(&g.job)
	g.jobMu.Unlock()
}

// runInstall 依次尝试官方源和镜像，下载二进制 + 校验 sha256 + 落盘。
func (g *Gateway) runInstall() {
	fail := func(err error) {
		g.setJob(func(j *GatewayJob) {
			j.Running = false
			j.Error = err.Error()
			j.DoneAt = time.Now().UnixMilli()
		})
	}
	if err := os.MkdirAll(g.Dir(), 0o755); err != nil {
		fail(fmt.Errorf("创建目录失败: %w", err))
		return
	}

	ver := gatewayDefaultVersion
	path := releasePath(ver)
	tmp := g.BinPath() + ".download"
	var lastErr error

	for _, src := range downloadSources() {
		// 每个源给 8 分钟，卡住的源不会把整个任务拖死。
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
		sum, err := g.download(ctx, src+path, tmp)
		cancel()
		if err != nil {
			lastErr = err
			g.setJob(func(j *GatewayJob) { j.Phase = "换源重试" })
			continue
		}
		g.setJob(func(j *GatewayJob) { j.Phase = "校验文件" })
		if want := g.fetchChecksum(ver, src); want != "" && !strings.EqualFold(sum, want) {
			_ = os.Remove(tmp)
			lastErr = fmt.Errorf("sha256 校验不通过，已丢弃下载的文件")
			continue
		}
		if err := os.Rename(tmp, g.BinPath()); err != nil {
			_ = os.Remove(tmp)
			lastErr = fmt.Errorf("落盘失败: %w", err)
			continue
		}
		_ = os.Chmod(g.BinPath(), 0o755)
		g.mu.Lock()
		g.ver, g.verAt = ver, time.Now()
		g.mu.Unlock()
		g.setJob(func(j *GatewayJob) {
			j.Running = false
			j.Phase = "完成"
			j.DoneAt = time.Now().UnixMilli()
		})
		return
	}
	_ = os.Remove(tmp)
	if lastErr == nil {
		lastErr = fmt.Errorf("没有可用的下载源")
	}
	fail(lastErr)
}

// download 把 url 拉到 dst，返回文件内容的 sha256（十六进制）。
func (g *Gateway) download(ctx context.Context, url, dst string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	// 私有网络环境里也可能配了 GitHub token，带上能少踩限流。
	if token := g.app.config.GitHubTokenValue(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载失败: HTTP %d", resp.StatusCode)
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return "", fmt.Errorf("写入失败: %w", err)
	}
	g.setJob(func(j *GatewayJob) {
		j.Phase = "下载中"
		j.Done = 0
		j.Total = resp.ContentLength
	})
	h := sha256.New()
	buf := make([]byte, 256*1024)
	var done int64
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				out.Close()
				return "", fmt.Errorf("写入失败: %w", werr)
			}
			h.Write(buf[:n])
			done += int64(n)
			g.setJob(func(j *GatewayJob) { j.Done = done })
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			out.Close()
			return "", fmt.Errorf("下载中断: %w", readErr)
		}
	}
	if err := out.Close(); err != nil {
		return "", fmt.Errorf("写入失败: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// fetchChecksum 取官方 .sha256（拿不到就返回空，不阻断安装）。
func (g *Gateway) fetchChecksum(ver, src string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src+releasePath(ver)+".sha256", nil)
	if err != nil {
		return ""
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return ""
	}
	fields := strings.Fields(string(raw))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// Version 返回二进制版本号（调 --version，结果缓存 10 分钟）。
func (g *Gateway) Version() string {
	g.mu.Lock()
	if g.ver != "" && time.Since(g.verAt) < 10*time.Minute {
		v := g.ver
		g.mu.Unlock()
		return v
	}
	g.mu.Unlock()

	if !g.Installed() {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	// 用 -V（简短版）。--version 打 JSON，而且 1.7.0 的构建里值是 "True"，
	// 所以只在结果像版本号时才采用，否则留空，由界面回落到托管版本。
	probe := exec.CommandContext(ctx, g.BinPath(), "-V")
	hideWindow(probe)
	out, err := probe.Output()
	if err != nil {
		return ""
	}
	v := strings.TrimSpace(string(out))
	if len(v) > 40 || !versionLike(v) {
		return ""
	}
	g.mu.Lock()
	g.ver, g.verAt = v, time.Now()
	g.mu.Unlock()
	return v
}

// versionLike 判断 -V 的输出像不像版本号（防止把构建里写错的字符串当版本显示）。
func versionLike(v string) bool {
	if v == "" {
		return false
	}
	dot := false
	for _, r := range v {
		switch {
		case r >= '0' && r <= '9':
		case r == '.':
			dot = true
		case r == '-' || r == '+' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
		default:
			return false
		}
	}
	return dot
}
