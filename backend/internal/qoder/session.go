package qoder

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// SessionListResult 对应 GET /api/qoder/sessions 的响应体。
type SessionListResult struct {
	Data             []PublicSession `json:"data"`
	DefaultSessionID *string         `json:"defaultSessionId"`
}

// ListSessions 拉取全部云端会话；defaultSessionID 在列表中存在时才回传。
func (c *Client) ListSessions(ctx context.Context, defaultSessionID string) (*SessionListResult, error) {
	raw, err := listAll[rawSession](c, ctx, "/sessions", listOpts{})
	if err != nil {
		return nil, err
	}
	sessions := make([]PublicSession, 0, len(raw))
	for i := range raw {
		ps, err := publicSession(&raw[i])
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, *ps)
	}
	result := &SessionListResult{Data: sessions}
	if defaultSessionID != "" {
		for _, s := range sessions {
			if s.ID == defaultSessionID {
				id := s.ID
				result.DefaultSessionID = &id
				break
			}
		}
	}
	return result, nil
}

// CreateSession 在云端新建一个会话（绑定流程用）。
// 上游需要 agent 与 environment_id，二者由 ResolveAgentAndEnv 确定；title 为空时沿用云端默认命名。
func (c *Client) CreateSession(ctx context.Context, agentID, environmentID, title string, memoryStores ...string) (*PublicSession, error) {
	body := map[string]any{"agent": agentID, "environment_id": environmentID}
	if len(memoryStores) > 0 {
		resources := []map[string]any{}
		for _, id := range memoryStores {
			resources = append(resources, map[string]any{"type": "memory_store", "memory_store_id": id, "access": "read_only", "instructions": "本仓库仅属于当前贴贴会话，仅使用7种 JSON 模板及同模板分页，位于 /data/.qoder/awareness/<path>。消息传输协议在 rules/assistant-behavior.json 的 instructions 字段，遗忘时读取；保留云端原有人设。初始模板的未提供不是事实；相关事实按对应模板检索：profile/users、profile/habits、agreements/shared、tasks/todo-board、context/realtime、rules/assistant-behavior，以及 rules/memory-policy.json。目录中的 YYYY-MM/NNNNNN.json 是对应模板的分页，不是新类型。expiresAt 已到期的内容不能作为当前事实。写入、取消和定时调度由后台系统执行，不代替后台创建定时器。"})
		}
		body["resources"] = resources
	}
	if strings.TrimSpace(title) != "" {
		body["title"] = title
	}
	var raw rawSession
	// 建会话可能涉及环境调度，单独放宽到 60s。
	if err := c.doJSON(ctx, http.MethodPost, "/sessions", body, &raw, 60*time.Second); err != nil {
		return nil, err
	}
	return publicSession(&raw)
}

// FindSessionByTitle 按标题在云端精确找回历史会话（解绑后重新绑定同一人时用）。
// 上游列表接口不支持按标题过滤，这里翻页拉全量后本地匹配；命中多个时取最新创建的，已归档的不参与恢复。
func (c *Client) FindSessionByTitle(ctx context.Context, title string) (*PublicSession, error) {
	if strings.TrimSpace(title) == "" {
		return nil, nil
	}
	raw, err := listAll[rawSession](c, ctx, "/sessions", listOpts{})
	if err != nil {
		return nil, err
	}
	var found *PublicSession
	for i := range raw {
		item := raw[i]
		if strings.TrimSpace(item.Title) != title || item.ArchivedAt != "" {
			continue
		}
		session, err := publicSession(&item)
		if err != nil {
			return nil, err
		}
		if found == nil || session.CreatedAt > found.CreatedAt {
			found = session
		}
	}
	return found, nil
}

// DeleteSession 删除云端会话（并发绑定时清理多余新建的会话用）。
func (c *Client) DeleteSession(ctx context.Context, id string) error {
	_, err := c.send(ctx, http.MethodDelete, "/sessions/"+url.PathEscape(id), "", nil, 0)
	return err
}

// MessagesResult 对应 GET /api/qoder/sessions/:id/messages 的响应体。// Cursor / IdleEventID / TurnError 是三态字段：nil 表示 JSON null。
type MessagesResult struct {
	Session     *PublicSession  `json:"session"`
	Messages    []PublicMessage `json:"messages"`
	Cursor      *string         `json:"cursor"`
	IdleEventID *string         `json:"idleEventId"`
	TurnError   *string         `json:"turnError"`
	Events      []Event         `json:"-"`
}

// GetMessages 增量拉取会话事件并转换为消息列表（对应 qoder.mjs getMessages）。
func (c *Client) GetMessages(ctx context.Context, id, after string) (*MessagesResult, error) {
	path := "/sessions/" + url.PathEscape(id)
	events, err := listAll[Event](c, ctx, path+"/events", listOpts{after: after, events: true})
	if err != nil {
		return nil, err
	}
	for _, ev := range events {
		if !eventIDRe.MatchString(ev.ID) {
			return nil, invalidResponse()
		}
	}
	// 事件读完后再取会话快照，避免把进行中的回合误报为 idle。
	var raw rawSession
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &raw, 0); err != nil {
		return nil, err
	}
	session, err := publicSession(&raw)
	if err != nil {
		return nil, err
	}

	result := &MessagesResult{Session: session, Messages: publicMessages(events), Events: events}
	if n := len(events); n > 0 {
		cursor := events[n-1].ID
		result.Cursor = &cursor
	} else if after != "" {
		cursor := after
		result.Cursor = &cursor
	}
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == "session.status_idle" {
			idleID := events[i].ID
			result.IdleEventID = &idleID
			break
		}
	}
	lastError, lastReply := -1, -1
	for i := len(events) - 1; i >= 0; i-- {
		if lastError == -1 && events[i].Type == "session.error" {
			lastError = i
		}
		if lastReply == -1 && events[i].Type == "agent.message" {
			lastReply = i
		}
	}
	if lastError != -1 || lastReply != -1 {
		msg := ""
		if lastError > lastReply {
			msg = publicTurnError(&events[lastError])
		}
		result.TurnError = &msg
	}
	return result, nil
}
