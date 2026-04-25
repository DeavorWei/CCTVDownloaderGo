package api

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/go-resty/resty/v2"
)

// VideoInfo 视频信息
type VideoInfo struct {
	PID         string         // 视频PID/GUID
	Title       string         // 视频标题（已安全化）
	M3U8URL     string         // M3U8播放列表URL
	HLSKey      string         // HLS流类型：hls_h5e_url 或 hls_url
	IsEncrypted bool           // 是否加密流（HLSKey == "hls_h5e_url"）
	Is4K        bool           // 是否为4K视频
	CoverURL    string         // 封面图URL
	RawTitle    string         // 原始标题（未处理）
	Extra       map[string]any // 扩展信息
}

// cctvAPIResponse CCTV API响应结构
// CCTV API响应结构不固定，HLS URL可能出现在三个位置：
// 1. 顶层（hls_url, hls_h5e_url 等）
// 2. manifest 对象内
// 3. cdn_info 对象内
type cctvAPIResponse struct {
	Title       string `json:"title"`
	Cover       string `json:"cover"`
	Image       string `json:"image"`  // 有些响应用image而非cover
	PlayChannel string `json:"play_channel"` // 播放频道（用于检测4K）

	// HLS URL可能在顶层
	HLSURL     string `json:"hls_url"`
	HLSH5EURL  string `json:"hls_h5e_url"`
	HLSEncURL  string `json:"hls_enc_url"`
	HLSEnc2URL string `json:"hls_enc2_url"`

	// HLS URL也可能在manifest内
	Manifest struct {
		HLSURL     string `json:"hls_url"`
		HLSH5EURL  string `json:"hls_h5e_url"`
		HLSEncURL  string `json:"hls_enc_url"`
		HLSEnc2URL string `json:"hls_enc2_url"`
	} `json:"manifest"`

	// HLS URL还可能在cdn_info内
	CDNInfo struct {
		HLSH5EURL string `json:"hls_h5e_url,omitempty"`
		HLSURL    string `json:"hls_url,omitempty"`
	} `json:"cdn_info"`

	// 其他字段
	Video struct {
		TotalLength     string `json:"totalLength"`
		ValidChapterNum int    `json:"validChapterNum"`
	} `json:"video"`
}

// extractHLSURL 从多个可能的位置提取HLS URL
// 优先级：顶层 > manifest > cdn_info
func (r *cctvAPIResponse) extractHLSURL(key string) string {
	switch key {
	case "hls_h5e_url":
		if r.HLSH5EURL != "" {
			return r.HLSH5EURL
		}
		if r.Manifest.HLSH5EURL != "" {
			return r.Manifest.HLSH5EURL
		}
		if r.CDNInfo.HLSH5EURL != "" {
			return r.CDNInfo.HLSH5EURL
		}
	case "hls_url":
		if r.HLSURL != "" {
			return r.HLSURL
		}
		if r.Manifest.HLSURL != "" {
			return r.Manifest.HLSURL
		}
		if r.CDNInfo.HLSURL != "" {
			return r.CDNInfo.HLSURL
		}
	case "hls_enc_url":
		if r.HLSEncURL != "" {
			return r.HLSEncURL
		}
		if r.Manifest.HLSEncURL != "" {
			return r.Manifest.HLSEncURL
		}
	case "hls_enc2_url":
		if r.HLSEnc2URL != "" {
			return r.HLSEnc2URL
		}
		if r.Manifest.HLSEnc2URL != "" {
			return r.Manifest.HLSEnc2URL
		}
	}
	return ""
}

// ErrPIDNotFound 无法提取PID
var ErrPIDNotFound = errors.New("无法从页面提取视频PID")

// CCTVClient CCTV API客户端
type CCTVClient struct {
	client *resty.Client
	logger *slog.Logger
}

// NewCCTVClient 创建CCTV API客户端
func NewCCTVClient(userAgent string, timeout time.Duration, logger *slog.Logger) *CCTVClient {
	client := resty.New().
		SetTimeout(timeout).
		SetHeader("User-Agent", userAgent).
		SetRetryCount(3).
		SetRetryWaitTime(2 * time.Second).
		SetRetryMaxWaitTime(10 * time.Second)

	return &CCTVClient{
		client: client,
		logger: logger,
	}
}

// ExtractPID 从页面URL提取PID
func (c *CCTVClient) ExtractPID(pageURL string) (string, error) {
	c.logger.Debug("请求视频页面", "url", pageURL)
	resp, err := c.client.R().Get(pageURL)
	if err != nil {
		return "", fmt.Errorf("请求页面失败: %w", err)
	}

	htmlContent := resp.String()
	c.logger.Debug("视频页面响应长度", "len", len(htmlContent))

	pid, err := ExtractPID(htmlContent)
	if err != nil {
		c.logger.Debug("PID提取失败", "error", err, "html_preview", truncateString(htmlContent, 500))
		return "", err
	}

	c.logger.Debug("PID提取成功", "pid", pid)
	return pid, nil
}

// GetVideoInfo 获取视频信息
func (c *CCTVClient) GetVideoInfo(pid string) (*VideoInfo, error) {
	// 生成签名
	tsp := fmt.Sprintf("%d", time.Now().Unix())
	vc := GenerateCNTVSignature(tsp)

	// 构建请求参数
	params := map[string]string{
		"pid":    pid,
		"client": "flash",
		"im":     "0",
		"tsp":    tsp,
		"vn":     Version,
		"uid":    FixedUID,
		"vc":     vc,
	}

	// 请求API
	apiURL := "https://vdn.apps.cntv.cn/api/getHttpVideoInfo.do"
	c.logger.Debug("请求CCTV API", "url", apiURL, "params", params)
	resp, err := c.client.R().SetQueryParams(params).Get(apiURL)
	if err != nil {
		return nil, fmt.Errorf("请求CCTV API失败: %w", err)
	}

	// 记录原始响应（debug级别）
	c.logger.Debug("CCTV API原始响应", "status", resp.StatusCode(), "body", resp.String())

	// 解析响应
	// CCTV API响应结构不固定，HLS URL可能出现在三个位置：
	// 1. 顶层（hls_url, hls_h5e_url 等）
	// 2. manifest 对象内
	// 3. cdn_info 对象内
	// 必须同时解析所有位置，按优先级提取
	var result cctvAPIResponse

	if err := parseJSONResponse(resp.Body(), &result); err != nil {
		return nil, fmt.Errorf("解析API响应失败: %w", err)
	}

	c.logger.Debug("CCTV API解析结果",
		"title", result.Title,
		"top_hls_url", result.HLSURL,
		"top_hls_h5e_url", result.HLSH5EURL,
		"top_hls_enc_url", result.HLSEncURL,
		"top_hls_enc2_url", result.HLSEnc2URL,
		"manifest_hls_url", result.Manifest.HLSURL,
		"manifest_hls_h5e_url", result.Manifest.HLSH5EURL,
		"manifest_hls_enc_url", result.Manifest.HLSEncURL,
		"manifest_hls_enc2_url", result.Manifest.HLSEnc2URL,
		"cdn_hls_h5e_url", result.CDNInfo.HLSH5EURL,
		"cdn_hls_url", result.CDNInfo.HLSURL,
	)

	// 确定HLS流类型和URL
	coverURL := result.Cover
	if coverURL == "" {
		coverURL = result.Image
	}
	info := &VideoInfo{
		PID:       pid,
		RawTitle:  result.Title,
		CoverURL:  coverURL,
		Extra:     make(map[string]any),
	}

	// 检测4K视频：play_channel字段包含"CCTV-4K"
	if strings.Contains(result.PlayChannel, "CCTV-4K") {
		info.Is4K = true
		c.logger.Info("检测到CCTV-4K频道视频", "pid", pid, "play_channel", result.PlayChannel)

		// 4K视频使用顶层hls_url，需要替换main为4000
		if result.HLSURL != "" {
			// 将URL中的main替换为4000以获取4K流
			info.M3U8URL = strings.Replace(result.HLSURL, "main", "4000", 1)
			info.HLSKey = "hls_url"
			info.IsEncrypted = false

			c.logger.Info("4K视频URL处理完成",
				"original_url", result.HLSURL,
				"processed_url", info.M3U8URL,
			)

			return info, nil
		}

		c.logger.Warn("4K视频但未找到顶层hls_url，尝试其他方式")
	}

	// 普通视频：优先选择加密流（hls_h5e_url > hls_enc_url > hls_enc2_url），其次普通流（hls_url）
	// 每种流类型都从多个位置提取：顶层 > manifest > cdn_info
	hlsCandidates := []struct {
		key string
		url string
	}{
		{"hls_h5e_url", result.extractHLSURL("hls_h5e_url")},
		{"hls_enc_url", result.extractHLSURL("hls_enc_url")},
		{"hls_enc2_url", result.extractHLSURL("hls_enc2_url")},
		{"hls_url", result.extractHLSURL("hls_url")},
	}

	for _, candidate := range hlsCandidates {
		if candidate.url != "" {
			info.HLSKey = candidate.key
			info.M3U8URL = candidate.url
			info.IsEncrypted = candidate.key != "hls_url"
			break
		}
	}

	if info.M3U8URL == "" {
		c.logger.Error("未找到可用的HLS流",
			"pid", pid,
			"top_hls_url", result.HLSURL,
			"top_hls_h5e_url", result.HLSH5EURL,
			"manifest_hls_url", result.Manifest.HLSURL,
			"manifest_hls_h5e_url", result.Manifest.HLSH5EURL,
			"cdn_hls_h5e_url", result.CDNInfo.HLSH5EURL,
			"cdn_hls_url", result.CDNInfo.HLSURL,
			"raw_response", resp.String(),
		)
		return nil, fmt.Errorf("未找到可用的HLS流")
	}

	c.logger.Info("获取视频信息成功",
		"pid", pid,
		"title", info.RawTitle,
		"hls_key", info.HLSKey,
		"encrypted", info.IsEncrypted,
		"is_4k", info.Is4K,
	)

	return info, nil
}

// SelectBestStream 从M3U8内容中选择最佳流
func (c *CCTVClient) SelectBestStream(m3u8Content string) (string, error) {
	parser := NewCCTVHLSBestParser()
	return parser.Best(m3u8Content)
}

// truncateString 截断字符串到指定长度，用于日志输出
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
