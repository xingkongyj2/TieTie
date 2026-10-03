package dbop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"strings"
	"tietie/backend/internal/memoryspace"
	"tietie/backend/internal/regions"
	"tietie/backend/internal/weather"
	"time"
)

type CarePreference struct {
	SessionID        string    `json:"-" gorm:"primaryKey;size:160"`
	UserID           int64     `json:"userId" gorm:"primaryKey"`
	Metrics          []string  `json:"metrics" gorm:"serializer:json;type:mediumtext"`
	UpdatedBy        int64     `json:"updatedBy"`
	UpdatedAt        time.Time `json:"updatedAt" gorm:"type:datetime(6)"`
	BindingCreatedAt time.Time `json:"-" gorm:"type:datetime(6)"`
}
type ProfileActionReceipt struct {
	ID        string    `gorm:"primaryKey;size:64"`
	Snapshot  string    `gorm:"type:mediumtext"`
	CreatedAt time.Time `gorm:"type:datetime(6)"`
}

func profileActorBinding(tx *gorm.DB, session string, actor, target int64, epoch time.Time) (*Binding, error) {
	b, err := bindingForSession(tx, session)
	if err != nil {
		return nil, err
	}
	if b == nil || (!epoch.IsZero() && !b.CreatedAt.Equal(epoch)) || (actor != b.UserA && actor != b.UserB) || (target != b.UserA && target != b.UserB) {
		return nil, ErrReminderForbidden
	}
	return b, nil
}

// Region changes preserve the target's other fields, authorize both identities,
// and commit the profile and durable memory outbox in the same transaction.
func (db *DB) ApplyProfileRegion(ctx context.Context, session, request string, actor, target int64, epoch time.Time, region regions.Location) (*UserProfile, error) {
	canonical, err := regions.Resolve(region.ProvinceCode, region.CityCode, region.DistrictCode)
	if err != nil {
		return nil, err
	}
	var row UserProfile
	err = db.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		b, err := profileActorBinding(tx, session, actor, target, epoch)
		if err != nil {
			return err
		}
		receiptID := "region_" + ControlID(session, request+b.CreatedAt.String())
		var receipt ProfileActionReceipt
		if err := tx.Where("id=?", receiptID).First(&receipt).Error; err == nil {
			return json.Unmarshal([]byte(receipt.Snapshot), &row)
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := tx.Where("user_id=?", target).First(&row).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			row = UserProfile{UserID: target, Gender: "unspecified", Hobbies: []string{}}
		} else if err != nil {
			return err
		}
		if row.Region != canonical || row.UpdatedAt.IsZero() {
			row.Region, row.RegionSourceUserID = canonical, actor
			if err := tx.Save(&row).Error; err != nil {
				return err
			}
			var user User
			if err := tx.First(&user, target).Error; err != nil {
				return err
			}
			var main Binding
			if err := tx.Where("user_a=? OR user_b=?", target, target).First(&main).Error; err != nil {
				return err
			}
			if err := syncUserProfile(tx, main, user, row); err != nil {
				return err
			}
			var channels []PrivateChannel
			if err := tx.Where("space_id=? AND binding_created_at=?", main.SessionID, main.CreatedAt).Find(&channels).Error; err != nil {
				return err
			}
			for _, channel := range channels {
				private := main
				private.SessionID = channel.SessionID
				if err := syncUserProfile(tx, private, user, row); err != nil {
					return err
				}
			}
		}
		body, _ := json.Marshal(row)
		return tx.Create(&ProfileActionReceipt{ID: receiptID, Snapshot: string(body)}).Error
	})
	return &row, err
}

func CarePreferenceMemoryPath(user int64) string {
	return memoryspace.FactPath("habit", "space", 0, fmt.Sprintf("weather_preferences_%d", user))
}
func (db *DB) ApplyCarePreference(ctx context.Context, session, request string, actor, target int64, epoch time.Time, metrics []string) (*CarePreference, error) {
	normalized, err := weather.NormalizeMetrics(metrics)
	if err != nil {
		return nil, err
	}
	var row CarePreference
	err = db.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		b, err := profileActorBinding(tx, session, actor, target, epoch)
		if err != nil {
			return err
		}
		receiptID := "weather_pref_" + ControlID(session, request+b.CreatedAt.String())
		var receipt ProfileActionReceipt
		if err := tx.Where("id=?", receiptID).First(&receipt).Error; err == nil {
			return json.Unmarshal([]byte(receipt.Snapshot), &row)
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var old CarePreference
		if err := tx.Where("session_id=? AND user_id=?", session, target).First(&old).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		row = CarePreference{SessionID: session, UserID: target, Metrics: normalized, UpdatedBy: actor, BindingCreatedAt: b.CreatedAt}
		if old.BindingCreatedAt.Equal(b.CreatedAt) && strings.Join(old.Metrics, ",") == strings.Join(normalized, ",") {
			row = old
		} else {
			if err := tx.Save(&row).Error; err != nil {
				return err
			}
			body, _ := json.Marshal(map[string]any{"entryType": "weather_preferences", "content": "该成员天气推送仅关注：" + strings.Join(weather.MetricLabels(normalized), "、"), "targetUserId": target, "sourceUserId": actor, "metrics": normalized, "confirmation": "用户明确指定", "updatedAt": row.UpdatedAt})
			if err := enqueueMemory(tx, MemoryRecord{SessionID: session, Path: CarePreferenceMemoryPath(target), Scope: "space", Category: "habit", SourceUserID: actor, Storage: "database_and_memory", Operation: "upsert", PendingContent: string(body), BindingCreatedAt: b.CreatedAt}); err != nil {
				return err
			}
		}
		body, _ := json.Marshal(row)
		return tx.Create(&ProfileActionReceipt{ID: receiptID, Snapshot: string(body)}).Error
	})
	return &row, err
}

// Enabling is a shared action; explain both members' current profile status in
// the group. Repeated clicks within five minutes do not flood the conversation.
func (db *DB) NotifyCareRegionMissing(ctx context.Context, session string, actor int64, mode string, now time.Time) error {
	return db.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		b, err := profileActorBinding(tx, session, actor, actor, time.Time{})
		if err != nil {
			return err
		}
		members, err := careMembers(tx, *b)
		if err != nil {
			return err
		}
		lines, fingerprint := []string{}, ""
		missing := false
		for _, m := range members {
			fingerprint += fmt.Sprintf("%d:%s:%s;", m.UserID, m.Region.CityCode, m.Region.DistrictCode)
			if m.Region.CityCode == "" {
				missing = true
				lines = append(lines, "@"+m.Name+" 还没有填写地区")
			} else {
				lines = append(lines, "@"+m.Name+" 已填写："+m.Region.Province+" "+m.Region.City+" "+m.Region.District)
			}
		}
		if !missing {
			return nil
		}
		label := "早安"
		if mode == "night" {
			label = "晚安"
		}
		text := strings.Join(lines, "\n") + "\n\n" + label + "提醒暂未开启。可以在「我的 → 关于我」填写，也可以直接告诉我：‘我在湖北武汉洪山区’或‘TA搬到浙江杭州西湖区了’。任意一方都能补充双方地区，填好后再开启就可以啦。"
		id := "evt_care_" + ControlID(session, "region_missing"+mode+fingerprint+b.CreatedAt.String()+fmt.Sprint(now.Unix()/300))
		report := CareReport{ID: id, SessionID: session, Mode: "region_notice", Date: now.In(weather.Shanghai).Format("2006-01-02"), Text: text, Cards: []weather.Card{}, CreatedAt: now.UTC(), BindingCreatedAt: b.CreatedAt}
		var count int64
		if err := tx.Model(&CareReport{}).Where("id=?", id).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return nil
		}
		return tx.Create(&report).Error
	})
}

func (db *DB) GetCarePreference(ctx context.Context, session string, user int64) ([]string, error) {
	b, err := bindingForSession(db.gdb.WithContext(ctx), session)
	if err != nil {
		return nil, err
	}
	if b == nil || (user != b.UserA && user != b.UserB) {
		return nil, ErrReminderForbidden
	}
	var row CarePreference
	if err := db.gdb.WithContext(ctx).Where("session_id=? AND user_id=? AND binding_created_at=?", session, user, b.CreatedAt).First(&row).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return weather.NormalizeMetrics(row.Metrics)
}
