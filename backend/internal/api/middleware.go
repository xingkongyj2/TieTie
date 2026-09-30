package api

import (
	"errors"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"tietie/backend/internal/auth"
	"tietie/backend/internal/qoder"
)

// authGuard 校验 Authorization: Bearer <JWT>，通过后把登录用户注入请求上下文。
func (s *Server) authGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenStr, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || strings.TrimSpace(tokenStr) == "" {
			writeError(w, qoder.NewApiError(401, "unauthorized", "请先登录。"))
			return
		}
		claims, err := s.Auth.ParseToken(strings.TrimSpace(tokenStr))
		if err != nil {
			if errors.Is(err, jwt.ErrTokenExpired) {
				writeError(w, qoder.NewApiError(401, "token_expired", "登录已过期，请重新登录。"))
				return
			}
			writeError(w, qoder.NewApiError(401, "invalid_token", "登录状态无效，请重新登录。"))
			return
		}
		next.ServeHTTP(w, r.WithContext(auth.ContextWithClaims(r.Context(), claims)))
	})
}

// requestLog 记录一行访问日志。
func requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(sw, r)
		log.Printf("%s %s %d %s", r.Method, r.URL.Path, sw.status, time.Since(start).Round(time.Millisecond))
	})
}

type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusWriter) WriteHeader(status int) {
	if !w.wroteHeader {
		w.status = status
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	w.wroteHeader = true
	return w.ResponseWriter.Write(b)
}

// Flush 让 SSE 处理器能拿到底层 Flusher。
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// recoverer 把 panic 转成统一的 500 响应。
func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("panic: %v", rec)
				if !headerWritten(r) {
					writeError(w, qoder.NewApiError(500, "internal_error", "会话服务发生错误，请稍后重试。"))
				}
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// headerWritten 无法在标准库中直接查询，这里恒返回 false；
// SSE handler 自行保证 panic 不会发生在头已写出之后（relay 循环内不 panic）。
func headerWritten(*http.Request) bool { return false }

// localHostnames 是未配置 AllowedOrigin 时允许的主机名。
var localHostnames = map[string]bool{
	"127.0.0.1": true, "localhost": true, "::1": true, "[::1]": true,
}

// ensureSameOrigin 复刻 qoder.mjs 的同源防护：
//   - Host 必须是本机回环，或与 QODER_ALLOWED_ORIGIN 完全一致；
//   - Origin / Sec-Fetch-Site 头不允许跨站。
func ensureSameOrigin(r *http.Request, allowedOrigin string) *qoder.ApiError {
	host := r.Host
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	origin := scheme + "://" + host

	hostname := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		hostname = h
	}

	if allowedOrigin != "" {
		u, err := url.Parse(allowedOrigin)
		if err != nil || u.Host == "" || u.Host != host {
			return qoder.NewApiError(403, "invalid_host", "此主机不允许访问云端会话。")
		}
		origin = u.Scheme + "://" + u.Host
	} else if !localHostnames[hostname] {
		return qoder.NewApiError(403, "invalid_host", "此主机不允许访问云端会话。")
	}

	if o := r.Header.Get("Origin"); o != "" && o != origin {
		return qoder.NewApiError(403, "cross_origin_denied", "仅允许从当前应用访问云端会话。")
	}
	if sfs := r.Header.Get("Sec-Fetch-Site"); sfs != "" && sfs != "same-origin" && sfs != "none" {
		return qoder.NewApiError(403, "cross_origin_denied", "仅允许从当前应用访问云端会话。")
	}
	return nil
}

// isClientGone 判断错误是否由客户端断开引起（SSE 场景下静默结束）。
func isClientGone(err error) bool {
	return err != nil && strings.Contains(err.Error(), "context canceled")
}
