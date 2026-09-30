package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"tietie/backend/internal/qoder"
)

// writeJSON 输出统一 JSON 响应（对应 qoder.mjs json()）。
func writeJSON(w http.ResponseWriter, status int, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("响应序列化失败: %v", err)
		http.Error(w, `{"error":{"code":"internal_error","message":"会话服务发生错误，请稍后重试。"}}`,
			http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// writeError 把错误转换为统一的 {"error":{code,message}} 响应。
// *qoder.ApiError 原样透出，其余错误折叠为 500 internal_error。
func writeError(w http.ResponseWriter, err error) {
	var apiErr *qoder.ApiError
	if !errors.As(err, &apiErr) {
		log.Printf("未分类错误: %v", err)
		apiErr = qoder.NewApiError(500, "internal_error", "会话服务发生错误，请稍后重试。")
	}
	writeJSON(w, apiErr.Status, map[string]any{
		"error": map[string]string{"code": apiErr.Code, "message": apiErr.Message},
	})
}

// writeMethodNotAllowed 对应 mjs 里已知路径 + 错误方法的 405。
func writeMethodNotAllowed(w http.ResponseWriter) {
	writeError(w, qoder.NewApiError(405, "unsupported_route", "不支持此会话操作。"))
}

// writeRouteNotFound 对应 mjs 里未知 /api/qoder 路径的 404。
func writeRouteNotFound(w http.ResponseWriter) {
	writeError(w, qoder.NewApiError(404, "unsupported_route", "不支持此会话操作。"))
}
