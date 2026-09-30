// Package config 集中读取后端运行所需的全部配置（环境变量）。
package config

import (
	"os"
	"strconv"
	"time"
)

// Config 是一份只读的运行配置快照。
type Config struct {
	Host             string        // 监听地址，默认 127.0.0.1
	Port             int           // 监听端口，默认 4173（与 Node 版一致）
	StaticDir        string        // 前端构建产物目录，默认 ../frontend/dist
	Upstream         string        // Qoder 云端地址
	Token            string        // QODER_ACCESS_TOKEN
	AllowedOrigin    string        // QODER_ALLOWED_ORIGIN，显式规范源（反向代理场景）
	DefaultSessionID string        // QODER_DEFAULT_SESSION_ID
	Timeout          time.Duration // 上游普通请求超时
	UploadTimeout    time.Duration // 上游文件上传超时
	DBDSN            string        // SQLite 数据库文件路径，默认 tietie.db
	AgentID          string        // QODER_AGENT_ID，绑定时新建会话用的 agent；留空自动探测
	EnvironmentID    string        // QODER_ENVIRONMENT_ID，留空自动探测
	JWTSecret        string        // JWT 签名密钥；生产环境必须通过 JWT_SECRET 设置
	JWTTTL           time.Duration // 令牌有效期，默认 30 天
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
		Host:             envOr("HOST", "127.0.0.1"),
		Port:             port,
		StaticDir:        envOr("STATIC_DIR", "../frontend/dist"),
		Upstream:         envOr("QODER_UPSTREAM", "https://api.qoder.com.cn/api/v1/cloud"),
		Token:            os.Getenv("QODER_ACCESS_TOKEN"),
		AllowedOrigin:    os.Getenv("QODER_ALLOWED_ORIGIN"),
		DefaultSessionID: os.Getenv("QODER_DEFAULT_SESSION_ID"),
		Timeout:          timeout,
		UploadTimeout:    60 * time.Second,
		DBDSN:            envOr("DB_DSN", "tietie.db"),
		AgentID:          os.Getenv("QODER_AGENT_ID"),
		EnvironmentID:    os.Getenv("QODER_ENVIRONMENT_ID"),
		JWTSecret:        envOr("JWT_SECRET", "tietie-dev-secret-change-me"),
		JWTTTL:           jwtTTL(),
	}
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
