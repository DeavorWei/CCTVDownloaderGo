package api

import (
	"fmt"
	"log/slog"
	"time"
)

// AlbumService 专辑服务 - 整合页面提取、栏目API、专辑API和4K API
type AlbumService struct {
	pageExtractor *PageExtractor
	columnClient  *ColumnClient
	albumClient   *AlbumClient
	cctv4kClient  *CCTV4KClient
	logger        *slog.Logger
}

// NewAlbumService 创建专辑服务
func NewAlbumService(userAgent string, timeout time.Duration, logger *slog.Logger) *AlbumService {
	return &AlbumService{
		pageExtractor: NewPageExtractor(userAgent, timeout, logger),
		columnClient:  NewColumnClient(userAgent, timeout, logger),
		albumClient:   NewAlbumClient(userAgent, timeout, logger),
		cctv4kClient:  NewCCTV4KClient(userAgent, timeout, logger),
		logger:        logger,
	}
}

// DateRange 日期范围
type DateRange struct {
	StartDate string // 起始日期 yyyyMM格式
	EndDate   string // 结束日期 yyyyMM格式
}

// GetAlbumFromURL 从URL获取专辑视频列表
// 流程：
// 1. 如果是4K频道，使用4K专用API
// 2. 先尝试栏目方式，失败则尝试专辑方式
func (s *AlbumService) GetAlbumFromURL(pageURL string, dateRange DateRange) (*AlbumInfo, error) {
	// 1. 提取页面信息
	pageInfo, err := s.pageExtractor.ExtractPageInfo(pageURL)
	if err != nil {
		return nil, fmt.Errorf("提取页面信息失败: %w", err)
	}

	// 2. 检测是否为4K频道
	if pageInfo.Is4K {
		s.logger.Info("检测到4K频道，使用4K专用API", "guid", pageInfo.GUID)
		return s.getAlbumFrom4KChannel(pageInfo)
	}

	// 3. 生成日期列表
	dateList := generateDateRange(dateRange.StartDate, dateRange.EndDate)
	s.logger.Info("生成日期列表", "dates", dateList)

	// 4. 先尝试栏目方式
	videos, err := s.columnClient.GetVideoListByColumn(pageInfo.ColumnID, dateList)
	if err == nil && len(videos) > 0 {
		s.logger.Info("栏目方式获取成功", "count", len(videos))
		return &AlbumInfo{
			Title:  pageInfo.Title,
			Videos: videos,
			Total:  len(videos),
		}, nil
	}

	s.logger.Info("栏目方式获取失败，尝试专辑方式")

	// 5. 获取真实专辑ID
	realAlbumID, err := s.albumClient.GetRealAlbumID(pageInfo.ItemID)
	if err != nil {
		return nil, fmt.Errorf("获取真实专辑ID失败: %w", err)
	}

	// 6. 尝试专辑方式
	videos, err = s.albumClient.GetVideoListByAlbum(realAlbumID, dateList)
	if err != nil || len(videos) == 0 {
		return nil, fmt.Errorf("所有方式都无法获取视频列表")
	}

	s.logger.Info("专辑方式获取成功", "count", len(videos))
	return &AlbumInfo{
		ID:     realAlbumID,
		Title:  pageInfo.Title,
		Videos: videos,
		Total:  len(videos),
	}, nil
}

// getAlbumFrom4KChannel 从4K频道获取视频列表
func (s *AlbumService) getAlbumFrom4KChannel(pageInfo *PageInfo) (*AlbumInfo, error) {
	// 使用4K专用API获取视频信息
	videos, err := s.cctv4kClient.GetVideoListByGUID(pageInfo.GUID)
	if err != nil {
		return nil, fmt.Errorf("获取4K视频列表失败: %w", err)
	}

	s.logger.Info("4K方式获取成功", "count", len(videos))
	return &AlbumInfo{
		ID:     pageInfo.GUID,
		Title:  pageInfo.Title,
		Videos: videos,
		Total:  len(videos),
	}, nil
}

// generateDateRange 生成日期列表（yyyyMM格式）
// 如果startDate和endDate都为空，返回当前月份往前12个月
func generateDateRange(startDate, endDate string) []string {
	now := time.Now()

	// 解析起始日期
	var start time.Time
	if startDate != "" {
		start, _ = time.Parse("200601", startDate)
		if start.IsZero() {
			start = now
		}
	} else {
		start = now
	}

	// 解析结束日期
	var end time.Time
	if endDate != "" {
		end, _ = time.Parse("200601", endDate)
		if end.IsZero() {
			end = now.AddDate(-1, 0, 0) // 默认往前一年
		}
	} else {
		end = now.AddDate(-1, 0, 0) // 默认往前一年
	}

	// 确保start >= end
	if start.Before(end) {
		start, end = end, start
	}

	// 生成月份列表
	var dateList []string
	for date := start; date.After(end) || date.Equal(end); date = date.AddDate(0, -1, 0) {
		dateList = append(dateList, date.Format("200601"))
	}

	return dateList
}