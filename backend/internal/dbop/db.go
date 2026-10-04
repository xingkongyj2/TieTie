// Package dbop 是数据访问层：日常读写基于 GORM，建库建表与索引补齐在 Open 里完成。
// 每张表一个文件，模型对象与它的增删改查放在一起；
// 跨多张表的查询逻辑放公共文件（db.go / 需要时另建 query.go）。
package dbop

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"regexp"
	"sync"
	"time"

	"github.com/go-sql-driver/mysql"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// DB 是数据库句柄。
type DB struct {
	gdb *gorm.DB
}

// MySQLConfig 是一组 MySQL 连接参数。
type MySQLConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	Database string
}

// Addr 返回 host:port，仅用于连接与日志。
func (c MySQLConfig) Addr() string { return fmt.Sprintf("%s:%d", c.Host, c.Port) }

// databasePattern 限制库名字符，DDL 里直接拼库名才是安全的。
var databasePattern = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)

// dsn 组装连接串；database 传空串表示只连服务器，用于建库。
// parseTime 让 DATETIME 还原成 time.Time，loc=UTC 与写入侧保持一致，
// 排序规则固定 utf8mb4_bin，保持原来 SQLite 的大小写敏感与二进制序比较。
func (c MySQLConfig) dsn(database string) string {
	cfg := mysql.NewConfig()
	cfg.Net = "tcp"
	cfg.Addr = c.Addr()
	cfg.User = c.User
	cfg.Passwd = c.Password
	cfg.DBName = database
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	cfg.Collation = "utf8mb4_bin" // 连接侧也走二进制序，LIKE 与范围比较才和 SQLite 一致
	cfg.Timeout = 10 * time.Second
	cfg.ReadTimeout = 30 * time.Second
	cfg.WriteTimeout = 30 * time.Second
	// 参数由驱动自己按 utf8mb4 转义后内联，省掉 PREPARE/EXECUTE/CLOSE 三次服务端往返。
	// 这台实例单条语句就要几百毫秒，省下往返直接缩短领取事务持锁的时间。
	cfg.InterpolateParams = true
	return cfg.FormatDSN()
}

// warmPool 提前把若干条连接建好放进空闲池：这台实例握手要 1–2 秒，
// 冷启动后第一个页面请求会串行付出好几次握手，预热后只剩查询本身的时间。
func warmPool(sqlDB *sql.DB, size int) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var group sync.WaitGroup
	for i := 0; i < size; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			conn, err := sqlDB.Conn(ctx)
			if err != nil {
				return
			}
			_ = conn.Close() // 归还到空闲池，不是关掉物理连接
		}()
	}
	group.Wait()
}

// ensureDatabase 建库（幂等），字符集与排序规则在建库时定死，后续建表继承库默认值。
func ensureDatabase(cfg MySQLConfig) error {
	db, err := sql.Open("mysql", cfg.dsn(""))
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.Exec("CREATE DATABASE IF NOT EXISTS `" + cfg.Database + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_bin"); err != nil {
		return fmt.Errorf("创建数据库 %s: %w", cfg.Database, err)
	}
	return nil
}

// Open 连接 MySQL，建库建表并补齐 AutoMigrate 表达不了的约束。
func Open(cfg MySQLConfig) (*DB, error) {
	if cfg.Host == "" {
		return nil, errors.New("未配置 MYSQL_HOST")
	}
	if !databasePattern.MatchString(cfg.Database) {
		return nil, fmt.Errorf("数据库名 %q 只能包含字母、数字和下划线", cfg.Database)
	}
	if err := ensureDatabase(cfg); err != nil {
		return nil, err
	}

	gdb, err := gorm.Open(
		gormmysql.Open(cfg.dsn(cfg.Database)),
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
	// 队列 worker 与 HTTP 处理共用连接池；上限压到 16，避免挤占同一实例上的其他库。
	// 这台远端实例新建一条连接要 1–2 秒，所以空闲连接一律留着复用：
	// 驱动默认开启的探活会在使用前发现被中间层掐掉的连接并自动重建，不需要靠主动丢弃来规避。
	sqlDB.SetMaxOpenConns(16)
	sqlDB.SetMaxIdleConns(16)
	sqlDB.SetConnMaxLifetime(time.Hour)
	warmPool(sqlDB, 8)

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
		&AnniversaryReminderSettings{},
		&AnniversaryActionReceipt{},
		&AnniversaryDeletionReceipt{},
		&Binding{},
		&ArchivedBinding{},
		&Session{},
		&ConversationProtocol{},
		&Message{},
		&File{},
		&Reminder{},
		&ReminderActionReceipt{},
		&ReminderDeletionReceipt{},
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
	if err := ensureSchemaExtras(gdb); err != nil {
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

// ensureSchemaExtras 补 AutoMigrate 表达不了的约束：
// 每日提醒的扫描索引，以及"每个空间最多一个置顶纪念日"。
// MySQL 没有部分索引，置顶唯一性用 stored 生成列 + 唯一索引实现：未置顶时生成为 NULL，NULL 不参与唯一约束。
func ensureSchemaExtras(gdb *gorm.DB) error {
	if err := ensureColumn(gdb, "anniversaries", "pinned_session_id",
		"ALTER TABLE anniversaries ADD COLUMN pinned_session_id VARCHAR(160) GENERATED ALWAYS AS (IF(pinned = 1, session_id, NULL)) STORED"); err != nil {
		return err
	}
	if err := ensureIndex(gdb, "anniversaries", "idx_anniversary_one_pin",
		"CREATE UNIQUE INDEX idx_anniversary_one_pin ON anniversaries (pinned_session_id)"); err != nil {
		return err
	}
	return ensureIndex(gdb, "reminders", "idx_reminders_daily",
		"CREATE INDEX idx_reminders_daily ON reminders (session_id, status, due_at)")
}

// ensureColumn 列不存在时执行 DDL。
func ensureColumn(gdb *gorm.DB, table, column, ddl string) error {
	var count int64
	err := gdb.Raw(`SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = ? AND column_name = ?`, table, column).Scan(&count).Error
	if err != nil || count > 0 {
		return err
	}
	return gdb.Exec(ddl).Error
}

// ensureIndex 索引不存在时执行 DDL。
func ensureIndex(gdb *gorm.DB, table, index, ddl string) error {
	var count int64
	err := gdb.Raw(`SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = ? AND index_name = ?`, table, index).Scan(&count).Error
	if err != nil || count > 0 {
		return err
	}
	return gdb.Exec(ddl).Error
}

// 各类后台队列的领取锁名。MySQL 的 advisory lock 是连接级的，名字只要全局唯一即可。
const (
	claimLockReminders            = "tietie_claim_reminders"
	claimLockControls             = "tietie_claim_controls"
	claimLockSessionSync          = "tietie_claim_session_sync"
	claimLockMemorySync           = "tietie_claim_memory_sync"
	claimLockImpressions          = "tietie_claim_impressions"
	claimLockCareModes            = "tietie_claim_care_modes"
	claimLockAnniversaryReminders = "tietie_claim_anniversary_reminders"
)

// claimWithLock 串行化"挑候选 + 改状态"这段领取动作。
// SQLite 时代靠单连接让每条 UPDATE...RETURNING 天然原子；换成连接池后，
// 领取条件往往依赖同空间其他行的状态（例如一个空间最多一条投递中），
// 两个并发领取各自只看到已提交的旧状态，就会同时领走同一空间的两条任务。
// advisory lock 是连接级的，所以另借一条连接当门闩：领取事务提交之后再释放，
// 否则下一个持有者会读到提交前的状态，等于没锁。领取完立刻提交，投递与云端调用都在锁外并发执行。
func claimWithLock(ctx context.Context, gdb *gorm.DB, lock string, claim func(tx *gorm.DB) error) error {
	sqlDB, err := gdb.DB()
	if err != nil {
		return err
	}
	gate, err := sqlDB.Conn(ctx)
	if err != nil {
		return err
	}
	defer gate.Close()
	var granted int
	if err := gate.QueryRowContext(ctx, "SELECT GET_LOCK(?, 10)", lock).Scan(&granted); err != nil {
		return err
	}
	if granted != 1 {
		return fmt.Errorf("等待领取锁 %s 超时", lock)
	}
	defer func() {
		var released int
		_ = gate.QueryRowContext(ctx, "SELECT RELEASE_LOCK(?)", lock).Scan(&released)
	}()
	// 领取的 UPDATE 会与业务侧的记忆写入争锁，InnoDB 选中一方回滚是常态，错误信息本身就是"try restarting"。
	for attempt := 1; ; attempt++ {
		err := gdb.WithContext(ctx).Transaction(claim)
		if err == nil {
			return nil
		}
		var myErr *mysql.MySQLError
		if attempt >= 3 || !errors.As(err, &myErr) || (myErr.Number != 1213 && myErr.Number != 1205) {
			return err
		}
		select {
		case <-time.After(200 * time.Millisecond):
		case <-ctx.Done():
			return err
		}
	}
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

// epochTime 表示"还没排定下一次"。MySQL 的 DATETIME 下限是 1000-01-01，
// Go 零值 time.Time（0001-01-01）在严格模式下会被直接拒收，所以关闭态统一落这个值。
var epochTime = time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)

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
