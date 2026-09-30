package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"

	"tietie/backend/internal/auth"
	"tietie/backend/internal/dbop"
	"tietie/backend/internal/qoder"
)

// BindingPayload 是返回给前端的绑定信息。
type BindingPayload struct {
	SessionID string `json:"sessionId"`
	PartnerID string `json:"partnerId"`
}

// UserPayload 是返回给前端的用户信息（永不含密码哈希）。
type UserPayload struct {
	UserID   string `json:"userId"`
	Username string `json:"username"`
	Code     string `json:"code"`
}

// AccountResult 是登录/注册/查询账号接口的统一响应体。
type AccountResult struct {
	Token   string          `json:"token,omitempty"`
	User    UserPayload     `json:"user"`
	Binding *BindingPayload `json:"binding"`
}

func userPayload(u *dbop.User) UserPayload {
	return UserPayload{UserID: u.ID, Username: u.Username, Code: u.Code}
}

// handleMe 处理 GET /api/account/me：用 JWT 里的用户 ID 返回账号与当前绑定。
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}
	userID := auth.UserIDFrom(r.Context())
	user, err := s.DB.GetUserByID(r.Context(), userID)
	if err != nil {
		writeError(w, err)
		return
	}
	if user == nil {
		writeError(w, qoder.NewApiError(401, "user_not_found", "账号不存在，请重新注册。"))
		return
	}
	result := AccountResult{User: userPayload(user)}
	if payload, err := s.bindingPayload(r.Context(), user); err != nil {
		writeError(w, err)
		return
	} else {
		result.Binding = payload
	}
	writeJSON(w, http.StatusOK, result)
}

// handleBind 处理 POST /api/account/bind：
// 用邀请码找到对方 → 两人 ID 组成唯一键查绑定 → 有历史会话直接返回，没有则云端新建后落库。
func (s *Server) handleBind(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if apiErr := decodeJSONBody(r, &body, 4096); apiErr != nil {
		writeError(w, apiErr)
		return
	}
	body.Code = strings.ToUpper(strings.TrimSpace(body.Code))
	if body.Code == "" {
		writeError(w, qoder.NewApiError(400, "invalid_code", "请输入对方的邀请码。"))
		return
	}

	self, err := s.DB.GetUserByID(r.Context(), auth.UserIDFrom(r.Context()))
	if err != nil {
		writeError(w, err)
		return
	}
	if self == nil {
		writeError(w, qoder.NewApiError(401, "user_not_found", "账号不存在，请重新注册。"))
		return
	}
	partner, err := s.DB.GetUserByCode(r.Context(), body.Code)
	if err != nil {
		writeError(w, err)
		return
	}
	if partner == nil {
		writeError(w, qoder.NewApiError(404, "invite_not_found", "邀请码不存在，请让对方打开贴贴获取专属邀请码。"))
		return
	}
	if partner.ID == self.ID {
		writeError(w, qoder.NewApiError(400, "cannot_bind_self", "这是你自己的邀请码，输入对方的才能绑定哦。"))
		return
	}

	// 已有绑定：直接复用历史会话。
	existing, err := s.DB.GetBindingByPair(r.Context(), self.ID, partner.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	if existing != nil {
		writeJSON(w, http.StatusOK, AccountResult{
			User:    userPayload(self),
			Binding: &BindingPayload{SessionID: existing.SessionID, PartnerID: partner.ID},
		})
		return
	}

	// 首次绑定：云端新建会话 → 落库。
	agentID, envID, err := s.Qoder.ResolveAgentAndEnv(r.Context(), s.Cfg.AgentID, s.Cfg.EnvironmentID)
	if err != nil {
		writeError(w, err)
		return
	}
	session, err := s.Qoder.CreateSession(r.Context(), agentID, envID)
	if err != nil {
		writeError(w, err)
		return
	}
	created, err := s.DB.CreateBinding(r.Context(), self.ID, partner.ID, session.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	sessionID := session.ID
	if !created {
		// 对方几乎同时也发起了绑定并先写入：以已存在的记录为准，删掉刚多建的会话。
		existing, err = s.DB.GetBindingByPair(r.Context(), self.ID, partner.ID)
		if err != nil {
			writeError(w, err)
			return
		}
		if existing == nil {
			writeError(w, qoder.NewApiError(500, "internal_error", "会话服务发生错误，请稍后重试。"))
			return
		}
		sessionID = existing.SessionID
		go func(orphan string) {
			if derr := s.Qoder.DeleteSession(context.WithoutCancel(context.Background()), orphan); derr != nil {
				logf("清理多余云端会话 %s 失败: %v", orphan, derr)
			}
		}(session.ID)
	}
	writeJSON(w, http.StatusOK, AccountResult{
		User:    userPayload(self),
		Binding: &BindingPayload{SessionID: sessionID, PartnerID: partner.ID},
	})
}

// bindingPayload 查询用户当前生效的绑定。
func (s *Server) bindingPayload(ctx context.Context, user *dbop.User) (*BindingPayload, error) {
	binding, err := s.DB.GetLatestBindingByUser(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	if binding == nil {
		return nil, nil
	}
	return &BindingPayload{SessionID: binding.SessionID, PartnerID: binding.OtherUser(user.ID)}, nil
}

// ensureBoundSession 校验目标会话属于当前登录用户的绑定，防止越权读写他人会话。
func (s *Server) ensureBoundSession(ctx context.Context, sessionID string) *qoder.ApiError {
	userID := auth.UserIDFrom(ctx)
	if userID == "" {
		return qoder.NewApiError(401, "unauthorized", "请先登录。")
	}
	binding, err := s.DB.GetLatestBindingByUser(ctx, userID)
	if err != nil {
		return qoder.NewApiError(500, "internal_error", "会话服务发生错误，请稍后重试。")
	}
	if binding == nil || binding.SessionID != sessionID {
		return qoder.NewApiError(403, "session_forbidden", "你没有访问此会话的权限。")
	}
	return nil
}

func generateCode() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for i, b := range buf {
		buf[i] = codeAlphabet[int(b)%len(codeAlphabet)]
	}
	return string(buf), nil
}

func randomID(prefix string, n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(buf), nil
}
