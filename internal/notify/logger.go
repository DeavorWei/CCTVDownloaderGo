package notify

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// LevelVerbose 详细日志级别，比Debug更细粒度
// 用于分片级别的日志输出，避免污染Debug级别
// slog.LevelDebug = -4, LevelVerbose = -8
const LevelVerbose = slog.Level(-8)

// SetupLogger 初始化日志系统
// 返回 logger 和 closer，closer 为 nil 时表示使用 stderr
// 调用者应使用 defer closer.Close() 确保程序退出时正确关闭文件
func SetupLogger(logLevel, logFile string) (*slog.Logger, io.Closer) {
	var handler slog.Handler

	level := parseLevel(logLevel)

	if logFile != "" {
		// 确保日志文件目录存在
		logDir := filepath.Dir(logFile)
		if logDir != "" && logDir != "." {
			if err := os.MkdirAll(logDir, 0755); err != nil {
				// 目录创建失败，降级到标准错误输出
				handler = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
				return slog.New(handler), io.NopCloser(nil)
			}
		}

		// 每次运行清空旧日志文件，确保日志干净
		os.Remove(logFile)

		// 文件输出：JSON格式，便于分析
		f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			// 降级到标准错误输出
			handler = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
			return slog.New(handler), io.NopCloser(nil)
		}
		handler = slog.NewJSONHandler(f, &slog.HandlerOptions{Level: level})
		return slog.New(handler), f
	}

	// 终端输出：可读格式
	handler = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	return slog.New(handler), io.NopCloser(nil)
}

// parseLevel 解析日志级别字符串
func parseLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "verbose":
		return LevelVerbose
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
