package qoder

import "fmt"

// ApiError 是带 HTTP 状态码的业务错误。
// api 层会把它原样转成 {"error":{"code":...,"message":...}} 返回给前端。
type ApiError struct {
	Status  int
	Code    string
	Message string
}

func (e *ApiError) Error() string {
	return fmt.Sprintf("%d %s: %s", e.Status, e.Code, e.Message)
}

// NewApiError 构造业务错误，供本包与 api 层使用。
func NewApiError(status int, code, message string) *ApiError {
	return &ApiError{Status: status, Code: code, Message: message}
}

// upstreamError 把上游 HTTP 状态码映射为对外错误（对应 qoder.mjs upstreamError）。
func upstreamError(status int) *ApiError {
	switch status {
	case 400:
		return NewApiError(400, "invalid_request", "云端无法处理这次请求，请刷新会话后重试。")
	case 401:
		return NewApiError(401, "authentication_failed", "云端令牌无效或已过期，请更新服务端 QODER_ACCESS_TOKEN。")
	case 403:
		return NewApiError(403, "permission_denied", "当前云端令牌没有访问此会话的权限。")
	case 404:
		return NewApiError(404, "session_not_found", "此云端会话不存在或已被删除。")
	case 409:
		return NewApiError(409, "session_busy", "云端会话正在处理消息或暂不可发送，请等待本轮回复完成后重试。")
	case 429:
		return NewApiError(429, "rate_limited", "云端请求过于频繁，请稍后重试。")
	default:
		return NewApiError(502, "upstream_unavailable", "云端服务暂时不可用，请稍后重试。")
	}
}

// invalidResponse 表示上游返回的数据不完整或结构不符合预期。
func invalidResponse() *ApiError {
	return NewApiError(502, "invalid_upstream_response", "云端返回的数据不完整，请稍后重试。")
}

// notConfigured 表示服务端尚未配置云端令牌。
func notConfigured() *ApiError {
	return NewApiError(503, "not_configured", "尚未配置云端令牌，请在服务端设置 QODER_ACCESS_TOKEN。")
}
