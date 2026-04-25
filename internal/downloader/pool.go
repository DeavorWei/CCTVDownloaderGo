package downloader

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/CCTVDownloadGo/cctvdown/internal/api"
	"github.com/CCTVDownloadGo/cctvdown/internal/notify"
)

// ProgressCallback 进度回调函数类型
// stage: 阶段名称, current: 当前完成数, total: 总数, message: 附加信息
type ProgressCallback func(stage string, current, total int64, message string)

// DownloadPool 下载协程池
type DownloadPool struct {
	workers    int
	maxRetries int
	timeout    time.Duration
	logger     *slog.Logger

	taskChan   chan *Segment
	resultChan chan *DownloadResult
	wg         sync.WaitGroup
	ctx        context.Context
	cancel     context.CancelFunc

	totalCount     int64
	completedCount int64
	failedCount    int64

	progressCallback ProgressCallback // 进度回调
}

// DownloadResult 下载结果
type DownloadResult struct {
	Index   int
	Success bool
	Path    string
	Error   error
}

// NewDownloadPool 创建下载协程池
func NewDownloadPool(workers, maxRetries int, timeout time.Duration, logger *slog.Logger) *DownloadPool {
	return &DownloadPool{
		workers:    workers,
		maxRetries: maxRetries,
		timeout:    timeout,
		logger:     logger,
	}
}

// SetProgressCallback 设置进度回调
func (p *DownloadPool) SetProgressCallback(callback ProgressCallback) {
	p.progressCallback = callback
}

// Start 启动下载池
func (p *DownloadPool) Start(ctx context.Context, segments []*Segment) <-chan *DownloadResult {
	p.ctx, p.cancel = context.WithCancel(ctx)
	p.taskChan = make(chan *Segment, len(segments))
	p.resultChan = make(chan *DownloadResult, len(segments))
	p.totalCount = int64(len(segments))
	p.completedCount = 0
	p.failedCount = 0

	// 启动worker
	for i := 0; i < p.workers; i++ {
		p.wg.Add(1)
		go p.worker()
	}

	// 提交任务
	go func() {
	Loop:
		for _, seg := range segments {
			select {
			case p.taskChan <- seg:
			case <-p.ctx.Done():
				break Loop
			}
		}
		close(p.taskChan)
	}()

	// 等待完成并关闭结果通道
	go func() {
		p.wg.Wait()
		close(p.resultChan)
	}()

	return p.resultChan
}

// Stop 停止下载池
func (p *DownloadPool) Stop() {
	if p.cancel != nil {
		p.cancel()
	}
}

// Progress 获取下载进度
func (p *DownloadPool) Progress() (total, completed, failed int64) {
	return p.totalCount, atomic.LoadInt64(&p.completedCount), atomic.LoadInt64(&p.failedCount)
}

// worker 下载工作协程
func (p *DownloadPool) worker() {
	defer p.wg.Done()

	for seg := range p.taskChan {
		select {
		case <-p.ctx.Done():
			return
		default:
		}

		err := DownloadSegment(seg, p.timeout, p.maxRetries)

		result := &DownloadResult{
			Index:   seg.Index,
			Success: err == nil,
			Path:    seg.OutputPath,
			Error:   err,
		}

		if err != nil {
			atomic.AddInt64(&p.failedCount, 1)
			p.logger.Warn("分片下载失败", "index", seg.Index, "error", err)
		} else {
			completed := atomic.AddInt64(&p.completedCount, 1)
			p.logger.Log(context.Background(), notify.LevelVerbose, "分片下载完成", "index", seg.Index, "path", seg.OutputPath)

			// 触发进度回调
			if p.progressCallback != nil {
				p.progressCallback("download", completed, p.totalCount, fmt.Sprintf("分片 %d", seg.Index))
			}
		}

		// 失败时也触发回调（用于更新失败计数）
		if err != nil && p.progressCallback != nil {
			failed := atomic.LoadInt64(&p.failedCount)
			p.progressCallback("download_failed", failed, p.totalCount, fmt.Sprintf("分片 %d 失败", seg.Index))
		}

		select {
		case p.resultChan <- result:
		case <-p.ctx.Done():
			return
		}
	}
}

// Downloader 下载器
type Downloader struct {
	workers          int
	maxRetries       int
	timeout          time.Duration
	userAgent        string
	logger           *slog.Logger
	progressCallback ProgressCallback // 进度回调
}

// NewDownloader 创建下载器
func NewDownloader(workers, maxRetries int, timeout time.Duration, userAgent string, logger *slog.Logger) *Downloader {
	return &Downloader{
		workers:    workers,
		maxRetries: maxRetries,
		timeout:    timeout,
		userAgent:  userAgent,
		logger:     logger,
	}
}

// SetProgressCallback 设置进度回调
func (d *Downloader) SetProgressCallback(callback ProgressCallback) {
	d.progressCallback = callback
}

// ParseM3U8 解析M3U8并返回分片列表
func (d *Downloader) ParseM3U8(m3u8URL string) ([]*Segment, error) {
	d.logger.Debug("开始获取M3U8播放列表", "url", m3u8URL)

	// 下载M3U8内容
	m3u8Content, err := d.fetchM3U8(m3u8URL)
	if err != nil {
		d.logger.Error("获取M3U8失败", "url", m3u8URL, "error", err)
		return nil, fmt.Errorf("获取M3U8失败: %w", err)
	}

	d.logger.Debug("M3U8内容获取成功", "url", m3u8URL, "content_length", len(m3u8Content))
	d.logger.Log(context.Background(), notify.LevelVerbose, "M3U8原始内容", "url", m3u8URL, "content", truncateContent(m3u8Content, 500))

	// 解析M3U8
	info, err := ParseM3U8(m3u8Content, m3u8URL)
	if err != nil {
		d.logger.Error("解析M3U8失败", "url", m3u8URL, "error", err, "content_preview", truncateContent(m3u8Content, 200))
		return nil, fmt.Errorf("解析M3U8失败: %w", err)
	}

	d.logger.Debug("M3U8解析结果", "url", m3u8URL, "is_master", info.IsMaster, "segments_count", len(info.Segments), "variants_count", len(info.Variants), "target_duration", info.TargetDur)

	// 如果是主播放列表，选择最佳流并重新解析
	if info.IsMaster && len(info.Variants) > 0 {
		d.logger.Debug("检测到主播放列表，需要选择最佳流", "variants", info.Variants)

		// 使用 CCTVHLSBestParser 基于评分公式选择最佳流
		parser := api.NewCCTVHLSBestParser()
		bestURL, err := parser.Best(m3u8Content)
		if err != nil {
			d.logger.Warn("最佳流选择失败，回退到最后一个变体", "error", err, "fallback_url", info.Variants[len(info.Variants)-1])
			bestURL = info.Variants[len(info.Variants)-1]
		} else {
			// 将相对URL转换为绝对URL
			bestURL = ResolveURL(m3u8URL, bestURL)
		}
		d.logger.Info("选择最佳流", "url", bestURL)

		m3u8Content, err = d.fetchM3U8(bestURL)
		if err != nil {
			d.logger.Error("获取最佳流M3U8失败", "url", bestURL, "error", err)
			return nil, fmt.Errorf("获取最佳流M3U8失败: %w", err)
		}

		d.logger.Debug("最佳流M3U8内容获取成功", "url", bestURL, "content_length", len(m3u8Content))
		d.logger.Log(context.Background(), notify.LevelVerbose, "最佳流M3U8原始内容", "url", bestURL, "content", truncateContent(m3u8Content, 500))

		info, err = ParseM3U8(m3u8Content, bestURL)
		if err != nil {
			d.logger.Error("解析最佳流M3U8失败", "url", bestURL, "error", err)
			return nil, fmt.Errorf("解析最佳流M3U8失败: %w", err)
		}

		d.logger.Debug("最佳流M3U8解析结果", "url", bestURL, "segments_count", len(info.Segments), "target_duration", info.TargetDur)
	}

	d.logger.Info("M3U8解析完成", "url", m3u8URL, "segments_count", len(info.Segments))
	return info.Segments, nil
}

// DownloadSegments 并发下载所有分片
func (d *Downloader) DownloadSegments(ctx context.Context, segments []*Segment, tsDir string) error {
	// 设置输出路径
	for _, seg := range segments {
		seg.OutputPath = GetSegmentOutputPath(tsDir, seg.Index)
	}

	pool := NewDownloadPool(d.workers, d.maxRetries, d.timeout, d.logger)
	pool.SetProgressCallback(d.progressCallback)
	resultChan := pool.Start(ctx, segments)

	var failCount int
	for result := range resultChan {
		if !result.Success {
			failCount++
		}
	}

	if failCount > 0 {
		d.logger.Warn("部分分片下载失败", "failed", failCount, "total", len(segments))
	}

	if failCount == len(segments) {
		return fmt.Errorf("所有分片下载失败")
	}

	return nil
}

// fetchM3U8 获取M3U8内容
func (d *Downloader) fetchM3U8(url string) (string, error) {
	d.logger.Debug("发起HTTP请求获取M3U8", "url", url, "timeout", d.timeout, "user_agent", d.userAgent)

	// 创建HTTP请求，设置必要的请求头
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		d.logger.Error("创建HTTP请求失败", "url", url, "error", err)
		return "", fmt.Errorf("创建请求失败: %w", err)
	}

	// 设置请求头，模拟浏览器访问
	req.Header.Set("User-Agent", d.userAgent)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	req.Header.Set("Referer", "https://tv.cctv.com/")
	req.Header.Set("Connection", "keep-alive")

	d.logger.Log(context.Background(), notify.LevelVerbose, "HTTP请求头详情", "url", url, "headers", formatHeaders(req.Header))

	client := &http.Client{Timeout: d.timeout}
	resp, err := client.Do(req)
	if err != nil {
		d.logger.Error("HTTP请求执行失败", "url", url, "error", err)
		return "", fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	d.logger.Debug("HTTP响应收到", "url", url, "status_code", resp.StatusCode, "content_length", resp.ContentLength)
	d.logger.Log(context.Background(), notify.LevelVerbose, "HTTP响应头详情", "url", url, "headers", formatHeaders(resp.Header))

	if resp.StatusCode != http.StatusOK {
		// 尝试读取响应体以获取更多错误信息
		body, _ := io.ReadAll(resp.Body)
		d.logger.Error("HTTP请求返回非200状态码",
			"url", url,
			"status_code", resp.StatusCode,
			"status_text", resp.Status,
			"response_body_preview", truncateContent(string(body), 500),
			"response_headers", formatHeaders(resp.Header))
		return "", fmt.Errorf("HTTP状态码: %d, 响应: %s", resp.StatusCode, truncateContent(string(body), 200))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		d.logger.Error("读取响应体失败", "url", url, "error", err)
		return "", fmt.Errorf("读取响应失败: %w", err)
	}

	d.logger.Debug("M3U8内容读取成功", "url", url, "body_length", len(body))
	return string(body), nil
}

// truncateContent 截断内容用于日志输出
func truncateContent(content string, maxLen int) string {
	if len(content) <= maxLen {
		return content
	}
	return content[:maxLen] + "...(截断)"
}

// formatHeaders 格式化HTTP头部用于日志输出
func formatHeaders(headers http.Header) string {
	var sb strings.Builder
	for key, values := range headers {
		sb.WriteString(key)
		sb.WriteString(": ")
		sb.WriteString(strings.Join(values, ", "))
		sb.WriteString("; ")
	}
	return sb.String()
}
