package api

import (
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/go-resty/resty/v2"
)

// PageInfo 页面信息
type PageInfo struct {
	Title    string // 节目标题
	ItemID   string // 视频ID
	ColumnID string // 栏目ID
	GUID     string // 视频GUID（用于4K视频）
	Is4K     bool   // 是否为4K频道视频
}

// ErrPageInfoNotFound 无法从页面提取信息
var ErrPageInfoNotFound = errors.New("无法从页面提取完整信息")

// pageInfoPatterns 页面信息提取正则规则
// 索引: 0=commentTitle, 1=itemid1, 2=column_id, 3=guid
var pageInfoPatterns = []string{
	`var commentTitle\s*=\s*["'](.*?)["'];`,
	`var itemid1\s*=\s*["'](.*?)["'];`,
	`var column_id\s*=\s*["'](.*?)["'];`,
	`var guid\s*=\s*["'](.*?)["'];`,
}

// compiledPageInfoPatterns 预编译正则
var compiledPageInfoPatterns []*regexp.Regexp

func init() {
	for _, pattern := range pageInfoPatterns {
		re := regexp.MustCompile(pattern)
		compiledPageInfoPatterns = append(compiledPageInfoPatterns, re)
	}
}

// PageExtractor 页面信息提取器
type PageExtractor struct {
	client *resty.Client
	logger *slog.Logger
}

// NewPageExtractor 创建页面信息提取器
func NewPageExtractor(userAgent string, timeout time.Duration, logger *slog.Logger) *PageExtractor {
	client := resty.New().
		SetHeader("User-Agent", userAgent).
		SetTimeout(timeout).
		SetRetryCount(3).
		SetRetryWaitTime(2 * time.Second)

	return &PageExtractor{
		client: client,
		logger: logger,
	}
}

// ExtractPageInfo 从页面URL提取关键信息
func (e *PageExtractor) ExtractPageInfo(pageURL string) (*PageInfo, error) {
	e.logger.Debug("请求视频页面", "url", pageURL)

	resp, err := e.client.R().Get(pageURL)
	if err != nil {
		return nil, err
	}

	htmlContent := resp.String()
	e.logger.Debug("视频页面响应长度", "len", len(htmlContent))

	info, err := ExtractPageInfoFromHTML(htmlContent)
	if err != nil {
		e.logger.Debug("页面信息提取失败", "error", err, "html_preview", truncateString(htmlContent, 500))
		return nil, err
	}

	e.logger.Info("页面信息提取成功",
		"title", info.Title,
		"item_id", info.ItemID,
		"column_id", info.ColumnID,
	)

	return info, nil
}

// ExtractPageInfoFromHTML 从HTML内容提取页面信息
func ExtractPageInfoFromHTML(htmlContent string) (*PageInfo, error) {
	info := &PageInfo{}

	// 提取标题（取第一个空格前的部分）
	if match := compiledPageInfoPatterns[0].FindStringSubmatch(htmlContent); match != nil {
		info.Title = strings.Split(match[1], " ")[0]
	}

	// 提取item_id
	if match := compiledPageInfoPatterns[1].FindStringSubmatch(htmlContent); match != nil {
		info.ItemID = match[1]
	}

	// 提取column_id
	if match := compiledPageInfoPatterns[2].FindStringSubmatch(htmlContent); match != nil {
		info.ColumnID = match[1]
	}

	// 提取guid（用于4K视频）
	if len(compiledPageInfoPatterns) > 3 {
		if match := compiledPageInfoPatterns[3].FindStringSubmatch(htmlContent); match != nil {
			info.GUID = match[1]
		}
	}

	// 4K视频特殊处理：当column_id为空时使用guid作为替代
	if info.ColumnID == "" && info.GUID != "" {
		info.ColumnID = info.GUID
		info.Title = "CCTV-4K"
		info.Is4K = true
	}

	// 验证数据完整性
	if info.Title == "" || info.ItemID == "" || info.ColumnID == "" {
		return nil, ErrPageInfoNotFound
	}

	return info, nil
}