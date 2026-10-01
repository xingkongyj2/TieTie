package qoder

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

type MemoryStore struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Status   string            `json:"status"`
	Metadata map[string]string `json:"metadata"`
}
type MemoryEntry struct {
	ID      string `json:"id"`
	Path    string `json:"path"`
	Content string `json:"content"`
	SHA256  string `json:"content_sha256"`
	Version int    `json:"version"`
}

func (c *Client) FindMemoryStore(ctx context.Context, name, owner string) (*MemoryStore, error) {
	page := ""
	for i := 0; i < 100; i++ {
		q := url.Values{"name": {name}, "limit": {"100"}}
		if page != "" {
			q.Set("page", page)
		}
		var env struct {
			Data     []MemoryStore `json:"data"`
			HasMore  bool          `json:"has_more"`
			NextPage string        `json:"next_page"`
		}
		if err := c.doJSON(ctx, http.MethodGet, "/memory_stores?"+q.Encode(), nil, &env, 0); err != nil {
			return nil, err
		}
		for _, s := range env.Data {
			if s.Name == name && s.Status == "active" && s.Metadata["tietie_owner"] == owner {
				return &s, nil
			}
		}
		if !env.HasMore {
			return nil, nil
		}
		if env.NextPage == "" || env.NextPage == page {
			return nil, invalidResponse()
		}
		page = env.NextPage
	}
	return nil, invalidResponse()
}
func (c *Client) CreateMemoryStore(ctx context.Context, name, owner string) (*MemoryStore, error) {
	var s MemoryStore
	err := c.doJSON(ctx, http.MethodPost, "/memory_stores", map[string]any{"name": name, "description": "贴贴双人空间的长期事实记忆；成员按真实 userId 区分。提醒由后台调度，已提醒不表示事项已完成。", "metadata": map[string]string{"tietie_owner": owner}}, &s, 0)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(s.ID, "memstore_") {
		return nil, invalidResponse()
	}
	return &s, nil
}
func memoryPath(store string) string { return "/memory_stores/" + url.PathEscape(store) + "/memories" }
func (c *Client) FindMemoryEntry(ctx context.Context, store, path string) (*MemoryEntry, error) {
	var env struct {
		Data     []MemoryEntry `json:"data"`
		HasMore  bool          `json:"has_more"`
		NextPage string        `json:"next_page"`
	}
	q := url.Values{"path_prefix": {path}, "limit": {"100"}}
	for i := 0; i < 100; i++ {
		env.Data, env.HasMore, env.NextPage = nil, false, ""
		if err := c.doJSON(ctx, http.MethodGet, memoryPath(store)+"?"+q.Encode(), nil, &env, 0); err != nil {
			return nil, err
		}
		for _, e := range env.Data {
			if e.Path == path {
				return c.GetMemory(ctx, store, e.ID)
			}
		}
		if !env.HasMore {
			return nil, nil
		}
		if env.NextPage == "" || env.NextPage == q.Get("page") {
			return nil, invalidResponse()
		}
		q.Set("page", env.NextPage)
	}
	return nil, invalidResponse()
}
func (c *Client) GetMemory(ctx context.Context, store, entry string) (*MemoryEntry, error) {
	var e MemoryEntry
	err := c.doJSON(ctx, http.MethodGet, memoryPath(store)+"/"+url.PathEscape(entry), nil, &e, 0)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(e.ID, "mem_") || e.Path == "" || e.Content == "" {
		return nil, invalidResponse()
	}
	return &e, nil
}

// UpsertMemory reconciles by the deterministic path after a lost response. SHA
// preconditions prevent an unseen cloud edit from being silently overwritten.
func (c *Client) UpsertMemory(ctx context.Context, store, path, content string) (*MemoryEntry, error) {
	if path == "" || strings.HasPrefix(path, "/") || strings.Contains(path, "..") || len(content) > 100*1024 {
		return nil, NewApiError(400, "invalid_memory", "记忆内容或路径无效。")
	}
	current, err := c.FindMemoryEntry(ctx, store, path)
	if err != nil {
		return nil, err
	}
	if current == nil {
		var e MemoryEntry
		err = c.doJSON(ctx, http.MethodPost, memoryPath(store), map[string]string{"path": path, "content": content}, &e, 0)
		if err == nil {
			if !strings.HasPrefix(e.ID, "mem_") || e.Path != path {
				return nil, invalidResponse()
			}
			return &e, nil
		}
		var apiErr *ApiError
		if !errors.As(err, &apiErr) || apiErr.Status != 409 {
			return nil, err
		}
		current, err = c.FindMemoryEntry(ctx, store, path)
		if err != nil {
			return nil, err
		}
		if current == nil {
			return nil, invalidResponse()
		}
	}
	if current.Content == content {
		return current, nil
	}
	var updated MemoryEntry
	err = c.doJSON(ctx, http.MethodPost, memoryPath(store)+"/"+url.PathEscape(current.ID), map[string]any{"content": content, "content_sha256": current.SHA256}, &updated, 0)
	if err != nil {
		return nil, err
	}
	if updated.ID != current.ID || updated.Path != path {
		return nil, invalidResponse()
	}
	return &updated, nil
}
func (c *Client) DeleteMemory(ctx context.Context, store, path string) error {
	e, err := c.FindMemoryEntry(ctx, store, path)
	if err != nil {
		return err
	}
	if e == nil {
		return nil
	}
	err = c.doJSON(ctx, http.MethodDelete, memoryPath(store)+"/"+url.PathEscape(e.ID), nil, nil, 0)
	var apiErr *ApiError
	if errors.As(err, &apiErr) && apiErr.Status == 404 {
		return nil
	}
	return err
}
func MemoryStoreName(session string) string { return fmt.Sprintf("TieTie-%s-memory", session) }

// Rename only changes the display name, preserving ownership metadata and entries.
func (c *Client) RenameMemoryStore(ctx context.Context, id, name string) error {
	var store MemoryStore
	if err := c.doJSON(ctx, http.MethodPost, "/memory_stores/"+url.PathEscape(id), map[string]string{"name": name}, &store, 0); err != nil {
		return err
	}
	if store.ID != id || store.Name != name {
		return invalidResponse()
	}
	return nil
}

// DeleteMemoryStore removes a newly created, unbound initialization resource.
func (c *Client) DeleteMemoryStore(ctx context.Context, id string) error {
	err := c.doJSON(ctx, http.MethodDelete, "/memory_stores/"+url.PathEscape(id), nil, nil, 0)
	var apiErr *ApiError
	if errors.As(err, &apiErr) && apiErr.Status == 404 {
		return nil
	}
	return err
}
