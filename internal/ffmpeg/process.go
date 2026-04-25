package ffmpeg

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"

	"github.com/CCTVDownloadGo/cctvdown/internal/notify"
	"golang.org/x/sync/semaphore"
)

// ProcessManager FFmpeg进程管理器
type ProcessManager struct {
	sem    *semaphore.Weighted
	logger *slog.Logger
}

// NewProcessManager 创建进程管理器
func NewProcessManager(maxConcurrent int64, logger *slog.Logger) *ProcessManager {
	return &ProcessManager{
		sem:    semaphore.NewWeighted(maxConcurrent),
		logger: logger,
	}
}

// Run 执行FFmpeg命令（受semaphore限制并发数）
func (m *ProcessManager) Run(ctx context.Context, args []string) error {
	if err := m.sem.Acquire(ctx, 1); err != nil {
		return fmt.Errorf("获取信号量失败: %w", err)
	}
	defer m.sem.Release(1)

	if len(args) == 0 {
		return fmt.Errorf("空命令")
	}

	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		m.logger.Error("FFmpeg命令执行失败",
			"cmd", args,
			"output", string(output),
			"error", err,
		)
		return fmt.Errorf("FFmpeg执行失败: %w, output: %s", err, string(output))
	}

	m.logger.Log(ctx, notify.LevelVerbose, "FFmpeg命令执行成功", "cmd", args)
	return nil
}

// RunWithOutput 执行FFmpeg命令并返回输出
func (m *ProcessManager) RunWithOutput(ctx context.Context, args []string) ([]byte, error) {
	if err := m.sem.Acquire(ctx, 1); err != nil {
		return nil, fmt.Errorf("获取信号量失败: %w", err)
	}
	defer m.sem.Release(1)

	if len(args) == 0 {
		return nil, fmt.Errorf("空命令")
	}

	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("FFmpeg执行失败: %w", err)
	}

	return output, nil
}