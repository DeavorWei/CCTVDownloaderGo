package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/CCTVDownloadGo/cctvdown/internal/api"
	"github.com/CCTVDownloadGo/cctvdown/internal/downloader"
	"github.com/CCTVDownloadGo/cctvdown/internal/notify"
	"github.com/CCTVDownloadGo/cctvdown/internal/processor"
	"github.com/CCTVDownloadGo/cctvdown/internal/title"
	"github.com/CCTVDownloadGo/cctvdown/internal/utils"
)

// TaskStatus 视频任务状态
type TaskStatus int

const (
	StatusPending     TaskStatus = iota // 待下载
	StatusDownloading                   // 下载中
	StatusDownloaded                    // 已下载，待处理
	StatusProcessing                    // 处理中（解密/合并）
	StatusCompleted                     // 已完成
	StatusFailed                        // 失败
	StatusSkipped                       // 跳过（已存在）
)

// String 返回状态字符串
func (s TaskStatus) String() string {
	switch s {
	case StatusPending:
		return "pending"
	case StatusDownloading:
		return "downloading"
	case StatusDownloaded:
		return "downloaded"
	case StatusProcessing:
		return "processing"
	case StatusCompleted:
		return "completed"
	case StatusFailed:
		return "failed"
	case StatusSkipped:
		return "skipped"
	default:
		return "unknown"
	}
}

// VideoTask 视频任务
type VideoTask struct {
	Index      int            `json:"index"`       // 视频序号（从1开始）
	GUID       string         `json:"guid"`        // 视频GUID
	Title      string         `json:"title"`       // 视频标题
	Time       string         `json:"time"`        // 发布时间
	Length     string         `json:"length"`      // 视频时长
	Brief      string         `json:"brief"`       // 视频简介
	TempDir    string         `json:"temp_dir"`    // 临时工作目录
	OutputPath string         `json:"output_path"` // 输出文件路径
	Status     TaskStatus     `json:"status"`      // 任务状态
	Error      string         `json:"error"`       // 错误信息
	StartTime  time.Time      `json:"start_time"`  // 开始时间
	EndTime    time.Time      `json:"end_time"`    // 结束时间
	Progress   *VideoProgress `json:"progress"`    // 进度信息
	VideoInfo  *api.VideoInfo `json:"video_info"`  // 视频详细信息
	Segments   []*downloader.Segment `json:"-"`    // TS分片列表（不序列化）
}

// VideoProgress 视频进度信息
type VideoProgress struct {
	Stage   string `json:"stage"`   // 当前阶段: download/decrypt/remux/merge
	Current int64  `json:"current"` // 当前进度
	Total   int64  `json:"total"`   // 总数
	Percent int    `json:"percent"` // 百分比
}

// NewVideoTask 从专辑视频项创建视频任务
func NewVideoTask(index int, video api.AlbumVideoItem, tempDir, outputPath string) *VideoTask {
	return &VideoTask{
		Index:      index,
		GUID:       video.GUID,
		Title:      video.Title,
		Time:       video.Time,
		Length:     video.Length,
		Brief:      video.Brief,
		TempDir:    tempDir,
		OutputPath: outputPath,
		Status:     StatusPending,
	}
}

// SetProgress 设置进度
func (t *VideoTask) SetProgress(stage string, current, total int64) {
	if t.Progress == nil {
		t.Progress = &VideoProgress{}
	}
	t.Progress.Stage = stage
	t.Progress.Current = current
	t.Progress.Total = total
	if total > 0 {
		t.Progress.Percent = int(current * 100 / total)
	}
}

// Duration 返回任务耗时
func (t *VideoTask) Duration() time.Duration {
	if t.StartTime.IsZero() {
		return 0
	}
	if t.EndTime.IsZero() {
		return time.Since(t.StartTime)
	}
	return t.EndTime.Sub(t.StartTime)
}

// VideoTaskResult 视频任务结果
type VideoTaskResult struct {
	Task    *VideoTask
	Success bool
	Error   error
}

// PipelineProgress 流水线进度
type PipelineProgress struct {
	Total            int          `json:"total"`             // 总视频数
	Completed        int          `json:"completed"`         // 已完成数
	Failed           int          `json:"failed"`            // 失败数
	Skipped          int          `json:"skipped"`           // 跳过数
	Downloading      []*VideoTask `json:"downloading"`       // 正在下载的任务
	Processing       []*VideoTask `json:"processing"`        // 正在处理的任务
	Pending          int          `json:"pending"`           // 待处理数
	StartTime        time.Time    `json:"start_time"`        // 开始时间
	LastCompletedTask *VideoTask  `json:"last_completed"`    // 最后完成的任务（用于显示完成消息）
}

// Percent 返回完成百分比
func (p *PipelineProgress) Percent() int {
	if p.Total == 0 {
		return 0
	}
	return (p.Completed + p.Failed + p.Skipped) * 100 / p.Total
}

// ETA 返回预计剩余时间
func (p *PipelineProgress) ETA() time.Duration {
	if p.StartTime.IsZero() || p.Completed == 0 {
		return 0
	}
	elapsed := time.Since(p.StartTime)
	avgTime := elapsed / time.Duration(p.Completed)
	remaining := p.Total - p.Completed - p.Failed - p.Skipped
	return avgTime * time.Duration(remaining)
}

// StateManager 状态管理器接口
type StateManager interface {
	MarkVideoDownloading(albumID, guid, title string)
	MarkVideoProcessing(albumID, guid, title string)
	MarkVideoCompleted(albumID, guid, title, outputPath string)
	MarkVideoFailed(albumID, guid, title, errMsg string)
	IsVideoCompleted(albumID, guid string) bool
	Save() error
}

// VideoPipelineConfig 流水线配置
type VideoPipelineConfig struct {
	DownloadSlots     int           // 同时下载的视频数
	ProcessWorkers    int           // 处理worker数
	DecryptWorkers    int           // 解密并行进程数
	DownloadWorkers   int           // 每个视频的下载并发数
	FFmpegConcurrency int           // FFmpeg并发数
	Timeout           time.Duration // 超时时间
	MaxRetries        int           // 最大重试次数
	UserAgent         string        // User-Agent
	FFmpegPath        string        // FFmpeg路径
	NodePath          string        // Node.js路径
	OutputDir         string        // 输出目录
	TempDir           string        // 临时目录
	AlbumTitle        string        // 专辑标题，用于创建专辑子目录
}

// VideoPipeline 视频级流水线管理器
type VideoPipeline struct {
	cfg        *VideoPipelineConfig
	logger     *slog.Logger
	cctvClient *api.CCTVClient
	downloader *downloader.Downloader
	processor  *processor.Processor
	stateMgr   StateManager
	notifyMgr  *notify.NotifyManager
	cleanup    *utils.CleanupManager

	// 通道
	downloadQueue chan *VideoTask       // 待下载队列
	processQueue  chan *VideoTask       // 待处理队列（已下载完成）
	resultQueue   chan *VideoTaskResult // 结果队列

	// 进度追踪
	progress   *PipelineProgress
	progressMu sync.RWMutex
	progressCb func(*PipelineProgress)

	// 控制
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewVideoPipeline 创建视频流水线
func NewVideoPipeline(cfg *VideoPipelineConfig, logger *slog.Logger, notifyMgr *notify.NotifyManager) *VideoPipeline {
	return &VideoPipeline{
		cfg:           cfg,
		logger:        logger,
		cctvClient:    api.NewCCTVClient("", cfg.Timeout, logger),
		downloadQueue: make(chan *VideoTask, cfg.DownloadSlots*2),
		processQueue:  make(chan *VideoTask, cfg.ProcessWorkers*2),
		resultQueue:   make(chan *VideoTaskResult, 100),
		progress:      &PipelineProgress{},
		notifyMgr:     notifyMgr,
		cleanup:       utils.NewCleanupManager(logger),
	}
}

// SetStateManager 设置状态管理器
func (p *VideoPipeline) SetStateManager(mgr StateManager) {
	p.stateMgr = mgr
}

// SetProgressCallback 设置进度回调
func (p *VideoPipeline) SetProgressCallback(cb func(*PipelineProgress)) {
	p.progressCb = cb
}

// Start 启动流水线
func (p *VideoPipeline) Start(ctx context.Context, videos []api.AlbumVideoItem, albumID string) error {
	p.ctx, p.cancel = context.WithCancel(ctx)

	// 初始化下载器和处理器
	p.downloader = downloader.NewDownloader(p.cfg.DownloadWorkers, p.cfg.MaxRetries, p.cfg.Timeout, p.cfg.UserAgent, p.logger)
	p.processor = processor.NewProcessor(p.cfg.FFmpegPath, p.cfg.NodePath, p.cfg.FFmpegConcurrency, p.logger)
	p.processor.SetDecryptWorkers(p.cfg.DecryptWorkers)

	// 计算实际输出目录（如果设置了专辑标题，创建专辑子目录）
	actualOutputDir := p.cfg.OutputDir
	if p.cfg.AlbumTitle != "" {
		albumDirName := title.SafeName(p.cfg.AlbumTitle)
		actualOutputDir = filepath.Join(p.cfg.OutputDir, albumDirName)
		if err := os.MkdirAll(actualOutputDir, 0755); err != nil {
			return fmt.Errorf("创建专辑目录失败: %w", err)
		}
		p.logger.Info("创建专辑输出目录", "path", actualOutputDir, "album", p.cfg.AlbumTitle)
	}

	// 初始化进度
	p.progress = &PipelineProgress{
		Total:     len(videos),
		StartTime: time.Now(),
	}

	// 启动下载worker池
	for i := 0; i < p.cfg.DownloadSlots; i++ {
		p.wg.Add(1)
		go p.downloadWorker(i)
	}

	// 启动处理worker池
	for i := 0; i < p.cfg.ProcessWorkers; i++ {
		p.wg.Add(1)
		go p.processWorker(i)
	}

	// 启动结果收集器
	p.wg.Add(1)
	go p.resultCollector()

	// 提交所有视频任务到下载队列
	go func() {
		for idx, video := range videos {
			tempDir := filepath.Join(p.cfg.TempDir, fmt.Sprintf("video_%03d_%s", idx+1, video.GUID))
			// 使用实际输出目录（包含专辑子目录）
			outputPath := filepath.Join(actualOutputDir, title.SafeName(video.Title)+".mp4")

			task := NewVideoTask(idx+1, video, tempDir, outputPath)

			// 检查是否已完成
			if p.stateMgr != nil && p.stateMgr.IsVideoCompleted(albumID, video.GUID) {
				task.Status = StatusSkipped
				p.resultQueue <- &VideoTaskResult{Task: task, Success: true}
				continue
			}

			select {
			case p.downloadQueue <- task:
			case <-p.ctx.Done():
				return
			}
		}
		close(p.downloadQueue)
	}()

	// 等待所有任务完成
	p.wg.Wait()

	return nil
}

// Stop 停止流水线
func (p *VideoPipeline) Stop() {
	if p.cancel != nil {
		p.cancel()
	}
}

// Wait 等待完成并返回结果
func (p *VideoPipeline) Wait() (completed, failed, skipped int) {
	return p.progress.Completed, p.progress.Failed, p.progress.Skipped
}

// downloadWorker 下载worker
func (p *VideoPipeline) downloadWorker(id int) {
	defer p.wg.Done()

	for task := range p.downloadQueue {
		select {
		case <-p.ctx.Done():
			return
		default:
		}

		// 更新状态
		task.Status = StatusDownloading
		task.StartTime = time.Now()
		if p.stateMgr != nil {
			p.stateMgr.MarkVideoDownloading("", task.GUID, task.Title)
		}

		// 更新进度
		p.updateProgress(func(pg *PipelineProgress) {
			pg.Downloading = append(pg.Downloading, task)
		})

		p.logger.Info("开始下载视频", "worker", id, "index", task.Index, "title", task.Title)

		// 执行下载
		err := p.downloadVideo(task)
		if err != nil {
			p.logger.Error("下载视频失败", "worker", id, "index", task.Index, "error", err)
			task.Error = err.Error()
			task.Status = StatusFailed
			p.resultQueue <- &VideoTaskResult{Task: task, Success: false, Error: err}
			continue
		}

		// 下载完成，进入处理队列
		task.Status = StatusDownloaded
		p.logger.Info("视频下载完成", "worker", id, "index", task.Index, "title", task.Title)

		// 从下载中列表移除
		p.updateProgress(func(pg *PipelineProgress) {
			for i, t := range pg.Downloading {
				if t.GUID == task.GUID {
					pg.Downloading = append(pg.Downloading[:i], pg.Downloading[i+1:]...)
					break
				}
			}
		})

		select {
		case p.processQueue <- task:
		case <-p.ctx.Done():
			return
		}
	}
}

// processWorker 处理worker（解密+合并）
func (p *VideoPipeline) processWorker(id int) {
	defer p.wg.Done()

	for task := range p.processQueue {
		select {
		case <-p.ctx.Done():
			return
		default:
		}

		// 更新状态
		task.Status = StatusProcessing
		if p.stateMgr != nil {
			p.stateMgr.MarkVideoProcessing("", task.GUID, task.Title)
		}

		// 更新进度
		p.updateProgress(func(pg *PipelineProgress) {
			pg.Processing = append(pg.Processing, task)
		})

		p.logger.Info("开始处理视频", "worker", id, "index", task.Index, "title", task.Title)

		// 执行处理
		err := p.processVideo(task)
		if err != nil {
			p.logger.Error("处理视频失败", "worker", id, "index", task.Index, "error", err)
			task.Error = err.Error()
			task.Status = StatusFailed
		} else {
			task.Status = StatusCompleted
			task.EndTime = time.Now()
			p.logger.Info("视频处理完成", "worker", id, "index", task.Index, "title", task.Title)
		}

		// 从处理中列表移除
		p.updateProgress(func(pg *PipelineProgress) {
			for i, t := range pg.Processing {
				if t.GUID == task.GUID {
					pg.Processing = append(pg.Processing[:i], pg.Processing[i+1:]...)
					break
				}
			}
		})

		p.resultQueue <- &VideoTaskResult{Task: task, Success: err == nil, Error: err}
	}
}

// resultCollector 结果收集器
func (p *VideoPipeline) resultCollector() {
	defer p.wg.Done()

	for result := range p.resultQueue {
		task := result.Task

		p.updateProgress(func(pg *PipelineProgress) {
			switch task.Status {
			case StatusCompleted:
				pg.Completed++
			case StatusFailed:
				pg.Failed++
			case StatusSkipped:
				pg.Skipped++
			}
			pg.Pending = pg.Total - pg.Completed - pg.Failed - pg.Skipped
		})

		// 更新状态管理器
		if p.stateMgr != nil {
			switch task.Status {
			case StatusCompleted:
				p.stateMgr.MarkVideoCompleted("", task.GUID, task.Title, task.OutputPath)
			case StatusFailed:
				p.stateMgr.MarkVideoFailed("", task.GUID, task.Title, task.Error)
			}
		}

		// 触发进度回调（带任务结果）
		if p.progressCb != nil {
			p.progressCb(p.getProgressWithTask(task))
		}

		// 清理临时目录
		if task.Status == StatusCompleted || task.Status == StatusFailed {
			p.cleanup.Register(task.TempDir)
			p.cleanup.Cleanup()
		}
	}
}

// downloadVideo 下载单个视频
func (p *VideoPipeline) downloadVideo(task *VideoTask) error {
	// 获取视频信息
	videoInfo, err := p.cctvClient.GetVideoInfo(task.GUID)
	if err != nil {
		return fmt.Errorf("获取视频信息失败: %w", err)
	}

	// 处理标题
	videoTitle := videoInfo.RawTitle
	if videoTitle == "" {
		videoTitle = task.Title
	}
	videoInfo.Title = title.SafeName(videoTitle)
	task.VideoInfo = videoInfo
	// 保留原有的目录结构（包含专辑子目录），只更新文件名
	outputDir := filepath.Dir(task.OutputPath)
	task.OutputPath = filepath.Join(outputDir, videoInfo.Title+".mp4")

	// 创建临时目录
	if err := os.MkdirAll(task.TempDir, 0755); err != nil {
		return fmt.Errorf("创建临时目录失败: %w", err)
	}

	// 解析M3U8
	segments, err := p.downloader.ParseM3U8(videoInfo.M3U8URL)
	if err != nil {
		return fmt.Errorf("解析M3U8失败: %w", err)
	}
	task.Segments = segments

	// 设置进度回调
	p.downloader.SetProgressCallback(func(stage string, current, total int64, message string) {
		task.SetProgress("download", current, total)
		if p.progressCb != nil {
			p.progressCb(p.getProgress())
		}
	})

	// 下载分片
	tsDir := filepath.Join(task.TempDir, "ts")
	if err := os.MkdirAll(tsDir, 0755); err != nil {
		return fmt.Errorf("创建TS目录失败: %w", err)
	}

	if err := p.downloader.DownloadSegments(p.ctx, segments, tsDir); err != nil {
		return fmt.Errorf("下载分片失败: %w", err)
	}

	return nil
}

// processVideo 处理单个视频（解密+合并）
func (p *VideoPipeline) processVideo(task *VideoTask) error {
	if task.VideoInfo == nil || len(task.Segments) == 0 {
		return fmt.Errorf("视频信息或分片列表为空")
	}

	tsDir := filepath.Join(task.TempDir, "ts")

	// 设置进度回调
	p.processor.SetProgressCallback(func(stage string, current, total int64, message string) {
		task.SetProgress(stage, current, total)
		if p.progressCb != nil {
			p.progressCb(p.getProgress())
		}
	})

	// 根据是否加密选择处理方式
	if task.VideoInfo.IsEncrypted {
		if err := p.processor.ProcessEncrypted(p.ctx, tsDir, task.Segments, task.OutputPath); err != nil {
			return fmt.Errorf("处理加密视频失败: %w", err)
		}
	} else {
		if err := p.processor.DownloadDirect(p.ctx, task.VideoInfo.M3U8URL, task.OutputPath); err != nil {
			return fmt.Errorf("直接下载失败: %w", err)
		}
	}

	return nil
}

// updateProgress 更新进度（线程安全）
func (p *VideoPipeline) updateProgress(fn func(*PipelineProgress)) {
	p.progressMu.Lock()
	defer p.progressMu.Unlock()
	fn(p.progress)
}

// getProgress 获取进度副本
func (p *VideoPipeline) getProgress() *PipelineProgress {
	p.progressMu.RLock()
	defer p.progressMu.RUnlock()

	// 返回副本
	pg := &PipelineProgress{
		Total:     p.progress.Total,
		Completed: p.progress.Completed,
		Failed:    p.progress.Failed,
		Skipped:   p.progress.Skipped,
		Pending:   p.progress.Pending,
		StartTime: p.progress.StartTime,
	}

	// 复制正在处理的任务列表
	for _, t := range p.progress.Downloading {
		pg.Downloading = append(pg.Downloading, t)
	}
	for _, t := range p.progress.Processing {
		pg.Processing = append(pg.Processing, t)
	}

	return pg
}

// getProgressWithTask 获取带有最后完成任务的进度副本
func (p *VideoPipeline) getProgressWithTask(task *VideoTask) *PipelineProgress {
	p.progressMu.RLock()
	defer p.progressMu.RUnlock()

	// 返回副本
	pg := &PipelineProgress{
		Total:             p.progress.Total,
		Completed:         p.progress.Completed,
		Failed:            p.progress.Failed,
		Skipped:           p.progress.Skipped,
		Pending:           p.progress.Pending,
		StartTime:         p.progress.StartTime,
		LastCompletedTask: task,
	}

	// 复制正在处理的任务列表
	for _, t := range p.progress.Downloading {
		pg.Downloading = append(pg.Downloading, t)
	}
	for _, t := range p.progress.Processing {
		pg.Processing = append(pg.Processing, t)
	}

	return pg
}

// GetProgress 获取当前进度
func (p *VideoPipeline) GetProgress() *PipelineProgress {
	return p.getProgress()
}

// Close 关闭流水线，清理资源
func (p *VideoPipeline) Close() {
	p.Stop()
	p.cleanup.Cleanup()
}
