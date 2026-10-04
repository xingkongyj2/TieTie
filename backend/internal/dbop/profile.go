package dbop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"tietie/backend/internal/memoryspace"
	"tietie/backend/internal/regions"
)

// Account profiles survive unbinding. Each space receives its own snapshot and
// corrections; old spaces are never silently rewritten after a new binding.
type UserProfile struct {
	UserID             int64            `json:"userId" gorm:"primaryKey"`
	Name               string           `json:"name" gorm:"size:24"`
	Gender             string           `json:"gender"`
	Birthday           string           `json:"birthday"`
	Hobbies            []string         `json:"hobbies" gorm:"serializer:json;type:mediumtext"`
	Bio                string           `json:"bio"`
	Avatar             string           `json:"avatar"`
	Region             regions.Location `json:"region" gorm:"embedded;embeddedPrefix:region_"`
	RegionSourceUserID int64            `json:"regionSourceUserId,omitempty"`
	UpdatedAt          time.Time        `json:"updatedAt" gorm:"type:datetime(6)"`
}

func (p UserProfile) Fields() map[string]any {
	var region any
	if p.Region.CityCode != "" {
		region = p.Region
	}
	return map[string]any{"name": p.Name, "gender": p.Gender, "birthday": p.Birthday, "hobbies": p.Hobbies, "bio": p.Bio, "avatar": p.Avatar, "region": region, "regionSourceUserId": p.RegionSourceUserID}
}

func (db *DB) GetUserProfile(ctx context.Context, userID int64) (*UserProfile, error) {
	var p UserProfile
	err := db.gdb.WithContext(ctx).Where("user_id=?", userID).First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &UserProfile{UserID: userID, Gender: "unspecified", Hobbies: []string{}}, nil
	}
	return &p, err
}

// At most one root and two account facts, independent of conversation history.
func (db *DB) ProfileMemoryRecords(ctx context.Context, session string, userIDs ...int64) ([]MemoryRecord, error) {
	ids := []string{MemoryID(session, "profile/users.json")}
	for _, id := range userIDs {
		ids = append(ids, MemoryID(session, memoryspace.FactPath("profile", "self", id, "account_profile")))
	}
	var records []MemoryRecord
	err := db.gdb.WithContext(ctx).Where("id IN ? AND operation='upsert' AND state!='deleted'", ids).Order("kind DESC, id ASC").Find(&records).Error
	return records, err
}

// Returns the actual current binding, not a caller-provided session or identity.
func (db *DB) SaveUserProfile(ctx context.Context, profile UserProfile, preserveRegion ...bool) (string, error) {
	session := ""
	err := db.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user User
		if err := tx.Where("id=?", profile.UserID).First(&user).Error; err != nil {
			return err
		}
		var previous UserProfile
		lookup := tx.Where("user_id=?", profile.UserID).First(&previous).Error
		if lookup != nil && !errors.Is(lookup, gorm.ErrRecordNotFound) {
			return lookup
		}
		if profile.Name == "" {
			profile.Name = previous.Name
			if profile.Name == "" {
				profile.Name = user.Username
			}
		}
		if len(preserveRegion) > 0 && preserveRegion[0] {
			profile.Region = previous.Region
		}
		canonical, err := regions.Resolve(profile.Region.ProvinceCode, profile.Region.CityCode, profile.Region.DistrictCode)
		if err != nil {
			return err
		}
		profile.Region = canonical
		if profile.Region != previous.Region {
			profile.RegionSourceUserID = profile.UserID
		} else {
			profile.RegionSourceUserID = previous.RegionSourceUserID
		}
		if lookup == nil && reflect.DeepEqual(previous.Fields(), profile.Fields()) {
			profile.UpdatedAt = previous.UpdatedAt
		} else {
			if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&profile).Error; err != nil {
				return err
			}
		}
		var binding Binding
		if err := tx.Where("user_a=? OR user_b=?", profile.UserID, profile.UserID).First(&binding).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		} else if err != nil {
			return err
		}
		session = binding.SessionID
		if err := syncUserProfile(tx, binding, user, profile); err != nil {
			return err
		}
		var channels []PrivateChannel
		if err := tx.Where("space_id=? AND binding_created_at=?", binding.SessionID, binding.CreatedAt).Find(&channels).Error; err != nil {
			return err
		}
		for _, channel := range channels {
			privateBinding := binding
			privateBinding.SessionID = channel.SessionID
			if err := syncUserProfile(tx, privateBinding, user, profile); err != nil {
				return err
			}
		}
		return nil
	})
	return session, err
}

func syncUserProfile(tx *gorm.DB, binding Binding, user User, profile UserProfile) error {
	displayName := profile.Name
	if displayName == "" {
		displayName = user.Username
	}
	path := "profile/users.json"
	var root MemoryRecord
	err := tx.Where("id=?", MemoryID(binding.SessionID, path)).First(&root).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		var members []User
		if err := tx.Where("id IN ?", []int64{binding.UserA, binding.UserB}).Find(&members).Error; err != nil {
			return err
		}
		if len(members) != 2 {
			return errors.New("missing profile members")
		}
		docs, err := memoryspace.Render(binding.SessionID, "", memoryspace.Member{ID: members[0].ID, Name: members[0].Username}, memoryspace.Member{ID: members[1].ID, Name: members[1].Username})
		if err != nil {
			return err
		}
		for _, doc := range docs {
			if doc.Path == path {
				root.Content = doc.Content
			}
		}
	} else if err != nil {
		return err
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(root.Content), &doc); err != nil {
		return err
	}
	users, ok := doc["users"].([]any)
	if !ok {
		return errors.New("invalid profile template")
	}
	for _, item := range users {
		member := item.(map[string]any)
		if member["userId"] != float64(user.ID) {
			continue
		}
		member["profileSource"], member["profileUpdatedAt"] = "self_profile", profile.UpdatedAt
		for key, value := range profile.Fields() {
			member[key] = value
		}
		member["name"] = displayName
	}
	body, _ := json.Marshal(doc)
	if err := queueHistoryDocument(tx, binding.SessionID, path, "template", string(body), binding.CreatedAt, false); err != nil {
		return err
	}
	data := profile.Fields()
	data["name"] = displayName
	fact, _ := json.Marshal(map[string]any{"content": fmt.Sprintf("%s在我的小档案中保存的当前个人资料。", displayName), "data": data, "sourceType": "self_profile", "confirmation": "已确认", "ownerId": user.ID, "sourceUserId": user.ID, "updatedAt": profile.UpdatedAt})
	logicalPath := memoryspace.FactPath("profile", "self", user.ID, "account_profile")
	var existing MemoryRecord
	if err := tx.Where("id=?", MemoryID(binding.SessionID, logicalPath)).First(&existing).Error; err == nil && existing.Content == string(fact) {
		return nil
	} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	return enqueueMemory(tx, MemoryRecord{SessionID: binding.SessionID, Path: logicalPath, Scope: "self", OwnerID: user.ID, SourceUserID: user.ID, Category: "profile", Storage: "database_and_memory", PendingContent: string(fact), Operation: "upsert", BindingCreatedAt: binding.CreatedAt})
}

func seedUserProfiles(tx *gorm.DB, binding Binding) error {
	for _, userID := range []int64{binding.UserA, binding.UserB} {
		var profile UserProfile
		if err := tx.Where("user_id=?", userID).First(&profile).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		} else if err != nil {
			return err
		}
		var user User
		if err := tx.Where("id=?", userID).First(&user).Error; err != nil {
			return err
		}
		if err := syncUserProfile(tx, binding, user, profile); err != nil {
			return err
		}
	}
	return nil
}
