package api

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"tietie/backend/internal/auth"
	"tietie/backend/internal/dbop"
	"tietie/backend/internal/qoder"
)

// 邀请码字符集：去掉 0/O、1/I/L 等易混淆字符。
const codeAlphabet = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"

// 用户名：2-24 位中文、字母、数字、下划线或短横线。
var usernameRe = regexp.MustCompile(`^[\p{Han}A-Za-z0-9_-]{2,24}$`)

func logf(format string, args ...any) { log.Printf(format, args...) }

// handleAuthRegister 处理 POST /api/auth/register：用户名+密码注册，成功即登录（返回 JWT）。
func (s *Server) handleAuthRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if apiErr := decodeJSONBody(r, &body, 4096); apiErr != nil {
		writeError(w, apiErr)
		return
	}
	username := strings.TrimSpace(body.Username)
	if !usernameRe.MatchString(username) {
		writeError(w, qoder.NewApiError(400, "invalid_username", "用户名需为 2-24 位中文、字母、数字、下划线或短横线。"))
		return
	}
	if pwErr := validatePassword(body.Password); pwErr != nil {
		writeError(w, pwErr)
		return
	}
	user, apiErr := s.createUser(r.Context(), username, body.Password)
	if apiErr != nil {
		writeError(w, apiErr)
		return
	}
	s.respondAuthed(w, r, user, true)
}

// handleAuthLogin 处理 POST /api/auth/login：用户名+密码登录，返回 JWT 与当前绑定。
func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if apiErr := decodeJSONBody(r, &body, 4096); apiErr != nil {
		writeError(w, apiErr)
		return
	}
	invalid := qoder.NewApiError(401, "invalid_credentials", "用户名或密码不正确。")
	user, err := s.DB.GetUserByUsername(r.Context(), strings.TrimSpace(body.Username))
	if err != nil {
		writeError(w, err)
		return
	}
	if user == nil || !auth.VerifyPassword(user.PasswordHash, body.Password) {
		writeError(w, invalid)
		return
	}
	s.respondAuthed(w, r, user, true)
}

// respondAuthed 签发令牌并返回账号信息（注册/登录共用）。
func (s *Server) respondAuthed(w http.ResponseWriter, r *http.Request, user *dbop.User, withBinding bool) {
	token, err := s.Auth.IssueToken(user.ID, user.Username)
	if err != nil {
		writeError(w, qoder.NewApiError(500, "internal_error", "会话服务发生错误，请稍后重试。"))
		return
	}
	result := AccountResult{Token: token, User: userPayload(user)}
	if withBinding {
		payload, err := s.bindingPayload(r.Context(), user)
		if err != nil {
			writeError(w, err)
			return
		}
		result.Binding = payload
	}
	writeJSON(w, http.StatusOK, result)
}

// createUser 创建用户：bcrypt 哈希密码、生成防碰撞邀请码。
func (s *Server) createUser(ctx context.Context, username, password string) (*dbop.User, *qoder.ApiError) {
	hash, err := auth.HashPassword(password)
	if err != nil {
		return nil, qoder.NewApiError(500, "internal_error", "会话服务发生错误，请稍后重试。")
	}
	internal := qoder.NewApiError(500, "internal_error", "会话服务发生错误，请稍后重试。")
	for attempt := 0; attempt < 10; attempt++ {
		code, err := generateCode()
		if err != nil {
			return nil, internal
		}
		taken, err := s.DB.CodeExists(ctx, code)
		if err != nil {
			return nil, qoder.NewApiError(500, "internal_error", "会话服务发生错误，请稍后重试。")
		}
		if taken {
			continue
		}
		id, err := randomID("usr_", 12)
		if err != nil {
			return nil, internal
		}
		user := &dbop.User{ID: id, Username: username, PasswordHash: hash, Code: code}
		err = s.DB.CreateUser(ctx, user)
		if err == nil {
			return user, nil
		}
		switch {
		case strings.Contains(err.Error(), "users.username"):
			return nil, qoder.NewApiError(409, "username_taken", "这个用户名已经被占用啦，换一个试试。")
		case strings.Contains(err.Error(), "users.code"):
			continue // 邀请码撞车，重试
		default:
			logf("创建用户失败: %v", err)
			return nil, internal
		}
	}
	return nil, internal
}

func validatePassword(password string) *qoder.ApiError {
	if utf8.RuneCountInString(password) < 6 || len(password) > 64 {
		return qoder.NewApiError(400, "invalid_password", "密码至少 6 位，最长 64 位。")
	}
	return nil
}

// decodeJSONBody 读取小体积 JSON 请求体（账号类接口通用）。
func decodeJSONBody(r *http.Request, out any, maxBytes int64) *qoder.ApiError {
	media := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]))
	if media != "" && media != "application/json" {
		return qoder.NewApiError(415, "unsupported_media_type", "请求必须使用 JSON 格式。")
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, maxBytes+1))
	if err != nil || int64(len(data)) > maxBytes {
		return qoder.NewApiError(400, "invalid_json", "请求格式不正确，请重试。")
	}
	if err := json.Unmarshal(data, out); err != nil {
		return qoder.NewApiError(400, "invalid_json", "请求格式不正确，请重试。")
	}
	return nil
}
