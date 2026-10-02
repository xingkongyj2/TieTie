package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"tietie/backend/internal/dbop"
	"tietie/backend/internal/memoryspace"
	"tietie/backend/internal/regions"
)

func TestProfileRegionCanonicalizationCorrectionAndClear(t *testing.T) {
	s, _, _, users, call := setupV2(t)
	ctx := context.Background()
	if res := call(0, "GET", "/api/account/regions", nil); res.Code != 200 || !strings.Contains(res.Body.String(), "仙桃市") {
		t.Fatal("region catalog unavailable", res.Body.String())
	}
	body := map[string]any{"gender": "female", "birthday": "1998-11-16", "hobbies": []string{"画画"}, "region": map[string]string{"provinceCode": "420000", "cityCode": "420100", "districtCode": "420111", "district": "伪造区县", "province": "伪造省份", "city": "伪造城市"}}
	if res := call(0, "PUT", "/api/account/profile", body); res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	check := func(want string) {
		t.Helper()
		profile, err := s.DB.GetUserProfile(ctx, users[0].ID)
		if err != nil || profile.Region.CityCode != want {
			t.Fatal(profile, err)
		}
		root, err := s.DB.GetMemoryRecord(ctx, dbop.MemoryID("sess_shared", "profile/users.json"), "sess_shared")
		if err != nil || root == nil {
			t.Fatal(root, err)
		}
		var doc struct {
			Users []struct {
				UserID int64             `json:"userId"`
				Region *regions.Location `json:"region"`
			}
		}
		if err := json.Unmarshal([]byte(root.Content), &doc); err != nil {
			t.Fatal(err)
		}
		for _, user := range doc.Users {
			if user.UserID != users[0].ID {
				if user.Region != nil {
					t.Fatal("partner region overwritten")
				}
				continue
			}
			if want == "" {
				if user.Region != nil {
					t.Fatal("cleared region remains in memory", user)
				}
			} else if user.Region == nil || user.Region.CityCode != want || *user.Region != profile.Region {
				t.Fatal("memory region differs from database", user)
			}
		}
	}
	check("420100")
	canonical, _ := s.DB.GetUserProfile(ctx, users[0].ID)
	if canonical.Region.Province != "湖北省" || canonical.Region.City != "武汉市" || canonical.Region.District != "洪山区" {
		t.Fatal("client labels were trusted", canonical.Region)
	}
	for _, invalid := range []any{map[string]string{"provinceCode": "420000", "cityCode": "330100"}, map[string]string{"provinceCode": "420000", "cityCode": "420100", "districtCode": "330106"}, map[string]string{"provinceCode": "", "cityCode": "", "districtCode": "420111"}, map[string]string{"provinceCode": "420000"}, "武汉", map[string]string{"provinceCode": "unknown", "cityCode": "unknown"}} {
		body["region"] = invalid
		if res := call(0, "PUT", "/api/account/profile", body); res.Code != 400 {
			t.Fatal("invalid region accepted", invalid, res.Code)
		}
		check("420100")
	}
	delete(body, "region")
	if res := call(0, "PUT", "/api/account/profile", body); res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	check("420100") // older clients and avatar edits must not clear the region.
	body["region"] = map[string]string{"provinceCode": "330000", "cityCode": "330100", "districtCode": "330106"}
	if res := call(0, "PUT", "/api/account/profile", body); res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	check("330100")
	body["region"] = nil
	if res := call(0, "PUT", "/api/account/profile", body); res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	check("")
	other, _ := s.DB.GetUserProfile(ctx, users[1].ID)
	if other.Region.CityCode != "" {
		t.Fatal("partner database region overwritten")
	}
}

func TestProfileSaveCorrectsMemoryAndChatWaitsForCurrentProfile(t *testing.T) {
	s, cloud, memory, users, call := setupV2(t)
	ctx := context.Background()
	path := "/api/account/profile"
	profile := map[string]any{"gender": "female", "birthday": "1998-11-16", "hobbies": []string{"  画画  ", "画画", "骑车"}, "bio": "爱看展", "avatar": "/avatars/cream-cat.png"}
	if res := call(0, "PUT", path, profile); res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	key := dbop.MemoryID("sess_shared", memoryspace.FactPath("profile", "self", users[0].ID, "account_profile"))
	before, err := s.DB.GetMemoryRecord(ctx, key, "sess_shared")
	if err != nil || before == nil || !strings.Contains(before.Content, "画画") {
		t.Fatal(before, err)
	}
	if res := call(1, "PUT", path, map[string]any{"gender": "male", "birthday": "1999-06-08", "hobbies": []string{"烹饪"}, "bio": "", "avatar": "/avatars/peach-cat.png"}); res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	// Even if cloud writes fail, both the edited profile and correction outbox
	// are committed. Clearing a field must not retain its previous value.
	memory.mu.Lock()
	memory.fail = true
	memory.mu.Unlock()
	profile["birthday"], profile["hobbies"], profile["bio"] = "", []string{}, ""
	for i := 0; i < 2; i++ {
		if res := call(0, "PUT", path, profile); res.Code != 200 {
			t.Fatal(res.Body.String())
		}
	}
	current, err := s.DB.GetMemoryRecord(ctx, key, "sess_shared")
	if err != nil || current.Revision != before.Revision+1 || current.State != "pending" || strings.Contains(current.Content, "画画") || strings.Contains(current.Content, "1998-11-16") {
		t.Fatal("correction lost, duplicated or still contains old facts", current, err)
	}
	old, err := s.DB.GetMemoryRevision(ctx, "sess_shared", key, int64(before.Revision))
	if err != nil || old == nil || !strings.Contains(old.Content, "画画") {
		t.Fatal("profile history lost", old, err)
	}
	for viewer := 0; viewer < 3; viewer++ {
		res := call(viewer, "GET", "/api/account/profiles", nil)
		var body struct {
			Profiles []dbop.UserProfile `json:"profiles"`
		}
		if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &body) != nil {
			t.Fatal(res.Body.String())
		}
		want := 2
		if viewer == 2 {
			want = 1
		}
		if len(body.Profiles) != want || body.Profiles[0].UserID != users[viewer].ID {
			t.Fatal("profile access isolation failed", body)
		}
	}
	if res := call(0, "POST", "/api/qoder/sessions/sess_shared/messages", map[string]string{"text": "记得我的资料吗"}); res.Code == 200 {
		t.Fatal("AI turn accepted with stale mounted profile")
	}
	cloud.mu.Lock()
	posts := cloud.postings
	cloud.mu.Unlock()
	if posts != 0 {
		t.Fatal("failed profile sync sent a stale AI turn")
	}
	memory.mu.Lock()
	memory.fail = false
	memory.mu.Unlock()
	if res := call(0, "POST", "/api/qoder/sessions/sess_shared/messages", map[string]string{"text": "记得我的资料吗"}); res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	records, err := s.DB.ProfileMemoryRecords(ctx, "sess_shared", users[0].ID, users[1].ID)
	if err != nil || len(records) != 3 {
		t.Fatal(records, err)
	}
	for _, record := range records {
		if record.State != "synced" {
			t.Fatal("profile was not synced before AI read", record)
		}
	}
	memory.mu.Lock()
	defer memory.mu.Unlock()
	for _, entry := range memory.entries {
		if !memoryspace.IsDocumentPath(entry.Path) {
			t.Fatal("profile created non-template document", entry.Path)
		}
		if entry.Path != "profile/users.json" {
			continue
		}
		var doc struct {
			Users []struct {
				UserID   int64 `json:"userId"`
				Birthday string
				Hobbies  []string
				Bio      string
				Gender   string
			}
		}
		if err := json.Unmarshal([]byte(entry.Content), &doc); err != nil {
			t.Fatal(err)
		}
		if len(doc.Users) != 2 || doc.Users[0].Birthday != "" || len(doc.Users[0].Hobbies) != 0 || doc.Users[0].Bio != "" || doc.Users[0].Gender != "female" || doc.Users[1].Birthday != "1999-06-08" || len(doc.Users[1].Hobbies) != 1 || doc.Users[1].Hobbies[0] != "烹饪" {
			t.Fatal("cloud profile clear or identity mismatch", doc)
		}
	}
}

func TestProfileRejectsInvalidDatesAndForgedIdentity(t *testing.T) {
	s, _, _, users, call := setupV2(t)
	for _, body := range []any{
		map[string]any{"gender": "female", "birthday": "2026-02-30"},
		map[string]any{"gender": "female", "birthday": "2099-01-01"},
		map[string]any{"gender": "other"},
		map[string]any{"gender": "male", "hobbies": []string{strings.Repeat("猫", 21)}},
		map[string]any{"gender": "male", "avatar": "https://example.invalid/tracker"},
	} {
		if res := call(0, "PUT", "/api/account/profile", body); res.Code != 400 {
			t.Fatal("invalid profile accepted", body, res.Code)
		}
	}
	if res := call(0, "PUT", "/api/account/profile", map[string]any{"gender": "male", "userId": users[1].ID}); res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	self, err := s.DB.GetUserProfile(context.Background(), users[0].ID)
	if err != nil || self.Gender != "male" {
		t.Fatal(self, err)
	}
	other, err := s.DB.GetUserProfile(context.Background(), users[1].ID)
	if err != nil || other.Gender != "unspecified" {
		t.Fatal("forged body identity edited partner", other, err)
	}
}
