package logging

import (
	"bytes"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSeparateChannelsShanghaiTimeAndRotation(t *testing.T) {
	dir := t.TempDir()
	var console bytes.Buffer
	logs, err := Open(Options{Dir: dir, Console: &console, Level: slog.LevelInfo, MaxBytes: 500, Backups: 2})
	if err != nil {
		t.Fatal(err)
	}
	System().Info("数据库就绪", "event", "database.ready")
	log.Print("兼容旧日志")
	Scheduler().Info("任务完成", "event", "reminder.completed", "due_at", time.Date(2026, 10, 1, 15, 10, 0, 0, time.UTC))
	Scheduler().Debug("不应出现")
	sys, _ := os.ReadFile(filepath.Join(dir, "system.log"))
	timer, _ := os.ReadFile(filepath.Join(dir, "scheduler.log"))
	if !bytes.Contains(sys, []byte("数据库就绪")) || !bytes.Contains(sys, []byte("兼容旧日志")) || bytes.Contains(sys, []byte("任务完成")) {
		t.Fatal(string(sys))
	}
	if !bytes.Contains(timer, []byte("任务完成")) || !bytes.Contains(timer, []byte("2026-10-01 23:10:00.000 +08:00")) || bytes.Contains(timer, []byte("不应出现")) {
		t.Fatal(string(timer))
	}
	if !strings.Contains(console.String(), "数据库就绪") || !strings.Contains(console.String(), "任务完成") {
		t.Fatal(console.String())
	}
	for i := 0; i < 30; i++ {
		Scheduler().Info("排队中", "event", "scheduler.heartbeat", "seq", i)
	}
	if err := logs.Close(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"scheduler.log", "scheduler.log.1", "scheduler.log.2"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "scheduler.log.3")); !os.IsNotExist(err) {
		t.Fatal("unbounded rotation")
	}
}
