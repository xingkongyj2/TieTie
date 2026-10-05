package dbop

import (
	"context"
	"database/sql/driver"
	"errors"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-sql-driver/mysql"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func mockWechatDB(t *testing.T) (*DB, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	gdb, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true, Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	return &DB{gdb: gdb}, mock
}

func expectWechatLookup(mock sqlmock.Sqlmock, userID int64) {
	rows := sqlmock.NewRows([]string{"app_id", "open_id", "user_id"})
	if userID > 0 {
		rows.AddRow("wx-test", "private-openid", userID)
	}
	mock.ExpectQuery("SELECT .* FROM `wechat_identities`").WithArgs("wx-test", "private-openid", 1).WillReturnRows(rows)
	if userID > 0 {
		mock.ExpectQuery("SELECT .* FROM `users`").WithArgs(userID, 1).WillReturnRows(sqlmock.NewRows([]string{"id", "username", "password", "code"}).AddRow(userID, "wx_existing", "", "1234"))
	}
}

type generatedWechatUsername struct{}

func (generatedWechatUsername) Match(v driver.Value) bool {
	s, ok := v.(string)
	return ok && regexp.MustCompile(`^微信用户_[0-9a-f]{12}$`).MatchString(s)
}

type generatedWechatCode struct{}

func (generatedWechatCode) Match(v driver.Value) bool {
	s, ok := v.(string)
	return ok && regexp.MustCompile(`^[0-9]{4}$`).MatchString(s)
}

func expectWechatUserInsert(mock sqlmock.Sqlmock, id int64) {
	mock.ExpectExec("INSERT INTO `users`").WithArgs(generatedWechatUsername{}, "", generatedWechatCode{}, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(id, 1))
}

func TestWechatIdentitySchemaUsesCompositePrimaryKeyAndUserConstraint(t *testing.T) {
	s, err := schema.Parse(&WechatIdentity{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.PrimaryFields) != 2 || s.PrimaryFields[0].DBName != "app_id" || s.PrimaryFields[1].DBName != "open_id" {
		t.Fatal("identity must have an app-scoped composite primary key")
	}
	var userUnique bool
	for _, index := range s.ParseIndexes() {
		if index.Name == "idx_wechat_identities_user_id" && index.Class == "UNIQUE" {
			userUnique = true
		}
	}
	if !userUnique {
		t.Fatal("a user must have at most one wechat identity")
	}
	if s.LookUpField("OpenID").TagSettings["TYPE"] != "varbinary(128)" {
		t.Fatal("openid uniqueness must be case-sensitive regardless of database collation")
	}
	relation := s.Relationships.Relations["User"]
	if relation == nil || relation.ParseConstraint() == nil || relation.ParseConstraint().OnDelete != "CASCADE" {
		t.Fatal("identity must reference its account")
	}
}

func TestWechatLoginReturnsExistingAccountWithoutCreatingUser(t *testing.T) {
	db, mock := mockWechatDB(t)
	expectWechatLookup(mock, 7)
	user, fresh, err := db.GetOrCreateWechatUser(context.Background(), "wx-test", "private-openid")
	if err != nil || fresh || user == nil || user.ID != 7 {
		t.Fatalf("user=%#v, new=%t, error=%v", user, fresh, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestWechatLookupPreservesOpenIDCaseAndAppScope(t *testing.T) {
	db, mock := mockWechatDB(t)
	for _, tc := range []struct {
		appID, openID string
		userID        int64
	}{
		{"wx-test", "CaseOpenID", 7},
		{"wx-test", "caseopenid", 8},
		{"wx-other-app", "CaseOpenID", 9},
	} {
		mock.ExpectQuery("SELECT .* FROM `wechat_identities`").WithArgs(tc.appID, tc.openID, 1).WillReturnRows(sqlmock.NewRows([]string{"app_id", "open_id", "user_id"}).AddRow(tc.appID, tc.openID, tc.userID))
		mock.ExpectQuery("SELECT .* FROM `users`").WithArgs(tc.userID, 1).WillReturnRows(sqlmock.NewRows([]string{"id", "username", "password", "code"}).AddRow(tc.userID, "微信用户_test", "", "1234"))
		user, fresh, err := db.GetOrCreateWechatUser(context.Background(), tc.appID, tc.openID)
		if err != nil || fresh || user == nil || user.ID != tc.userID {
			t.Fatalf("lookup did not preserve the app scope and case: user=%#v, new=%t, error=%v", user, fresh, err)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestWechatRegistrationCommitsUserAndIdentityTogether(t *testing.T) {
	db, mock := mockWechatDB(t)
	expectWechatLookup(mock, 0)
	mock.ExpectBegin()
	expectWechatUserInsert(mock, 8)
	mock.ExpectExec("INSERT INTO `wechat_identities`").WithArgs("wx-test", "private-openid", int64(8), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	user, fresh, err := db.GetOrCreateWechatUser(context.Background(), "wx-test", "private-openid")
	if err != nil || !fresh || user == nil || user.ID != 8 || user.Password != "" {
		t.Fatalf("user=%#v, new=%t, error=%v", user, fresh, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentWechatLoginRollsBackExtraUserAndReadsWinner(t *testing.T) {
	db, mock := mockWechatDB(t)
	expectWechatLookup(mock, 0)
	mock.ExpectBegin()
	expectWechatUserInsert(mock, 9)
	mock.ExpectExec("INSERT INTO `wechat_identities`").WithArgs("wx-test", "private-openid", int64(9), sqlmock.AnyArg()).WillReturnError(&mysql.MySQLError{Number: 1062, Message: "duplicate identity"})
	mock.ExpectRollback()
	expectWechatLookup(mock, 8)
	user, fresh, err := db.GetOrCreateWechatUser(context.Background(), "wx-test", "private-openid")
	if err != nil || fresh || user == nil || user.ID != 8 {
		t.Fatalf("user=%#v, new=%t, error=%v", user, fresh, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestWechatInviteCollisionRetriesWholeTransaction(t *testing.T) {
	db, mock := mockWechatDB(t)
	expectWechatLookup(mock, 0)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `users`").WithArgs(generatedWechatUsername{}, "", generatedWechatCode{}, sqlmock.AnyArg()).WillReturnError(&mysql.MySQLError{Number: 1062, Message: "duplicate code"})
	mock.ExpectRollback()
	expectWechatLookup(mock, 0)
	mock.ExpectBegin()
	expectWechatUserInsert(mock, 10)
	mock.ExpectExec("INSERT INTO `wechat_identities`").WithArgs("wx-test", "private-openid", int64(10), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	user, fresh, err := db.GetOrCreateWechatUser(context.Background(), "wx-test", "private-openid")
	if err != nil || !fresh || user == nil || user.ID != 10 {
		t.Fatalf("user=%#v, new=%t, error=%v", user, fresh, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestWechatIdentityWriteFailureRollsBackNewUser(t *testing.T) {
	db, mock := mockWechatDB(t)
	expectWechatLookup(mock, 0)
	mock.ExpectBegin()
	expectWechatUserInsert(mock, 11)
	want := errors.New("identity write failed")
	mock.ExpectExec("INSERT INTO `wechat_identities`").WillReturnError(want)
	mock.ExpectRollback()
	user, fresh, err := db.GetOrCreateWechatUser(context.Background(), "wx-test", "private-openid")
	if !errors.Is(err, want) || fresh || user != nil {
		t.Fatalf("user=%#v, new=%t, error=%v", user, fresh, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestWechatLoginWithoutDatabaseAndInvalidIdentity(t *testing.T) {
	var db *DB
	if _, _, err := db.GetOrCreateWechatUser(context.Background(), "wx-test", "private-openid"); !errors.Is(err, errNoDB) {
		t.Fatal("nil database must fail safely")
	}
	db, mock := mockWechatDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, _, err := db.GetOrCreateWechatUser(ctx, "", "private-openid"); err == nil {
		t.Fatal("empty appid must not create an identity")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
