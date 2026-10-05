// Package api 是后端的 HTTP 汇聚层：所有对前端的路由集中注册在 router.go，
// 每个业务域一个 handler 文件；handler 只做参数校验、编排（qoder 客户端 + dbop 落库）与响应输出。
package api

import (
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"tietie/backend/internal/auth"
	"tietie/backend/internal/config"
	"tietie/backend/internal/dbop"
	"tietie/backend/internal/qoder"
	"tietie/backend/internal/weather"
	"tietie/backend/internal/wechat"
)

// Server 聚合全部 handler 依赖。
type Server struct {
	Cfg              *config.Config
	Qoder            *qoder.Client
	Auth             *auth.Service
	DB               *dbop.DB // 可为 nil：未配置数据库时账号类接口返回 503
	Weather          weather.Provider
	Wechat           wechat.CodeExchanger
	WechatMessages   wechat.MessageSender
	weatherOnce      sync.Once
	locksMu          sync.Mutex
	sessionLocks     map[string]*conversationLock
	wakeOnce         sync.Once
	conversationWake chan struct{}
	controlWake      chan struct{}
	memoryWake       chan struct{}
}

// NewRouter 是全后端唯一的路由注册点。
// 新增接口 = 对应 handler 文件里加方法 + 这里加一行。
func NewRouter(s *Server) http.Handler {
	mux := http.NewServeMux()

	// ---- 公开接口：登录/注册（无需 JWT）----
	mux.Handle("/api/auth/register", http.HandlerFunc(s.handleAuthRegister))
	mux.Handle("/api/auth/login", http.HandlerFunc(s.handleAuthLogin))
	mux.Handle("/api/auth/wechat", http.HandlerFunc(s.handleAuthWechat))
	mux.Handle("/api/assets/reminder-titles.woff", http.HandlerFunc(s.handleTitleFont))
	mux.Handle("/api/assets/OFL.txt", http.HandlerFunc(s.handleTitleFontLicense))

	// ---- 需登录接口：JWT 鉴权 ----
	authed := func(h func(http.ResponseWriter, *http.Request)) http.Handler {
		return s.authGuard(http.HandlerFunc(h))
	}
	mux.Handle("/api/account/me", authed(s.handleMe))
	mux.Handle("/api/account/wechat-subscription", authed(s.handleWechatSubscription))
	mux.Handle("/api/account/profiles", authed(s.handleProfiles))
	mux.Handle("/api/account/profile", authed(s.handleProfile))
	mux.Handle("/api/account/regions", authed(s.handleRegions))
	mux.Handle("/api/account/bind", authed(s.handleBind))
	mux.Handle("/api/account/unbind", authed(s.handleUnbind))
	mux.Handle("/api/qoder/sessions/{id}/messages", authed(s.handleMessages))
	mux.Handle("POST /api/qoder/sessions/{id}/cancel", authed(s.handleCancelTurn))
	mux.Handle("GET /api/qoder/sessions/{id}/message-preview", authed(s.handleMessagePreview))
	mux.Handle("GET /api/qoder/sessions/{id}/memories", authed(s.handleMemoryIndex))
	mux.Handle("/api/qoder/sessions/{id}/partner-impression", authed(s.handlePartnerImpression))
	mux.Handle("/api/qoder/sessions/{id}/assistant-settings", authed(s.handleAssistantSettings))
	mux.Handle("/api/qoder/sessions/{id}/countdowns", authed(s.handleCountdowns))
	mux.Handle("/api/qoder/sessions/{id}/care-settings", authed(s.handleCareSettings))
	mux.Handle("/api/qoder/sessions/{id}/care-preview", authed(s.handleCarePreview))
	mux.Handle("/api/qoder/sessions/{id}/anniversaries", authed(s.handleAnniversaries))
	mux.Handle("/api/qoder/sessions/{id}/anniversary-reminder-settings", authed(s.handleAnniversaryReminderSettings))
	mux.Handle("/api/qoder/sessions/{id}/anniversaries/{anniversaryId}", authed(s.handleAnniversaryPin))
	mux.Handle("/api/qoder/sessions/{id}/tool-result", authed(s.handleToolResult))
	mux.Handle("/api/qoder/sessions/{id}/stream", authed(s.handleStream))
	mux.Handle("/api/qoder/sessions/{id}/private-stream", authed(s.handleStream))
	mux.Handle("/api/qoder/sessions/{id}/reminders", authed(s.handleReminders))
	mux.Handle("/api/qoder/sessions/{id}/reminders/{reminderId}", authed(s.handleReminderUpdate))

	// ---- 未知 API 路径统一 JSON 404 ----
	notFound := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { writeRouteNotFound(w) })
	mux.Handle("/api/qoder/", notFound)
	mux.Handle("/api/qoder", notFound)
	mux.Handle("/api/", notFound)

	// ---- 前端静态资源（SPA，无需登录）----
	mux.Handle("/", http.HandlerFunc(s.handleStatic))

	return requestLog(recoverer(openCORS(mux)))
}

// handleStatic 服务 dist/ 静态文件，SPA 路由回退到 index.html（对应 index.mjs serveStatic）。
func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		http.NotFound(w, r)
		return
	}
	root, err := filepath.Abs(s.Cfg.StaticDir)
	if err != nil {
		staticNotFound(w)
		return
	}
	clean := path.Clean("/" + r.URL.Path)
	target := filepath.Join(root, filepath.FromSlash(clean))
	if rel, err := filepath.Rel(root, target); err != nil ||
		rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	info, err := os.Stat(target)
	if err != nil || info.IsDir() {
		if path.Ext(clean) != "" {
			staticNotFound(w)
			return
		}
		target = filepath.Join(root, "index.html")
		if info, err = os.Stat(target); err != nil || info.IsDir() {
			staticNotFound(w)
			return
		}
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if strings.EqualFold(path.Ext(target), ".html") {
		w.Header().Set("Cache-Control", "no-cache")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=3600")
	}
	f, err := os.Open(target)
	if err != nil {
		staticNotFound(w)
		return
	}
	defer f.Close()
	http.ServeContent(w, r, filepath.Base(target), info.ModTime(), f)
}

func staticNotFound(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte("页面不存在。请先运行 npm run build。"))
}
