package conversation

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Only an entire, unambiguous one-shot relative request is handled locally.
// Questions, quoted examples, recurrence and multiple times still go to the AI.
var relativeRequest = regexp.MustCompile(`^(?:请|麻烦你|帮我)?([0-9]+(?:\.[0-9]+)?|半|一|两|二|三|四|五|六|七|八|九|十)(秒钟?|分钟|小时|天)后(?:请)?提醒(我们俩|我们|两个人|我俩|我|对方|他|她|TA|ta)[，,、：:\s]*(.+?)[。！!～~]*$`)

type DirectReminder struct {
	Title        string
	DueAt        time.Time
	RecipientIDs []int64
}

func ParseDirectReminder(text string, ctx Context) (DirectReminder, bool) {
	m := relativeRequest.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		return DirectReminder{}, false
	}
	title := strings.TrimSpace(m[4])
	if title == "" || len([]rune(title)) > 500 || strings.ContainsAny(title, "?？\n\r\"“”‘’") {
		return DirectReminder{}, false
	}
	for _, word := range []string{"每天", "每周", "每月", "每隔", "重复", "提醒", "然后", "如果", "或者", "还是", "取消"} {
		if strings.Contains(title, word) {
			return DirectReminder{}, false
		}
	}
	n, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		n = map[string]float64{"半": .5, "一": 1, "两": 2, "二": 2, "三": 3, "四": 4, "五": 5, "六": 6, "七": 7, "八": 8, "九": 9, "十": 10}[m[1]]
	}
	unit := time.Minute
	switch m[2] {
	case "秒", "秒钟":
		unit = time.Second
	case "小时":
		unit = time.Hour
	case "天":
		unit = 24 * time.Hour
	}
	duration := n * float64(unit)
	if duration < float64(time.Second) || duration > float64(365*24*time.Hour) {
		return DirectReminder{}, false
	}
	ids := []int64{}
	other := false
	switch m[3] {
	case "我":
		ids = append(ids, ctx.AuthorID)
	case "对方", "他", "她", "TA", "ta":
		other = true
	default:
		for _, member := range ctx.Members {
			ids = append(ids, member.ID)
		}
	}
	if other {
		for _, member := range ctx.Members {
			if member.ID != ctx.AuthorID {
				ids = append(ids, member.ID)
			}
		}
	}
	validAuthor := false
	for _, member := range ctx.Members {
		if member.ID == ctx.AuthorID {
			validAuthor = true
		}
	}
	if !validAuthor || len(ctx.Members) != 2 || len(ids) == 0 {
		return DirectReminder{}, false
	}
	return DirectReminder{Title: title, DueAt: ctx.Now.Add(time.Duration(duration)), RecipientIDs: ids}, true
}
