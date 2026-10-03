package dbop

import (
	"context"
	"gorm.io/gorm"
	"time"
)

// Each binding epoch has independent cloud history and memory per private owner.
// No private data is ever sent to the shared agent before an explicit due event.
type PrivateChannel struct {
	SessionID        string    `gorm:"primaryKey;size:160"`
	SpaceID          string    `gorm:"not null;uniqueIndex:idx_private_owner,priority:1;size:160"`
	OwnerID          int64     `gorm:"not null;uniqueIndex:idx_private_owner,priority:2"`
	BindingCreatedAt time.Time `gorm:"not null;uniqueIndex:idx_private_owner,priority:3;type:datetime(6)"`
	CreatedAt        time.Time `gorm:"type:datetime(6)"`
}

func (PrivateChannel) TableName() string { return "private_channels" }
func (db *DB) GetPrivateChannel(ctx context.Context, id string) (*PrivateChannel, error) {
	var row PrivateChannel
	err := db.gdb.WithContext(ctx).Where("session_id=?", id).First(&row).Error
	return firstOrNil(&row, err)
}
func (db *DB) PrivateChannelForOwner(ctx context.Context, space string, owner int64, epoch time.Time) (*PrivateChannel, error) {
	var row PrivateChannel
	err := db.gdb.WithContext(ctx).Where("space_id=? AND owner_id=? AND binding_created_at=?", space, owner, epoch).First(&row).Error
	return firstOrNil(&row, err)
}
func (db *DB) SavePrivateChannel(ctx context.Context, row PrivateChannel) error {
	return db.gdb.WithContext(ctx).Create(&row).Error
}
func (db *DB) CreateInitializedPrivateChannel(ctx context.Context, row PrivateChannel, store SpaceMemoryStore, records ...MemoryRecord) error {
	return db.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		if err := tx.Create(&store).Error; err != nil {
			return err
		}
		if len(records) > 0 {
			if err := tx.Create(&records).Error; err != nil {
				return err
			}
		}
		binding, err := bindingForSession(tx, row.SessionID)
		if err != nil || binding == nil {
			return err
		}
		if err := seedUserProfiles(tx, *binding); err != nil {
			return err
		}
		return seedAssistantStyle(tx, row)
	})
}

func bindingForSession(tx *gorm.DB, id string) (*Binding, error) {
	var binding Binding
	err := tx.Where("session_id=?", id).First(&binding).Error
	if err != gorm.ErrRecordNotFound {
		return firstOrNil(&binding, err)
	}
	err = tx.Table("private_channels AS p").Select("b.user_a,b.user_b,p.session_id,b.created_at").Joins("JOIN bindings b ON b.session_id=p.space_id AND b.created_at=p.binding_created_at").Where("p.session_id=?", id).Take(&binding).Error
	return firstOrNil(&binding, err)
}

// Used only for trusted clock events, never as a public cross-channel lookup.
func (db *DB) GetDispatchReminder(ctx context.Context, delivery, id string) (*Reminder, error) {
	var row Reminder
	err := db.gdb.WithContext(ctx).Where("id=? AND (session_id=? OR delivery_session_id=?)", id, delivery, delivery).First(&row).Error
	return firstOrNil(&row, err)
}
func (r Reminder) DeliverySession() string {
	if r.DeliverySessionID != "" {
		return r.DeliverySessionID
	}
	return r.SessionID
}
func (r Reminder) MemorySession() string {
	if r.MemorySessionID != "" {
		return r.MemorySessionID
	}
	return r.SessionID
}

// The public board includes only this owner's private plans. Once an actual
// reminder is delivered its row moves to the shared channel, without its origin.
func (db *DB) ListVisibleReminders(ctx context.Context, space string, owner int64) ([]Reminder, error) {
	rows, err := db.ListReminders(ctx, space)
	if err != nil {
		return nil, err
	}
	binding, err := db.GetBindingBySessionID(ctx, space)
	if err != nil || binding == nil {
		return rows, err
	}
	private, err := db.PrivateChannelForOwner(ctx, space, owner, binding.CreatedAt)
	if err != nil {
		return nil, err
	}
	if private != nil {
		extra, e := db.ListReminders(ctx, private.SessionID)
		if e != nil {
			return nil, e
		}
		rows = append(rows, extra...)
	}
	for i := range rows {
		rows[i].SessionID = space
		if rows[i].MemorySessionID != "" {
			rows[i].SourceEventID = ""
		}
	}
	return rows, nil
}
func (db *DB) VisibleReminder(ctx context.Context, space, id string, owner int64) (*Reminder, error) {
	row, err := db.GetReminder(ctx, space, id)
	if err != nil || row != nil {
		return row, err
	}
	binding, err := db.GetBindingBySessionID(ctx, space)
	if err != nil || binding == nil {
		return nil, err
	}
	private, err := db.PrivateChannelForOwner(ctx, space, owner, binding.CreatedAt)
	if err != nil || private == nil {
		return nil, err
	}
	return db.GetReminder(ctx, private.SessionID, id)
}
