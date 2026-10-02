package api

import (
	"context"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"tietie/backend/internal/auth"
	"tietie/backend/internal/dbop"
	"tietie/backend/internal/memoryspace"
	"tietie/backend/internal/qoder"
)

func (s *Server) handleProfiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}
	id := auth.UserIDFrom(r.Context())
	self, err := s.DB.GetUserProfile(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	profiles := []*dbop.UserProfile{self}
	binding, err := s.DB.GetLatestBindingByUser(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	if binding != nil {
		partner, err := s.DB.GetUserProfile(r.Context(), binding.OtherUser(id))
		if err != nil {
			writeError(w, err)
			return
		}
		profiles = append(profiles, partner)
	}
	writeJSON(w, http.StatusOK, map[string]any{"profiles": profiles})
}

func (s *Server) handleProfile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeMethodNotAllowed(w)
		return
	}
	var body struct {
		Gender   string   `json:"gender"`
		Birthday string   `json:"birthday"`
		Hobbies  []string `json:"hobbies"`
		Bio      string   `json:"bio"`
		Avatar   string   `json:"avatar"`
	}
	if err := decodeJSONBody(r, &body, 8192); err != nil {
		writeError(w, err)
		return
	}
	if body.Gender == "" {
		body.Gender = "unspecified"
	}
	valid := body.Gender == "male" || body.Gender == "female" || body.Gender == "unspecified"
	if body.Birthday != "" {
		date, err := time.Parse("2006-01-02", body.Birthday)
		today := time.Now().In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("2006-01-02")
		valid = valid && err == nil && date.Format("2006-01-02") == body.Birthday && body.Birthday <= today
	}
	valid = valid && len(body.Hobbies) <= 8 && utf8.RuneCountInString(body.Bio) <= 200 && len(body.Avatar) <= 200 && (body.Avatar == "" || strings.HasPrefix(body.Avatar, "/") && !strings.HasPrefix(body.Avatar, "//"))
	hobbies := []string{}
	seen := map[string]bool{}
	for _, item := range body.Hobbies {
		item = strings.TrimSpace(item)
		if utf8.RuneCountInString(item) > 20 {
			valid = false
		}
		if item != "" && !seen[item] {
			hobbies = append(hobbies, item)
			seen[item] = true
		}
	}
	if !valid {
		writeError(w, qoder.NewApiError(400, "invalid_profile", "请检查生日、性别和爱好，生日不能晚于今天。"))
		return
	}
	profile := dbop.UserProfile{UserID: auth.UserIDFrom(r.Context()), Gender: body.Gender, Birthday: body.Birthday, Hobbies: hobbies, Bio: strings.TrimSpace(body.Bio), Avatar: body.Avatar}
	session, err := s.DB.SaveUserProfile(r.Context(), profile)
	if err != nil {
		writeError(w, err)
		return
	}
	saved, err := s.DB.GetUserProfile(r.Context(), profile.UserID)
	if err != nil {
		writeError(w, err)
		return
	}
	status := "not_bound"
	if session != "" {
		status = "pending"
	}
	writeJSON(w, http.StatusOK, map[string]any{"profile": saved, "memoryStatus": status})
}

func (s *Server) memoryMember(ctx context.Context, user *dbop.User) (memoryspace.Member, error) {
	profile, err := s.DB.GetUserProfile(ctx, user.ID)
	if err != nil {
		return memoryspace.Member{}, err
	}
	fields := profile.Fields()
	if !profile.UpdatedAt.IsZero() {
		fields["profileSource"], fields["profileUpdatedAt"] = "self_profile", profile.UpdatedAt
	}
	return memoryspace.Member{ID: user.ID, Name: user.Username, Profile: fields}, nil
}
