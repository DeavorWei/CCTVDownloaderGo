package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/go-resty/resty/v2"
)

// AlbumVideoItem 专辑视频项
type AlbumVideoItem struct {
	GUID   string `json:"guid"`   // 视频GUID
	Time   string `json:"time"`   // 发布时间
	Title  string `json:"title"`  // 视频标题
	Image  string `json:"image"`  // 封面图URL
	Brief  string `json:"brief"`  // 简介
	Length string `json:"length"` // 时长
}

// AlbumInfo 专辑信息
type AlbumInfo struct {
	ID     string            // 专辑ID
	Title  string            // 专辑标题
	Videos []AlbumVideoItem  // 视频列表
	Total  int               // 总数
}

// albumAPIResponse 专辑API响应结构
type albumAPIResponse struct {
	Data struct {
		List []AlbumVideoItem `json:"list"`
	} `json:"data"`
}

// realAlbumIDResponse 真实专辑ID响应结构
type realAlbumIDResponse struct {
	Data struct {
		ID string `json:"id"`
	} `json:"data"`
}

// ErrAlbumNotFound 专辑未找到
var ErrAlbumNotFound = errors.New("无法获取专辑信息")

// AlbumClient 专辑API客户端
type AlbumClient struct {
	client *resty.Client
	logger *slog.Logger
}

// NewAlbumClient 创建专辑API客户端
func NewAlbumClient(userAgent string, timeout time.Duration, logger *slog.Logger) *AlbumClient {
	client := resty.New().
		SetTimeout(timeout).
		SetHeader("User-Agent", userAgent).
		SetRetryCount(3).
		SetRetryWaitTime(2*time.Second).
		SetRetryMaxWaitTime(10*time.Second)

	return &AlbumClient{
		client: client,
		logger: logger,
	}
}

// GetRealAlbumID 获取真实专辑ID
func (c *AlbumClient) GetRealAlbumID(itemID string) (string, error) {
	c.logger.Debug("获取真实专辑ID", "item_id", itemID)

	url := "https://api.cntv.cn/NewVideoset/getVideoAlbumInfoByVideoId"
	resp, err := c.client.R().
		SetQueryParams(map[string]string{
			"id":        itemID,
			"serviceId": "tvcctv",
		}).
		Get(url)

	if err != nil {
		return "", fmt.Errorf("请求专辑ID失败: %w", err)
	}

	var result realAlbumIDResponse
	if err := json.Unmarshal(resp.Body(), &result); err != nil {
		return "", fmt.Errorf("解析专辑ID响应失败: %w", err)
	}

	if result.Data.ID == "" {
		c.logger.Warn("无法获取真实专辑ID", "item_id", itemID)
		return "", ErrAlbumNotFound
	}

	c.logger.Info("获取真实专辑ID成功", "album_id", result.Data.ID)
	return result.Data.ID, nil
}

// GetVideoListByAlbum 通过专辑ID获取视频列表
// 注意：专辑API的日期参数不起作用，始终返回全部视频，需要通过GUID去重
func (c *AlbumClient) GetVideoListByAlbum(albumID string, dateList []string) ([]AlbumVideoItem, error) {
	c.logger.Info("通过专辑ID获取视频列表", "album_id", albumID, "dates", dateList)

	// 使用map进行GUID去重
	seen := make(map[string]bool)
	var allVideos []AlbumVideoItem

	for _, date := range dateList {
		c.logger.Debug("处理月份", "date", date)

		url := c.buildAlbumAPIURL(albumID, date)
		resp, err := c.client.R().Get(url)

		if err != nil {
			c.logger.Warn("获取月份数据失败", "date", date, "error", err)
			continue
		}

		var result albumAPIResponse
		if err := json.Unmarshal(resp.Body(), &result); err != nil {
			c.logger.Warn("解析月份数据失败", "date", date, "error", err)
			continue
		}

		if len(result.Data.List) == 0 {
			c.logger.Debug("月份无数据", "date", date)
			continue
		}

		c.logger.Info("月份获取成功", "date", date, "count", len(result.Data.List))

		// 基于GUID去重添加视频
		for _, video := range result.Data.List {
			if !seen[video.GUID] {
				seen[video.GUID] = true
				allVideos = append(allVideos, video)
			}
		}
	}

	if len(allVideos) == 0 {
		c.logger.Warn("专辑方式获取视频列表为空", "album_id", albumID)
	} else {
		c.logger.Info("专辑视频去重完成", "unique_count", len(allVideos), "total_requests", len(dateList))
	}

	return allVideos, nil
}

// buildAlbumAPIURL 构建专辑API URL
func (c *AlbumClient) buildAlbumAPIURL(albumID, date string) string {
	// https://api.cntv.cn/NewVideo/getVideoListByAlbumIdNew?id=xxx&n=100&p=1&d=yyyyMM&mode=0&serviceId=tvcctv&sort=asc&pub=1
	return fmt.Sprintf(
		"https://api.cntv.cn/NewVideo/getVideoListByAlbumIdNew?id=%s&n=100&p=1&d=%s&mode=0&serviceId=tvcctv&sort=asc&pub=1",
		albumID, date,
	)
}
