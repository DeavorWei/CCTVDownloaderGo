package api

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/go-resty/resty/v2"
)

// CCTVNewsClient CCTVNews API客户端（EMAS签名）
type CCTVNewsClient struct {
	client *resty.Client
	appKey string   // EMAS AppKey
	secret []byte   // EMAS HMAC密钥
	logger *slog.Logger
}

// NewCCTVNewsClient 创建CCTVNews API客户端
func NewCCTVNewsClient(userAgent string, timeout time.Duration, logger *slog.Logger) *CCTVNewsClient {
	client := resty.New().
		SetTimeout(timeout).
		SetHeader("User-Agent", userAgent).
		SetRetryCount(3).
		SetRetryWaitTime(2 * time.Second)

	return &CCTVNewsClient{
		client: client,
		appKey: EMASAppKey,
		secret: []byte(EMASecret),
		logger: logger,
	}
}

// ParseFromURL 从URL解析CCTVNews视频信息
func (c *CCTVNewsClient) ParseFromURL(url string) ([]*VideoInfo, error) {
	// CCTVNews使用EMAS API获取视频信息
	// 此处为框架实现，具体API端点需根据实际情况调整
	params := map[string]string{
		"url": url,
	}

	headers := c.generateEMASHeaders(params, "videoDetail", "1.0")

	resp, err := c.client.R().
		SetQueryParams(params).
		SetHeaders(headers).
		Get("https://newsapp.cctv.com/api/videoDetail")
	if err != nil {
		return nil, fmt.Errorf("请求CCTVNews API失败: %w", err)
	}

	var result struct {
		Data []struct {
			Title    string `json:"title"`
			VideoURL string `json:"video_url"`
			CoverURL string `json:"cover_url"`
		} `json:"data"`
	}

	if err := parseJSONResponse(resp.Body(), &result); err != nil {
		return nil, fmt.Errorf("解析CCTVNews响应失败: %w", err)
	}

	var videos []*VideoInfo
	for _, item := range result.Data {
		videos = append(videos, &VideoInfo{
			RawTitle:    item.Title,
			CoverURL:    item.CoverURL,
			M3U8URL:     item.VideoURL,
			HLSKey:      "hls_url", // CCTVNews通常为普通流
			IsEncrypted: false,
			Extra:       make(map[string]any),
		})
	}

	return videos, nil
}

// generateEMASHeaders 生成EMAS签名请求头
func (c *CCTVNewsClient) generateEMASHeaders(params map[string]string, apiName, apiVer string) map[string]string {
	timestamp := fmt.Sprintf("%d", time.Now().UnixMilli())

	// 生成MD5哈希
	md5Hash := GenerateEMASMD5Hash(params)

	// 生成HMAC-SHA256签名
	sign := GenerateEMASSignature(c.appKey, md5Hash, timestamp, apiName, apiVer, c.secret)

	return map[string]string{
		"X-CCTV-AppKey":   c.appKey,
		"X-CCTV-Timestamp": timestamp,
		"X-CCTV-Sign":     sign,
		"X-CCTV-ApiName":  apiName,
		"X-CCTV-ApiVer":   apiVer,
	}
}
