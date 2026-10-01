// Package logging writes readable operational events to the console and to
// separate rotating system/scheduler files. Callers log IDs, never credentials
// or raw conversation payloads.
package logging

import (
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var system = slog.Default().With("模块", "系统")
var timer = slog.Default().With("模块", "定时任务")

func System() *slog.Logger    { return system }
func Scheduler() *slog.Logger { return timer }

type Options struct {
	Dir      string
	Level    slog.Level
	MaxBytes int64
	Backups  int
	Console  io.Writer
}
type Logs struct{ files []*rotatingFile }

// Open is called once, before workers start. Both channels appear on console;
// file routing keeps high-volume task events out of the system file.
func Open(o Options) (*Logs, error) {
	if o.Dir == "" {
		o.Dir = "logs"
	}
	if o.MaxBytes <= 0 {
		o.MaxBytes = 10 << 20
	}
	if o.Backups <= 0 {
		o.Backups = 5
	}
	if o.Console == nil {
		o.Console = os.Stdout
	}
	if err := os.MkdirAll(o.Dir, 0700); err != nil {
		return nil, err
	}
	logs := &Logs{}
	console := &lockedWriter{writer: o.Console}
	options := &slog.HandlerOptions{Level: o.Level, ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
		if a.Value.Kind() == slog.KindTime {
			a.Value = slog.StringValue(a.Value.Time().In(time.FixedZone("Asia/Shanghai", 28800)).Format("2006-01-02 15:04:05.000 +08:00"))
		}
		return a
	}}
	makeLogger := func(name, module string) (*slog.Logger, error) {
		f, err := newRotatingFile(filepath.Join(o.Dir, name), o.MaxBytes, o.Backups)
		if err != nil {
			return nil, err
		}
		logs.files = append(logs.files, f)
		return slog.New(slog.NewTextHandler(io.MultiWriter(console, f), options)).With("模块", module), nil
	}
	var err error
	system, err = makeLogger("system.log", "系统")
	if err != nil {
		logs.Close()
		return nil, err
	}
	timer, err = makeLogger("scheduler.log", "定时任务")
	if err != nil {
		logs.Close()
		return nil, err
	}
	slog.SetDefault(system)
	log.SetFlags(0)
	log.SetOutput(legacyWriter{})
	return logs, nil
}
func (l *Logs) Close() error {
	var result error
	for _, f := range l.files {
		if err := f.Close(); err != nil {
			result = err
		}
	}
	return result
}

type legacyWriter struct{}

func (legacyWriter) Write(p []byte) (int, error) {
	system.Info(strings.TrimSpace(string(p)))
	return len(p), nil
}

type lockedWriter struct {
	mu     sync.Mutex
	writer io.Writer
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writer.Write(p)
}

type rotatingFile struct {
	mu        sync.Mutex
	path      string
	file      *os.File
	size, max int64
	backups   int
}

func newRotatingFile(path string, max int64, backups int) (*rotatingFile, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	return &rotatingFile{path: path, file: f, size: info.Size(), max: max, backups: backups}, nil
}
func (w *rotatingFile) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.size > 0 && w.size+int64(len(p)) > w.max {
		if err := w.file.Close(); err != nil {
			return 0, err
		}
		for i := w.backups; i > 0; i-- {
			src := w.path
			if i > 1 {
				src = fmt.Sprintf("%s.%d", w.path, i-1)
			}
			dst := fmt.Sprintf("%s.%d", w.path, i)
			if err := os.Rename(src, dst); err != nil && !os.IsNotExist(err) {
				return 0, err
			}
		}
		f, err := os.OpenFile(w.path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
		if err != nil {
			return 0, err
		}
		w.file = f
		w.size = 0
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}
func (w *rotatingFile) Close() error { w.mu.Lock(); defer w.mu.Unlock(); return w.file.Close() }
