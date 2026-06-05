package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	maxFileSize   = 10 * 1024 * 1024
	maxRetainDays = 7
)

type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

type Logger struct {
	mu      sync.Mutex
	dir     string
	file    *os.File
	curDate string
	debug   bool
}

var global *Logger

func Init() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(home, ".config", "ai-commit", "logs")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	v := strings.TrimSpace(strings.ToLower(os.Getenv("GIT_AI_DEBUG")))
	debug := v == "1" || v == "true" || v == "yes" || v == "on"
	if !debug {
		v = strings.TrimSpace(strings.ToLower(os.Getenv("GI_DEBUG")))
		debug = v == "1" || v == "true" || v == "yes" || v == "on"
	}

	l := &Logger{dir: dir, debug: debug}
	if err := l.rotate(); err != nil {
		return err
	}
	l.cleanup()
	global = l
	return nil
}

func Close() {
	if global != nil {
		global.close()
	}
}

func Debug(format string, args ...any) {
	if global != nil {
		global.log(LevelDebug, format, args...)
	}
}

func Info(format string, args ...any) {
	if global != nil {
		global.log(LevelInfo, format, args...)
	}
}

func Warn(format string, args ...any) {
	if global != nil {
		global.log(LevelWarn, format, args...)
	}
}

func Error(format string, args ...any) {
	if global != nil {
		global.log(LevelError, format, args...)
	}
}

func LogDir() string {
	if global != nil {
		return global.dir
	}
	return ""
}

func (l *Logger) log(level Level, format string, args ...any) {
	if level == LevelDebug && !l.debug {
		return
	}
	msg := fmt.Sprintf(format, args...)
	now := time.Now()
	line := fmt.Sprintf("%s [%s] %s\n", now.Format("2006-01-02 15:04:05.000"), level, msg)

	l.mu.Lock()
	defer l.mu.Unlock()

	today := now.Format("2006-01-02")
	if today != l.curDate {
		l.rotateLocked()
	}

	if l.file != nil {
		l.file.WriteString(line)
	}
}

func (l *Logger) rotate() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.rotateLocked()
}

func (l *Logger) rotateLocked() error {
	if l.file != nil {
		l.file.Close()
		l.file = nil
	}
	today := time.Now().Format("2006-01-02")
	l.curDate = today
	path := filepath.Join(l.dir, today+".log")

	info, err := os.Stat(path)
	if err == nil && info.Size() >= int64(maxFileSize) {
		l.truncate(path)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	l.file = f
	return nil
}

func (l *Logger) truncate(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	half := len(data) / 2
	idx := half
	for idx < len(data) && data[idx] != '\n' {
		idx++
	}
	if idx < len(data) {
		idx++
	}
	kept := data[idx:]
	os.WriteFile(path, kept, 0644)
}

func (l *Logger) cleanup() {
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -maxRetainDays).Format("2006-01-02")

	var logFiles []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".log") || e.IsDir() {
			continue
		}
		date := strings.TrimSuffix(name, ".log")
		if len(date) == 10 && date < cutoff {
			logFiles = append(logFiles, filepath.Join(l.dir, name))
		}
	}

	sort.Strings(logFiles)
	for _, f := range logFiles {
		os.Remove(f)
	}
}

func (l *Logger) close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		l.file.Close()
		l.file = nil
	}
}
