package conversation

import (
	"strings"
	"testing"
)

func TestAnniversaryActionRequiresCalendarDateAndTypedStorage(t *testing.T) {
	valid := `{"protocol":"tietie.control","version":2,"requestId":"turn_anniversary","actions":[{"type":"save_anniversary","key":"first_trip","title":"第一次旅行","date":"2024-02-29","anniversaryKind":"other","storage":"database_and_memory"}]}`
	if got := ParseAssistant(valid); got.ProtocolError != "" || len(got.Actions) != 1 {
		t.Fatal(got)
	}
	for _, bad := range []string{
		strings.Replace(valid, "2024-02-29", "2025-02-29", 1),
		strings.Replace(valid, "2024-02-29", "02-29", 1),
		strings.Replace(valid, `"other"`, `"unknown"`, 1),
		strings.Replace(valid, "database_and_memory", "memory_only", 1),
		strings.Replace(valid, `"date":`, `"recipientIds":[1],"date":`, 1),
	} {
		if got := ParseAssistant(bad); got.ProtocolError == "" || got.Text != "" {
			t.Fatal("invalid date action accepted", got)
		}
	}
}

func TestAnniversaryDeletionRequiresOnlyRealTarget(t *testing.T) {
	valid := `{"protocol":"tietie.control","version":2,"requestId":"delete_date","actions":[{"type":"delete_anniversary","key":"remove_date","anniversaryId":"ann_real"}]}`
	if got := ParseAssistant(valid); got.ProtocolError != "" || len(got.Actions) != 1 {
		t.Fatal(got)
	}
	for _, bad := range []string{strings.Replace(valid, "ann_real", "", 1), strings.Replace(valid, `"anniversaryId":`, `"date":"2026-05-01","anniversaryId":`, 1), strings.Replace(valid, `"anniversaryId":`, `"memoryKey":"memory_fake","anniversaryId":`, 1), strings.Replace(valid, `"anniversaryId":`, `"storage":"memory_only","anniversaryId":`, 1)} {
		if got := ParseAssistant(bad); got.ProtocolError == "" {
			t.Fatal("invalid deletion accepted", got)
		}
	}
}
