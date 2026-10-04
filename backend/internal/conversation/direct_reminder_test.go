package conversation

import (
	"reflect"
	"testing"
	"time"
)

func reminderTestContext(now time.Time) Context {
	return Context{Now: now, AuthorID: 1, Members: []Member{{ID: 1, Name: "甲"}, {ID: 2, Name: "乙"}}}
}

func TestSingleReminderResolvesExplicitShanghaiClock(t *testing.T) {
	now := time.Date(2026, 10, 4, 8, 8, 0, 0, time.FixedZone("CST", 8*60*60))
	for _, test := range []struct {
		text string
		due  string
		ids  []int64
	}{
		{"明天上午8点提醒我喝一杯温水", "2026-10-05T08:00:00+08:00", []int64{1}},
		{"明天上午八点三十分提醒我喝水", "2026-10-05T08:30:00+08:00", []int64{1}},
		{"后天下午3点半提醒对方带伞", "2026-10-06T15:30:00+08:00", []int64{2}},
		{"今天晚上8点，提醒我们俩散步", "2026-10-04T20:00:00+08:00", []int64{1, 2}},
		{"明天20时15分提醒TA吃药", "2026-10-05T20:15:00+08:00", []int64{2}},
		{"明天凌晨零点提醒我休息", "2026-10-05T00:00:00+08:00", []int64{1}},
		{"明天中午十二点提醒我吃饭", "2026-10-05T12:00:00+08:00", []int64{1}},
	} {
		t.Run(test.text, func(t *testing.T) {
			parsed, ok := ParseSingleReminder(test.text, reminderTestContext(now))
			if !ok || parsed.DueAt.Format(time.RFC3339) != test.due || !reflect.DeepEqual(parsed.RecipientIDs, test.ids) {
				t.Fatalf("parsed=%+v, ok=%v; want due=%s recipients=%v", parsed, ok, test.due, test.ids)
			}
		})
	}
}

func TestSingleReminderUsesShanghaiCalendarAcrossMonthBoundary(t *testing.T) {
	// This instant is already February 1 in the user's timezone.
	now := time.Date(2026, 1, 31, 23, 55, 0, 0, time.UTC)
	parsed, ok := ParseSingleReminder("明天上午8点提醒我喝水", reminderTestContext(now))
	if !ok || parsed.DueAt.Format(time.RFC3339) != "2026-02-02T08:00:00+08:00" {
		t.Fatalf("Shanghai date must determine tomorrow: parsed=%+v ok=%v", parsed, ok)
	}
}

func TestSingleReminderDoesNotGuessAmbiguousOrMultipleTimes(t *testing.T) {
	ctx := reminderTestContext(time.Date(2026, 10, 4, 8, 8, 0, 0, time.FixedZone("CST", 8*60*60)))
	for _, text := range []string{
		"明天8点提醒我喝水", "明天上午提醒我喝水", "明天晚上12点提醒我睡觉",
		"明天上午13点提醒我喝水", "明天上午8点60分提醒我喝水",
		"每天明天上午8点提醒我喝水", "明天上午8点提醒我喝水，每天重复",
		"明天上午8点提醒我喝水每年", "明天上午8点提醒我喝水，每星期一",
		"明天上午8点提醒我喝水周一周三", "明天上午8点提醒我喝水隔三天",
		"明天上午8点提醒我喝水，9点吃药", "明天上午8点提醒我喝水，半小时后吃药",
		"明天上午8点提醒我喝水，晚上吃药", "明天上午8点提醒我喝水，可以吗？",
		"如果我说明天上午8点提醒我喝水", "明天上午8点提醒我“喝水”",
		"5分钟后提醒我喝水，10分钟后吃药",
	} {
		t.Run(text, func(t *testing.T) {
			if parsed, ok := ParseSingleReminder(text, ctx); ok {
				t.Fatalf("unclear request should not be grounded: %+v", parsed)
			}
		})
	}
}

func TestElapsedTodayIsNotRolledForward(t *testing.T) {
	ctx := reminderTestContext(time.Date(2026, 10, 4, 8, 8, 0, 0, time.FixedZone("CST", 8*60*60)))
	text := "今天上午8点提醒我喝水"
	parsed, ok := ParseSingleReminder(text, ctx)
	if !ok || parsed.DueAt.Format(time.RFC3339) != "2026-10-04T08:00:00+08:00" {
		t.Fatalf("explicit elapsed time must remain today for validation: %+v ok=%v", parsed, ok)
	}
	if _, ok := ParseDirectReminder(text, ctx); ok {
		t.Fatal("the direct creation path must reject an elapsed time")
	}
}

func TestDirectReminderKeepsExistingRelativeBehavior(t *testing.T) {
	ctx := reminderTestContext(time.Date(2026, 10, 4, 8, 8, 0, 0, time.FixedZone("CST", 8*60*60)))
	for _, test := range []struct {
		text string
		wait time.Duration
		ids  []int64
	}{
		{"1分钟后提醒我喝水", time.Minute, []int64{1}},
		{"半小时后提醒我们，出发", 30 * time.Minute, []int64{1, 2}},
		{"请两天后提醒对方带伞", 48 * time.Hour, []int64{2}},
	} {
		parsed, ok := ParseDirectReminder(test.text, ctx)
		if !ok || !parsed.DueAt.Equal(ctx.Now.Add(test.wait)) || !reflect.DeepEqual(parsed.RecipientIDs, test.ids) {
			t.Fatalf("relative behavior changed: text=%s parsed=%+v ok=%v", test.text, parsed, ok)
		}
	}
}

func TestDirectReminderRoutesRecurringRequestsToAI(t *testing.T) {
	ctx := reminderTestContext(time.Date(2026, 10, 4, 8, 8, 0, 0, time.FixedZone("CST", 8*60*60)))
	for _, text := range []string{
		"每天上午8点提醒我喝水",
		"每周一和周三上午8点提醒我喝水",
		"三天后提醒我喝水，每隔三天重复",
		"明天上午8点提醒我喝水，指定10月8日和12日再提醒",
	} {
		if parsed, ok := ParseDirectReminder(text, ctx); ok {
			t.Errorf("recurring request must not create a one-time reminder: %q => %+v", text, parsed)
		}
	}
}
