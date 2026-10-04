package conversation

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Only an entire, unambiguous one-shot request is handled locally.
// Questions, quoted examples, recurrence and multiple times still go to the AI.
var relativeRequest = regexp.MustCompile(`^(?:请|麻烦你|帮我)?([0-9]+(?:\.[0-9]+)?|半|一|两|二|三|四|五|六|七|八|九|十)(秒钟?|分钟|小时|天)后(?:请)?提醒(我们俩|我们|两个人|我俩|我|对方|他|她|TA|ta)[，,、：:\s]*(.+?)[。！!～~]*$`)

const clockNumber = `(?:[0-9]{1,2}|[零〇一二两三四五六七八九十]{1,3})`

var explicitRequest = regexp.MustCompile(`^(?:请|麻烦你|帮我)?(今天|明天|后天)\s*(凌晨|早上|上午|中午|下午|晚上)?\s*(` + clockNumber + `)(?:点|时)(半|` + clockNumber + `分)?[，,\s]*(?:请)?提醒(我们俩|我们|两个人|我俩|我|对方|他|她|TA|ta)[，,、：:\s]*(.+?)[。！!～~]*$`)
var additionalReminderTime = regexp.MustCompile(`(?:[0-9零〇一二两三四五六七八九十半]+(?:点|时|[:：][0-9]{2}|分钟后|秒钟?后|小时后|天后)|今天|明天|后天|今晚|凌晨|早上|上午|中午|下午|晚上|傍晚|每年|每日|每星期|每个)`)

type DirectReminder struct {
	Title        string
	DueAt        time.Time
	RecipientIDs []int64
}

// ParseSingleReminder is the strict grounding path for a model control action.
// It also returns an explicit elapsed time so the caller can reject it instead
// of accepting the model's replacement date. Legacy relative parsing is kept.
func ParseSingleReminder(text string, ctx Context) (DirectReminder, bool) {
	if parsed, ok := ParseExplicitReminder(text, ctx); ok {
		return parsed, true
	}
	parsed, ok := ParseDirectReminder(text, ctx)
	return parsed, ok && !additionalReminderTime.MatchString(parsed.Title)
}

func ParseDirectReminder(text string, ctx Context) (DirectReminder, bool) {
	m := relativeRequest.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		parsed, ok := ParseExplicitReminder(text, ctx)
		return parsed, ok && parsed.DueAt.After(ctx.Now)
	}
	title := strings.TrimSpace(m[4])
	if !directReminderTitle(title) {
		return DirectReminder{}, false
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
	ids, ok := directReminderRecipients(m[3], ctx)
	if !ok {
		return DirectReminder{}, false
	}
	return DirectReminder{Title: title, DueAt: ctx.Now.Add(time.Duration(duration)), RecipientIDs: ids}, true
}

// ParseExplicitReminder resolves a complete day/clock request in Asia/Shanghai.
// It never rolls an elapsed "today" time forward; callers must reject a past DueAt.
func ParseExplicitReminder(text string, ctx Context) (DirectReminder, bool) {
	m := explicitRequest.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil || ctx.Now.IsZero() {
		return DirectReminder{}, false
	}
	title := strings.TrimSpace(m[6])
	if !directReminderTitle(title) || additionalReminderTime.MatchString(title) {
		return DirectReminder{}, false
	}
	hour, ok := reminderClockNumber(m[3])
	if !ok || hour < 0 || hour > 23 {
		return DirectReminder{}, false
	}
	minute := 0
	if m[4] == "半" {
		minute = 30
	} else if m[4] != "" {
		minute, ok = reminderClockNumber(strings.TrimSuffix(m[4], "分"))
		if !ok || minute < 0 || minute > 59 {
			return DirectReminder{}, false
		}
	}
	// Periods that conflict with their clock value are refused. Bare 1–12 clocks
	// lack AM/PM; bare 0 or 13–23 are explicit 24-hour values.
	switch m[2] {
	case "":
		ok = hour == 0 || hour >= 13
	case "凌晨":
		ok = hour <= 5
	case "早上":
		ok = hour >= 5 && hour <= 11
	case "上午":
		ok = hour >= 6 && hour <= 11
	case "中午":
		ok = hour >= 11 && hour <= 13
	case "下午":
		if hour >= 1 && hour <= 6 {
			hour += 12
		}
		ok = hour >= 13 && hour <= 18
	case "晚上":
		if hour >= 6 && hour <= 11 {
			hour += 12
		}
		ok = hour >= 18 && hour <= 23
	}
	if !ok {
		return DirectReminder{}, false
	}
	ids, ok := directReminderRecipients(m[5], ctx)
	if !ok {
		return DirectReminder{}, false
	}
	zone := time.FixedZone("Asia/Shanghai", 8*60*60)
	now := ctx.Now.In(zone)
	day := map[string]int{"今天": 0, "明天": 1, "后天": 2}[m[1]]
	due := time.Date(now.Year(), now.Month(), now.Day()+day, hour, minute, 0, 0, zone)
	return DirectReminder{Title: title, DueAt: due, RecipientIDs: ids}, true
}

func reminderClockNumber(value string) (int, bool) {
	if n, err := strconv.Atoi(value); err == nil {
		return n, true
	}
	digits := map[rune]int{'零': 0, '〇': 0, '一': 1, '二': 2, '两': 2, '三': 3, '四': 4, '五': 5, '六': 6, '七': 7, '八': 8, '九': 9}
	runes := []rune(value)
	if len(runes) == 1 && runes[0] != '十' {
		n, ok := digits[runes[0]]
		return n, ok
	}
	parts := strings.Split(value, "十")
	if len(parts) != 2 {
		return 0, false
	}
	tens, units := 1, 0
	if parts[0] != "" {
		prefix := []rune(parts[0])
		if len(prefix) != 1 {
			return 0, false
		}
		var ok bool
		tens, ok = digits[prefix[0]]
		if !ok || tens == 0 {
			return 0, false
		}
	}
	if parts[1] != "" {
		suffix := []rune(parts[1])
		if len(suffix) != 1 {
			return 0, false
		}
		var ok bool
		units, ok = digits[suffix[0]]
		if !ok || units == 0 {
			return 0, false
		}
	}
	return tens*10 + units, true
}

func directReminderTitle(title string) bool {
	if title == "" || len([]rune(title)) > 500 || strings.ContainsAny(title, "?？\n\r\"“”‘’") {
		return false
	}
	for _, word := range []string{"每天", "每周", "每月", "每隔", "重复", "提醒", "然后", "如果", "或者", "还是", "取消"} {
		if strings.Contains(title, word) {
			return false
		}
	}
	return true
}

func directReminderRecipients(target string, ctx Context) ([]int64, bool) {
	ids := []int64{}
	other := false
	switch target {
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
		return nil, false
	}
	return ids, true
}
