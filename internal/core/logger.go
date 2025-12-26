// Package core 提供日志功能，参考 aria2 的 Logger 和 LogFactory 设计
package core

import (
	"fmt"
	"log"
	"os"
	"sync"
)

// LogLevel 表示日志级别，对应 aria2 的 LEVEL 枚举
type LogLevel int

const (
	// LogLevelDebug 调试级别
	LogLevelDebug LogLevel = 1 << iota
	// LogLevelInfo 信息级别
	LogLevelInfo
	// LogLevelNotice 通知级别
	LogLevelNotice
	// LogLevelWarn 警告级别
	LogLevelWarn
	// LogLevelError 错误级别
	LogLevelError
)

// String 返回日志级别的字符串表示
func (l LogLevel) String() string {
	switch l {
	case LogLevelDebug:
		return "debug"
	case LogLevelInfo:
		return "info"
	case LogLevelNotice:
		return "notice"
	case LogLevelWarn:
		return "warn"
	case LogLevelError:
		return "error"
	default:
		return "unknown"
	}
}

// ParseLogLevel 从字符串解析日志级别
func ParseLogLevel(levelStr string) LogLevel {
	switch levelStr {
	case "debug":
		return LogLevelDebug
	case "info":
		return LogLevelInfo
	case "notice":
		return LogLevelNotice
	case "warn", "warning":
		return LogLevelWarn
	case "error":
		return LogLevelError
	default:
		return LogLevelNotice // 默认级别
	}
}

// Logger 日志器接口，对应 aria2 的 Logger
type Logger interface {
	// Debug 输出调试日志
	Debug(msg string)
	// Info 输出信息日志
	Info(msg string)
	// Notice 输出通知日志
	Notice(msg string)
	// Warn 输出警告日志
	Warn(msg string)
	// Error 输出错误日志
	Error(msg string)

	// SetFileLogLevel 设置文件日志级别
	SetFileLogLevel(level LogLevel)
	// SetConsoleLogLevel 设置控制台日志级别
	SetConsoleLogLevel(level LogLevel)
	// SetConsoleOutput 设置是否输出到控制台
	SetConsoleOutput(enabled bool)
	// SetColorOutput 设置是否启用彩色输出
	SetColorOutput(enabled bool)

	// FileLogLevelEnabled 检查文件日志级别是否启用
	FileLogLevelEnabled(level LogLevel) bool
	// ConsoleLogLevelEnabled 检查控制台日志级别是否启用
	ConsoleLogLevelEnabled(level LogLevel)
	// LevelEnabled 检查日志级别是否启用（文件或控制台）
	LevelEnabled(level LogLevel) bool
}

// DefaultLogger 默认日志器实现
type DefaultLogger struct {
	mu               sync.RWMutex
	fileLogLevel     LogLevel
	consoleLogLevel  LogLevel
	consoleOutput    bool
	colorOutput      bool
	file             *os.File
	logger           *log.Logger
}

// NewDefaultLogger 创建新的默认日志器
func NewDefaultLogger() *DefaultLogger {
	return &DefaultLogger{
		fileLogLevel:    LogLevelDebug,
		consoleLogLevel: LogLevelNotice,
		consoleOutput:   true,
		colorOutput:     true,
		logger:          log.New(os.Stdout, "", log.LstdFlags),
	}
}

// Debug 输出调试日志
func (l *DefaultLogger) Debug(msg string) {
	l.log(LogLevelDebug, msg)
}

// Info 输出信息日志
func (l *DefaultLogger) Info(msg string) {
	l.log(LogLevelInfo, msg)
}

// Notice 输出通知日志
func (l *DefaultLogger) Notice(msg string) {
	l.log(LogLevelNotice, msg)
}

// Warn 输出警告日志
func (l *DefaultLogger) Warn(msg string) {
	l.log(LogLevelWarn, msg)
}

// Error 输出错误日志
func (l *DefaultLogger) Error(msg string) {
	l.log(LogLevelError, msg)
}

// log 内部日志输出方法
func (l *DefaultLogger) log(level LogLevel, msg string) {
	if !l.LevelEnabled(level) {
		return
	}

	// 格式化消息
	formattedMsg := fmt.Sprintf("[%s] %s", level, msg)

	// 输出到文件
	if l.FileLogLevelEnabled(level) && l.logger != nil {
		l.logger.Println(formattedMsg)
	}

	// 输出到控制台
	if l.ConsoleLogLevelEnabled(level) && l.consoleOutput {
		if l.colorOutput {
			formattedMsg = l.colorize(level, formattedMsg)
		}
		log.Println(formattedMsg)
	}
}

// SetFileLogLevel 设置文件日志级别
func (l *DefaultLogger) SetFileLogLevel(level LogLevel) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fileLogLevel = level
}

// SetConsoleLogLevel 设置控制台日志级别
func (l *DefaultLogger) SetConsoleLogLevel(level LogLevel) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.consoleLogLevel = level
}

// SetConsoleOutput 设置是否输出到控制台
func (l *DefaultLogger) SetConsoleOutput(enabled bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.consoleOutput = enabled
}

// SetColorOutput 设置是否启用彩色输出
func (l *DefaultLogger) SetColorOutput(enabled bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.colorOutput = enabled
}

// FileLogLevelEnabled 检查文件日志级别是否启用
func (l *DefaultLogger) FileLogLevelEnabled(level LogLevel) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return level >= l.fileLogLevel
}

// ConsoleLogLevelEnabled 检查控制台日志级别是否启用
func (l *DefaultLogger) ConsoleLogLevelEnabled(level LogLevel) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.consoleOutput && level >= l.consoleLogLevel
}

// LevelEnabled 检查日志级别是否启用（文件或控制台）
func (l *DefaultLogger) LevelEnabled(level LogLevel) bool {
	return l.FileLogLevelEnabled(level) || l.ConsoleLogLevelEnabled(level)
}

// colorize 为日志消息添加颜色
func (l *DefaultLogger) colorize(level LogLevel, msg string) string {
	reset := "\033[0m"
	var color string

	switch level {
	case LogLevelDebug:
		color = "\033[36m" // 青色
	case LogLevelInfo:
		color = "\033[32m" // 绿色
	case LogLevelNotice:
		color = "\033[34m" // 蓝色
	case LogLevelWarn:
		color = "\033[33m" // 黄色
	case LogLevelError:
		color = "\033[31m" // 红色
	default:
		return msg
	}

	return color + msg + reset
}

// LogFactory 日志工厂，对应 aria2 的 LogFactory
type LogFactory struct {
	mu       sync.RWMutex
	instance Logger
}

// NewLogFactory 创建日志工厂
func NewLogFactory() *LogFactory {
	return &LogFactory{
		instance: NewDefaultLogger(),
	}
}

// GetInstance 获取日志器实例（单例）
func (lf *LogFactory) GetInstance() Logger {
	lf.mu.RLock()
	defer lf.mu.RUnlock()
	return lf.instance
}

// SetFileLogLevel 设置文件日志级别
func (lf *LogFactory) SetFileLogLevel(level LogLevel) {
	lf.mu.Lock()
	defer lf.mu.Unlock()
	lf.instance.SetFileLogLevel(level)
}

// SetFileLogLevelFromString 从字符串设置文件日志级别
func (lf *LogFactory) SetFileLogLevelFromString(levelStr string) {
	lf.SetFileLogLevel(ParseLogLevel(levelStr))
}

// SetConsoleLogLevel 设置控制台日志级别
func (lf *LogFactory) SetConsoleLogLevel(level LogLevel) {
	lf.mu.Lock()
	defer lf.mu.Unlock()
	lf.instance.SetConsoleLogLevel(level)
}

// SetConsoleLogLevelFromString 从字符串设置控制台日志级别
func (lf *LogFactory) SetConsoleLogLevelFromString(levelStr string) {
	lf.SetConsoleLogLevel(ParseLogLevel(levelStr))
}

// SetConsoleOutput 设置是否输出到控制台
func (lf *LogFactory) SetConsoleOutput(enabled bool) {
	lf.mu.Lock()
	defer lf.mu.Unlock()
	lf.instance.SetConsoleOutput(enabled)
}

// SetColorOutput 设置是否启用彩色输出
func (lf *LogFactory) SetColorOutput(enabled bool) {
	lf.mu.Lock()
	defer lf.mu.Unlock()
	lf.instance.SetColorOutput(enabled)
}

// 全局日志工厂实例
var globalLogFactory = NewLogFactory()

// GetGlobalLogger 获取全局日志器
func GetGlobalLogger() Logger {
	return globalLogFactory.GetInstance()
}

// SetGlobalFileLogLevel 设置全局文件日志级别
func SetGlobalFileLogLevel(level LogLevel) {
	globalLogFactory.SetFileLogLevel(level)
}

// SetGlobalFileLogLevelFromString 从字符串设置全局文件日志级别
func SetGlobalFileLogLevelFromString(levelStr string) {
	globalLogFactory.SetFileLogLevelFromString(levelStr)
}

// SetGlobalConsoleLogLevel 设置全局控制台日志级别
func SetGlobalConsoleLogLevel(level LogLevel) {
	globalLogFactory.SetConsoleLogLevel(level)
}

// SetGlobalConsoleLogLevelFromString 从字符串设置全局控制台日志级别
func SetGlobalConsoleLogLevelFromString(levelStr string) {
	globalLogFactory.SetConsoleLogLevelFromString(levelStr)
}