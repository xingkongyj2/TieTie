package qoder

import (
	"context"
	"net/http"
	"net/url"
)

// AgentInfo 是云端可用的 Agent 概要。
type AgentInfo struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	ArchivedAt *string `json:"archived_at"`
}

// EnvironmentInfo 是云端可用的运行环境概要。
type EnvironmentInfo struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	ArchivedAt *string `json:"archived_at"`
}

// ListAgents 拉取账号下的 Agent 列表。
func (c *Client) ListAgents(ctx context.Context) ([]AgentInfo, error) {
	var env listEnvelope[AgentInfo]
	if err := c.doJSON(ctx, http.MethodGet, "/agents?"+limitQuery(), nil, &env, 0); err != nil {
		return nil, err
	}
	if env.Data == nil {
		return nil, invalidResponse()
	}
	return *env.Data, nil
}

// ListEnvironments 拉取账号下的运行环境列表。
func (c *Client) ListEnvironments(ctx context.Context) ([]EnvironmentInfo, error) {
	var env listEnvelope[EnvironmentInfo]
	if err := c.doJSON(ctx, http.MethodGet, "/environments?"+limitQuery(), nil, &env, 0); err != nil {
		return nil, err
	}
	if env.Data == nil {
		return nil, invalidResponse()
	}
	return *env.Data, nil
}

// ResolveAgentAndEnv 确定新建会话使用的 agent 与 environment：
// 配置显式指定则直接用；否则自动探测（第一个未归档 agent；名为 Default 的环境，否则第一个）。
func (c *Client) ResolveAgentAndEnv(ctx context.Context, cfgAgentID, cfgEnvID string) (string, string, error) {
	agentID, envID := cfgAgentID, cfgEnvID
	if agentID == "" {
		agents, err := c.ListAgents(ctx)
		if err != nil {
			return "", "", err
		}
		for _, a := range agents {
			if a.ArchivedAt == nil || *a.ArchivedAt == "" {
				agentID = a.ID
				break
			}
		}
		if agentID == "" {
			return "", "", NewApiError(503, "no_agent", "云端账号下没有可用的 Agent，请先在 Qoder 中创建。")
		}
	}
	if envID == "" {
		envs, err := c.ListEnvironments(ctx)
		if err != nil {
			return "", "", err
		}
		fallback := ""
		for _, e := range envs {
			if e.ArchivedAt != nil && *e.ArchivedAt != "" {
				continue
			}
			if e.Name == "Default" {
				envID = e.ID
				break
			}
			if fallback == "" {
				fallback = e.ID
			}
		}
		if envID == "" {
			envID = fallback
		}
		if envID == "" {
			return "", "", NewApiError(503, "no_environment", "云端账号下没有可用的运行环境，请先在 Qoder 中创建。")
		}
	}
	return agentID, envID, nil
}

func limitQuery() string {
	q := url.Values{}
	q.Set("limit", "100")
	return q.Encode()
}
