package dbop

import (
	"context"
	"database/sql/driver"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"tietie/backend/internal/regions"
)

type avatarReferenceArg struct{}

func (avatarReferenceArg) Match(v driver.Value) bool {
	s, ok := v.(string)
	return ok && regexp.MustCompile(`^/api/assets/avatars/[0-9a-f]{32}$`).MatchString(s)
}

func profileRowsForAvatar() *sqlmock.Rows {
	region, _ := regions.ResolveNames("湖北省", "武汉市", "洪山区")
	return sqlmock.NewRows([]string{"user_id", "name", "gender", "birthday", "hobbies", "bio", "avatar", "region_province_code", "region_province", "region_city_code", "region_city", "region_district_code", "region_district", "region_code_system", "region_source_user_id"}).AddRow(8, "之前的名字", "female", "2000-01-02", `["散步"]`, "原来的个人简介", "/avatars/cream-cat.png", region.ProvinceCode, region.Province, region.CityCode, region.City, region.DistrictCode, region.District, region.CodeSystem, 9)
}

func TestSaveWechatChosenProfilePreservesOtherFieldsAtomically(t *testing.T) {
	for _, failProfile := range []bool{false, true} {
		db, mock := mockWechatDB(t)
		region, err := regions.ResolveNames("湖北省", "武汉市", "洪山区")
		if err != nil {
			t.Fatal(err)
		}
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT .* FROM `users`.*FOR UPDATE").WithArgs(int64(8), 1).WillReturnRows(sqlmock.NewRows([]string{"id", "username"}).AddRow(8, "account-name"))
		mock.ExpectQuery("SELECT .* FROM `user_profiles`").WithArgs(int64(8), 1).WillReturnRows(profileRowsForAvatar())
		mock.ExpectExec("INSERT INTO `user_avatars`").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec("SAVEPOINT .*").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectQuery("SELECT .* FROM `users`").WithArgs(int64(8), 1).WillReturnRows(sqlmock.NewRows([]string{"id", "username"}).AddRow(8, "account-name"))
		mock.ExpectQuery("SELECT .* FROM `user_profiles`").WithArgs(int64(8), 1).WillReturnRows(profileRowsForAvatar())
		save := mock.ExpectExec("INSERT INTO `user_profiles`").WithArgs("新昵称", "female", "2000-01-02", `["散步"]`, "原来的个人简介", avatarReferenceArg{}, region.ProvinceCode, region.Province, region.CityCode, region.City, region.DistrictCode, region.District, region.CodeSystem, int64(9), sqlmock.AnyArg(), int64(8), sqlmock.AnyArg())
		if failProfile {
			save.WillReturnError(errors.New("profile unavailable"))
			mock.ExpectExec("ROLLBACK TO SAVEPOINT .*").WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectRollback()
		} else {
			save.WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectQuery("SELECT .* FROM `bindings`").WithArgs(int64(8), int64(8), 1).WillReturnRows(sqlmock.NewRows([]string{"user_a", "user_b"}))
			mock.ExpectCommit()
		}
		err = db.SaveWechatChosenProfile(context.Background(), 8, "新昵称", "image/jpeg", []byte("image"))
		if (err != nil) != failProfile {
			t.Fatalf("profile save fail=%v error=%v", failProfile, err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestWechatProfileImageFailureRollsBack(t *testing.T) {
	db, mock := mockWechatDB(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `users`.*FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"id", "username"}).AddRow(8, "account-name"))
	mock.ExpectQuery("SELECT .* FROM `user_profiles`").WillReturnRows(profileRowsForAvatar())
	mock.ExpectExec("INSERT INTO `user_avatars`").WillReturnError(errors.New("storage unavailable"))
	mock.ExpectRollback()
	if err := db.SaveWechatChosenProfile(context.Background(), 8, "新昵称", "image/jpeg", []byte("image")); err == nil {
		t.Fatal("failed image insertion must fail profile persistence")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAvatarContentSurvivesDatabaseRoundTrip(t *testing.T) {
	db, mock := mockWechatDB(t)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `user_avatars`").WithArgs(sqlmock.AnyArg(), int64(8), "image/jpeg", []byte("stored bytes"), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	avatar, err := db.SaveUserAvatar(context.Background(), 8, "image/jpeg", []byte("stored bytes"))
	if err != nil || !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(avatar.ID) {
		t.Fatalf("invalid stored avatar: %#v %v", avatar, err)
	}
	mock.ExpectQuery("SELECT .* FROM `user_avatars`").WithArgs(avatar.ID, 1).WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "mime_type", "content"}).AddRow(avatar.ID, 8, "image/jpeg", []byte("stored bytes")))
	loaded, err := db.GetUserAvatar(context.Background(), avatar.ID)
	if err != nil || loaded.UserID != 8 || string(loaded.Content) != "stored bytes" {
		t.Fatalf("invalid loaded avatar: %#v %v", loaded, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
