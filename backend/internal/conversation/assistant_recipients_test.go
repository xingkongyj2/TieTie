package conversation

import (
	"reflect"
	"testing"
)

func TestAssistantReplyNormalizesRecipientIDs(t *testing.T) {
	for _, recipients := range []string{`[20,21]`, `["20","21"]`, `[20,"21"]`, `["9223372036854775807"]`} {
		t.Run(recipients, func(t *testing.T) {
			body := `{"protocol":"tietie.message","version":2,"requestId":"bind_welcome_test","text":"欢迎你们","recipientIds":` + recipients + `,"source":"chat"}`
			reply := ParseAssistant(body)
			want := []int64{20, 21}
			if recipients == `["9223372036854775807"]` {
				want = []int64{9223372036854775807}
			}
			if reply.Control || reply.ProtocolError != "" || reply.Text != "欢迎你们" || reply.RequestID != "bind_welcome_test" || !reflect.DeepEqual(reply.RecipientIDs, want) {
				t.Fatalf("valid reply was hidden or changed: %#v", reply)
			}
		})
	}
}

func TestAssistantReplyStillRejectsInvalidRecipientIDs(t *testing.T) {
	for _, recipients := range []string{
		`[]`, `null`, `[0]`, `[-1]`, `[20,20]`, `[20,"20"]`, `[20,21,22]`,
		`["0"]`, `["-1"]`, `["020"]`, `[" 20"]`, `["+20"]`, `["20.0"]`, `["2e1"]`,
		`[20.5]`, `["甲"]`, `[null]`, `[true]`, `[{}]`, `["9223372036854775808"]`,
	} {
		t.Run(recipients, func(t *testing.T) {
			body := `{"protocol":"tietie.message","version":2,"requestId":"bind_welcome_test","text":"欢迎你们","recipientIds":` + recipients + `,"source":"chat"}`
			reply := ParseAssistant(body)
			if !reply.Control || reply.ProtocolError == "" || reply.Text != "" {
				t.Fatalf("invalid recipients became a public reply: %#v", reply)
			}
		})
	}
}
