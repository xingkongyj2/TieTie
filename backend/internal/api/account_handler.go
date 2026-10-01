package api

import (
	"context"
	"crypto/rand"
	"errors"
	"net/http"
	"strings"

	"tietie/backend/internal/auth"
	"tietie/backend/internal/dbop"
	"tietie/backend/internal/qoder"
)

// BindingPayload 是返回给前端的绑定信息。
type BindingPayload struct {
	SessionID string `json:"sessionId"`
	PartnerID int64  `json:"partnerId"`
}

// UserPayload 是返回给前端的用户信息（永不含密码）。
type UserPayload struct {
	UserID   int64  `json:"userId"`
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
// 用邀请码找到对方 → 复用同一对的历史会话或校验双方均未绑定 → 云端新建后落库。
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
	body.Code = strings.TrimSpace(body.Code)
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
	selfBinding, err := s.DB.GetLatestBindingByUser(r.Context(), self.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	if selfBinding != nil {
		writeError(w, qoder.NewApiError(409, "already_bound", "你已和其他人绑定，不能再绑定新用户。"))
		return
	}
	partnerBinding, err := s.DB.GetLatestBindingByUser(r.Context(), partner.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	if partnerBinding != nil {
		writeError(w, qoder.NewApiError(409, "partner_already_bound", "对方已和其他人绑定，不能重复绑定。"))
		return
	}

	// 本地没有这对的绑定记录：先按配对标题在云端找回历史会话（解绑后重绑同一人），找不到才新建。
	title := dbop.SessionTitle(self.ID, partner.ID)
	recovered, err := s.Qoder.FindSessionByTitle(r.Context(), title)
	if err != nil {
		writeError(w, err)
		return
	}
	var sessionID string
	var createdSession *qoder.PublicSession
	if recovered != nil {
		sessionID = recovered.ID
	} else {
		agentID, envID, err := s.Qoder.ResolveAgentAndEnv(r.Context(), s.Cfg.AgentID, s.Cfg.EnvironmentID)
		if err != nil {
			writeError(w, err)
			return
		}
		createdSession, err = s.Qoder.CreateSession(r.Context(), agentID, envID, title)
		if err != nil {
			writeError(w, err)
			return
		}
		sessionID = createdSession.ID
	}
	discarded := func() {
		if createdSession != nil {
			s.cleanupOrphanSession(createdSession.ID)
		}
	}
	created, err := s.DB.CreateBinding(r.Context(), self.ID, partner.ID, sessionID)
	if err != nil {
		discarded()
		if errors.Is(err, dbop.ErrAlreadyBound) {
			writeError(w, qoder.NewApiError(409, "already_bound", "你或对方已和其他人绑定，不能重复绑定。"))
			return
		}
		writeError(w, err)
		return
	}
	if !created {
		discarded()
		// 对方几乎同时也发起了绑定并先写入：以已存在的记录为准。
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
	}
	writeJSON(w, http.StatusOK, AccountResult{
		User:    userPayload(self),
		Binding: &BindingPayload{SessionID: sessionID, PartnerID: partner.ID},
	})
}

// handleUnbind 处理 POST /api/account/unbind：退出当前会话，即解除绑定关系。
// 双方共享同一行绑定，解绑后两人都回到绑定引导页；云端会话与其中的历史都不删除。
func (s *Server) handleUnbind(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
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
	binding, err := s.DB.Unbind(r.Context(), self.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	if binding != nil {
		logf("用户 %d 退出会话 %s", self.ID, binding.SessionID)
	}
	writeJSON(w, http.StatusOK, AccountResult{User: userPayload(self)})
}

func (s *Server) cleanupOrphanSession(sessionID string) {
	go func() {
		if err := s.Qoder.DeleteSession(context.WithoutCancel(context.Background()), sessionID); err != nil {
			logf("清理多余云端会话 %s 失败: %v", sessionID, err)
		}
	}()
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
	if userID == 0 {
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

// generateCode 生成 4 位数字邀请码（0000-9999）；撞车由调用方重试。
func generateCode() (string, error) {
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for i, b := range buf {
		buf[i] = byte('0' + b%10)
	}
	return string(buf), nil
}
