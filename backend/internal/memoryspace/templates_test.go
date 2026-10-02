package memoryspace

import (
	"encoding/json"
	"strings"
	"testing"
	"tietie/backend/internal/regions"
)

func TestNewSpaceCarriesSavedRegionIntoExistingProfileTemplate(t *testing.T) {
	location, _ := regions.Resolve("420000", "420100", "420111")
	docs, err := Render("new_session", "new_store", Member{ID: 1, Name: "A", Profile: map[string]any{"region": location}}, Member{ID: 2, Name: "B"})
	if err != nil || len(docs) != 7 {
		t.Fatal(docs, err)
	}
	for _, doc := range docs {
		if doc.Path != "profile/users.json" {
			continue
		}
		var root struct {
			Users []struct{ Region *regions.Location }
		}
		if err := json.Unmarshal([]byte(doc.Content), &root); err != nil {
			t.Fatal(err)
		}
		if len(root.Users) != 2 || root.Users[0].Region == nil || *root.Users[0].Region != location || root.Users[1].Region != nil {
			t.Fatal("initial region missing or assigned to wrong user", root)
		}
	}
}

func TestRenderKeepsNamesAsDataAndLeavesUnknownFactsUnset(t *testing.T) {
	docs, err := Render("sess_test", "space_test", Member{ID: 2, Name: `B "quoted"`}, Member{ID: 1, Name: "A\nnew line"})
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 7 {
		t.Fatal(docs)
	}
	for _, doc := range docs {
		if !json.Valid([]byte(doc.Content)) || strings.Contains(doc.Content, "{{") {
			t.Fatal(doc)
		}
	}
	for _, doc := range docs {
		if doc.Path == "profile/users.json" {
			var profiles struct {
				Users []struct {
					ID                   int64 `json:"userId"`
					Name, Nickname, Diet string
				}
			}
			if err := json.Unmarshal([]byte(doc.Content), &profiles); err != nil {
				t.Fatal(err)
			}
			if profiles.Users[0].ID != 1 || profiles.Users[0].Name != "A\nnew line" || profiles.Users[0].Nickname != "未提供" || profiles.Users[0].Diet != "未提供" {
				t.Fatal(profiles)
			}
		}
	}
}
