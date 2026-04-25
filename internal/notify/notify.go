package notify

import (
	"fmt"
	"log/slog"
	"os"
	"time"
)

// NotifyManager 简化的通知管理器
// 提供阶段开始、进度更新、完成、错误等通知功能
type NotifyManager struct {
	quiet     bool
	logger    *slog.Logger
	startTime time.Time
}

// NewNotifyManager 创建通知管理器
func NewNotifyManager(quiet bool, logger *slog.Logger) *NotifyManager {
	return &NotifyManager{
		quiet:     quiet,
		logger:    logger,
		startTime: time.Now(),
	}
}

// StartPhase 开始阶段通知
// 输出格式: [开始] xxx...
func (m *NotifyManager) StartPhase(format string, args ...interface{}) {
	if m.quiet {
		return
	}
	msg := fmt.Sprintf(format, args...)
	fmt.Printf("[开始] %s...\n", msg)
}

// Progress 进度更新通知
// 输出格式: [进度] xxx
func (m *NotifyManager) Progress(format string, args ...interface{}) {
	if m.quiet {
		return
	}
	msg := fmt.Sprintf(format, args...)
	fmt.Printf("[进度] %s\n", msg)
}

// CompletePhase 完成阶段通知
// 输出格式: [完成] xxx
func (m *NotifyManager) CompletePhase(format string, args ...interface{}) {
	if m.quiet {
		return
	}
	msg := fmt.Sprintf(format, args...)
	fmt.Printf("[完成] %s\n", msg)
}

// Info 信息通知
// 输出格式: [信息] xxx
func (m *NotifyManager) Info(format string, args ...interface{}) {
	if m.quiet {
		return
	}
	msg := fmt.Sprintf(format, args...)
	fmt.Printf("[信息] %s\n", msg)
}

// Error 错误通知
// 输出格式: [错误] xxx
func (m *NotifyManager) Error(format string, args ...interface{}) {
	if m.quiet {
		return
	}
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(os.Stderr, "[错误] %s\n", msg)
}

// Summary 打印总结信息
// 输出格式: [信息] 总耗时: xxx
func (m *NotifyManager) Summary() {
	if m.quiet {
		return
	}
	elapsed := time.Since(m.startTime)
	fmt.Printf("[信息] 总耗时: %s\n", formatDuration(elapsed))
}

// formatDuration 格式化持续时间
func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return d.String()
	}
	minutes := int(d.Minutes())
	seconds := int(d.Seconds()) % 60
	return fmt.Sprintf("%d分%d秒", minutes, seconds)
}
