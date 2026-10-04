// Package config 集中读取后端运行所需的全部配置（环境变量）。
package config

import (
	"os"
	"strconv"
	"time"

	"tietie/backend/internal/dbop"
)

// Config 是一份只读的运行配置快照。
type Config struct {
	QWeatherHost           string
	QWeatherKey            string
	QWeatherKeyID          string
	QWeatherDeveloperID    string
	QWeatherProjectID      string
	QWeatherPrivateKeyFile string

	Host                        string           // 监听地址，默认 127.0.0.1
	Port                        int              // 监听端口，默认 4173（与 Node 版一致）
	StaticDir                   string           // 前端构建产物目录，默认 ../frontend/dist
	Upstream                    string           // Qoder 云端地址
	Token                       string           // QODER_ACCESS_TOKEN
	DefaultSessionID            string           // QODER_DEFAULT_SESSION_ID
	Timeout                     time.Duration    // 上游普通请求超时
	UploadTimeout               time.Duration    // 上游文件上传超时
	DB                          dbop.MySQLConfig // MySQL 连接参数（MYSQL_* 环境变量）
	AgentID                     string           // QODER_AGENT_ID，绑定时新建会话用的 agent；留空自动探测
	EnvironmentID               string           // QODER_ENVIRONMENT_ID，留空自动探测
	JWTSecret                   string           // JWT 签名密钥；生产环境必须通过 JWT_SECRET 设置
	JWTTTL                      time.Duration    // 令牌有效期，默认 30 天
	SchedulerPollInterval       time.Duration    // 到期队列检查间隔，默认 1s
	SchedulerBatchSize          int              // 每次原子领取数量，默认 64
	ConversationProtocolVersion int              // 新会话协议为2；旧历史仍可读取
	CloudMemoryEnabled          bool             // 默认开启 Qoder 云端记忆同步
	BackgroundWorkersEnabled    bool             // 默认开启后台队列；连接生产库的本地实例应关闭
	LogDir                      string           // 系统与定时任务日志目录
	LogLevel                    string           // info / debug / warn / error
	SchedulerConcurrency        int              // 并发云端请求上限，默认 4
}

// Load 从环境变量读取配置，缺省值与 server/index.mjs、server/qoder.mjs 保持一致。
func Load() Config {
	port := 4173
	if v := os.Getenv("PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 65535 {
			port = n
		}
	}
	timeout := 15 * time.Second
	if v := os.Getenv("QODER_TIMEOUT_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			timeout = time.Duration(n) * time.Second
		}
	}
	return Config{

		QWeatherHost:           os.Getenv("QWEATHER_API_HOST"),
		QWeatherKey:            os.Getenv("QWEATHER_API_KEY"),
		QWeatherKeyID:          os.Getenv("QWEATHER_KEY_ID"),
		QWeatherDeveloperID:    os.Getenv("QWEATHER_DEVELOPER_ID"),
		QWeatherProjectID:      os.Getenv("QWEATHER_PROJECT_ID"),
		QWeatherPrivateKeyFile: os.Getenv("QWEATHER_PRIVATE_KEY_FILE"),

		ConversationProtocolVersion: 2,
		CloudMemoryEnabled:          os.Getenv("QODER_MEMORY_ENABLED") != "false",
		BackgroundWorkersEnabled:    os.Getenv("BACKGROUND_WORKERS_ENABLED") != "false",
		LogDir:                      envOr("LOG_DIR", "logs"),
		LogLevel:                    envOr("LOG_LEVEL", "info"),
		Host:                        envOr("HOST", "127.0.0.1"),
		Port:                        port,
		StaticDir:                   envOr("STATIC_DIR", "../frontend/dist"),
		Upstream:                    envOr("QODER_UPSTREAM", "https://api.qoder.com.cn/api/v1/cloud"),
		Token:                       os.Getenv("QODER_ACCESS_TOKEN"),
		DefaultSessionID:            os.Getenv("QODER_DEFAULT_SESSION_ID"),
		Timeout:                     timeout,
		UploadTimeout:               60 * time.Second,
		DB: dbop.MySQLConfig{
			Host:          os.Getenv("MYSQL_HOST"),
			Port:          envInt("MYSQL_PORT", 3306, 65535),
			User:          envOr("MYSQL_USER", "root"),
			Password:      os.Getenv("MYSQL_PASSWORD"),
			Database:      envOr("MYSQL_DATABASE", "tietie"),
			TLSCAFile:     os.Getenv("MYSQL_TLS_CA_FILE"),
			TLSServerName: envOr("MYSQL_TLS_SERVER_NAME", os.Getenv("MYSQL_HOST")),
		},
		AgentID:               os.Getenv("QODER_AGENT_ID"),
		EnvironmentID:         os.Getenv("QODER_ENVIRONMENT_ID"),
		JWTSecret:             envOr("JWT_SECRET", "tietie-dev-secret-change-me"),
		JWTTTL:                jwtTTL(),
		SchedulerPollInterval: time.Duration(envInt("SCHEDULER_POLL_SECONDS", 1, 60)) * time.Second,
		SchedulerBatchSize:    envInt("SCHEDULER_BATCH_SIZE", 64, 512),
		SchedulerConcurrency:  envInt("SCHEDULER_CONCURRENCY", 4, 32),
	}
}

func envInt(key string, fallback, maximum int) int {
	value, err := strconv.Atoi(os.Getenv(key))
	if err != nil || value <= 0 || value > maximum {
		return fallback
	}
	return value
}

// jwtTTL 读取令牌有效期（小时），默认 30 天。
func jwtTTL() time.Duration {
	if v := os.Getenv("JWT_TTL_HOURS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Hour
		}
	}
	return 30 * 24 * time.Hour
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
