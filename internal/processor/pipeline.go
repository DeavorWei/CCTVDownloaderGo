package processor

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/CCTVDownloadGo/cctvdown/internal/downloader"
	"github.com/CCTVDownloadGo/cctvdown/internal/ffmpeg"
	"golang.org/x/sync/errgroup"
)

// ProgressCallback 进度回调函数类型
type ProgressCallback func(stage string, current, total int64, message string)

// Processor 视频处理器
type Processor struct {
	ffmpegPath        string
	nodePath          string
	ffmpegConcurrency int
	decryptWorkers    int              // 解密并行进程数
	logger            *slog.Logger
	processManager    *ffmpeg.ProcessManager
	progressCallback  ProgressCallback // 进度回调
}

// NewProcessor 创建处理器
func NewProcessor(ffmpegPath, nodePath string, ffmpegConcurrency int, logger *slog.Logger) *Processor {
	return &Processor{
		ffmpegPath:        ffmpegPath,
		nodePath:          nodePath,
		ffmpegConcurrency: ffmpegConcurrency,
		decryptWorkers:    8, // 默认8线程
		logger:            logger,
		processManager:    ffmpeg.NewProcessManager(int64(ffmpegConcurrency), logger),
	}
}

// SetProgressCallback 设置进度回调
func (p *Processor) SetProgressCallback(callback ProgressCallback) {
	p.progressCallback = callback
}

// SetDecryptWorkers 设置解密并行进程数
func (p *Processor) SetDecryptWorkers(workers int) {
	if workers > 0 {
		p.decryptWorkers = workers
	}
}

// updateProgress 更新进度（内部辅助方法）
func (p *Processor) updateProgress(stage string, current, total int64, message string) {
	if p.progressCallback != nil {
		p.progressCallback(stage, current, total, message)
	}
}

// ProcessEncrypted 处理加密流
// 使用errgroup实现5阶段并行流水线：demux→解密→remux→有序合并→最终合并
func (p *Processor) ProcessEncrypted(ctx context.Context, tsDir string, segments []*downloader.Segment, outputPath string) error {
	p.logger.Info("开始处理加密流（流水线模式）", "segments", len(segments))

	// 创建工作目录
	h264Dir := filepath.Join(tsDir, "h264")
	aacDir := filepath.Join(tsDir, "aac")
	decDir := filepath.Join(tsDir, "dec")
	mp4Dir := filepath.Join(tsDir, "mp4")
	for _, dir := range []string{h264Dir, aacDir, decDir, mp4Dir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}

	// 创建有序合并缓冲区
	orderedBuffer := NewOrderedBuffer(len(segments))

	// 创建阶段间通信通道
	demuxInputChan := make(chan *downloader.Segment, len(segments))   // demux完成→解密
	decryptInputChan := make(chan *ProcessedSegment, len(segments))   // 解密完成→remux
	remuxInputChan := make(chan *ProcessedSegment, len(segments))     // remux完成→有序合并

	// 进度计数器
	var demuxCount atomic.Int64
	var remuxCount atomic.Int64
	totalSegments := int64(len(segments))

	g, ctx := errgroup.WithContext(ctx)

	// 阶段1: demux分离H264+AAC
	g.Go(func() error {
		defer close(demuxInputChan)
		return p.demuxStage(ctx, segments, h264Dir, aacDir, demuxInputChan, &demuxCount, totalSegments)
	})

	// 阶段2: 批量映射解密
	g.Go(func() error {
		defer close(decryptInputChan)
		return p.decryptStage(ctx, demuxInputChan, h264Dir, aacDir, decDir, decryptInputChan, totalSegments)
	})

	// 阶段3: remux封装为MP4
	g.Go(func() error {
		defer close(remuxInputChan)
		return p.remuxStage(ctx, decryptInputChan, mp4Dir, remuxInputChan, &remuxCount, totalSegments)
	})

	// 阶段4: 有序合并
	g.Go(func() error {
		err := p.muxStage(ctx, remuxInputChan, orderedBuffer)
		orderedBuffer.Close()
		return err
	})

	// 阶段5: 最终合并
	g.Go(func() error {
		return p.finalMergeStage(ctx, orderedBuffer, mp4Dir, outputPath)
	})

	if err := g.Wait(); err != nil {
		return err
	}

	p.logger.Info("加密流处理完成", "output", outputPath)
	return nil
}

// demuxStage demux分离阶段：并发分离H264+AAC
func (p *Processor) demuxStage(ctx context.Context, segments []*downloader.Segment, h264Dir, aacDir string, outputChan chan<- *downloader.Segment, count *atomic.Int64, total int64) error {
	var wg sync.WaitGroup
	errChan := make(chan error, len(segments))
	sem := make(chan struct{}, p.ffmpegConcurrency)

	for _, seg := range segments {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		wg.Add(1)
		go func(s *downloader.Segment) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			tsPath := s.OutputPath
			h264Path := filepath.Join(h264Dir, fmt.Sprintf("seg_%05d.264", s.Index))
			aacPath := filepath.Join(aacDir, fmt.Sprintf("seg_%05d.aac", s.Index))

			// 分离视频流（使用精确的 -map 0:v:0）
			videoCmd := ffmpeg.BuildDemuxVideoCommand(p.ffmpegPath, tsPath, h264Path)
			if err := p.processManager.Run(ctx, videoCmd); err != nil {
				errChan <- fmt.Errorf("分离视频失败[%d]: %w", s.Index, err)
				return
			}

			// 分离音频流（使用精确的 -map 0:a:0）
			audioCmd := ffmpeg.BuildDemuxAudioCommand(p.ffmpegPath, tsPath, aacPath)
			if err := p.processManager.Run(ctx, audioCmd); err != nil {
				errChan <- fmt.Errorf("分离音频失败[%d]: %w", s.Index, err)
				return
			}

			// 更新进度
			current := count.Add(1)
			p.updateProgress("demux", current, total, fmt.Sprintf("分片 %d", s.Index))

			// 发送到下一阶段
			select {
			case outputChan <- s:
			case <-ctx.Done():
			}
		}(seg)
	}

	wg.Wait()
	close(errChan)

	for err := range errChan {
		if err != nil {
			return err
		}
	}

	p.logger.Info("demux阶段完成")
	return nil
}

// decryptStage 解密阶段：多进程并行解密所有H264文件
func (p *Processor) decryptStage(ctx context.Context, inputChan <-chan *downloader.Segment, h264Dir, aacDir, decDir string, outputChan chan<- *ProcessedSegment, total int64) error {
	// 获取解密脚本目录
	scriptDir, err := p.getDecryptScriptDir()
	if err != nil {
		return fmt.Errorf("获取解密脚本失败: %w", err)
	}

	decryptor := NewNodeDecryptor(p.nodePath, scriptDir, p.logger)

	// 1. 收集所有分片任务
	var decryptTasks []DecryptTask
	var segments []*ProcessedSegment
	var collectedCount atomic.Int64

	for seg := range inputChan {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		h264Path := filepath.Join(h264Dir, fmt.Sprintf("seg_%05d.264", seg.Index))
		decPath := filepath.Join(decDir, fmt.Sprintf("seg_%05d_dec.264", seg.Index))
		aacPath := filepath.Join(aacDir, fmt.Sprintf("seg_%05d.aac", seg.Index))

		decryptTasks = append(decryptTasks, DecryptTask{
			In:  h264Path,
			Out: decPath,
		})

		segments = append(segments, &ProcessedSegment{
			Index:      seg.Index,
			H264Path:   decPath,
			AACPath:    aacPath,
			DecSuccess: true,
		})

		// 更新收集进度
		current := collectedCount.Add(1)
		p.updateProgress("decrypt_collect", current, total, fmt.Sprintf("收集分片 %d", seg.Index))
	}

	if len(decryptTasks) == 0 {
		return nil
	}

	// 更新进度：开始解密
	p.updateProgress("decrypt", 0, total, "开始多进程并行解密")

	// 2. 执行多进程并行解密（核心优化）
	// 使用配置的解密并行进程数
	workerCount := p.decryptWorkers
	if workerCount <= 0 {
		workerCount = 8 // 默认8线程
	}

	progressCallback := func(current, totalProgress int64) {
		p.updateProgress("decrypt", current, totalProgress, fmt.Sprintf("解密进度 %d/%d", current, totalProgress))
	}

	if err := decryptor.DecryptBatchMappedParallel(ctx, decryptTasks, workerCount, progressCallback); err != nil {
		return err
	}

	// 确保进度显示完成
	p.updateProgress("decrypt", total, total, "解密完成")

	// 3. 将解密结果推入下一阶段
	for _, s := range segments {
		select {
		case outputChan <- s:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	p.logger.Info("解密阶段完成")
	return nil
}

// remuxStage remux封装阶段：并发将解密后的H264与AAC封装为MP4
func (p *Processor) remuxStage(ctx context.Context, inputChan <-chan *ProcessedSegment, mp4Dir string, outputChan chan<- *ProcessedSegment, count *atomic.Int64, total int64) error {
	if err := os.MkdirAll(mp4Dir, 0755); err != nil {
		return err
	}

	var wg sync.WaitGroup
	errChan := make(chan error, 100) // 足够大的缓冲
	sem := make(chan struct{}, p.ffmpegConcurrency)

	// 用于安全地收集结果
	var outputsMu sync.Mutex
	var outputs []*ProcessedSegment

	for seg := range inputChan {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		wg.Add(1)
		go func(s *ProcessedSegment) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			mp4Path := filepath.Join(mp4Dir, fmt.Sprintf("seg_%05d.mp4", s.Index))

			// remux封装：H264+AAC → MP4，重新生成PTS时间戳
			cmd := ffmpeg.BuildRemuxSegmentCommand(p.ffmpegPath, s.H264Path, s.AACPath, mp4Path)
			if err := p.processManager.Run(ctx, cmd); err != nil {
				errChan <- fmt.Errorf("remux失败[%d]: %w", s.Index, err)
				return
			}

			// 更新进度
			current := count.Add(1)
			p.updateProgress("remux", current, total, fmt.Sprintf("分片 %d", s.Index))

			// 保存结果
			s.MP4Path = mp4Path
			outputsMu.Lock()
			outputs = append(outputs, s)
			outputsMu.Unlock()
		}(seg)
	}

	wg.Wait()
	close(errChan)

	// 检查错误
	for err := range errChan {
		if err != nil {
			return err
		}
	}

	// 按序发送结果（按 Index 排序）
	sort.Slice(outputs, func(i, j int) bool {
		return outputs[i].Index < outputs[j].Index
	})
	for _, seg := range outputs {
		select {
		case outputChan <- seg:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	p.logger.Info("remux阶段完成")
	return nil
}

// muxStage 有序合并阶段：通过OrderedBuffer保证分片按原始顺序输出
func (p *Processor) muxStage(ctx context.Context, inputChan <-chan *ProcessedSegment, orderedBuffer *OrderedBuffer) error {
	for seg := range inputChan {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// 提交到有序缓冲区，自动按序输出
		orderedBuffer.Submit(seg)
	}

	p.logger.Info("有序合并阶段完成")
	return nil
}

// finalMergeStage 最终合并阶段：将所有分片MP4合并为最终输出
func (p *Processor) finalMergeStage(ctx context.Context, orderedBuffer *OrderedBuffer, mp4Dir, outputPath string) error {
	// 更新进度：开始最终合并
	p.updateProgress("merge", 0, 1, "准备合并")

	// 从有序缓冲区收集所有分片MP4
	var mp4Files []string
	for seg := range orderedBuffer.Output() {
		if seg.DecSuccess && seg.MP4Path != "" {
			mp4Files = append(mp4Files, seg.MP4Path)
		}
	}

	if len(mp4Files) == 0 {
		return fmt.Errorf("没有可合并的文件")
	}

	// 创建concat列表文件
	listFile := filepath.Join(mp4Dir, "merge_list.txt")
	listContent := ffmpeg.BuildConcatListFile(mp4Files)
	if err := os.WriteFile(listFile, []byte(listContent), 0644); err != nil {
		return err
	}

	// 更新进度：正在合并
	p.updateProgress("merge", 0, 1, fmt.Sprintf("合并 %d 个分片", len(mp4Files)))

	// 执行合并（首选 -c copy）
	cmd := ffmpeg.BuildMergeCCTVTsCommand(p.ffmpegPath, listFile, outputPath)
	if err := p.processManager.Run(ctx, cmd); err != nil {
		return err
	}

	// 更新进度：合并完成
	p.updateProgress("merge", 1, 1, "合并完成")

	p.logger.Info("最终合并完成", "output", outputPath)
	return nil
}

// DownloadDirect 直接下载普通流
func (p *Processor) DownloadDirect(ctx context.Context, m3u8URL, outputPath string) error {
	p.logger.Info("开始直接下载普通流", "url", m3u8URL)

	// 更新进度：开始下载
	p.updateProgress("download", 0, 1, "开始下载")

	cmd := ffmpeg.BuildDirectDownloadCommand(p.ffmpegPath, m3u8URL, outputPath)
	if err := p.processManager.Run(ctx, cmd); err != nil {
		return fmt.Errorf("直接下载失败: %w", err)
	}

	// 更新进度：下载完成
	p.updateProgress("download", 1, 1, "下载完成")

	p.logger.Info("普通流下载完成", "output", outputPath)
	return nil
}

// getDecryptScriptDir 获取解密脚本目录
func (p *Processor) getDecryptScriptDir() (string, error) {
	// 首先检查当前目录
	cwd, _ := os.Getwd()
	scriptDir := filepath.Join(cwd, "assets", "decrypt")
	if _, err := os.Stat(filepath.Join(scriptDir, "dec.mjs")); err == nil {
		return scriptDir, nil
	}

	// 检查可执行文件目录
	execPath, _ := os.Executable()
	execDir := filepath.Dir(execPath)
	scriptDir = filepath.Join(execDir, "assets", "decrypt")
	if _, err := os.Stat(filepath.Join(scriptDir, "dec.mjs")); err == nil {
		return scriptDir, nil
	}

	return "", fmt.Errorf("未找到解密脚本目录")
}
