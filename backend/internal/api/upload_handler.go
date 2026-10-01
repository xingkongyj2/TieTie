package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"tietie/backend/internal/document"
	"tietie/backend/internal/dto"
	"tietie/backend/internal/qoder"
)

// parseMessage 解析并校验 POST /messages 的请求体（对应 qoder.mjs readMessage）。
// 校验通过后返回规范化的 MessageInput，交给 qoder.Client.SendMessage。
func parseMessage(r *http.Request) (*qoder.MessageInput, *qoder.ApiError) {
	media := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]))
	if media != "application/json" {
		return nil, qoder.NewApiError(415, "unsupported_media_type", "消息请求必须使用 JSON 格式。")
	}
	if r.ContentLength > dto.MaxBodyBytes {
		return nil, qoder.NewApiError(413, "body_too_large", "附件总大小超过限制。")
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, dto.MaxBodyBytes+1))
	if err != nil {
		return nil, qoder.NewApiError(400, "request_aborted", "消息请求已中断。")
	}
	if int64(len(data)) > dto.MaxBodyBytes {
		return nil, qoder.NewApiError(413, "body_too_large", "附件总大小超过限制。")
	}

	var raw any
	if json.Unmarshal(data, &raw) != nil {
		return nil, qoder.NewApiError(400, "invalid_json", "消息格式不正确，请重新发送。")
	}
	invalidMessage := qoder.NewApiError(400, "invalid_message",
		"请输入消息或添加最多 4 个附件，文字不超过 2000 字。")
	body, ok := raw.(map[string]any)
	if !ok {
		return nil, invalidMessage
	}
	for key := range body {
		if key != "text" && key != "attachments" && key != "visibility" {
			return nil, invalidMessage
		}
	}
	visibility := "shared"
	if raw, present := body["visibility"]; present {
		value, valid := raw.(string)
		if !valid || (value != "shared" && value != "private") {
			return nil, invalidMessage
		}
		visibility = value
	}
	text, ok := body["text"].(string)
	if !ok || utf8.RuneCountInString(text) > dto.MaxTextRunes {
		return nil, invalidMessage
	}
	var rawAttachments []any
	if v, present := body["attachments"]; present && v != nil {
		if rawAttachments, ok = v.([]any); !ok {
			return nil, invalidMessage
		}
	}
	if len(rawAttachments) > dto.MaxAttachments {
		return nil, invalidMessage
	}
	if strings.TrimSpace(text) == "" && len(rawAttachments) == 0 {
		return nil, invalidMessage
	}

	attachments := make([]qoder.Attachment, 0, len(rawAttachments))
	for _, item := range rawAttachments {
		attachment, apiErr := parseAttachment(item)
		if apiErr != nil {
			return nil, apiErr
		}
		attachments = append(attachments, attachment)
	}
	return &qoder.MessageInput{Text: strings.TrimSpace(text), Attachments: attachments, Visibility: visibility}, nil
}

// parseAttachment 校验单个附件并按类型规范化。
func parseAttachment(item any) (qoder.Attachment, *qoder.ApiError) {
	invalidName := qoder.NewApiError(400, "invalid_attachment", "附件名称或格式无效。")
	m, ok := item.(map[string]any)
	if !ok {
		return qoder.Attachment{}, invalidName
	}
	for key := range m {
		switch key {
		case "kind", "name", "mimeType", "data", "content":
		default:
			return qoder.Attachment{}, invalidName
		}
	}
	name, _ := m["name"].(string)
	mimeType, mimeOK := m["mimeType"].(string)
	if name == "" || utf8.RuneCountInString(name) > dto.MaxNameRunes ||
		dto.InvalidFileName(name) || !mimeOK {
		return qoder.Attachment{}, invalidName
	}
	_, hasData := m["data"]
	_, hasContent := m["content"]
	kind, _ := m["kind"].(string)

	switch kind {
	case "image":
		bad := qoder.NewApiError(400, "invalid_attachment", "图片格式或大小不支持。")
		encoded, _ := m["data"].(string)
		if !dto.ImageTypes[mimeType] || encoded == "" || len(encoded) > dto.MaxImageBase64 ||
			!qoder.ValidBase64(encoded) || hasContent {
			return qoder.Attachment{}, bad
		}
		decoded, err := qoder.DecodeBase64(encoded)
		if err != nil {
			return qoder.Attachment{}, bad
		}
		return qoder.Attachment{Kind: "image", Name: name, MimeType: mimeType, Data: decoded}, nil

	case "file":
		bad := qoder.NewApiError(400, "invalid_attachment", "仅支持 4 MB 以内的文本类文件。")
		content, contentOK := m["content"].(string)
		normalizedMime, mimeSupported := dto.SupportedTextMime(mimeType)
		if !(dto.SupportedTextName(name) || mimeSupported) || !contentOK || content == "" ||
			len(content) > dto.MaxFileBytes || hasData || strings.ContainsRune(content, 0) {
			return qoder.Attachment{}, bad
		}
		if !mimeSupported {
			normalizedMime = "text/plain"
		}
		return qoder.Attachment{Kind: "file", Name: name, MimeType: normalizedMime, Content: content}, nil

	case "document":
		bad := qoder.NewApiError(400, "invalid_attachment", "Office 文件格式或大小不支持。")
		encoded, _ := m["data"].(string)
		if !document.IsOfficeName(name) || encoded == "" || len(encoded) > dto.MaxDocumentBase64 ||
			!qoder.ValidBase64(encoded) || hasContent {
			return qoder.Attachment{}, bad
		}
		decoded, err := qoder.DecodeBase64(encoded)
		if err != nil {
			return qoder.Attachment{}, bad
		}
		if len(decoded) == 0 || len(decoded) > dto.MaxFileBytes {
			return qoder.Attachment{}, qoder.NewApiError(400, "invalid_attachment", "Office 文件超过 4 MB。")
		}
		return qoder.Attachment{Kind: "document", Name: name, MimeType: mimeType, Data: decoded}, nil
	}
	return qoder.Attachment{}, qoder.NewApiError(400, "invalid_attachment", "附件类型不支持。")
}
