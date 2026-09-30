package api

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"unicode"

	"tietie/backend/internal/qoder"
)

// handleStream 处理 GET /api/qoder/sessions/{id}/stream：
// 打开上游 SSE，把帧转换成公开事件后实时转发给前端（对应 qoder.mjs stream 分支 + relayStream）。
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}
	id := r.PathValue("id")
	if !qoder.ValidSessionID(id) {
		writeRouteNotFound(w)
		return
	}
	if apiErr := s.ensureBoundSession(r.Context(), id); apiErr != nil {
		writeError(w, apiErr)
		return
	}

	// 游标校验：查询串只允许一个合法的 after；Last-Event-ID 优先。
	invalidCursor := qoder.NewApiError(400, "invalid_query", "实时会话游标无效。")
	query := r.URL.Query()
	for key, vals := range query {
		if key != "after" || len(vals) > 1 {
			writeError(w, invalidCursor)
			return
		}
	}
	after := query.Get("after")
	if after != "" && !qoder.ValidEventID(after) {
		writeError(w, invalidCursor)
		return
	}
	lastEventID := r.Header.Get("Last-Event-ID")
	if lastEventID != "" && !qoder.ValidEventID(lastEventID) {
		writeError(w, invalidCursor)
		return
	}
	resume := lastEventID
	if resume == "" {
		resume = after
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, qoder.NewApiError(500, "internal_error", "会话服务发生错误，请稍后重试。"))
		return
	}

	body, err := s.Qoder.OpenEventStream(r.Context(), id, resume)
	if err != nil {
		if isClientGone(err) {
			return
		}
		writeError(w, err)
		return
	}
	defer body.Close()

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	_, _ = io.WriteString(w, ": connected\n\n")
	flusher.Flush()

	relayStream(r, w, flusher, body)
}

// relayStream 逐行解析上游 SSE，把完整帧交给 qoder.ParseStreamEvent 过滤转换后写给前端。
func relayStream(r *http.Request, w io.Writer, flusher http.Flusher, body io.Reader) {
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64*1024), 300*1024)

	frameID := ""
	var dataLines []string
	flushFrame := func() {
		defer func() { frameID = ""; dataLines = nil }()
		if len(dataLines) == 0 {
			return
		}
		item := qoder.ParseStreamEvent([]byte(strings.Join(dataLines, "\n")))
		if item == nil {
			return
		}
		if qoder.ValidEventID(frameID) {
			fmt.Fprintf(w, "id: %s\n", frameID)
		}
		encoded, err := json.Marshal(item)
		if err != nil {
			return
		}
		fmt.Fprintf(w, "data: %s\n\n", encoded)
		flusher.Flush()
	}

	for sc.Scan() {
		if r.Context().Err() != nil {
			return // 前端已断开
		}
		line := strings.TrimSuffix(sc.Text(), "\r")
		switch {
		case line == "":
			flushFrame()
		case strings.HasPrefix(line, ":"):
			_, _ = io.WriteString(w, ": heartbeat\n\n")
			flusher.Flush()
		case strings.HasPrefix(line, "id:"):
			frameID = strings.TrimSpace(line[len("id:"):])
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimLeftFunc(line[len("data:"):], unicode.IsSpace))
		}
	}
	if err := sc.Err(); err != nil && !isClientGone(err) {
		log.Printf("SSE 上游流读取中断: %v", err)
	}
}
