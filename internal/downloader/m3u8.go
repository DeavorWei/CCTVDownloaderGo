package downloader

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Segment TS分片信息
type Segment struct {
	Index     int    // 分片序号
	URL       string // 分片URL
	Duration  float64 // 分片时长（秒）
	Title     string // 分片标题
	OutputPath string // 下载输出路径（由下载器填充）
}

// M3U8Info M3U8播放列表信息
type M3U8Info struct {
	IsMaster  bool       // 是否为主播放列表
	TargetDur float64    // 目标时长
	Segments  []*Segment // 分片列表
	Variants  []string   // 变体流URL列表
}

// ParseM3U8 解析M3U8播放列表
func ParseM3U8(content string, baseURL string) (*M3U8Info, error) {
	info := &M3U8Info{}

	scanner := bufio.NewScanner(strings.NewReader(content))

	// 检查是否为M3U8文件
	if !scanner.Scan() || !strings.HasPrefix(scanner.Text(), "#EXTM3U") {
		return nil, fmt.Errorf("不是有效的M3U8文件")
	}

	var currentSegment *Segment
	var targetDuration float64
	segIndex := 0

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "#EXT-X-TARGETDURATION:") {
			// 目标时长
			durStr := strings.TrimPrefix(line, "#EXT-X-TARGETDURATION:")
			targetDuration, _ = strconv.ParseFloat(durStr, 64)
			info.TargetDur = targetDuration

		} else if strings.HasPrefix(line, "#EXT-X-STREAM-INF:") {
			// 主播放列表中的变体流
			info.IsMaster = true

		} else if strings.HasPrefix(line, "#EXTINF:") {
			// 分片信息
			currentSegment = &Segment{Index: segIndex}
			// 解析时长
			infStr := strings.TrimPrefix(line, "#EXTINF:")
			parts := strings.SplitN(infStr, ",", 2)
			if len(parts) >= 1 {
				currentSegment.Duration, _ = strconv.ParseFloat(parts[0], 64)
			}
			if len(parts) >= 2 {
				currentSegment.Title = parts[1]
			}

		} else if strings.HasPrefix(line, "#EXT-X-ENDLIST") {
			// 播放列表结束
			break

		} else if !strings.HasPrefix(line, "#") {
			// URI行
			if info.IsMaster {
				// 变体流URL
				absURL := ResolveURL(baseURL, line)
				info.Variants = append(info.Variants, absURL)
			} else if currentSegment != nil {
				// 分片URL
				currentSegment.URL = ResolveURL(baseURL, line)
				info.Segments = append(info.Segments, currentSegment)
				segIndex++
				currentSegment = nil
			}
		}
	}

	return info, nil
}

// ResolveURL 解析相对URL为绝对URL
// 导出此函数供其他包使用
func ResolveURL(baseURL, refURL string) string {
	// 已经是绝对URL
	if strings.HasPrefix(refURL, "http://") || strings.HasPrefix(refURL, "https://") {
		return refURL
	}

	base, err := url.Parse(baseURL)
	if err != nil {
		return refURL
	}

	ref, err := url.Parse(refURL)
	if err != nil {
		return refURL
	}

	return base.ResolveReference(ref).String()
}

// DownloadSegment 下载单个TS分片
func DownloadSegment(segment *Segment, timeout time.Duration, maxRetries int) error {
	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			// 指数退避
			waitTime := time.Duration(attempt*attempt) * time.Second
			if waitTime > 30*time.Second {
				waitTime = 30 * time.Second
			}
			time.Sleep(waitTime)
		}

		client := &http.Client{Timeout: timeout}
		resp, err := client.Get(segment.URL)
		if err != nil {
			lastErr = fmt.Errorf("下载分片%d失败: %w", segment.Index, err)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			lastErr = fmt.Errorf("下载分片%d返回状态码%d", segment.Index, resp.StatusCode)
			continue
		}

		// 写入文件
		file, err := CreateFile(segment.OutputPath)
		if err != nil {
			resp.Body.Close()
			return fmt.Errorf("创建文件失败: %w", err)
		}

		_, err = io.Copy(file, resp.Body)
		file.Close()
		resp.Body.Close()

		if err != nil {
			lastErr = fmt.Errorf("写入分片%d失败: %w", segment.Index, err)
			continue
		}

		return nil // 成功
	}

	return lastErr
}

// CreateFile 创建文件（确保目录存在）
func CreateFile(path string) (*os.File, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	return os.Create(path)
}

// GetSegmentOutputPath 生成分片输出路径
func GetSegmentOutputPath(tsDir string, index int) string {
	return filepath.Join(tsDir, fmt.Sprintf("seg_%05d.ts", index))
}
