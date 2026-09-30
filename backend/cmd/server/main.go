// tietie 后端入口：加载配置 → 连接数据库(可选) → 构造上游客户端 → 挂载路由 → 启动服务。
package main

import (
	"bufio"
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"tietie/backend/internal/api"
	"tietie/backend/internal/auth"
	"tietie/backend/internal/config"
	"tietie/backend/internal/dbop"
	"tietie/backend/internal/qoder"
)

func main() {
	loadDotEnv(".env.local", ".env")
	cfg := config.Load()

	// SQLite 数据库：默认 tietie.db（backend/ 运行目录下），启动时自动建表。
	db, err := dbop.Open(cfg.DBDSN)
	if err != nil {
		log.Fatalf("数据库打开失败: %v", err)
	}
	defer db.Close()
	log.Printf("数据库就绪: %s", cfg.DBDSN)

	srv := &api.Server{
		Cfg:   &cfg,
		Qoder: qoder.NewClient(cfg),
		Auth:  auth.NewService(cfg.JWTSecret, cfg.JWTTTL),
		DB:    db,
	}
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
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	}()

	log.Printf("TieTie backend: http://%s", httpSrv.Addr)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("服务启动失败: %v", err)
	}
	log.Println("服务已停止")
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
