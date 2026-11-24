package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ptyHost 是跨平台的伪终端句柄：Windows 用 ConPTY，Linux/macOS 用 POSIX PTY。
type ptyHost interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	Resize(cols, rows int) error
	Wait(ctx context.Context) (uint32, error)
	Close() error
}

// Event 是推给前端的一条终端事件。
type Event struct {
	Type     string `json:"type"`
	Data     string `json:"data,omitempty"`
	ExitCode int    `json:"exitCode,omitempty"`
}

// Session 是一个 PTY 会话。
type Session struct {
	ID       string
	Label    string
	title    string
	agent    string // 会话对应的 CLI id（流水线派发时写入，界面用来画品牌头像）
	Cwd      string
	pty      ptyHost
	mu       sync.Mutex
	subs     map[chan Event]struct{}
	buffer   []byte
	lastOut  time.Time
	state    string
	exitCode int
	closed   bool
}

// maxBuffer 控制重连时能回放多少历史输出。
const maxBuffer = 256 * 1024

// appendBuffer 记录输出，方便晚订阅的前端补齐历史。
func (s *Session) appendBuffer(chunk []byte) {
	s.mu.Lock()
	s.buffer = append(s.buffer, chunk...)
	if len(s.buffer) > maxBuffer {
		s.buffer = s.buffer[len(s.buffer)-maxBuffer:]
	}
	s.lastOut = time.Now()
	s.mu.Unlock()
}

// OutputSig 返回当前输出总字节数和最后一次输出的时间，
// 供流水线判断 CLI 是否已经静止（TUI 启动完成）。
func (s *Session) OutputSig() (int, time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.buffer), s.lastOut
}

// ansiPattern 匹配 ANSI 颜色、光标和 OSC 标题序列。
var ansiPattern = regexp.MustCompile("\x1b(?:\\[[0-9;?=>]*[ -/]*[@-~]|\\][^\x07\x1b]*(?:\x07|\x1b\\\\)|[@-_])")

// boxOnly 判断一行是不是只有边框字符（终端 UI 的分隔线）。
var boxOnly = regexp.MustCompile(`^[\s─━═╌╍┄┅┈┉│┃┆┇┊┋╭╮╯╰┌┐└┘]+$`)

// TailText 返回最近的终端输出（已去掉控制序列），给看板卡片做实时预览。
func (s *Session) TailText(max int) string {
	s.mu.Lock()
	raw := make([]byte, 0, min(len(s.buffer), max*8))
	if len(s.buffer) > max*8 {
		raw = append(raw, s.buffer[len(s.buffer)-max*8:]...)
	} else {
		raw = append(raw, s.buffer...)
	}
	s.mu.Unlock()

	text := ansiPattern.ReplaceAllString(strings.ToValidUTF8(string(raw), ""), "")
	text = strings.ReplaceAll(text, "\r", "")
	lines := make([]string, 0, 8)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || boxOnly.MatchString(line) {
			continue
		}
		lines = append(lines, line)
	}
	if len(lines) > 4 {
		lines = lines[len(lines)-4:]
	}
	out := strings.Join(lines, " · ")
	if len([]rune(out)) > max {
		out = string([]rune(out)[:max])
	}
	return out
}

// PlainText 返回去掉 ANSI 控制序列的完整终端文本，阶段结束后归档用。
func (s *Session) PlainText(max int) string {
	s.mu.Lock()
	raw := make([]byte, 0, len(s.buffer))
	if max > 0 && len(s.buffer) > max {
		raw = append(raw, s.buffer[len(s.buffer)-max:]...)
	} else {
		raw = append(raw, s.buffer...)
	}
	s.mu.Unlock()

	text := ansiPattern.ReplaceAllString(strings.ToValidUTF8(string(raw), ""), "")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return strings.ReplaceAll(text, "\r", "\n")
}

// SessionSnapshot 是给前端列表用的只读视图。
type SessionSnapshot struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Title    string `json:"title,omitempty"` // 流水线会话的「任务 · 阶段」标记
	Agent    string `json:"agent,omitempty"` // 会话使用的 CLI id，界面用来画品牌头像
	Cwd      string `json:"cwd"`
	State    string `json:"state"`
	ExitCode int    `json:"exitCode"`
}

// Snapshot 返回给前端列表用的只读视图。
func (s *Session) Snapshot() SessionSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return SessionSnapshot{ID: s.ID, Label: s.Label, Title: s.title, Agent: s.agent, Cwd: s.Cwd, State: s.state, ExitCode: s.exitCode}
}

// SetAgent 记录会话使用的 CLI id，供界面显示品牌头像。
func (s *Session) SetAgent(agent string) {
	s.mu.Lock()
	s.agent = agent
	s.mu.Unlock()
}

// SetTitle 给会话加一个可读标记（流水线用来标「任务 · 阶段」）。
func (s *Session) SetTitle(title string) {
	s.mu.Lock()
	s.title = title
	s.mu.Unlock()
}

func (s *Session) setState(state string, code int) {
	s.mu.Lock()
	s.state = state
	s.exitCode = code
	s.mu.Unlock()
}

// State 返回 running / exited / error。
func (s *Session) State() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

func (s *Session) broadcast(event Event) {
	s.mu.Lock()
	subs := make([]chan Event, 0, len(s.subs))
	for ch := range s.subs {
		subs = append(subs, ch)
	}
	s.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- event:
		default: // 订阅者太慢就丢帧，别把终端堵死
		}
	}
}

// subscribeWithHistory 原子地注册订阅并取回历史，避免重复或漏帧。
func (s *Session) subscribeWithHistory() ([]byte, chan Event) {
	ch := make(chan Event, 256)
	s.mu.Lock()
	defer s.mu.Unlock()
	history := make([]byte, len(s.buffer))
	copy(history, s.buffer)
	s.subs[ch] = struct{}{}
	return history, ch
}

func (s *Session) unsubscribe(ch chan Event) {
	s.mu.Lock()
	delete(s.subs, ch)
	s.mu.Unlock()
	close(ch)
}

// SessionManager 管理所有 PTY 会话。
type SessionManager struct {
	mu    sync.Mutex
	seq   int
	items map[string]*Session
}

// NewSessionManager 创建会话管理器。
func NewSessionManager() *SessionManager {
	return &SessionManager{items: make(map[string]*Session)}
}

// Count 返回当前会话数。
func (m *SessionManager) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.items)
}

// List 返回全部会话快照。
func (m *SessionManager) List() []SessionSnapshot {
	m.mu.Lock()
	items := make([]*Session, 0, len(m.items))
	for _, s := range m.items {
		items = append(items, s)
	}
	m.mu.Unlock()

	out := make([]SessionSnapshot, 0, len(items))
	for _, s := range items {
		out = append(out, s.Snapshot())
	}
	return out
}

// Start 在指定工作目录启动一个 PTY 会话。
func (m *SessionManager) Start(command, cwd string) (*Session, error) {
	if err := ptyAvailable(); err != nil {
		return nil, err
	}
	workdir := cwd
	if workdir == "" {
		workdir, _ = os.UserHomeDir()
	}
	if info, err := os.Stat(workdir); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("工作目录不存在: %s", workdir)
	}

	handle, err := startPTY(command, workdir, 120, 32)
	if err != nil {
		return nil, fmt.Errorf("启动失败: %w", err)
	}

	m.mu.Lock()
	m.seq++
	id := fmt.Sprintf("s%d", m.seq)
	session := &Session{
		ID:    id,
		Label: command,
		Cwd:   workdir,
		pty:   handle,
		subs:  make(map[chan Event]struct{}),
		state: "running",
	}
	m.items[id] = session
	m.mu.Unlock()

	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, readErr := handle.Read(buf)
			if n > 0 {
				chunk := append([]byte(nil), buf[:n]...)
				session.appendBuffer(chunk)
				session.broadcast(Event{
					Type: "data",
					Data: base64.StdEncoding.EncodeToString(chunk),
				})
			}
			if readErr != nil {
				break
			}
		}
	}()

	go func() {
		code, waitErr := handle.Wait(context.Background())
		state := "exited"
		if waitErr != nil {
			state = "error"
		}
		session.setState(state, int(code))
		session.broadcast(Event{Type: "exit", ExitCode: int(code)})
	}()

	return session, nil
}

func (m *SessionManager) get(id string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.items[id]
}

// SetTitle 给会话设置可读标记。
func (m *SessionManager) SetTitle(id, title string) {
	if s := m.get(id); s != nil {
		s.SetTitle(title)
	}
}

// SetAgent 给会话设置 CLI id（流水线派发时用）。
func (m *SessionManager) SetAgent(id, agent string) {
	if s := m.get(id); s != nil {
		s.SetAgent(agent)
	}
}

// Write 往会话里写输入。
func (m *SessionManager) Write(id string, data []byte) error {
	session := m.get(id)
	if session == nil {
		return fmt.Errorf("会话不存在")
	}
	_, err := session.pty.Write(data)
	return err
}

// Resize 调整 PTY 尺寸。
func (m *SessionManager) Resize(id string, cols, rows int) {
	session := m.get(id)
	if session == nil || cols < 2 || rows < 2 {
		return
	}
	_ = session.pty.Resize(cols, rows)
}

// Close 结束会话并释放 PTY。
func (m *SessionManager) Close(id string) {
	session := m.get(id)
	if session == nil {
		return
	}
	_ = session.pty.Close()
	m.mu.Lock()
	delete(m.items, id)
	m.mu.Unlock()
}

// Stream 把会话输出以 SSE 推给前端。
func (m *SessionManager) Stream(w http.ResponseWriter, r *http.Request, id string) {
	session := m.get(id)
	if session == nil {
		writeError(w, http.StatusNotFound, "会话不存在")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "不支持流式输出")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	history, ch := session.subscribeWithHistory()
	defer session.unsubscribe(ch)

	// 先补历史，晚订阅也不丢输出。
	if len(history) > 0 {
		writeSSE(w, Event{Type: "data", Data: base64.StdEncoding.EncodeToString(history)})
		flusher.Flush()
	}
	if snapshot := session.Snapshot(); snapshot.State != "running" {
		writeSSE(w, Event{Type: "exit", ExitCode: snapshot.ExitCode})
		flusher.Flush()
		return
	}

	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case event, open := <-ch:
			if !open {
				return
			}
			writeSSE(w, event)
			flusher.Flush()
			if event.Type == "exit" {
				return
			}
		}
	}
}
