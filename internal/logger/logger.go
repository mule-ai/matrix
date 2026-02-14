package logger

import (
	"io"
	"log"
	"os"
	"strings"

	"github.com/mule-ai/mule/matrix-microservice/internal/config"
)

type LogLevel int

const (
	DEBUG LogLevel = iota
	INFO
	WARN
	ERROR
)

type Logger struct {
	logger *log.Logger
	level  LogLevel
}

func New(cfg *config.LoggingConfig) (*Logger, error) {
	var writer io.Writer = os.Stdout

	// If a log file is specified, write to both stdout and file
	if cfg.File != "" {
		file, err := os.OpenFile(cfg.File, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
		if err != nil {
			return nil, err
		}
		writer = io.MultiWriter(os.Stdout, file)
	}

	// Parse log level from config
	level := INFO // default to INFO
	switch strings.ToLower(cfg.Level) {
	case "debug":
		level = DEBUG
	case "info":
		level = INFO
	case "warn":
		level = WARN
	case "error":
		level = ERROR
	}

	return &Logger{
		logger: log.New(writer, "", log.LstdFlags|log.Lshortfile),
		level:  level,
	}, nil
}

func (l *Logger) shouldLog(level LogLevel) bool {
	return level >= l.level
}

func (l *Logger) Info(format string, v ...interface{}) {
	if l.shouldLog(INFO) {
		l.logger.Printf("[INFO] "+format, v...)
	}
}

func (l *Logger) Error(format string, v ...interface{}) {
	if l.shouldLog(ERROR) {
		l.logger.Printf("[ERROR] "+format, v...)
	}
}

func (l *Logger) Debug(format string, v ...interface{}) {
	if l.shouldLog(DEBUG) {
		l.logger.Printf("[DEBUG] "+format, v...)
	}
}

func (l *Logger) Warn(format string, v ...interface{}) {
	if l.shouldLog(WARN) {
		l.logger.Printf("[WARN] "+format, v...)
	}
}
