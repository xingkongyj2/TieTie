package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tietie/backend/internal/config"
)

func TestTitleFontIsPublicAndIndependentOfStaticBuild(t *testing.T) {
	router := NewRouter(&Server{Cfg: &config.Config{StaticDir: t.TempDir()}})
	request := httptest.NewRequest(http.MethodGet, "/api/assets/reminder-titles.woff", nil)
	request.Header.Set("Origin", "https://servicewechat.com")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), reminderTitlesFont) {
		t.Fatalf("font response: %d, %d bytes", response.Code, response.Body.Len())
	}
	if !bytes.HasPrefix(response.Body.Bytes(), []byte("wOFF")) {
		t.Fatal("embedded font must be WOFF, retaining old iOS support")
	}
	for key, expected := range map[string]string{"Content-Type": "font/woff", "Cache-Control": "public, max-age=3600", "Access-Control-Allow-Origin": "*", "X-Content-Type-Options": "nosniff"} {
		if response.Header().Get(key) != expected {
			t.Errorf("%s = %q, want %q", key, response.Header().Get(key), expected)
		}
	}
	if response.Header().Get("ETag") == "" || !strings.Contains(response.Header().Get("Link"), "/api/assets/OFL.txt") {
		t.Fatal("font requires a cache validator and its license link")
	}
	request = httptest.NewRequest(http.MethodGet, "/api/assets/reminder-titles.woff", nil)
	request.Header.Set("If-None-Match", response.Header().Get("ETag"))
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNotModified || response.Body.Len() != 0 {
		t.Fatalf("cached font: %d, %d bytes", response.Code, response.Body.Len())
	}
}

func TestTitleFontMethodsAndLicense(t *testing.T) {
	router := NewRouter(&Server{Cfg: &config.Config{StaticDir: t.TempDir()}})
	for method, status := range map[string]int{http.MethodHead: http.StatusOK, http.MethodPost: http.StatusMethodNotAllowed, http.MethodOptions: http.StatusNoContent} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(method, "/api/assets/reminder-titles.woff", nil))
		if response.Code != status || response.Body.Len() != 0 {
			t.Errorf("%s: %d, %d bytes", method, response.Code, response.Body.Len())
		}
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/assets/OFL.txt", nil))
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte("SIL OPEN FONT LICENSE")) {
		t.Fatal("font license must be publicly accessible")
	}
}

func TestAPICORSAllowsCurrentClientMethodsAndCareCursor(t *testing.T) {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodOptions, "/api/qoder/sessions/demo/care-settings", nil)
	request.Header.Set("Origin", "http://127.0.0.1:5174")
	request.Header.Set("Access-Control-Request-Method", "PUT")
	request.Header.Set("Access-Control-Request-Headers", "X-Tietie-Care-After")
	openCORS(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("preflight reached business handler") })).ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || !strings.Contains(response.Header().Get("Access-Control-Allow-Methods"), "PUT") || !strings.Contains(response.Header().Get("Access-Control-Allow-Methods"), "DELETE") || !strings.Contains(response.Header().Get("Access-Control-Allow-Headers"), "X-Tietie-Care-After") {
		t.Fatalf("incomplete client CORS contract: %v", response.Header())
	}
}
