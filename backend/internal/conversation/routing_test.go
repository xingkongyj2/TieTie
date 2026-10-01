package conversation

import "testing"

func TestPartnerMentionUsesRealOtherMemberAndTokenBoundaries(t *testing.T) {
	ctx := Context{AuthorID: 1, Members: []Member{{ID: 1, Name: "我"}, {ID: 2, Name: "alex"}}}
	for _, test := range []struct {
		text   string
		target int64
	}{
		{"@alex 我喜欢徒步", 2}, {"嗨@alex！", 2}, {"@alex", 2},
		{"@我 今天忙吗", 0}, {"@对方 今天忙吗", 0}, {"dev@alex.com", 0},
		{"@alexander 好久不见", 0}, {"默认和机器人聊天", 0},
	} {
		if got := PartnerMention(ctx, test.text); got != test.target {
			t.Errorf("%q: got %d want %d", test.text, got, test.target)
		}
	}
}

func TestSilentProtocolCannotContainUserMessageOrActions(t *testing.T) {
	valid := ParseAssistant(`{"protocol":"tietie.silent","version":2,"requestId":"turn_123"}`)
	if !valid.Silent || !valid.Control || valid.ProtocolError != "" {
		t.Fatal(valid)
	}
	invalid := ParseAssistant(`{"protocol":"tietie.silent","version":2,"requestId":"turn_123","text":"已记住"}`)
	if invalid.ProtocolError == "" || invalid.Silent {
		t.Fatal(invalid)
	}
}
