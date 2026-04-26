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
	AlbumID  string // 专辑ID
}

// cctv4kVideoInfoResponse 4K视频信息API响应结构
type cctv4kVideoInfoResponse struct {
	VID     string `json:"vid"`
	Title   string `json:"title"`
	Brief   string `json:"brief"`
	Image   string `json:"img"`
	Time    string `json:"time"`
	AlbumID string `json:"album_id"`
}

// cctv4kAlbumListResponse 4K专辑视频列表API响应结构
type cctv4kAlbumListResponse struct {
	Data struct {
		Total int `json:"total"`
		List  []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
			URL   string `json:"url"`
			GUID  string `json:"guid"`
			Image string `json:"image"`
			Time  string `json:"time"`
			Brief string `json:"brief"`
		} `json:"list"`
	} `json:"data"`
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
		GUID:    result.VID,
		Title:   result.Title,
		Time:    result.Time,
		Image:   result.Image,
		Brief:   result.Brief,
		AlbumID: result.AlbumID,
	}

	c.logger.Info("获取4K视频信息成功",
		"guid", guid,
		"title", info.Title,
		"album_id", info.AlbumID,
		"time", info.Time,
	)

	return info, nil
}

// GetVideoListByAlbumID 通过专辑ID获取4K视频列表
// 使用API：https://api.cntv.cn/NewVideo/getVideoListByAlbumIdNew?id=xxx&serviceId=cctv4k&p=1&n=100&sort=asc&mode=0&pub=2
func (c *CCTV4KClient) GetVideoListByAlbumID(albumID string) ([]AlbumVideoItem, error) {
	c.logger.Info("通过专辑ID获取4K视频列表", "album_id", albumID)

	apiURL := "https://api.cntv.cn/NewVideo/getVideoListByAlbumIdNew"

	resp, err := c.client.R().
		SetQueryParams(map[string]string{
			"id":        albumID,
			"serviceId": "cctv4k",
			"p":         "1",
			"n":         "100",
			"sort":      "asc",
			"mode":      "0",
			"pub":       "2",
		}).
		Get(apiURL)

	if err != nil {
		return nil, fmt.Errorf("请求4K专辑视频列表失败: %w", err)
	}

	c.logger.Debug("4K专辑API响应", "status", resp.StatusCode(), "body", resp.String())

	var result cctv4kAlbumListResponse
	if err := parseJSONResponse(resp.Body(), &result); err != nil {
		return nil, fmt.Errorf("解析4K专辑视频列表失败: %w", err)
	}

	var videos []AlbumVideoItem
	for _, item := range result.Data.List {
		videos = append(videos, AlbumVideoItem{
			GUID:  item.GUID,
			Title: item.Title,
			Time:  item.Time,
			Image: item.Image,
			Brief: item.Brief,
		})
	}

	c.logger.Info("获取4K专辑视频列表成功", "album_id", albumID, "total", result.Data.Total, "count", len(videos))
	return videos, nil
}

// GetVideoListByGUID 使用GUID获取视频列表
// 逻辑：
// 1. 先获取单个视频信息（包含album_id）
// 2. 如果有album_id，则获取专辑全部视频列表
// 3. 如果没有album_id，返回单个视频
func (c *CCTV4KClient) GetVideoListByGUID(guid string) ([]AlbumVideoItem, error) {
	c.logger.Info("通过GUID获取4K视频列表", "guid", guid)

	// 第一步：获取单个视频信息（包含album_id）
	info, err := c.GetVideoInfoByGUID(guid)
	if err != nil {
		return nil, err
	}

	// 第二步：如果有album_id，获取专辑全部视频列表
	if info.AlbumID != "" {
		c.logger.Info("检测到专辑ID，获取专辑全部视频", "album_id", info.AlbumID)

		videos, err := c.GetVideoListByAlbumID(info.AlbumID)
		if err != nil {
			c.logger.Warn("获取专辑视频列表失败，回退到单视频模式", "error", err)
			// 回退：返回单个视频
			return []AlbumVideoItem{
				{
					GUID:  info.GUID,
					Title: info.Title,
					Time:  info.Time,
					Image: info.Image,
					Brief: info.Brief,
				},
			}, nil
		}

		return videos, nil
	}

	// 没有album_id，返回单个视频（如单集视频）
	c.logger.Info("无专辑ID，返回单个视频")
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

// Process4KURL 已废弃
// 4K视频现在统一使用 manifest.hls_h5e_url 字段，通过解析主播放列表获取码率URL
// 不再需要手动替换URL路径
// Is4KChannel 检测是否为4K频道
func Is4KChannel(playChannel string) bool {
	return strings.Contains(playChannel, "CCTV-4K")
}
