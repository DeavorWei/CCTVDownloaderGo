package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/CCTVDownloadGo/cctvdown/internal/api"
	"github.com/CCTVDownloadGo/cctvdown/internal/config"
	"github.com/CCTVDownloadGo/cctvdown/internal/downloader"
	"github.com/CCTVDownloadGo/cctvdown/internal/notify"
	"github.com/CCTVDownloadGo/cctvdown/internal/processor"
	"github.com/CCTVDownloadGo/cctvdown/internal/title"
	"github.com/CCTVDownloadGo/cctvdown/internal/utils"
)

// Controller 应用控制器
type Controller struct {
	cfg     *config.Config
	logger  *slog.Logger
	cleanup *utils.CleanupManager

	cctvClient     *api.CCTVClient
	cctvNewsClient *api.CCTVNewsClient
	notify         *notify.NotifyManager
}

// NewController 创建控制器
func NewController(cfg *config.Config, logger *slog.Logger, cleanup *utils.CleanupManager) *Controller {
	return &Controller{
		cfg:     cfg,
		logger:  logger,
		cleanup: cleanup,
	}
}

// Run 执行下载任务
func (c *Controller) Run(ctx context.Context, url, guid string) error {
	// 创建通知管理器
	c.notify = notify.NewNotifyManager(c.cfg.Quiet, c.logger)

	// 初始化API客户端
	c.cctvClient = api.NewCCTVClient(c.cfg.UserAgent, c.cfg.Timeout, c.logger)
	c.cctvNewsClient = api.NewCCTVNewsClient(c.cfg.UserAgent, c.cfg.Timeout, c.logger)

	var videoInfo *api.VideoInfo
	var err error

	// 阶段1: 获取视频信息
	c.notify.StartPhase("获取视频信息")

	if guid != "" {
		// 直接使用GUID
		videoInfo, err = c.cctvClient.GetVideoInfo(guid)
		if err != nil {
			c.notify.Error("获取视频信息失败: %v", err)
			return fmt.Errorf("获取视频信息失败: %w", err)
		}
		videoInfo.PID = guid
	} else {
		// 从URL提取PID并获取视频信息
		videoInfo, err = c.processFromURL(ctx, url)
		if err != nil {
			c.notify.Error("获取视频信息失败: %v", err)
			return err
		}
	}

	// 使用已获取的视频标题（避免重复API调用）
	videoTitle := videoInfo.RawTitle
	if videoTitle == "" {
		c.logger.Warn("视频标题为空，使用GUID作为文件名")
		videoTitle = videoInfo.PID
	}
	videoInfo.Title = title.SafeName(videoTitle)

	c.notify.Info("视频标题: %s", videoTitle)
	c.notify.CompletePhase("获取视频信息")

	c.logger.Info("视频信息",
		"pid", videoInfo.PID,
		"title", videoInfo.Title,
		"encrypted", videoInfo.IsEncrypted,
		"hls_key", videoInfo.HLSKey,
	)

	// 创建临时工作目录
	tempDir := c.cfg.GetTempWorkDir(videoInfo.PID)
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return fmt.Errorf("创建临时目录失败: %w", err)
	}
	c.cleanup.Register(tempDir)
	// 无论下载成功或失败，都自动清理临时文件
	defer c.cleanup.Cleanup()

	// 根据流类型选择下载策略
	if videoInfo.IsEncrypted {
		return c.downloadEncrypted(ctx, videoInfo, tempDir)
	}
	return c.downloadDirect(ctx, videoInfo, tempDir)
}

// processFromURL 从URL处理视频
func (c *Controller) processFromURL(ctx context.Context, url string) (*api.VideoInfo, error) {
	// 提取PID
	pid, err := c.cctvClient.ExtractPID(url)
	if err != nil {
		return nil, fmt.Errorf("无法从URL提取视频ID: %w", err)
	}
	c.logger.Info("提取到PID", "pid", pid)

	// 获取视频信息
	videoInfo, err := c.cctvClient.GetVideoInfo(pid)
	if err != nil {
		return nil, fmt.Errorf("获取视频信息失败: %w", err)
	}

	return videoInfo, nil
}

// downloadEncrypted 下载加密流
func (c *Controller) downloadEncrypted(ctx context.Context, info *api.VideoInfo, tempDir string) error {
	// 阶段2: 解析M3U8
	c.notify.StartPhase("解析M3U8")

	// 检查Node.js是否可用
	if err := utils.CheckNodeJS(c.cfg.NodePath); err != nil {
		c.notify.Error("加密视频需要Node.js: %v", err)
		return fmt.Errorf("加密视频需要Node.js: %w", err)
	}

	// 创建下载器
	dl := downloader.NewDownloader(
		c.cfg.DownloadWorkers,
		c.cfg.MaxRetries,
		c.cfg.Timeout,
		c.cfg.UserAgent,
		c.logger,
	)

	// 解析M3U8
	segments, err := dl.ParseM3U8(info.M3U8URL)
	if err != nil {
		c.notify.Error("解析M3U8失败: %v", err)
		return fmt.Errorf("解析M3U8失败: %w", err)
	}
	c.notify.CompletePhase("M3U8解析完成，共%d个分片", len(segments))

	c.logger.Info("M3U8解析完成", "segments", len(segments))

	// 阶段3: 下载分片
	c.notify.StartPhase("下载分片")

	// 设置下载进度回调
	lastProgress := int64(0)
	dl.SetProgressCallback(func(stage string, current, total int64, message string) {
		// 每10%输出一次进度
		progressStep := total / 10
		if progressStep < 1 {
			progressStep = 1
		}
		if current-lastProgress >= progressStep || current == total {
			percentage := float64(current) / float64(total) * 100
			c.notify.Progress("下载: %d/%d (%.0f%%)", current, total, percentage)
			lastProgress = current
		}
	})

	// 下载所有分片
	tsDir := filepath.Join(tempDir, "ts")
	if err := os.MkdirAll(tsDir, 0755); err != nil {
		c.notify.Error("创建TS目录失败: %v", err)
		return fmt.Errorf("创建TS目录失败: %w", err)
	}

	if err := dl.DownloadSegments(ctx, segments, tsDir); err != nil {
		c.notify.Error("下载分片失败: %v", err)
		return fmt.Errorf("下载分片失败: %w", err)
	}
	c.notify.CompletePhase("下载完成")

	// 创建处理器
	proc := processor.NewProcessor(
		c.cfg.FFmpegPath,
		c.cfg.NodePath,
		c.cfg.FFmpegConcurrency,
		c.logger,
	)

	// 用于跟踪已启动的阶段（避免重复启动）
	var startedStages sync.Map

	// 设置处理器进度回调
	proc.SetProgressCallback(func(stage string, current, total int64, message string) {
		switch stage {
		case "demux":
			// 首次更新时启动阶段
			if _, loaded := startedStages.LoadOrStore("demux", true); !loaded {
				c.notify.StartPhase("Demux分离")
			}
			// 每10%输出一次进度
			progressStep := total / 10
			if progressStep < 1 {
				progressStep = 1
			}
			if current%progressStep == 0 || current == total {
				percentage := float64(current) / float64(total) * 100
				c.notify.Progress("Demux: %d/%d (%.0f%%)", current, total, percentage)
			}
		case "decrypt_collect":
			// 收集阶段启动解密阶段
			if _, loaded := startedStages.LoadOrStore("decrypt", true); !loaded {
				c.notify.StartPhase("解密处理")
			}
		case "decrypt":
			// 每10%输出一次进度
			progressStep := total / 10
			if progressStep < 1 {
				progressStep = 1
			}
			if current%progressStep == 0 || current == total {
				percentage := float64(current) / float64(total) * 100
				c.notify.Progress("解密: %d/%d (%.0f%%)", current, total, percentage)
			}
		case "remux":
			// 首次更新时启动阶段
			if _, loaded := startedStages.LoadOrStore("remux", true); !loaded {
				c.notify.StartPhase("Remux封装")
			}
			// 每10%输出一次进度
			progressStep := total / 10
			if progressStep < 1 {
				progressStep = 1
			}
			if current%progressStep == 0 || current == total {
				percentage := float64(current) / float64(total) * 100
				c.notify.Progress("Remux: %d/%d (%.0f%%)", current, total, percentage)
			}
		case "merge":
			// 首次更新时启动阶段
			if _, loaded := startedStages.LoadOrStore("merge", true); !loaded {
				c.notify.StartPhase("最终合并")
			}
		}
	})

	// 处理加密流：demux -> 解密 -> 合并
	outputPath := filepath.Join(c.cfg.OutputDir, info.Title+".mp4")
	if err := proc.ProcessEncrypted(ctx, tsDir, segments, outputPath); err != nil {
		c.notify.Error("处理加密流失败: %v", err)
		return fmt.Errorf("处理加密流失败: %w", err)
	}

	// 完成所有阶段
	if _, ok := startedStages.Load("demux"); ok {
		c.notify.CompletePhase("Demux分离完成")
	}
	if _, ok := startedStages.Load("decrypt"); ok {
		c.notify.CompletePhase("解密完成")
	}
	if _, ok := startedStages.Load("remux"); ok {
		c.notify.CompletePhase("Remux封装完成")
	}
	if _, ok := startedStages.Load("merge"); ok {
		c.notify.CompletePhase("合并完成")
	}

	c.notify.Info("视频已保存: %s", outputPath)
	c.notify.Summary()
	return nil
}

// downloadDirect 直接下载普通流
func (c *Controller) downloadDirect(ctx context.Context, info *api.VideoInfo, tempDir string) error {
	// 阶段2: 解析M3U8
	c.notify.StartPhase("解析M3U8")

	// 创建下载器
	dl := downloader.NewDownloader(
		c.cfg.DownloadWorkers,
		c.cfg.MaxRetries,
		c.cfg.Timeout,
		c.cfg.UserAgent,
		c.logger,
	)

	// 解析M3U8获取分片数量（用于进度显示）
	segments, err := dl.ParseM3U8(info.M3U8URL)
	if err != nil {
		c.notify.Error("解析M3U8失败: %v", err)
		return fmt.Errorf("解析M3U8失败: %w", err)
	}
	c.notify.CompletePhase("M3U8解析完成，共%d个分片", len(segments))

	c.logger.Info("M3U8解析完成", "segments", len(segments))

	// 创建处理器
	proc := processor.NewProcessor(
		c.cfg.FFmpegPath,
		c.cfg.NodePath,
		c.cfg.FFmpegConcurrency,
		c.logger,
	)

	// 阶段3: 下载
	c.notify.StartPhase("下载视频")

	// 设置处理器进度回调
	proc.SetProgressCallback(func(stage string, current, total int64, message string) {
		// 下载进度由FFmpeg输出，这里不处理
	})

	// FFmpeg直接下载
	outputPath := filepath.Join(c.cfg.OutputDir, info.Title+".mp4")
	if err := proc.DownloadDirect(ctx, info.M3U8URL, outputPath); err != nil {
		c.notify.Error("直接下载失败: %v", err)
		return fmt.Errorf("直接下载失败: %w", err)
	}
	c.notify.CompletePhase("下载完成")

	c.notify.Info("视频已保存: %s", outputPath)
	c.notify.Summary()
	return nil
}
