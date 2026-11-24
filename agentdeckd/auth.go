package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"time"
)

// authCookie 是登录成功后写入的 HttpOnly Cookie 名称。
const authCookie = "agentdeck_token"

// Auth 负责对外监听时的访问鉴权。token 为空表示不启用（仅本机监听时）。
type Auth struct {
	token   string
	enabled bool
}

// NewAuth 创建鉴权器；token 为空时关闭鉴权。
func NewAuth(token string) *Auth {
	token = strings.TrimSpace(token)
	return &Auth{token: token, enabled: token != ""}
}

// Enabled 返回是否启用了鉴权。
func (a *Auth) Enabled() bool { return a != nil && a.enabled }

// Authenticated 判断请求是否携带了正确令牌。
func (a *Auth) Authenticated(r *http.Request) bool {
	if !a.Enabled() {
		return true
	}
	if secureEqual(bearerToken(r), a.token) {
		return true
	}
	if cookie, err := r.Cookie(authCookie); err == nil && secureEqual(cookie.Value, a.token) {
		return true
	}
	if query := r.URL.Query().Get("token"); query != "" && secureEqual(query, a.token) {
		return true
	}
	return false
}

// Middleware 保护所有数据接口；探活、登录和静态界面保持可访问。
func (a *Auth) Middleware(next http.Handler) http.Handler {
	if !a.Enabled() {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions || !strings.HasPrefix(r.URL.Path, "/api/") || authPublicPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if a.Authenticated(r) {
			next.ServeHTTP(w, r)
			return
		}
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"error":        "unauthorized",
			"authRequired": true,
		})
	})
}

// authPublicPath 是不需要令牌的接口。
func authPublicPath(path string) bool {
	switch path {
	case "/api/v1/health", "/api/v1/auth/status", "/api/v1/auth/login", "/api/v1/auth/logout":
		return true
	}
	return false
}

// Register 注册登录相关接口。
func (a *Auth) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/auth/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"enabled":       a.Enabled(),
			"authenticated": a.Authenticated(r),
		})
	})

	mux.HandleFunc("POST /api/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
		if !a.Enabled() {
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "enabled": false})
			return
		}
		var body struct {
			Token string `json:"token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "请求格式错误")
			return
		}
		if !secureEqual(strings.TrimSpace(body.Token), a.token) {
			// 稍微拖慢暴力尝试，不落日志里的明文令牌。
			time.Sleep(400 * time.Millisecond)
			writeError(w, http.StatusUnauthorized, "访问令牌不正确")
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name:     authCookie,
			Value:    a.token,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			Secure:   requestIsHTTPS(r),
			MaxAge:   int((30 * 24 * time.Hour).Seconds()),
		})
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "enabled": true})
	})

	mux.HandleFunc("POST /api/v1/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{
			Name:     authCookie,
			Value:    "",
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			Secure:   requestIsHTTPS(r),
			MaxAge:   -1,
		})
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
}

// bearerToken 从 Authorization 或 X-AgentDeck-Token 里取令牌。
func bearerToken(r *http.Request) string {
	if raw := strings.TrimSpace(r.Header.Get("Authorization")); raw != "" {
		if len(raw) > 7 && strings.EqualFold(raw[:7], "bearer ") {
			return strings.TrimSpace(raw[7:])
		}
		return raw
	}
	return strings.TrimSpace(r.Header.Get("X-AgentDeck-Token"))
}

// secureEqual 常量时间比较，避免时序侧信道。
func secureEqual(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// requestIsHTTPS 判断请求是否走 HTTPS（含反向代理）。
func requestIsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https")
}

// randomToken 生成 32 字节的 URL 安全随机令牌。
func randomToken() string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

// localHostGuard 在未启用令牌时校验 Host 指向本机。
// 防的是 DNS rebinding：恶意网页把域名解析到 127.0.0.1，借浏览器调用本地接口。
func localHostGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLoopbackHost(r.Host) {
			writeError(w, http.StatusForbidden, "只允许通过本机地址访问（对外开放请用 -token）")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isLoopbackHost 判断 Host 头（可带端口）是否指向本机。
func isLoopbackHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// isLoopbackAddr 判断监听地址是否只对本机开放。
func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if host == "" {
		return false // :3210 表示监听所有网卡
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
