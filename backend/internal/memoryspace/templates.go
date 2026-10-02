// Package memoryspace owns the versioned, embedded templates for a new space.
// Runtime documents reuse these schemas; seeding never infers personal data.
package memoryspace

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
)

const Version = 2
const TodoPath = "tasks/todo-board.json"
const BehaviorPath = "rules/assistant-behavior.json"

// TemplatePath is the single routing table for runtime memory categories.
// A category never grants permission to invent another document type.
func TemplatePath(category string) string {
	switch category {
	case "", "profile":
		return "profile/users.json"
	case "habit":
		return "profile/habits.json"
	case "agreement":
		return "agreements/shared.json"
	case "reminder":
		return TodoPath
	case "realtime":
		return "context/realtime.json"
	case "behavior":
		return BehaviorPath
	case "policy":
		return "rules/memory-policy.json"
	default:
		return ""
	}
}

func IsTemplatePath(path string) bool {
	for _, category := range []string{"profile", "habit", "agreement", "reminder", "realtime", "behavior", "policy"} {
		if TemplatePath(category) == path {
			return true
		}
	}
	return false
}

//go:embed templates
var templates embed.FS

type Member struct {
	ID      int64
	Name    string
	Profile map[string]any
}
type Document struct{ Path, Content string }

func Render(session, space string, a, b Member) ([]Document, error) {
	if a.ID > b.ID {
		a, b = b, a
	}
	values := map[string]string{"group_chat_id": session, "memory_space_id": space, "group_default_rules": "默认全员同步；明确指定对象时按指定执行。"}
	for _, m := range []struct {
		prefix string
		member Member
	}{{"user_a", a}, {"user_b", b}} {
		values[m.prefix+"_id"] = fmt.Sprint(m.member.ID)
		values[m.prefix+"_name"] = m.member.Name
		for _, key := range []string{"nickname", "schedule", "diet", "life_preference", "taboos"} {
			values[m.prefix+"_"+key] = "未提供"
		}
	}
	var out []Document
	walkErr := fs.WalkDir(templates, "templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		body, readErr := templates.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		content := string(body)
		for key, value := range values {
			encoded, _ := json.Marshal(value)
			content = strings.ReplaceAll(content, "{{"+key+"}}", string(encoded[1:len(encoded)-1]))
		}
		// IDs are JSON numbers, while only known textual fields are strings.
		var document map[string]any
		if err := json.Unmarshal([]byte(content), &document); err != nil {
			return fmt.Errorf("invalid template %s: %w", path, err)
		}
		if strings.Contains(content, "{{") {
			return fmt.Errorf("unresolved template %s", path)
		}
		{
			if users, ok := document["users"].([]any); ok {
				for i, u := range users {
					user := u.(map[string]any)
					if i == 0 {
						user["userId"] = a.ID
					} else {
						user["userId"] = b.ID
					}
					profile := a.Profile
					if i != 0 {
						profile = b.Profile
					}
					for _, key := range []string{"gender", "birthday", "hobbies", "bio", "avatar", "region", "profileSource", "profileUpdatedAt"} {
						if value, ok := profile[key]; ok {
							user[key] = value
						}
					}
				}
			}
			if _, ok := document["memberIds"]; ok {
				document["memberIds"] = []int64{a.ID, b.ID}
			}
			body, _ := json.MarshalIndent(document, "", "  ")
			content = string(body) + "\n"
		}
		out = append(out, Document{Path: strings.TrimPrefix(path, "templates/"), Content: content})
		return nil
	})
	return out, walkErr
}

func Behavior() string {
	body, _ := templates.ReadFile("templates/rules/assistant-behavior.json")
	return string(body)
}
func TodoTemplate() string {
	body, _ := templates.ReadFile("templates/" + TodoPath)
	return string(body)
}

// FactPath is an internal logical key, never a cloud document path.
func FactPath(category, scope string, owner int64, key string) string {
	return fmt.Sprintf("%s#%s/%d/%s", TemplatePath(category), scope, owner, key)
}

// Explicit structured fields keep user-controlled data from becoming protocol
// metadata and keep future clients from parsing natural-language summaries.
func ValidateData(category string, data map[string]string) error {
	fields := map[string][]string{
		"profile":   {"name", "nickname", "schedule", "diet", "lifePreference", "taboos", "hobbies", "profession", "communicationPreference"},
		"habit":     {"content", "schedule", "preferredTime", "confirmation"},
		"agreement": {"content", "confirmation"},
		"realtime":  {"category", "content"},
		"behavior":  {"content", "confirmation"},
	}
	if category == "" {
		category = "profile"
	}
	total := 0
	for key, value := range data {
		valid := false
		for _, field := range fields[category] {
			if key == field {
				valid = true
			}
		}
		if !valid {
			return fmt.Errorf("invalid structured memory field: %s", key)
		}
		total += len(key) + len(value)
	}
	if total > 16*1024 {
		return fmt.Errorf("structured memory too large")
	}
	return nil
}
