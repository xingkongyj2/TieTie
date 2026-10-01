package dbop

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

// migrateLegacyUsers 将旧的字符串用户 ID 和绑定键转换为自增整数。
// 旧密码哈希无法还原，先带入过渡列，后续迁移到独立表。
func migrateLegacyUsers(gdb *gorm.DB) error {
	if !gdb.Migrator().HasTable("users") {
		return nil
	}
	hasOldColumn, err := sqliteColumnExists(gdb, "users", "password_hash")
	if err != nil || !hasOldColumn {
		return err
	}
	return gdb.Transaction(func(tx *gorm.DB) error {
		var users []struct {
			ID           string
			Username     string
			PasswordHash string
			Code         string
			CreatedAt    time.Time
		}
		if err := tx.Raw("SELECT id, username, password_hash, code, created_at FROM users ORDER BY rowid").Scan(&users).Error; err != nil {
			return err
		}
		var bindings []struct {
			UserA     string
			UserB     string
			SessionID string
			CreatedAt time.Time
		}
		hasBindings := tx.Migrator().HasTable("bindings")
		if hasBindings {
			if err := tx.Raw("SELECT user_a, user_b, session_id, created_at FROM bindings").Scan(&bindings).Error; err != nil {
				return err
			}
		}

		if err := tx.Exec("CREATE TABLE users_migrating (id INTEGER PRIMARY KEY AUTOINCREMENT, username TEXT NOT NULL, password TEXT NOT NULL, legacy_password_hash TEXT, code TEXT NOT NULL, created_at DATETIME)").Error; err != nil {
			return err
		}
		idMap := make(map[string]int64, len(users))
		for i, user := range users {
			id := int64(i + 1)
			idMap[user.ID] = id
			if err := tx.Exec("INSERT INTO users_migrating (id, username, password, legacy_password_hash, code, created_at) VALUES (?, ?, '', ?, ?, ?)",
				id, user.Username, user.PasswordHash, user.Code, user.CreatedAt).Error; err != nil {
				return err
			}
		}
		if hasBindings {
			if err := tx.Exec("CREATE TABLE bindings_migrating (user_a INTEGER NOT NULL, user_b INTEGER NOT NULL, session_id TEXT NOT NULL, created_at DATETIME, PRIMARY KEY (user_a, user_b))").Error; err != nil {
				return err
			}
			for _, binding := range bindings {
				a, aOK := idMap[binding.UserA]
				b, bOK := idMap[binding.UserB]
				if !aOK || !bOK {
					return fmt.Errorf("binding references unknown user")
				}
				a, b = PairKey(a, b)
				if err := tx.Exec("INSERT INTO bindings_migrating (user_a, user_b, session_id, created_at) VALUES (?, ?, ?, ?)",
					a, b, binding.SessionID, binding.CreatedAt).Error; err != nil {
					return err
				}
			}
			if err := tx.Exec("DROP TABLE bindings").Error; err != nil {
				return err
			}
			if err := tx.Exec("ALTER TABLE bindings_migrating RENAME TO bindings").Error; err != nil {
				return err
			}
		}
		if err := tx.Exec("DROP TABLE users").Error; err != nil {
			return err
		}
		return tx.Exec("ALTER TABLE users_migrating RENAME TO users").Error
	})
}

// migrateLegacyPasswords 移除 users 的过渡列，同时保留尚未登录的旧账号校验能力。
func migrateLegacyPasswords(gdb *gorm.DB) error {
	return gdb.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("CREATE TABLE IF NOT EXISTS legacy_passwords (user_id INTEGER PRIMARY KEY, password_hash TEXT NOT NULL)").Error; err != nil {
			return err
		}
		if !tx.Migrator().HasTable("users") {
			return nil
		}
		hasColumn, err := sqliteColumnExists(tx, "users", "legacy_password_hash")
		if err != nil || !hasColumn {
			return err
		}
		if err := tx.Exec("INSERT OR IGNORE INTO legacy_passwords (user_id, password_hash) SELECT id, legacy_password_hash FROM users WHERE password = '' AND legacy_password_hash <> ''").Error; err != nil {
			return err
		}
		return tx.Exec("ALTER TABLE users DROP COLUMN legacy_password_hash").Error
	})
}

func sqliteColumnExists(gdb *gorm.DB, table, column string) (bool, error) {
	var count int64
	err := gdb.Raw("SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?", table, column).Scan(&count).Error
	return count > 0, err
}

// enforceUniqueBindings 保留每个用户最早的绑定，将旧的重复绑定归档，并安装数据库约束。
func enforceUniqueBindings(gdb *gorm.DB) error {
	return gdb.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("CREATE TABLE IF NOT EXISTS archived_bindings (user_a INTEGER NOT NULL, user_b INTEGER NOT NULL, session_id TEXT NOT NULL, created_at DATETIME, archived_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, PRIMARY KEY (user_a, user_b))").Error; err != nil {
			return err
		}
		var bindings []Binding
		if err := tx.Order("created_at ASC, user_a ASC, user_b ASC").Find(&bindings).Error; err != nil {
			return err
		}
		seen := make(map[int64]bool)
		for _, binding := range bindings {
			if seen[binding.UserA] || seen[binding.UserB] {
				if err := tx.Exec("INSERT OR IGNORE INTO archived_bindings (user_a, user_b, session_id, created_at) VALUES (?, ?, ?, ?)",
					binding.UserA, binding.UserB, binding.SessionID, binding.CreatedAt).Error; err != nil {
					return err
				}
				if err := tx.Where("user_a = ? AND user_b = ?", binding.UserA, binding.UserB).Delete(&Binding{}).Error; err != nil {
					return err
				}
				continue
			}
			seen[binding.UserA], seen[binding.UserB] = true, true
		}
		if err := tx.Exec(`CREATE TRIGGER IF NOT EXISTS bindings_one_partner_insert BEFORE INSERT ON bindings
WHEN EXISTS (SELECT 1 FROM bindings WHERE user_a IN (NEW.user_a, NEW.user_b) OR user_b IN (NEW.user_a, NEW.user_b))
BEGIN SELECT RAISE(ABORT, 'binding_user_already_bound'); END`).Error; err != nil {
			return err
		}
		return tx.Exec(`CREATE TRIGGER IF NOT EXISTS bindings_one_partner_update BEFORE UPDATE OF user_a, user_b ON bindings
WHEN EXISTS (SELECT 1 FROM bindings WHERE (user_a IN (NEW.user_a, NEW.user_b) OR user_b IN (NEW.user_a, NEW.user_b)) AND NOT (user_a = OLD.user_a AND user_b = OLD.user_b))
BEGIN SELECT RAISE(ABORT, 'binding_user_already_bound'); END`).Error
	})
}
