package dbop

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func expectDispatchReceiptUpdate(mock sqlmock.Sqlmock) *sqlmock.ExpectedExec {
	return mock.ExpectExec(regexp.QuoteMeta("UPDATE `reminders` SET `dispatch_event_ids`=?,`updated_at`=? WHERE id = ? AND status = ?")).
		WithArgs(`["accepted-1","accepted-2"]`, sqlmock.AnyArg(), "reminder-1", ReminderDispatching)
}

func TestRecordReminderDispatchOnlySavesAcceptanceReceipt(t *testing.T) {
	db, mock := mockWechatDB(t)
	db.SetWechatNotifications(WechatNotificationOptions{Enabled: true, AppID: "wx-test", TemplateID: "template"})
	// The only expected statement is the receipt update: acceptance must neither
	// rebuild reminder memory/history nor enqueue a push before the AI replies.
	expectDispatchReceiptUpdate(mock).WillReturnResult(sqlmock.NewResult(0, 1))
	if err := db.RecordReminderDispatch(context.Background(), "reminder-1", []string{"accepted-1", "accepted-2"}); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRecordReminderDispatchPreservesStateGuardAndDatabaseErrors(t *testing.T) {
	failure := errors.New("receipt update failed")
	for _, tc := range []struct {
		name string
		rows int64
		err  error
		want error
	}{
		{name: "dispatch no longer active", rows: 0, want: ErrReminderState},
		{name: "database failure", err: failure, want: failure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock := mockWechatDB(t)
			expected := expectDispatchReceiptUpdate(mock)
			if tc.err != nil {
				expected.WillReturnError(tc.err)
			} else {
				expected.WillReturnResult(sqlmock.NewResult(0, tc.rows))
			}
			err := db.RecordReminderDispatch(context.Background(), "reminder-1", []string{"accepted-1", "accepted-2"})
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRecordReminderDispatchRejectsInvalidReceiptWithoutDatabaseAccess(t *testing.T) {
	for _, ids := range [][]string{nil, {}, {""}, {"accepted-1", " \t\n"}} {
		db, mock := mockWechatDB(t)
		if err := db.RecordReminderDispatch(context.Background(), "reminder-1", ids); !errors.Is(err, ErrReminderInvalid) {
			t.Fatalf("receipt %q: error = %v, want invalid receipt", ids, err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRecordReminderDispatchRequiresDatabaseForValidReceipt(t *testing.T) {
	for _, db := range []*DB{nil, {}} {
		if err := db.RecordReminderDispatch(context.Background(), "reminder-1", []string{"accepted-1"}); !errors.Is(err, errNoDB) {
			t.Fatalf("error = %v, want database unavailable", err)
		}
	}
}
