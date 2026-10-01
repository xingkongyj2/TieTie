package conversation

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

const SilentReply = "silent"

// Match the other authenticated member, avoiding email addresses and prefixes.
func PartnerMention(ctx Context, text string) int64 {
	for _, member := range ctx.Members {
		if member.ID == ctx.AuthorID || member.Name == "" {
			continue
		}
		token := "@" + member.Name
		for offset := 0; offset < len(text); {
			at := strings.Index(text[offset:], token)
			if at < 0 {
				break
			}
			at += offset
			end := at + len(token)
			leftOK := true
			if at > 0 {
				previous, _ := utf8.DecodeLastRuneInString(text[:at])
				leftOK = !(previous < 128 && (unicode.IsLetter(previous) || unicode.IsDigit(previous) || strings.ContainsRune("_.+-", previous)))
			}
			rightOK := end == len(text)
			if !rightOK {
				next, _ := utf8.DecodeRuneInString(text[end:])
				rightOK = unicode.IsSpace(next) || unicode.IsPunct(next) && next != '_' && next != '.'
			}
			if leftOK && rightOK {
				return member.ID
			}
			offset = end
		}
	}
	return 0
}
