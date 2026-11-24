package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// CliJob 是一次 CLI 安装 / 更新任务的进度快照。
type CliJob struct {
	Target    string   `json:"target,omitempty"`
	Label     string   `json:"label,omitempty"`
	Kind      string   `json:"kind,omitempty"` // install | update
	Running   bool     `json:"running"`
	OK        bool     `json:"ok"`
	Error     string   `json:"error,omitempty"`
	Lines     []string `json:"lines"`
	StartedAt int64    `json:"startedAt,omitempty"`
	DoneAt    int64    `json:"doneAt,omitempty"`
}

// CliInstaller 用 npm 全局包的方式安装 / 更新 CLI，一次只跑一个任务。
type CliInstaller struct {
	config   *Config
	versions *VersionChecker

	mu   sync.Mutex
	job  CliJob
	stop context.CancelFunc
}

const cliJobMaxLines = 200

// ansiPattern 去掉 npm 输出里的颜色控制符（sessions 里已有同用途的正则，这里换个名字）。
var cliAnsiPattern = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)

// NewCliInstaller 创建安装器。
func NewCliInstaller(config *Config, versions *VersionChecker) *CliInstaller {
	return &CliInstaller{config: config, versions: versions}
}

// Snapshot 返回当前任务快照（没有任务时返回 nil）。
func (c *CliInstaller) Snapshot() *CliJob {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.job.StartedAt == 0 {
		return nil
	}
	job := c.job
	job.Lines = append([]string(nil), c.job.Lines...)
	return &job
}

// Cancel 中止正在进行的任务。
func (c *CliInstaller) Cancel() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.job.Running {
		return false
	}
	if c.stop != nil {
		c.stop()
		c.stop = nil
	}
	return true
}

// Start 安装或更新一款 CLI：未安装走 npm 安装；已安装时优先用 CLI 自带更新命令
// （例如 pi update），npm 全局安装的包走 npm install -g。
func (c *CliInstaller) Start(id string) error {
	def, ok := agentByID(id)
	if !ok {
		return fmt.Errorf("未知的 CLI: %s", id)
	}

	idx := newPathIndex()
	exe := idx.find(append([]string{def.Bin}, def.AltBins...))
	kind := "install"
	if exe != "" {
		kind = "update"
	}

	pkg := strings.TrimSpace(c.config.GetCliOverride(id).Package)
	if pkg == "" {
		pkg = strings.TrimSpace(def.Npm)
	}
	if pkg == "" {
		if detected, ok := c.versions.PackageFor(def.Bin); ok {
			pkg = detected
		}
	}

	// 自管理安装的 CLI（不在 npm 全局目录）用自带更新命令，避免 npm 更新到另一份副本。
	self := kind == "update" && len(def.Update) > 0 && !c.versions.IsNpmManaged(exe)
	if kind == "update" && !self && !c.versions.IsNpmManaged(exe) {
		return fmt.Errorf("%s 不是 npm 全局安装，也没有内置更新命令，请手动更新", def.Name)
	}
	npm := ""
	if !self {
		if pkg == "" {
			return fmt.Errorf("%s 没有对应的 npm 包，请先在 npm 包一栏填写", def.Name)
		}
		npm = idx.find([]string{"npm"})
		if npm == "" {
			return fmt.Errorf("找不到 npm，请先安装 Node.js")
		}
	}

	c.mu.Lock()
	if c.job.Running {
		c.mu.Unlock()
		return fmt.Errorf("已有安装任务在跑，等它结束或先取消")
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.stop = cancel
	c.job = CliJob{
		Target:    id,
		Label:     def.Name,
		Kind:      kind,
		Running:   true,
		StartedAt: time.Now().UnixMilli(),
		Lines:     []string{},
	}
	c.mu.Unlock()

	go c.run(ctx, jobTask{id: id, label: def.Name, kind: kind, exe: exe, npm: npm, pkg: pkg, self: self, updateArgs: def.Update})
	return nil
}

// jobTask 是一次任务的执行参数。
type jobTask struct {
	id         string
	label      string
	kind       string
	exe        string
	npm        string
	pkg        string
	self       bool
	updateArgs []string
}

func (c *CliInstaller) run(ctx context.Context, task jobTask) {
	verb := "安装"
	if task.kind == "update" {
		verb = "更新"
	}

	if task.self {
		c.log(task.id, fmt.Sprintf("%s %s（%s %s）…", verb, task.label, filepath.Base(task.exe), strings.Join(task.updateArgs, " ")))
	} else {
		c.log(task.id, fmt.Sprintf("%s %s（npm 全局包 %s@latest）…", verb, task.label, task.pkg))
	}
	cmd := installCommand(ctx, task)
	cmd.Env = append(os.Environ(), "npm_config_update_notifier=false", "NO_COLOR=1")

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		c.finish(task.id, false, err)
		return
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		c.finish(task.id, false, err)
		return
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		c.log(task.id, scanner.Text())
	}
	err = cmd.Wait()
	if ctx.Err() != nil {
		c.finish(task.id, false, fmt.Errorf("已取消"))
		return
	}
	if err != nil {
		c.finish(task.id, false, fmt.Errorf("%s退出失败: %v", verb, err))
		return
	}
	// 包变了，版本缓存和 node_modules 映射都要重读。
	c.versions.Invalidate()
	c.log(task.id, fmt.Sprintf("%s完成，重新探测版本…", verb))
	c.finish(task.id, true, nil)
}

// log 追加一行任务日志；\r 进度行只保留最后一段。
func (c *CliInstaller) log(target, line string) {
	line = cliAnsiPattern.ReplaceAllString(line, "")
	if idx := strings.LastIndexByte(line, '\r'); idx >= 0 {
		line = line[idx+1:]
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.job.Target != target {
		return
	}
	if len(c.job.Lines) >= cliJobMaxLines {
		c.job.Lines = append([]string(nil), c.job.Lines[len(c.job.Lines)/2:]...)
	}
	c.job.Lines = append(c.job.Lines, line)
}

func (c *CliInstaller) finish(target string, ok bool, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.job.Target != target {
		return
	}
	c.job.Running = false
	c.job.OK = ok
	c.job.DoneAt = time.Now().UnixMilli()
	if err != nil {
		c.job.Error = err.Error()
		if len(c.job.Lines) >= cliJobMaxLines {
			c.job.Lines = append([]string(nil), c.job.Lines[len(c.job.Lines)/2:]...)
		}
		c.job.Lines = append(c.job.Lines, "失败："+err.Error())
	}
	if c.stop != nil {
		c.stop()
		c.stop = nil
	}
}
