package clipvault

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type DailyLogger struct {
	dir         string
	console     io.Writer // 额外输出（如 os.Stdout），为 nil 时不输出到控制台
	mu          sync.Mutex
	currentDate string
	file        *os.File
}

func NewDailyLogger(dir string) (*DailyLogger, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &DailyLogger{dir: dir}, nil
}

// NewDailyLoggerWithConsole 创建同时写文件和控制台的 logger。
func NewDailyLoggerWithConsole(dir string, console io.Writer) (*DailyLogger, error) {
	l, err := NewDailyLogger(dir)
	if err != nil {
		return nil, err
	}
	l.console = console
	return l, nil
}

func (l *DailyLogger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}

func (l *DailyLogger) Info(event string, fields map[string]any) {
	l.write("info", event, fields)
}

func (l *DailyLogger) Warn(event string, fields map[string]any) {
	l.write("warn", event, fields)
}

func (l *DailyLogger) Error(event string, err error, fields map[string]any) {
	// 在本地副本上追加 error 字段，避免就地修改调用方传入的 map。
	// fields 为 nil 时 range 为空操作，local 仍是可用的空 map。
	local := make(map[string]any, len(fields)+1)
	for k, v := range fields {
		local[k] = v
	}
	local["error"] = err.Error()
	l.write("error", event, local)
}

func (l *DailyLogger) write(level, event string, fields map[string]any) {
	// nil 守卫：当 SearchServer.logger 未初始化（如部分测试中）时，
	// 避免 writeAPIError 等错误路径解引用 nil 导致整个测试二进制崩溃。
	// 生产路径始终通过 NewSearchServer 注入 logger，故此守卫不影响线上行为。
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	if err := l.rotateLocked(time.Now()); err != nil {
		return
	}
	entry := map[string]any{
		"ts":    time.Now().Format(time.RFC3339Nano),
		"level": level,
		"event": event,
	}
	for k, v := range fields {
		entry[k] = v
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	line := append(data, '\n')
	_, _ = l.file.Write(line)
	if l.console != nil {
		_, _ = l.console.Write(line)
	}
}

func (l *DailyLogger) rotateLocked(now time.Time) error {
	date := now.Format(time.DateOnly)
	if l.file != nil && l.currentDate == date {
		return nil
	}
	if l.file != nil {
		_ = l.file.Close()
		l.file = nil
	}
	if err := os.MkdirAll(l.dir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(l.dir, date+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	l.file = f
	l.currentDate = date
	return nil
}
