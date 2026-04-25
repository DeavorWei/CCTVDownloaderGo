package title

import (
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/CCTVDownloadGo/cctvdown/internal/api"
	"github.com/CCTVDownloadGo/cctvdown/internal/utils"
)

var (
	unsafeChars     = regexp.MustCompile(`[^\p{Han}\w\-]`)
	multipleUScores = regexp.MustCompile(`_+`)
	windowsReserved = regexp.MustCompile(`(?i)^(CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])$`)
	onlyUnderscores  = regexp.MustCompile(`^_+$`)
)

// SafeName 生成安全的文件名
func SafeName(title string) string {
	// 替换特殊字符为下划线
	safe := unsafeChars.ReplaceAllString(title, "_")

	// 合并连续的下划线为单个下划线
	safe = multipleUScores.ReplaceAllString(safe, "_")

	// 移除开头和结尾的下划线
	safe = strings.Trim(safe, "_")

	// 空或全下划线时使用默认名
	if safe == "" || onlyUnderscores.MatchString(safe) {
		safe = "video"
	}

	// 限制长度（使用字符数而非字节数）
	if utils.RuneCount(safe) > 150 {
		safe = utils.TruncateString(safe, 150)
	}

	// Windows保留名处理
	if windowsReserved.MatchString(safe) {
		safe = "_" + safe
	}

	return safe
}

// Service 标题服务
type Service struct {
	client *api.CCTVClient
	logger *slog.Logger
}

// NewService 创建标题服务
func NewService(client *api.CCTVClient, logger *slog.Logger) *Service {
	return &Service{
		client: client,
		logger: logger,
	}
}

// GetTitle 获取视频标题
func (s *Service) GetTitle(pid string) (string, error) {
	// 调用API获取视频信息
	info, err := s.client.GetVideoInfo(pid)
	if err != nil {
		return "", fmt.Errorf("获取视频信息失败: %w", err)
	}

	if info.RawTitle == "" {
		return pid, nil // 降级使用PID
	}

	return info.RawTitle, nil
}
