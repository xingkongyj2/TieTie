package dbop

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"gorm.io/gorm/clause"
)

// File 是云端文件上传记录（files 表），sha256 用于秒传去重。
type File struct {
	ID          uint64    `json:"id"          gorm:"column:id;primaryKey;autoIncrement"`
	QoderFileID string    `json:"qoderFileId" gorm:"column:qoder_file_id;uniqueIndex;size:160;not null"`
	SessionID   string    `json:"sessionId"   gorm:"column:session_id;size:160;not null;default:''"`
	Name        string    `json:"name"        gorm:"column:name;size:255;not null;default:''"`
	MIME        string    `json:"mime"        gorm:"column:mime;size:128;not null;default:''"`
	Size        int       `json:"size"        gorm:"column:size;not null;default:0"`
	SHA256      string    `json:"sha256"      gorm:"column:sha256;size:64;not null;default:'';index"`
	CreatedAt   time.Time `json:"createdAt"   gorm:"column:created_at;autoCreateTime"`
}

// TableName 指定表名。
func (File) TableName() string { return "files" }

// SaveFileRecord 记录一次云端文件上传（自动计算内容哈希；qoder_file_id 冲突时刷新会话归属）。
func (db *DB) SaveFileRecord(ctx context.Context, qoderFileID, sessionID, name, mime string, content []byte) error {
	if !db.enabled() {
		return errNoDB
	}
	sum := sha256.Sum256(content)
	record := &File{
		QoderFileID: qoderFileID,
		SessionID:   sessionID,
		Name:        name,
		MIME:        mime,
		Size:        len(content),
		SHA256:      hex.EncodeToString(sum[:]),
	}
	return db.gdb.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "qoder_file_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"session_id"}),
		}).
		Create(record).Error
}

// FindFileBySHA256 按内容哈希查找已上传过的文件记录（秒传去重）；没有则 (nil, nil)。
func (db *DB) FindFileBySHA256(ctx context.Context, content []byte) (*File, error) {
	if !db.enabled() {
		return nil, errNoDB
	}
	sum := sha256.Sum256(content)
	var f File
	err := db.gdb.WithContext(ctx).
		Where(map[string]any{"sha256": hex.EncodeToString(sum[:])}).
		Order("id DESC").
		First(&f).Error
	return firstOrNil(&f, err)
}
