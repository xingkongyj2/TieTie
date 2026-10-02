// Package dbop 是数据访问层：日常读写基于 GORM，旧表结构迁移使用 SQL。
// 每张表一个文件，模型对象与它的增删改查放在一起；
// 跨多张表的查询逻辑放公共文件（db.go / 需要时另建 query.go）。
package dbop

import (
	"errors"
	"log"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// DB 是数据库句柄。
type DB struct {
	gdb *gorm.DB
}

// Open 打开 SQLite 数据库并自动迁移全部表结构。
// dsn 形如 "tietie.db"（相对运行目录）或绝对路径。
func Open(dsn string) (*DB, error) {
	gdb, err := gorm.Open(
		sqlite.Open("file:"+dsn+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"),
		&gorm.Config{Logger: logger.New(
			log.Default(),
			logger.Config{
				SlowThreshold:             500 * time.Millisecond,
				LogLevel:                  logger.Warn,
				IgnoreRecordNotFoundError: true, // 查无记录是正常业务分支，不打错误日志
				Colorful:                  false,
				ParameterizedQueries:      true, // 不把密码、聊天正文等 SQL 参数写入日志
			},
		)},
	)
	if err != nil {
		return nil, err
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		return nil, err
	}
	// SQLite 单写者：限制单连接，避免 SQLITE_BUSY。
	sqlDB.SetMaxOpenConns(1)

	if err := migrateLegacyUsers(gdb); err != nil {
		return nil, err
	}
	if err := migrateLegacyPasswords(gdb); err != nil {
		return nil, err
	}
	needsTaskBackfill := !gdb.Migrator().HasColumn(&Reminder{}, "task_status")
	needsMemoryKindBackfill := !gdb.Migrator().HasColumn(&MemoryRecord{}, "kind")
	needsCloudMemoryBackfill := !gdb.Migrator().HasTable(&MemoryRecord{})
	needsMemoryBackfill := !gdb.Migrator().HasTable(&ReminderMemory{})
	if err := gdb.AutoMigrate(
		&User{},
		&UserProfile{},
		&CareMode{},
		&CareReport{},
		&CarePreference{},
		&ProfileActionReceipt{},
		&Countdown{},
		&CountdownReceipt{},
		&Anniversary{},
		&AnniversaryActionReceipt{},
		&AnniversaryDeletionReceipt{},
		&Binding{},
		&Session{},
		&ConversationProtocol{},
		&Message{},
		&File{},
		&Reminder{},
		&ReminderActionReceipt{},
		&ReminderMemory{},
		&ReminderHistoryLocation{},
		&ReminderHistoryMonth{},
		&SpaceMemoryStore{},
		&Impression{},
		&PrivateChannel{},
		&MemoryRecord{},
		&MemoryPageLocation{},
		&MemoryPage{},
		&MemoryRevision{},
		&ControlJob{},
		&MemoryOperationReceipt{},
	); err != nil {
		return nil, err
	}
	if err := gdb.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_anniversary_one_pin ON anniversaries(session_id) WHERE pinned=1").Error; err != nil {
		return nil, err
	}
	if err := gdb.Exec("CREATE INDEX IF NOT EXISTS idx_reminders_daily ON reminders(session_id, status, due_at)").Error; err != nil {
		return nil, err
	}
	if needsMemoryKindBackfill {
		if err := gdb.Exec("UPDATE memory_records SET kind='reminder' WHERE path LIKE 'shared/reminders/%'").Error; err != nil {
			return nil, err
		}
	}
	if err := enforceUniqueBindings(gdb); err != nil {
		return nil, err
	}
	// Backfill a previous reminder schema without changing original due times.
	if err := gdb.Exec(`UPDATE reminders SET run_at = CASE WHEN next_attempt_at > due_at THEN next_attempt_at ELSE due_at END
WHERE run_at IS NULL OR run_at = '' OR run_at < '0002-01-01'`).Error; err != nil {
		return nil, err
	}
	if err := gdb.Exec(`UPDATE sessions SET next_sync_at = ? WHERE conversation_pending = 1 AND next_sync_at IS NULL`, time.Now().UTC()).Error; err != nil {
		return nil, err
	}
	if err := gdb.Exec(`UPDATE sessions SET conversation_version = 0 WHERE conversation_version IS NULL`).Error; err != nil {
		return nil, err
	}
	// Backfill task state and factual memory for existing reminder installations.
	if err := gdb.Transaction(func(tx *gorm.DB) error {
		if needsTaskBackfill {
			if err := tx.Exec(`UPDATE reminders SET task_status = CASE status WHEN 'delivered' THEN 'completed' WHEN 'dispatching' THEN 'running' WHEN 'completed' THEN CASE WHEN delivered_at IS NULL THEN 'cancelled' ELSE 'completed' END WHEN 'cancelled' THEN CASE WHEN delivered_at IS NULL THEN 'cancelled' ELSE 'completed' END WHEN 'failed' THEN 'failed' WHEN 'uncertain' THEN 'uncertain' ELSE 'pending' END, task_completed_at = delivered_at`).Error; err != nil {
				return err
			}
		}
		if !needsMemoryBackfill && !needsCloudMemoryBackfill {
			return nil
		}
		var rows []Reminder
		// Only missing memories require migration; ordinary startups do not rewrite history.
		query := tx.Model(&Reminder{})
		if !needsCloudMemoryBackfill {
			query = query.Where("id NOT IN (SELECT reminder_id FROM reminder_memories)")
		}
		if err := query.Find(&rows).Error; err != nil {
			return err
		}
		for _, r := range rows {
			if err := syncReminderMemory(tx, &r); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if err := migrateReminderHistory(gdb); err != nil {
		return nil, err
	}
	if err := migrateTemplateSchemas(gdb); err != nil {
		return nil, err
	}
	if err := reconcileDeletedAnniversaries(gdb); err != nil {
		return nil, err
	}
	return &DB{gdb: gdb}, nil
}

// Close 关闭数据库。
func (db *DB) Close() error {
	if db == nil || db.gdb == nil {
		return nil
	}
	sqlDB, err := db.gdb.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// enabled 报告数据库是否可用（nil 安全）。
func (db *DB) enabled() bool { return db != nil && db.gdb != nil }

// firstOrNil 把 GORM 的"记录不存在"统一转成 (nil, nil)。
func firstOrNil[T any](v *T, err error) (*T, error) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return v, nil
}

var errNoDB = errors.New("数据库未启用")
