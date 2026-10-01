package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"tietie/backend/internal/memoryspace"
	"time"

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
// 用邀请码找到对方 → 校验双方均未绑定 → 新建会话和独立记忆空间后落库。
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

	// 当前已有绑定：幂等返回，不重复初始化。
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

	// Every new binding gets a fresh session and a fresh memory store. An active
	// binding remains idempotent; unbinding never restores cloud history by title.
	if !s.useV2() || !s.Cfg.CloudMemoryEnabled {
		writeError(w, qoder.NewApiError(503, "memory_required", "新建空间需要开启 V2 会话协议和云端记忆。"))
		return
	}
	agentID, envID, err := s.Qoder.ResolveAgentAndEnv(r.Context(), s.Cfg.AgentID, s.Cfg.EnvironmentID)
	if err != nil {
		writeError(w, err)
		return
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		writeError(w, err)
		return
	}
	spaceID := "space_" + hex.EncodeToString(nonce)
	store, err := s.Qoder.CreateMemoryStore(r.Context(), "TieTie-"+spaceID+"-memory", spaceID)
	if err != nil {
		writeError(w, err)
		return
	}
	// Seed before mounting, then fill the actual group/session ID before publish.
	sessionID := ""
	discarded := func() { s.cleanupInitializedSpace(sessionID, store.ID) }
	documents, err := memoryspace.Render("待创建会话", spaceID, memoryspace.Member{ID: self.ID, Name: self.Username}, memoryspace.Member{ID: partner.ID, Name: partner.Username})
	if err != nil {
		discarded()
		writeError(w, err)
		return
	}
	var records []dbop.MemoryRecord
	for _, doc := range documents {
		entry, err := s.Qoder.UpsertMemory(r.Context(), store.ID, doc.Path, doc.Content)
		if err != nil {
			discarded()
			writeError(w, err)
			return
		}
		records = append(records, dbop.MemoryRecord{Path: doc.Path, Kind: "template", Scope: "space", Storage: "database_and_memory", Content: doc.Content, State: "synced", Operation: "upsert", StoreID: store.ID, EntryID: entry.ID, Revision: 1})
	}
	session, err := s.Qoder.CreateSession(r.Context(), agentID, envID, dbop.SessionTitle(self.ID, partner.ID), store.ID)
	if err != nil {
		if definitivelyRejected(err) {
			discarded()
		} else {
			logf("创建会话结果待核实，保留其独立记忆仓库 %s: %v", store.ID, err)
		}
		writeError(w, err)
		return
	}
	sessionID = session.ID
	if err := s.Qoder.RenameMemoryStore(r.Context(), store.ID, qoder.MemoryStoreName(sessionID)); err != nil {
		discarded()
		writeError(w, err)
		return
	}
	finalized, err := memoryspace.Render(sessionID, spaceID, memoryspace.Member{ID: self.ID, Name: self.Username}, memoryspace.Member{ID: partner.ID, Name: partner.Username})
	if err != nil {
		discarded()
		writeError(w, err)
		return
	}
	for i := range records {
		records[i].SessionID = sessionID
		records[i].ID = dbop.MemoryID(sessionID, records[i].Path)
		if records[i].Path == "agreements/shared.json" {
			for _, doc := range finalized {
				if doc.Path == records[i].Path {
					entry, err := s.Qoder.UpsertMemory(r.Context(), store.ID, doc.Path, doc.Content)
					if err != nil {
						discarded()
						writeError(w, err)
						return
					}
					records[i].Content = doc.Content
					records[i].EntryID = entry.ID
				}
			}
		}
	}
	created, err := s.DB.CreateInitializedBinding(r.Context(), self.ID, partner.ID, sessionID, dbop.SpaceMemoryStore{SessionID: sessionID, StoreID: store.ID, NativeMounted: true, SpaceID: spaceID, TemplateVersion: memoryspace.Version}, records)
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
	currentBinding, err := s.DB.GetLatestBindingByUser(r.Context(), self.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	if currentBinding != nil {
		unlock := s.lockConversation(currentBinding.SessionID)
		defer unlock()
		if err := s.DB.CancelSessionReminders(r.Context(), currentBinding.SessionID); err != nil {
			writeError(w, err)
			return
		}
		if err := s.DB.ClearConversationPending(r.Context(), currentBinding.SessionID); err != nil {
			writeError(w, err)
			return
		}
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

// Only unbound resources created by this request are discarded. Both resources
// are independent of all previous spaces and can be removed on a lost race.
func (s *Server) cleanupInitializedSpace(session, store string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if session != "" {
		if err := s.Qoder.DeleteSession(ctx, session); err != nil {
			logf("清理未绑定会话 %s 失败: %v", session, err)
			return // Keep its mounted store if deletion did not succeed.
		}
	}
	if err := s.Qoder.DeleteMemoryStore(ctx, store); err != nil {
		logf("清理未绑定记忆仓库 %s 失败: %v", store, err)
	}
}
