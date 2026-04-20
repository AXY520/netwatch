package logger

import (
	"fmt"
	"log"
	"os"
	"time"
)

var (
	logger *log.Logger
	level  = LevelInfo
)

const (
	LevelDebug = iota
	LevelInfo
	LevelWarn
	LevelError
)

func Init() {
	logger = log.New(os.Stdout, "", log.Ldate|log.Ltime|log.Lshortfile)
}

func SetLevel(l int) {
	level = l
}

func Debug(format string, args ...any) {
	if level <= LevelDebug {
		logger.Output(2, fmt.Sprintf("[DEBUG] "+format, args...))
	}
}

func Info(format string, args ...any) {
	if level <= LevelInfo {
		logger.Output(2, fmt.Sprintf("[INFO] "+format, args...))
	}
}

func Warn(format string, args ...any) {
	if level <= LevelWarn {
		logger.Output(2, fmt.Sprintf("[WARN] "+format, args...))
	}
}

func Error(format string, args ...any) {
	if level <= LevelError {
		logger.Output(2, fmt.Sprintf("[ERROR] "+format, args...))
	}
}

func ErrorErr(err error) {
	if err != nil {
		logger.Output(2, fmt.Sprintf("[ERROR] %v", err))
	}
}

func formatDuration(d time.Duration) string {
	return d.Round(time.Millisecond).String()
}