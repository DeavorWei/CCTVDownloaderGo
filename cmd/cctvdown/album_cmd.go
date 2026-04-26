package main

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/CCTVDownloadGo/cctvdown/internal/api"
	"github.com/CCTVDownloadGo/cctvdown/internal/config"
	"github.com/CCTVDownloadGo/cctvdown/internal/notify"
	"github.com/CCTVDownloadGo/cctvdown/internal/pipeline"
	"github.com/CCTVDownloadGo/cctvdown/internal/state"
	"github.com/CCTVDownloadGo/cctvdown/internal/title"
	"github.com/CCTVDownloadGo/cctvdown/internal/utils"
)

// ResumeChoice 用户对续传的选择
type ResumeChoice string

const (
	ResumeChoiceResume   ResumeChoice = "resume"   // 继续下载
	ResumeChoiceRestart  ResumeChoice = "restart"  // 重新开始
	ResumeChoiceCancel   ResumeChoice = "cancel"   // 取消
)

// AlbumController 专辑下载控制器
type AlbumController struct {
	cfg     *config.Config
	logger  *slog.Logger
	cleanup *utils.CleanupManager
}

// NewAlbumController 创建专辑控制器
func NewAlbumController(cfg *config.Config, logger *slog.Logger, cleanup *utils.CleanupManager) *AlbumController {
	return &AlbumController{
		cfg:     cfg,
		logger:  logger,
		cleanup: cleanup,
	}
}

// RunGet 执行专辑获取和下载
// restart 参数为 true 时强制重新开始，忽略已有状态
func (c *AlbumController) RunGet(ctx context.Context, url string, startDate, endDate string, skipConfirm bool, restart bool) error {
	// 创建通知管理器
	notifyMgr := notify.NewNotifyManager(c.cfg.Quiet, c.logger)

	// 创建专辑服务
	albumService := api.NewAlbumService(c.cfg.UserAgent, c.cfg.Timeout, c.logger)

	// 创建状态管理器
	stateMgr := state.NewAlbumStateManager(c.cfg.TempDir, c.logger)

	// 阶段1: 获取专辑信息
	notifyMgr.StartPhase("获取专辑信息")

	dateRange := api.DateRange{
		StartDate: startDate,
		EndDate:   endDate,
	}

	albumInfo, err := albumService.GetAlbumFromURL(url, dateRange)
	if err != nil {
		notifyMgr.Error("获取专辑信息失败: %v", err)
		return err
	}

	notifyMgr.CompletePhase("获取专辑信息")
	notifyMgr.Info("专辑名称: %s", albumInfo.Title)
	notifyMgr.Info("总视频数: %d", len(albumInfo.Videos))

	if startDate != "" && endDate != "" {
		notifyMgr.Info("日期范围: %s - %s", startDate, endDate)
	}

	// 获取或创建下载状态
	downloadState, resumeChoice, err := c.getOrCreateState(stateMgr, albumInfo, url, dateRange, skipConfirm, restart, notifyMgr)
	if err != nil {
		return err
	}
	if resumeChoice == ResumeChoiceCancel {
		notifyMgr.Info("用户取消操作")
		return nil
	}

	// 根据选择过滤视频列表
	if resumeChoice == ResumeChoiceResume && downloadState != nil {
		albumInfo = c.filterCompletedVideos(albumInfo, downloadState, stateMgr, notifyMgr)
	}

	// 如果不是跳过确认且不是续传模式，显示列表并等待用户确认
	if !skipConfirm && resumeChoice != ResumeChoiceResume {
		c.printVideoList(albumInfo)

		fmt.Println()
		fmt.Print("是否开始下载? (输入 y 确认，其他键退出): ")

		reader := bufio.NewReader(os.Stdin)
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(strings.ToLower(input))

		if input != "y" {
			// 标记取消
			if downloadState != nil {
				stateMgr.MarkAlbumCancelled(downloadState.AlbumID)
			}
			notifyMgr.Info("用户取消操作")
			return nil
		}
	}

	// 保存初始状态
	if downloadState == nil {
		downloadState = stateMgr.CreateNewState(
			albumInfo.ID,
			albumInfo.Title,
			url,
			state.DateRange{StartDate: startDate, EndDate: endDate},
			len(albumInfo.Videos),
		)
		// 设置专辑输出目录
		if albumInfo.Title != "" {
			downloadState.AlbumOutputDir = filepath.Join(c.cfg.OutputDir, title.SafeName(albumInfo.Title))
		}
	}
	if err := stateMgr.Save(downloadState); err != nil {
		c.logger.Warn("保存状态失败", "error", err)
	}

	// 开始批量下载
	return c.downloadAlbum(ctx, albumInfo, downloadState, stateMgr, notifyMgr)
}

// getOrCreateState 获取或创建下载状态，并询问用户选择
func (c *AlbumController) getOrCreateState(stateMgr *state.AlbumStateManager, albumInfo *api.AlbumInfo, url string, dateRange api.DateRange, skipConfirm bool, restart bool, notifyMgr *notify.NotifyManager) (*state.AlbumDownloadState, ResumeChoice, error) {
	// 如果强制重新开始，直接返回 nil
	if restart {
		// 删除已有状态
		if albumInfo.ID != "" {
			stateMgr.Delete(albumInfo.ID)
		}
		return nil, ResumeChoiceRestart, nil
	}

	// 检查是否有已存在的下载状态
	existingState, err := stateMgr.Load(albumInfo.ID)
	if err != nil {
		c.logger.Warn("加载状态失败，将创建新状态", "error", err)
		return nil, ResumeChoiceRestart, nil
	}

	// 如果没有已存在的状态，或已完成数为0，创建新状态
	if existingState == nil || len(existingState.CompletedGUIDs) == 0 {
		return nil, ResumeChoiceRestart, nil
	}

	// 发现可恢复的下载任务
	if !skipConfirm {
		// 显示恢复提示
		c.showResumePrompt(existingState, notifyMgr)

		// 等待用户选择
		choice := c.getUserResumeChoice()
		switch choice {
		case "c":
			return existingState, ResumeChoiceResume, nil
		case "r":
			stateMgr.Delete(albumInfo.ID)
			return nil, ResumeChoiceRestart, nil
		case "x":
			return existingState, ResumeChoiceCancel, nil
		default:
			// 默认继续下载
			return existingState, ResumeChoiceResume, nil
		}
	}

	// 非交互模式：默认继续下载
	notifyMgr.Info("发现未完成的下载任务 (%d/%d)，自动继续下载", len(existingState.CompletedGUIDs), existingState.TotalCount)
	return existingState, ResumeChoiceResume, nil
}

// showResumePrompt 显示恢复提示
func (c *AlbumController) showResumePrompt(existingState *state.AlbumDownloadState, notifyMgr *notify.NotifyManager) {
	fmt.Println()
	fmt.Println("==========================================")
	fmt.Println("发现未完成的下载任务")
	fmt.Println("==========================================")
	fmt.Printf("专辑名称: %s\n", existingState.AlbumTitle)
	fmt.Printf("视频总数: %d\n", existingState.TotalCount)
	fmt.Printf("已完成:   %d\n", len(existingState.CompletedGUIDs))
	fmt.Printf("失败:     %d\n", len(existingState.FailedGUIDs))
	fmt.Printf("上次更新: %s\n", existingState.LastUpdated.Format("2006-01-02 15:04:05"))
	fmt.Println()
	fmt.Println("请选择操作:")
	fmt.Println("  [c] 继续下载 - 跳过已完成的视频")
	fmt.Println("  [r] 重新开始 - 删除进度，重新下载全部")
	fmt.Println("  [x] 取消退出")
	fmt.Println()
	fmt.Print("请输入选择 [c/r/x]: ")
}

// getUserResumeChoice 获取用户选择
func (c *AlbumController) getUserResumeChoice() string {
	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(strings.ToLower(input))

	switch input {
	case "c", "":
		return "c"
	case "r":
		return "r"
	case "x":
		return "x"
	default:
		return "c"
	}
}

// filterCompletedVideos 过滤已完成的视频
func (c *AlbumController) filterCompletedVideos(albumInfo *api.AlbumInfo, downloadState *state.AlbumDownloadState, stateMgr *state.AlbumStateManager, notifyMgr *notify.NotifyManager) *api.AlbumInfo {
	var pendingVideos []api.AlbumVideoItem
	skippedCount := 0

	// 确定输出目录：优先使用状态中保存的专辑目录，向后兼容旧状态文件
	outputDir := c.cfg.OutputDir
	if downloadState.AlbumOutputDir != "" {
		outputDir = downloadState.AlbumOutputDir
	}

	for _, video := range albumInfo.Videos {
		if stateMgr.IsVideoCompleted(downloadState.AlbumID, video.GUID) {
			// 记录跳过的视频，使用正确的输出目录
			outputPath := filepath.Join(outputDir, title.SafeName(video.Title)+".mp4")
			stateMgr.MarkVideoSkipped(downloadState.AlbumID, video.GUID, video.Title, outputPath)
			skippedCount++
			c.logger.Info("跳过已下载", "guid", video.GUID, "title", video.Title)
		} else {
			pendingVideos = append(pendingVideos, video)
		}
	}

	if skippedCount > 0 {
		notifyMgr.Info("跳过已下载: %d 个视频", skippedCount)
		notifyMgr.Info("待下载: %d 个视频", len(pendingVideos))
	}

	// 返回新的专辑信息，只包含待下载的视频
	return &api.AlbumInfo{
		ID:     albumInfo.ID,
		Title:  albumInfo.Title,
		Videos: pendingVideos,
		Total:  len(pendingVideos),
	}
}

// printVideoList 打印视频列表
func (c *AlbumController) printVideoList(album *api.AlbumInfo) {
	fmt.Println()
	fmt.Printf("专辑名称: %s\n", album.Title)
	fmt.Printf("总视频数: %d\n", len(album.Videos))
	fmt.Println()
	fmt.Println("序号\t标题\t\t\t\t发布时间\t时长")
	fmt.Println("----------------------------------------------------------------------")

	for i, video := range album.Videos {
		// 截取标题，防止过长（使用字符数而非字节数）
		videoTitle := video.Title
		if utils.RuneCount(videoTitle) > 30 {
			videoTitle = utils.TruncateString(videoTitle, 27) + "..."
		}
		fmt.Printf("%d\t%s\t%s\t%s\n", i+1, videoTitle, video.Time, video.Length)
	}

	// 打印每个视频的简介
	fmt.Println()
	fmt.Println("========== 视频简介 ==========")
	for i, video := range album.Videos {
		fmt.Printf("\n[%d] %s\n", i+1, video.Title)
		fmt.Printf("时长: %s\n", video.Length)
		if video.Brief != "" {
			fmt.Printf("简介: %s\n", video.Brief)
		}
	}
}

// downloadAlbum 批量下载专辑视频（流水线模式）
func (c *AlbumController) downloadAlbum(ctx context.Context, album *api.AlbumInfo, downloadState *state.AlbumDownloadState, stateMgr *state.AlbumStateManager, notifyMgr *notify.NotifyManager) error {
	notifyMgr.StartPhase("批量下载")

	// 计算跳过数量（从状态中获取）
	var skipCount int
	for _, result := range downloadState.Results {
		if result.Status == "skipped" {
			skipCount++
		}
	}

	totalOriginal := downloadState.TotalCount

	// 创建流水线配置
	pipelineCfg := &pipeline.VideoPipelineConfig{
		DownloadSlots:     c.cfg.AlbumDownloadSlots,
		ProcessWorkers:    c.cfg.AlbumProcessWorkers,
		DecryptWorkers:    c.cfg.DecryptWorkers,
		DownloadWorkers:   c.cfg.DownloadWorkers,
		FFmpegConcurrency: c.cfg.FFmpegConcurrency,
		Timeout:           c.cfg.Timeout,
		MaxRetries:        c.cfg.MaxRetries,
		UserAgent:         c.cfg.UserAgent,
		FFmpegPath:        c.cfg.FFmpegPath,
		NodePath:          c.cfg.NodePath,
		OutputDir:         c.cfg.OutputDir,
		TempDir:           c.cfg.TempDir,
		AlbumTitle:        album.Title, // 传递专辑标题，用于创建专辑子目录
	}

	// 创建流水线
	pl := pipeline.NewVideoPipeline(pipelineCfg, c.logger, notifyMgr)
	pl.SetStateManager(&pipelineStateAdapter{mgr: stateMgr, albumID: downloadState.AlbumID})

	// 用于跟踪已显示开始消息的任务
	shownStart := make(map[string]bool)
	// 用于跟踪已显示的进度百分比（每10%更新一次）
	shownProgress := make(map[string]int) // key: GUID, value: 上次显示的百分比
	var progressMu sync.Mutex

	// 设置进度回调
	pl.SetProgressCallback(func(pg *pipeline.PipelineProgress) {
		progressMu.Lock()
		defer progressMu.Unlock()

		// 显示正在下载的视频（显示开始消息和进度百分比）
		for _, t := range pg.Downloading {
			if !shownStart[t.GUID+"_start"] {
				shownStart[t.GUID+"_start"] = true
				notifyMgr.Info("[%d/%d] 开始下载: %s", t.Index, pg.Total, t.Title)
			}

			// 显示下载进度百分比（每10%显示一次）
			if t.Progress != nil && t.Progress.Stage == "download" {
				current := t.Progress.Percent
				lastShown := shownProgress[t.GUID]
				// 每10%显示一次，或者完成时显示
				if current >= lastShown+10 || current == 100 {
					shownProgress[t.GUID] = current
					notifyMgr.Info("[%d/%d] 下载进度: %d%%", t.Index, pg.Total, current)
				}
			}
		}

		// 显示正在处理的视频（仅显示开始消息）
		for _, t := range pg.Processing {
			if !shownStart[t.GUID+"_process"] {
				shownStart[t.GUID+"_process"] = true
				notifyMgr.Info("[%d/%d] 处理中: %s", t.Index, pg.Total, t.Title)
			}
		}

		// 显示完成的视频
		if pg.LastCompletedTask != nil {
			t := pg.LastCompletedTask
			if t.Status == pipeline.StatusCompleted && !shownStart[t.GUID+"_done"] {
				shownStart[t.GUID+"_done"] = true
				notifyMgr.Info("[%d/%d] 下载完成: %s", t.Index, pg.Total, t.Title)
			} else if t.Status == pipeline.StatusFailed && !shownStart[t.GUID+"_fail"] {
				shownStart[t.GUID+"_fail"] = true
				notifyMgr.Error("[%d/%d] 下载失败: %s - %s", t.Index, pg.Total, t.Title, t.Error)
			}
		}
	})

	// 启动流水线
	if err := pl.Start(ctx, album.Videos, downloadState.AlbumID); err != nil {
		// 上下文取消，标记专辑取消
		stateMgr.MarkAlbumCancelled(downloadState.AlbumID)
		return err
	}

	// 获取结果
	completed, failed, _ := pl.Wait()
	pl.Close()

	notifyMgr.CompletePhase("批量下载完成")

	// 显示最终统计
	c.showFinalSummary(downloadState, stateMgr, notifyMgr, completed, failed, skipCount, totalOriginal)

	return nil
}

// pipelineStateAdapter 适配器，将AlbumStateManager适配为pipeline.StateManager接口
type pipelineStateAdapter struct {
	mgr     *state.AlbumStateManager
	albumID string
}

func (a *pipelineStateAdapter) MarkVideoDownloading(albumID, guid, title string) {
	a.mgr.MarkVideoDownloading(a.albumID, guid, title)
}

func (a *pipelineStateAdapter) MarkVideoProcessing(albumID, guid, title string) {
	a.mgr.MarkVideoProcessing(a.albumID, guid, title)
}

func (a *pipelineStateAdapter) MarkVideoCompleted(albumID, guid, title, outputPath string) {
	a.mgr.MarkVideoCompleted(a.albumID, guid, title, outputPath)
}

func (a *pipelineStateAdapter) MarkVideoFailed(albumID, guid, title, errMsg string) {
	a.mgr.MarkVideoFailed(a.albumID, guid, title, errMsg)
}

func (a *pipelineStateAdapter) IsVideoCompleted(albumID, guid string) bool {
	return a.mgr.IsVideoCompleted(a.albumID, guid)
}

func (a *pipelineStateAdapter) Save() error {
	return nil
}

// showFinalSummary 显示最终统计
func (c *AlbumController) showFinalSummary(downloadState *state.AlbumDownloadState, stateMgr *state.AlbumStateManager, notifyMgr *notify.NotifyManager, successCount, failCount, skipCount, totalOriginal int) {
	// 加载最终状态
	finalState, _ := stateMgr.Load(downloadState.AlbumID)

	fmt.Println()
	fmt.Println("==========================================")
	fmt.Println("专辑下载完成")
	fmt.Println("==========================================")
	fmt.Printf("专辑名称: %s\n", downloadState.AlbumTitle)
	fmt.Printf("总视频数: %d\n", totalOriginal)
	fmt.Printf("本次成功: %d\n", successCount)
	fmt.Printf("续传跳过: %d\n", skipCount)
	fmt.Printf("下载失败: %d\n", failCount)

	// 显示失败列表
	if finalState != nil && len(finalState.FailedGUIDs) > 0 {
		fmt.Println()
		fmt.Println("失败列表:")
		for _, result := range finalState.Results {
			if result.Status == "failed" {
				fmt.Printf("  - %s: %s\n", result.Title, result.Error)
			}
		}
	}

	fmt.Printf("\n输出目录: %s\n", c.cfg.OutputDir)
	fmt.Println("==========================================")

	// 显示耗时
	if finalState != nil && !finalState.EndTime.IsZero() {
		duration := finalState.EndTime.Sub(finalState.StartTime)
		fmt.Printf("总耗时: %s\n", formatDuration(duration))
	}
}

// formatDuration 格式化耗时
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.0f秒", d.Seconds())
	}
	if d < time.Hour {
		return fmt.Sprintf("%.0f分钟", d.Minutes())
	}
	return fmt.Sprintf("%.1f小时", d.Hours())
}

// ListPendingDownloads 列出所有未完成的下载任务
func (c *AlbumController) ListPendingDownloads() error {
	stateMgr := state.NewAlbumStateManager(c.cfg.TempDir, c.logger)

	states, err := stateMgr.List()
	if err != nil {
		return fmt.Errorf("获取下载状态失败: %w", err)
	}

	if len(states) == 0 {
		fmt.Println("没有未完成的下载任务")
		return nil
	}

	fmt.Println()
	fmt.Println("==========================================")
	fmt.Println("未完成的下载任务")
	fmt.Println("==========================================")

	for _, s := range states {
		fmt.Printf("\n专辑: %s\n", s.AlbumTitle)
		fmt.Printf("进度: %d/%d (%.0f%%)\n", len(s.CompletedGUIDs), s.TotalCount, float64(len(s.CompletedGUIDs))/float64(s.TotalCount)*100)
		fmt.Printf("失败: %d\n", len(s.FailedGUIDs))
		fmt.Printf("来源: %s\n", s.SourceURL)
		fmt.Printf("更新: %s\n", s.LastUpdated.Format("2006-01-02 15:04:05"))
	}

	fmt.Println()
	fmt.Println("==========================================")
	return nil
}

// ClearCompletedStates 清理已完成的下载状态
func (c *AlbumController) ClearCompletedStates() error {
	stateMgr := state.NewAlbumStateManager(c.cfg.TempDir, c.logger)

	if err := stateMgr.ClearCompleted(); err != nil {
		return fmt.Errorf("清理状态失败: %w", err)
	}

	fmt.Println("已清理完成的下载状态")
	return nil
}