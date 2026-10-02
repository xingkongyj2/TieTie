//go:build live

package api

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"tietie/backend/internal/conversation"
	"tietie/backend/internal/memoryspace"
	"tietie/backend/internal/qoder"
)

// Isolated accounts, database, conversation and store. Verifies real cloud
// seeding and AI recall after an edit without touching existing user data.
func TestLiveProfileInitializationAndCorrection(t *testing.T) {
	if os.Getenv("QODER_LIVE_TEST") != "1" {
		t.Skip("requires QODER_LIVE_TEST=1 and -tags live")
	}
	cfg := loadLiveTestConfig(t)
	cfg.ConversationProtocolVersion, cfg.CloudMemoryEnabled = 2, true
	s, _, _, users, call := setupV2(t)
	s.Cfg, s.Qoder = &cfg, qoder.NewClient(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if _, err := s.DB.Unbind(ctx, users[0].ID); err != nil {
		t.Fatal(err)
	}
	profile := map[string]any{"gender": "female", "birthday": "1998-11-16", "hobbies": []string{"画画", "骑车"}, "bio": "喜欢看展", "avatar": "/avatars/cream-cat.png", "region": map[string]string{"provinceCode": "420000", "cityCode": "420100", "districtCode": "420111"}}
	if res := call(0, "PUT", "/api/account/profile", profile); res.Code != 200 {
		t.Fatal("initial profile save failed", res.Code)
	}
	if res := call(1, "PUT", "/api/account/profile", map[string]any{"gender": "male", "birthday": "1999-06-08", "hobbies": []string{"烹饪"}, "bio": "", "avatar": "/avatars/peach-cat.png"}); res.Code != 200 {
		t.Fatal(res.Code)
	}
	if res := call(0, "POST", "/api/account/bind", map[string]string{"code": users[1].Code}); res.Code != 200 {
		t.Fatal("isolated initialization failed", res.Code)
	}
	binding, err := s.DB.GetLatestBindingByUser(ctx, users[0].ID)
	if err != nil || binding == nil {
		t.Fatal(err)
	}
	store, err := s.DB.GetSpaceMemoryStore(ctx, binding.SessionID)
	if err != nil || store == nil {
		t.Fatal(err)
	}
	defer s.cleanupInitializedSpace(binding.SessionID, store.StoreID)
	readProfiles := func(wantBirthday string, wantHobbies []string, wantDistrict string) {
		t.Helper()
		entries := listLiveMemoryDocuments(t, ctx, s.Qoder, store.StoreID)
		found := false
		for _, ref := range entries {
			if !memoryspace.IsDocumentPath(ref.Path) {
				t.Fatal("non-template profile file", ref.Path)
			}
			if ref.Path != "profile/users.json" {
				continue
			}
			found = true
			entry, err := s.Qoder.GetMemory(ctx, store.StoreID, ref.ID)
			if err != nil {
				t.Fatal(err)
			}
			var doc struct {
				Users []struct {
					UserID   int64 `json:"userId"`
					Birthday string
					Hobbies  []string
					Bio      string
					Region   struct{ Province, City, District string }
				}
			}
			if err := json.Unmarshal([]byte(entry.Content), &doc); err != nil {
				t.Fatal(err)
			}
			if len(doc.Users) != 2 || doc.Users[0].UserID != users[0].ID || doc.Users[0].Region.District != wantDistrict || doc.Users[0].Birthday != wantBirthday || strings.Join(doc.Users[0].Hobbies, ",") != strings.Join(wantHobbies, ",") || doc.Users[1].Birthday != "1999-06-08" || strings.Join(doc.Users[1].Hobbies, ",") != "烹饪" {
				t.Fatal("cloud profiles incomplete or mixed", doc)
			}
		}
		if !found {
			t.Fatal("cloud profile root missing")
		}
	}
	readProfiles("1998-11-16", []string{"画画", "骑车"}, "洪山区")
	path := "/api/qoder/sessions/" + binding.SessionID + "/messages"
	ask := func(text, birthday string, hobbies []string) {
		t.Helper()
		if res := call(0, "POST", path, map[string]string{"text": text}); res.Code != 200 {
			t.Fatal("profile recall request failed", res.Code)
		}
		requestID := ""
		for ctx.Err() == nil {
			if err := s.syncPendingConversation(ctx, binding.SessionID); err != nil {
				t.Fatal(err)
			}
			jobs, err := s.DB.ClaimControls(ctx, time.Now(), 4)
			if err != nil {
				t.Fatal(err)
			}
			for _, job := range jobs {
				requestID = job.RequestID
				if err := s.runControl(ctx, job); err != nil {
					t.Fatal(err)
				}
			}
			history, err := s.Qoder.GetMessages(ctx, binding.SessionID, "")
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range history.Events {
				input, ok := conversation.DecodeInput(eventText(event))
				if ok && !input.Hidden && input.UserID == users[0].ID && input.Text == text {
					requestID = input.RequestID
				}
			}
			for _, msg := range history.Messages {
				if msg.Sender != "ai" || msg.Source != "chat" || requestID == "" || msg.RequestID != requestID {
					continue
				}
				if !strings.Contains(msg.Text, birthday) {
					t.Fatalf("AI birthday recall is stale or incorrect: %s", msg.Text)
				}
				for _, hobby := range hobbies {
					if !strings.Contains(msg.Text, hobby) {
						t.Fatalf("AI hobby recall is stale or incorrect: %s", msg.Text)
					}
				}
				t.Logf("real AI account profile recall: %s", msg.Text)
				return
			}
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
			}
		}
		t.Fatal("AI profile recall timed out")
	}
	ask("请读取我在我的小档案保存的资料，说一下我的生日、喜欢的事物和地区（省份、城市、区县），生日用 YYYY-MM-DD 格式。", "1998-11-16", []string{"画画", "骑车", "湖北", "武汉", "洪山"})
	profile["birthday"], profile["hobbies"], profile["bio"] = "2000-04-09", []string{"瑜伽"}, ""
	profile["region"] = map[string]string{"provinceCode": "330000", "cityCode": "330100", "districtCode": "330106"}
	if res := call(0, "PUT", "/api/account/profile", profile); res.Code != 200 {
		t.Fatal("profile correction failed", res.Code)
	}
	ask("我刚更新了我的小档案，请读取最新资料，说一下我现在的生日、喜欢的事物和地区（省份、城市、区县），生日用 YYYY-MM-DD 格式，只列当前档案值。", "2000-04-09", []string{"瑜伽", "浙江", "杭州", "西湖"})
	readProfiles("2000-04-09", []string{"瑜伽"}, "西湖区")
}
