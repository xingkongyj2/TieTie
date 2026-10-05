package api

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"fmt"
	"net/http"
	"time"
)

// Font and license travel with the API binary, independently of the H5 build.
//
//go:embed assets/reminder-titles.woff
var reminderTitlesFont []byte

//go:embed assets/OFL.txt
var reminderTitlesLicense []byte

func servePublicAsset(w http.ResponseWriter, r *http.Request, name, contentType string, content []byte) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("ETag", fmt.Sprintf(`"%x"`, sha256.Sum256(content)))
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(content))
}

func (s *Server) handleTitleFont(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Link", `</api/assets/OFL.txt>; rel="license"`)
	servePublicAsset(w, r, "reminder-titles.woff", "font/woff", reminderTitlesFont)
}

func (s *Server) handleTitleFontLicense(w http.ResponseWriter, r *http.Request) {
	servePublicAsset(w, r, "OFL.txt", "text/plain; charset=utf-8", reminderTitlesLicense)
}
