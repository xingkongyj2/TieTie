package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"tietie/backend/internal/auth"
	"tietie/backend/internal/dbop"
	"tietie/backend/internal/memoryspace"
	"tietie/backend/internal/qoder"
	"tietie/backend/internal/regions"
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
		Name     *string         `json:"name"`
		Gender   string          `json:"gender"`
		Birthday string          `json:"birthday"`
		Hobbies  []string        `json:"hobbies"`
		Bio      string          `json:"bio"`
		Avatar   string          `json:"avatar"`
		Region   json.RawMessage `json:"region"`
	}
	if err := decodeJSONBody(r, &body, 8192); err != nil {
		writeError(w, err)
		return
	}
	if body.Gender == "" {
		body.Gender = "unspecified"
	}
	name := ""
	if body.Name != nil {
		name = strings.TrimSpace(*body.Name)
		if name == "" || utf8.RuneCountInString(name) > 24 {
			writeError(w, qoder.NewApiError(400, "invalid_profile_name", "名称请填写 1-24 个字。"))
			return
		}
	}
	valid := body.Gender == "male" || body.Gender == "female" || body.Gender == "unspecified"
	if body.Birthday != "" {
		date, err := time.Parse("2006-01-02", body.Birthday)
		today := time.Now().In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("2006-01-02")
		valid = valid && err == nil && date.Format("2006-01-02") == body.Birthday && body.Birthday <= today
	}
	valid = valid && len(body.Hobbies) <= 8 && utf8.RuneCountInString(body.Bio) <= 200 && len(body.Avatar) <= 512 && validProfileAvatar(body.Avatar)
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
	if err := s.validateOwnedAvatar(r, body.Avatar); err != nil {
		writeError(w, err)
		return
	}
	profile := dbop.UserProfile{UserID: auth.UserIDFrom(r.Context()), Name: name, Gender: body.Gender, Birthday: body.Birthday, Hobbies: hobbies, Bio: strings.TrimSpace(body.Bio), Avatar: body.Avatar}
	if len(body.Region) > 0 && string(body.Region) != "null" {
		var selected regions.Location
		err := json.Unmarshal(body.Region, &selected)
		if err == nil {
			profile.Region, err = regions.Resolve(selected.ProvinceCode, selected.CityCode, selected.DistrictCode)
		}
		if err != nil {
			writeError(w, qoder.NewApiError(400, "invalid_region", "请选择有效的省份、城市和区／县。"))
			return
		}
	}
	session, err := s.DB.SaveUserProfile(r.Context(), profile, len(body.Region) == 0)
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
		s.wakeMemory()
	}
	writeJSON(w, http.StatusOK, map[string]any{"profile": saved, "memoryStatus": status})
}

// Bundled assets and owned server images use root-relative paths. Older remote
// profile images remain valid only over HTTPS; temporary device paths cannot be saved.
func validProfileAvatar(value string) bool {
	if value == "" {
		return true
	}
	if strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//") {
		return true
	}
	u, err := url.Parse(value)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil
}

func (s *Server) handleRegions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}
	writeJSON(w, http.StatusOK, regions.Catalog())
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
	name := profile.Name
	if name == "" {
		name = user.Username
	}
	return memoryspace.Member{ID: user.ID, Name: name, Profile: fields}, nil
}
