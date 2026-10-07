package api

import (
	"net/http"
	"strconv"
	"time"

	"tietie/backend/internal/asr"
	"tietie/backend/internal/auth"
	"tietie/backend/internal/qoder"
)

const (
	voiceSessionLimit  = 20
	voiceSessionWindow = time.Minute
)

// handleVoiceSession accepts no recognition options from the request. Each
// successful response authorizes only a short-lived connection to Tencent ASR.
func (s *Server) handleVoiceSession(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}
	userID := auth.UserIDFrom(r.Context())
	if userID <= 0 {
		writeError(w, qoder.NewApiError(401, "unauthorized", "请先登录。"))
		return
	}
	if s.Cfg == nil {
		writeVoiceUnavailable(w)
		return
	}
	cfg := asr.Config{
		AppID:       s.Cfg.TencentASRAppID,
		SecretID:    s.Cfg.TencentASRSecretID,
		SecretKey:   s.Cfg.TencentASRSecretKey,
		EngineModel: s.Cfg.TencentASREngineModel,
	}
	if cfg.Validate() != nil {
		writeVoiceUnavailable(w)
		return
	}
	now := time.Now().UTC()
	if allowed, retryAfter := s.allowVoiceSession(userID, now); !allowed {
		seconds := int64((retryAfter + time.Second - 1) / time.Second)
		if seconds < 1 {
			seconds = 1
		}
		w.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
		writeError(w, qoder.NewApiError(429, "voice_session_rate_limited", "语音使用较频繁，请稍后再试。"))
		return
	}
	session, err := asr.NewSession(cfg, now)
	if err != nil {
		// Never pass signer errors, credential values, or signed URLs to logging.
		writeError(w, qoder.NewApiError(500, "voice_session_failed", "暂时无法开始语音识别，请稍后重试。"))
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func writeVoiceUnavailable(w http.ResponseWriter) {
	writeError(w, qoder.NewApiError(503, "voice_unavailable", "语音识别暂未配置完成，请稍后再试。"))
}

// A rolling minute prevents boundary bursts. There are at most twenty retained
// timestamps per active user; every request removes inactive users completely.
func (s *Server) allowVoiceSession(userID int64, now time.Time) (bool, time.Duration) {
	s.voiceSessionMu.Lock()
	defer s.voiceSessionMu.Unlock()
	if s.voiceSessionRequests == nil {
		s.voiceSessionRequests = make(map[int64][]time.Time)
	}
	cutoff := now.Add(-voiceSessionWindow)
	for id, requests := range s.voiceSessionRequests {
		first := 0
		for first < len(requests) && !requests[first].After(cutoff) {
			first++
		}
		if first == len(requests) {
			delete(s.voiceSessionRequests, id)
		} else if first > 0 {
			copy(requests, requests[first:])
			s.voiceSessionRequests[id] = requests[:len(requests)-first]
		}
	}
	requests := s.voiceSessionRequests[userID]
	if len(requests) >= voiceSessionLimit {
		return false, requests[0].Add(voiceSessionWindow).Sub(now)
	}
	s.voiceSessionRequests[userID] = append(requests, now)
	return true, 0
}
