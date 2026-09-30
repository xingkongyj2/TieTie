package qoder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
)

// UploadFile 以 multipart 形式上传文件，返回云端 file_id。
// 对应 qoder.mjs sendMessage 里的 FormData 上传（超时 60s）。
func (c *Client) UploadFile(ctx context.Context, name, mimeType string, content []byte) (string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, name))
	h.Set("Content-Type", mimeType)
	part, err := w.CreatePart(h)
	if err != nil {
		return "", NewApiError(500, "internal_error", "会话服务发生错误，请稍后重试。")
	}
	if _, err := part.Write(content); err != nil {
		return "", NewApiError(500, "internal_error", "会话服务发生错误，请稍后重试。")
	}
	if err := w.WriteField("name", name); err != nil {
		return "", NewApiError(500, "internal_error", "会话服务发生错误，请稍后重试。")
	}
	if err := w.Close(); err != nil {
		return "", NewApiError(500, "internal_error", "会话服务发生错误，请稍后重试。")
	}

	data, err := c.send(ctx, http.MethodPost, "/files", w.FormDataContentType(), &buf, c.UploadTimeout)
	if err != nil {
		return "", err
	}
	var file struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(data, &file) != nil || !fileIDRe.MatchString(file.ID) {
		return "", invalidResponse()
	}
	return file.ID, nil
}

// MountResource 把已上传的文件挂载到会话，返回挂载路径。
func (c *Client) MountResource(ctx context.Context, sessionID, fileID string) (string, error) {
	body := map[string]any{"type": "file", "file_id": fileID}
	var res struct {
		MountPath string `json:"mount_path"`
	}
	path := "/sessions/" + url.PathEscape(sessionID) + "/resources"
	if err := c.doJSON(ctx, http.MethodPost, path, body, &res, 0); err != nil {
		return "", err
	}
	if !strings.HasPrefix(res.MountPath, "/") {
		return "", invalidResponse()
	}
	return res.MountPath, nil
}
