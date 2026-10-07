package main

import (
	"bufio"
	"context"
	"errors"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"tietie/backend/internal/api"
	"tietie/backend/internal/auth"
	"tietie/backend/internal/config"
	"tietie/backend/internal/dbop"
	"tietie/backend/internal/logging"
	"tietie/backend/internal/qoder"
	"tietie/backend/internal/scheduler"
	"tietie/backend/internal/wechat"
)

func main() {
	loadDotEnv(".env.local", ".env")
	cfg := config.Load()
	var level slog.Level
	if err := level.UnmarshalText([]byte(cfg.LogLevel)); err != nil {
		level = slog.LevelInfo
	}
	logs, err := logging.Open(logging.Options{Dir: cfg.LogDir, Level: level})
	if err != nil {
		log.Fatalf("日志模块启动失败: %v", err)
	}
	defer logs.Close()
	logDir, _ := filepath.Abs(cfg.LogDir)
	logging.System().Info("后端启动，日志模块已就绪", "event", "server.starting", "pid", os.Getpid(), "system_log", filepath.Join(logDir, "system.log"), "scheduler_log", filepath.Join(logDir, "scheduler.log"), "log_level", level.String())

	// MySQL：MYSQL_HOST 等连接参数来自环境变量，启动时建库建表。
	db, err := dbop.Open(cfg.DB)
	if err != nil {
		log.Fatalf("数据库打开失败: %v", err)
	}
	defer db.Close()
	logging.System().Info("数据库迁移完成，提醒队列和记忆表已就绪", "event", "database.ready", "address", cfg.DB.Addr(), "database", cfg.DB.Database)
	options := scheduler.Options{PollInterval: cfg.SchedulerPollInterval, BatchSize: cfg.SchedulerBatchSize, Concurrency: cfg.SchedulerConcurrency}.Normalized()
	logging.System().Info("Qoder 云端助手连接配置已加载，沿用云端角色和系统提示词", "event", "qoder.configured", "configured", cfg.Token != "", "timeout", cfg.Timeout, "conversation_protocol", cfg.ConversationProtocolVersion, "cloud_memory_enabled", cfg.CloudMemoryEnabled)
	logging.System().Info("后台服务配置就绪", "event", "scheduler.configured", "poll_interval", options.PollInterval, "batch_size", options.BatchSize, "concurrency", options.Concurrency)

	srv := &api.Server{
		Cfg:            &cfg,
		Qoder:          qoder.NewClient(cfg),
		Auth:           auth.NewService(cfg.JWTSecret, cfg.JWTTTL),
		DB:             db,
		Wechat:         wechat.NewClient(cfg.WechatAppID, cfg.WechatAppSecret, cfg.WechatTimeout),
		WechatMessages: wechat.NewMessageClient(wechat.MessageOptions{AppID: cfg.WechatAppID, AppSecret: cfg.WechatAppSecret, Timeout: cfg.WechatTimeout, ReminderTemplateID: cfg.WechatReminderTemplateID, ReminderTitleKey: cfg.WechatReminderTitleKey, ReminderTimeKey: cfg.WechatReminderTimeKey, ReminderContentKey: cfg.WechatReminderContentKey, ReminderTypeKey: cfg.WechatReminderTypeKey, ReminderSourceKey: cfg.WechatReminderSourceKey, MiniprogramState: cfg.WechatMiniprogramState}),
	}
	db.SetWechatNotifications(dbop.WechatNotificationOptions{Enabled: srv.WechatMessages.Enabled(), AppID: cfg.WechatAppID, TemplateID: cfg.WechatReminderTemplateID, SubscriptionType: cfg.WechatReminderSubscriptionType})
	if cfg.JWTSecret == "tietie-dev-secret-change-me" {
		log.Println("警告: 正在使用默认 JWT_SECRET，生产环境请通过环境变量设置自定义密钥")
	}

	httpSrv := &http.Server{
		Addr:              cfg.Host + ":" + itoa(cfg.Port),
		Handler:           api.NewRouter(srv),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		// WriteTimeout 保持 0：SSE 长连接不能被写超时掐断。
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	workerDone := make(chan struct{})
	if cfg.BackgroundWorkersEnabled {
		go func() {
			defer close(workerDone)
			pushDone := make(chan struct{})
			go func() { defer close(pushDone); srv.RunWechatNotificationWorker(ctx) }()
			srv.RunConversationWorker(ctx)
			<-pushDone
		}()
	} else {
		close(workerDone)
		logging.System().Info("后台队列已禁用，本实例仅提供 HTTP 服务", "event", "scheduler.disabled")
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	}()

	logging.System().Info("HTTP 服务启动", "event", "server.listening", "address", "http://"+httpSrv.Addr)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("服务启动失败: %v", err)
	}
	stop()
	<-workerDone
	logging.System().Info("后台任务已退出，服务已停止", "event", "server.stopped")
}

// loadDotEnv 按顺序加载 .env 文件，已存在的环境变量优先（部署方可自行注入）。
func loadDotEnv(paths ...string) {
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			key, value, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			key = strings.TrimSpace(key)
			value = strings.TrimSpace(strings.Trim(strings.TrimSpace(value), `"'`))
			if key != "" {
				if _, exists := os.LookupEnv(key); !exists {
					_ = os.Setenv(key, value)
				}
			}
		}
		_ = f.Close()
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
