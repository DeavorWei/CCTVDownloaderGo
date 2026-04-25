package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/go-resty/resty/v2"
)

// ColumnClient 栏目API客户端
type ColumnClient struct {
	client *resty.Client
	logger *slog.Logger
}

// NewColumnClient 创建栏目API客户端
func NewColumnClient(userAgent string, timeout time.Duration, logger *slog.Logger) *ColumnClient {
	client := resty.New().
		SetTimeout(timeout).
		SetHeader("User-Agent", userAgent).
		SetRetryCount(3).
		SetRetryWaitTime(2*time.Second).
		SetRetryMaxWaitTime(10*time.Second)

	return &ColumnClient{
		client: client,
		logger: logger,
	}
}

// GetVideoListByColumn 通过栏目ID获取视频列表
func (c *ColumnClient) GetVideoListByColumn(columnID string, dateList []string) ([]AlbumVideoItem, error) {
	c.logger.Info("通过栏目ID获取视频列表", "column_id", columnID, "dates", dateList)

	var allVideos []AlbumVideoItem

	for _, date := range dateList {
		c.logger.Debug("处理月份", "date", date)

		url := c.buildColumnAPIURL(columnID, date)
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
		allVideos = append(allVideos, result.Data.List...)
	}

	if len(allVideos) == 0 {
		c.logger.Warn("栏目方式获取视频列表为空", "column_id", columnID)
	}

	return allVideos, nil
}

// buildColumnAPIURL 构建栏目API URL
func (c *ColumnClient) buildColumnAPIURL(columnID, date string) string {
	// https://api.cntv.cn/NewVideo/getVideoListByColumn?id=xxx&n=100&p=1&d=yyyyMM&mode=0&serviceId=tvcctv&sort=desc
	return fmt.Sprintf(
		"https://api.cntv.cn/NewVideo/getVideoListByColumn?id=%s&n=100&p=1&d=%s&mode=0&serviceId=tvcctv&sort=desc",
		columnID, date,
	)
}
