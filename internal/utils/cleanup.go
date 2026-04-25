package utils

import (
	"context"
	"log/slog"
	"os"
	"sync"
)

// CleanupManager 临时文件清理管理器
type CleanupManager struct {
	tempDirs []string
	mu       sync.Mutex
	logger   *slog.Logger
}

// NewCleanupManager 创建清理管理器
func NewCleanupManager(logger *slog.Logger) *CleanupManager {
	return &CleanupManager{
		logger: logger,
	}
}

// Register 注册需要清理的临时目录
func (c *CleanupManager) Register(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tempDirs = append(c.tempDirs, path)
}

// Cleanup 清理所有注册的临时目录
func (c *CleanupManager) Cleanup() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var lastErr error
	for _, dir := range c.tempDirs {
		if err := os.RemoveAll(dir); err != nil {
			c.logger.Error("清理临时目录失败", "dir", dir, "error", err)
			lastErr = err
		} else {
			c.logger.Info("已清理临时目录", "dir", dir)
		}
	}
	c.tempDirs = nil
	return lastErr
}

// CleanupOnCancel 注册到context取消时自动清理
func (c *CleanupManager) CleanupOnCancel(ctx context.Context) {
	go func() {
		<-ctx.Done()
		c.logger.Warn("收到取消信号，正在清理临时文件...")
		c.Cleanup()
	}()
}
