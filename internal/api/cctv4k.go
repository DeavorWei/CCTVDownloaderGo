package api

import (
	"bufio"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/go-resty/resty/v2"
)

// CCTV4KClient CCTV-4K专用API客户端
// 4K视频使用不同的API端点和处理逻辑
type CCTV4KClient struct {
	client *resty.Client
	logger *slog.Logger
}

// NewCCTV4KClient 创建CCTV-4K API客户端
func NewCCTV4KClient(userAgent string, timeout time.Duration, logger *slog.Logger) *CCTV4KClient {
	client := resty.New().
		SetTimeout(timeout).
		SetHeader("User-Agent", userAgent).
		SetRetryCount(3).
		SetRetryWaitTime(2*time.Second).
		SetRetryMaxWaitTime(10*time.Second)

	return &CCTV4KClient{
		client: client,
		logger: logger,
	}
}

// Video4KInfo 4K视频信息
type Video4KInfo struct {
	GUID     string // 视频GUID
	Title    string // 视频标题
	Time     string // 发布时间
	Image    string // 封面图URL
	Brief    string // 简介
	M3U8URL  string // 4K M3U8 URL（已处理）
}

// cctv4kVideoInfoResponse 4K视频信息API响应结构
type cctv4kVideoInfoResponse struct {
	VID   string `json:"vid"`
	Title string `json:"title"`
	Brief string `json:"brief"`
	Image string `json:"img"`
	Time  string `json:"time"`
}

// GetVideoInfoByGUID 通过GUID获取4K视频信息
// 使用专用API：https://zy.api.cntv.cn/video/videoinfoByGuid?serviceId=cctv4k&guid=xxx
func (c *CCTV4KClient) GetVideoInfoByGUID(guid string) (*Video4KInfo, error) {
	c.logger.Info("获取4K视频信息", "guid", guid)

	apiURL := "https://zy.api.cntv.cn/video/videoinfoByGuid"
	
	resp, err := c.client.R().
		SetQueryParams(map[string]string{
			"serviceId": "cctv4k",
			"guid":      guid,
		}).
		Get(apiURL)

	if err != nil {
		return nil, fmt.Errorf("请求4K视频信息失败: %w", err)
	}

	c.logger.Debug("4K API响应", "status", resp.StatusCode(), "body", resp.String())

	var result cctv4kVideoInfoResponse
	if err := parseJSONResponse(resp.Body(), &result); err != nil {
		return nil, fmt.Errorf("解析4K视频信息失败: %w", err)
	}

	if result.VID == "" {
		return nil, fmt.Errorf("未获取到4K视频信息，guid: %s", guid)
	}

	info := &Video4KInfo{
		GUID:  result.VID,
		Title: result.Title,
		Time:  result.Time,
		Image: result.Image,
		Brief: result.Brief,
	}

	c.logger.Info("获取4K视频信息成功",
		"guid", guid,
		"title", info.Title,
		"time", info.Time,
	)

	return info, nil
}

// GetVideoListByGUID 使用GUID获取视频列表（单个视频）
// 用于4K频道页面，当column_id为空时使用guid作为标识
func (c *CCTV4KClient) GetVideoListByGUID(guid string) ([]AlbumVideoItem, error) {
	c.logger.Info("通过GUID获取4K视频列表", "guid", guid)

	info, err := c.GetVideoInfoByGUID(guid)
	if err != nil {
		return nil, err
	}

	// 将单个视频转换为列表格式
	videos := []AlbumVideoItem{
		{
			GUID:  info.GUID,
			Title: info.Title,
			Time:  info.Time,
			Image: info.Image,
			Brief: info.Brief,
		},
	}

	c.logger.Info("获取4K视频列表成功", "count", len(videos))
	return videos, nil
}

// Parse4KM3U8Content 解析4K M3U8内容，提取TS文件列表
// 4K视频的M3U8是媒体播放列表，直接包含TS文件，不是主播放列表
func (c *CCTV4KClient) Parse4KM3U8Content(m3u8Content, baseURL string) ([]string, error) {
	c.logger.Debug("解析4K M3U8内容", "baseURL", baseURL)

	var tsList []string
	basePath := baseURL[:strings.LastIndex(baseURL, "/")+1]

	scanner := bufio.NewScanner(strings.NewReader(m3u8Content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		
		// 跳过注释行和空行
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// TS文件行
		if strings.HasSuffix(line, ".ts") {
			tsURL := basePath + line
			tsList = append(tsList, tsURL)
		}
	}

	if len(tsList) == 0 {
		return nil, fmt.Errorf("未找到TS文件")
	}

	c.logger.Info("解析4K M3U8完成", "ts_count", len(tsList))
	return tsList, nil
}

// Process4KURL 处理4K视频URL
// 将URL中的"main"替换为"4000"以获取4K流
func Process4KURL(originalURL string) string {
	// 替换main为4000
	processedURL := strings.Replace(originalURL, "main", "4000", 1)
	return processedURL
}

// Is4KChannel 检测是否为4K频道
func Is4KChannel(playChannel string) bool {
	return strings.Contains(playChannel, "CCTV-4K")
}
