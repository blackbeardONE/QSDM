package logging

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"gopkg.in/natefinch/lumberjack.v2"
)

// LogLevel represents the logging level
type LogLevel int

const (
	LogLevelDebug LogLevel = iota
	LogLevelInfo
	LogLevelWarn
	LogLevelError
)

var logLevelNames = map[string]LogLevel{
	"DEBUG": LogLevelDebug,
	"INFO":  LogLevelInfo,
	"WARN":  LogLevelWarn,
	"ERROR": LogLevelError,
}

func parseLogLevel(level string) LogLevel {
	if l, ok := logLevelNames[strings.ToUpper(level)]; ok {
		return l
	}
	return LogLevelInfo // Default to INFO
}

type Logger struct {
	debug      *log.Logger
	info       *log.Logger
	warn       *log.Logger
	error      *log.Logger
	mu         sync.Mutex
	jsonOutput bool
	logLevel   LogLevel
	requestID  string // Current request ID for context
}

// NewLogger creates a new logger with specified log file and output format
func NewLogger(logFile string, jsonOutput bool) *Logger {
	return NewLoggerWithLevel(logFile, jsonOutput, "INFO")
}

// NewLoggerWithLevel creates a new logger with specified log level
func NewLoggerWithLevel(logFile string, jsonOutput bool, logLevel string) *Logger {
	lumberjackLogger := &lumberjack.Logger{
		Filename:   logFile,
		MaxSize:    100, // megabytes
		MaxBackups: 7,
		MaxAge:     28,   // days
		Compress:   true, // compress rotated files
	}

	multiWriter := io.MultiWriter(os.Stdout, lumberjackLogger)

	return &Logger{
		debug:      log.New(multiWriter, "DEBUG: ", log.Ldate|log.Ltime|log.Lshortfile),
		info:       log.New(multiWriter, "INFO: ", log.Ldate|log.Ltime|log.Lshortfile),
		warn:       log.New(multiWriter, "WARN: ", log.Ldate|log.Ltime|log.Lshortfile),
		error:      log.New(multiWriter, "ERROR: ", log.Ldate|log.Ltime|log.Lshortfile),
		jsonOutput: jsonOutput,
		logLevel:   parseLogLevel(logLevel),
		requestID:  "",
	}
}

// SetRequestID sets the current request ID for context tracking
func (l *Logger) SetRequestID(requestID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.requestID = requestID
}

// NewRequestID generates a new request ID and sets it
func (l *Logger) NewRequestID() string {
	requestID := uuid.New().String()
	l.SetRequestID(requestID)
	return requestID
}

// GetRequestID returns the current request ID
func (l *Logger) GetRequestID() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.requestID
}

func (l *Logger) Debug(msg string, keyvals ...interface{}) {
	if l.logLevel > LogLevelDebug {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.jsonOutput {
		l.debug.Println(l.formatJSON("DEBUG", msg, keyvals...))
	} else {
		l.debug.Println(l.formatText(msg, keyvals...))
	}
}

func (l *Logger) Info(msg string, keyvals ...interface{}) {
	if l.logLevel > LogLevelInfo {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.jsonOutput {
		l.info.Println(l.formatJSON("INFO", msg, keyvals...))
	} else {
		l.info.Println(l.formatText(msg, keyvals...))
	}
}

func (l *Logger) Warn(msg string, keyvals ...interface{}) {
	if l.logLevel > LogLevelWarn {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.jsonOutput {
		l.warn.Println(l.formatJSON("WARN", msg, keyvals...))
	} else {
		l.warn.Println(l.formatText(msg, keyvals...))
	}
}

func (l *Logger) Error(msg string, keyvals ...interface{}) {
	// Error level is always logged
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.jsonOutput {
		l.error.Println(l.formatJSON("ERROR", msg, keyvals...))
	} else {
		l.error.Println(l.formatText(msg, keyvals...))
	}
}


func (l *Logger) formatJSON(level string, msg string, keyvals ...interface{}) string {
	m := make(map[string]interface{})
	m["level"] = level
	m["msg"] = msg
	m["timestamp"] = time.Now().Format(time.RFC3339)
	
	// Add request ID if available
	if l.requestID != "" {
		m["request_id"] = l.requestID
	}
	
	for i := 0; i < len(keyvals)-1; i += 2 {
		key, ok := keyvals[i].(string)
		if ok {
			m[key] = keyvals[i+1]
		}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return msg
	}
	return string(b)
}

func (l *Logger) formatText(msg string, keyvals ...interface{}) string {
	s := msg
	if l.requestID != "" {
		s += " request_id=" + l.requestID
	}
	for i := 0; i < len(keyvals)-1; i += 2 {
		s += " " + keyvals[i].(string) + "=" + toString(keyvals[i+1])
	}
	return s
}

func toString(v interface{}) string {
	switch v := v.(type) {
	case string:
		return v
	case int, int32, int64, float32, float64:
		return fmt.Sprintf("%v", v)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(b)
	}
}

// NewSilentLogger returns a logger that discards all output (for tests; avoids log file locks on Windows).
func NewSilentLogger() *Logger {
	d := log.New(io.Discard, "", 0)
	return &Logger{
		debug:      d,
		info:       d,
		warn:       d,
		error:      d,
		jsonOutput: false,
		logLevel:   LogLevelDebug,
		requestID:  "",
	}
}
