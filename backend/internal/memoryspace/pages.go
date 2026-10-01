package memoryspace

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var pageSuffix = regexp.MustCompile(`^/[0-9]{4}-(0[1-9]|1[0-2])/[0-9]{6}\.json$`)

// Only the seven schemas and their mechanically numbered pages may be written.
func IsDocumentPath(path string) bool {
	if IsTemplatePath(path) {
		return true
	}
	for _, category := range []string{"profile", "habit", "agreement", "reminder", "realtime", "behavior"} {
		prefix := strings.TrimSuffix(TemplatePath(category), ".json")
		if strings.HasPrefix(path, prefix) && pageSuffix.MatchString(strings.TrimPrefix(path, prefix)) {
			return true
		}
	}
	return false
}

func Template(path string) (string, error) {
	if !IsTemplatePath(path) {
		return "", fmt.Errorf("unknown memory template")
	}
	body, err := templates.ReadFile("templates/" + path)
	return string(body), err
}

// Page uses the original schema, rather than introducing fact/observation types.
// Provenance belongs to each entry; structured fields are materialized for AI.
func Page(base string, month string, page int, category string, facts []json.RawMessage) (string, error) {
	var doc map[string]any
	if err := json.Unmarshal([]byte(base), &doc); err != nil {
		return "", err
	}
	delete(doc, "recordDirectory")
	if _, ok := doc["instructions"]; ok {
		doc["instructions"] = "服务端对话协议位于根模板 rules/assistant-behavior.json 的 instructions 字段。"
	}
	doc["pagination"] = map[string]any{"month": month, "page": page, "pageSize": 8}
	switch category {
	case "profile":
		users, _ := doc["users"].([]any)
		shared := []any{}
		for _, u := range users {
			u.(map[string]any)["entries"] = []any{}
		}
		for _, raw := range facts {
			var fact map[string]any
			if err := json.Unmarshal(raw, &fact); err != nil {
				return "", err
			}
			owner, _ := fact["ownerId"].(float64)
			found := false
			for _, u := range users {
				user := u.(map[string]any)
				if user["userId"] != owner {
					continue
				}
				user["entries"] = append(user["entries"].([]any), fact)
				for _, key := range []string{"name", "nickname", "schedule", "diet", "lifePreference", "taboos", "hobbies", "profession", "communicationPreference"} {
					if value, ok := fact[key]; ok {
						user[key] = value
					}
				}
				found = true
			}
			if !found {
				shared = append(shared, fact)
			}
		}
		doc["users"], doc["sharedEntries"] = users, shared
	case "agreement":
		doc["agreements"] = facts
	case "behavior":
		doc["additions"] = facts
	default:
		doc["entries"] = facts
	}
	body, err := json.Marshal(doc)
	if len(body) > 96*1024 {
		return "", fmt.Errorf("template page too large")
	}
	return string(body), err
}

func Entries(body string) ([]json.RawMessage, error) {
	var doc struct {
		Users []struct {
			Entries []json.RawMessage `json:"entries"`
		} `json:"users"`
		Shared     []json.RawMessage `json:"sharedEntries"`
		Entries    []json.RawMessage `json:"entries"`
		Agreements []json.RawMessage `json:"agreements"`
		Additions  []json.RawMessage `json:"additions"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return nil, err
	}
	out := append(doc.Entries, doc.Shared...)
	out = append(out, doc.Agreements...)
	out = append(out, doc.Additions...)
	for _, u := range doc.Users {
		out = append(out, u.Entries...)
	}
	return out, nil
}

// Path and content must agree with the embedded schema's data type.
func ValidateDocument(path, body string) error {
	if !IsDocumentPath(path) {
		return fmt.Errorf("unknown memory document path")
	}
	templatePath := path
	if !IsTemplatePath(path) {
		for _, c := range []string{"profile", "habit", "agreement", "reminder", "realtime", "behavior"} {
			root := TemplatePath(c)
			if strings.HasPrefix(path, strings.TrimSuffix(root, ".json")+"/") {
				templatePath = root
				break
			}
		}
	}
	base, err := Template(templatePath)
	if err != nil {
		return err
	}
	var expected, doc map[string]any
	if err := json.Unmarshal([]byte(base), &expected); err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return err
	}
	if doc["type"] != expected["type"] {
		return fmt.Errorf("memory document type disagrees with template")
	}
	field := map[string]string{"profile/users.json": "users", "profile/habits.json": "entries", "agreements/shared.json": "agreements", "tasks/todo-board.json": "reminders", "context/realtime.json": "entries", "rules/assistant-behavior.json": "additions", "rules/memory-policy.json": "rules"}[templatePath]
	if _, ok := doc[field].([]any); !ok {
		return fmt.Errorf("missing template data field %s", field)
	}
	return nil
}
