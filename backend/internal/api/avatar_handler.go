package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png"
	"net/http"
	"regexp"
	"strings"

	"tietie/backend/internal/auth"
	"tietie/backend/internal/dbop"
	"tietie/backend/internal/qoder"
)

const maxAvatarBytes = 2 << 20
const maxAvatarJSONBytes = (maxAvatarBytes+2)/3*4 + 4096
const avatarAssetPrefix = "/api/assets/avatars/"

var avatarIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

type avatarInput struct {
	data   []byte
	config image.Config
}

func invalidAvatarError() *qoder.ApiError {
	return qoder.NewApiError(400, "invalid_avatar", "请选择 2 MB 以内的 PNG 或 JPEG 头像。")
}

// This bounded header check is safe before authentication. Pixel allocation and
// decoding happen separately only after the login code has been verified.
func validateAvatarInput(encoded string) (*avatarInput, *qoder.ApiError) {
	if encoded == "" || len(encoded) > (maxAvatarBytes+2)/3*4 {
		return nil, invalidAvatarError()
	}
	data, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(data) == 0 || len(data) > maxAvatarBytes {
		return nil, invalidAvatarError()
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "png" && format != "jpeg") || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > 4096 || cfg.Height > 4096 || int64(cfg.Width)*int64(cfg.Height) > 16<<20 {
		return nil, invalidAvatarError()
	}
	return &avatarInput{data: data, config: cfg}, nil
}

func normalizeAvatar(encoded string) ([]byte, *qoder.ApiError) {
	input, err := validateAvatarInput(encoded)
	if err != nil {
		return nil, err
	}
	return normalizeAvatarInput(input)
}

// Re-encoding removes metadata and limits the image stored by the service.
func normalizeAvatarInput(input *avatarInput) ([]byte, *qoder.ApiError) {
	cfg := input.config
	source, _, err := image.Decode(bytes.NewReader(input.data))
	if err != nil {
		return nil, invalidAvatarError()
	}
	width, height := cfg.Width, cfg.Height
	if width > 512 || height > 512 {
		if width >= height {
			height = max(1, height*512/width)
			width = 512
		} else {
			width = max(1, width*512/height)
			height = 512
		}
	}
	normalized := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			r, g, b, a := source.At(source.Bounds().Min.X+x*cfg.Width/width, source.Bounds().Min.Y+y*cfg.Height/height).RGBA()
			// Flatten transparent input onto white when writing JPEG.
			normalized.SetRGBA(x, y, color.RGBA{uint8((r + 65535 - a) >> 8), uint8((g + 65535 - a) >> 8), uint8((b + 65535 - a) >> 8), 255})
		}
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, normalized, &jpeg.Options{Quality: 88}); err != nil {
		return nil, invalidAvatarError()
	}
	return out.Bytes(), nil
}

func (s *Server) handleAvatarUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}
	var body struct {
		AvatarBase64 string `json:"avatarBase64"`
	}
	if err := decodeJSONBody(r, &body, maxAvatarJSONBytes); err != nil {
		writeError(w, err)
		return
	}
	image, apiErr := normalizeAvatar(body.AvatarBase64)
	if apiErr != nil {
		writeError(w, apiErr)
		return
	}
	avatar, err := s.DB.SaveUserAvatar(r.Context(), auth.UserIDFrom(r.Context()), "image/jpeg", image)
	if err != nil {
		writeError(w, qoder.NewApiError(503, "avatar_upload_failed", "头像保存失败，请稍后重试。"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"avatar": avatarAssetPrefix + avatar.ID})
}

func (s *Server) handleAvatarAsset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeMethodNotAllowed(w)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, avatarAssetPrefix)
	if !avatarIDPattern.MatchString(id) {
		http.NotFound(w, r)
		return
	}
	avatar, err := s.DB.GetUserAvatar(r.Context(), id)
	if err != nil {
		writeError(w, qoder.NewApiError(503, "avatar_unavailable", "暂时无法读取头像。"))
		return
	}
	if avatar == nil {
		http.NotFound(w, r)
		return
	}
	servePublicAsset(w, r, id+".jpg", avatar.MimeType, avatar.Content)
}

func (s *Server) validateOwnedAvatar(r *http.Request, value string) *qoder.ApiError {
	return validateAvatarOwnership(r.Context(), auth.UserIDFrom(r.Context()), value, s.DB)
}

type avatarReader interface {
	GetUserAvatar(context.Context, string) (*dbop.UserAvatar, error)
}

func validateAvatarOwnership(ctx context.Context, userID int64, value string, store avatarReader) *qoder.ApiError {
	if !strings.HasPrefix(value, "/api/assets/avatars") {
		return nil
	}
	id := strings.TrimPrefix(value, avatarAssetPrefix)
	if !strings.HasPrefix(value, avatarAssetPrefix) || !avatarIDPattern.MatchString(id) {
		return qoder.NewApiError(400, "invalid_avatar", "头像地址无效，请重新选择。")
	}
	avatar, err := store.GetUserAvatar(ctx, id)
	if err != nil {
		return qoder.NewApiError(503, "avatar_unavailable", "暂时无法保存头像，请稍后重试。")
	}
	if avatar == nil || avatar.UserID != userID {
		return qoder.NewApiError(400, "invalid_avatar", "请使用自己选择的头像。")
	}
	return nil
}
