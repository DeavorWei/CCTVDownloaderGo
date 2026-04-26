// Package state 提供专辑下载状态持久化管理
package state

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// AlbumStatus 专辑下载状态枚举
type AlbumStatus string

const (
	// AlbumStatusInProgress 下载中
	AlbumStatusInProgress AlbumStatus = "in_progress"
	// AlbumStatusCompleted 已完成
	AlbumStatusCompleted AlbumStatus = "completed"
	// AlbumStatusCancelled 已取消
	AlbumStatusCancelled AlbumStatus = "cancelled"
	// AlbumStatusFailed 失败
	AlbumStatusFailed AlbumStatus = "failed"
)

// DateRange 日期范围
type DateRange struct {
	StartDate string `json:"start_date"` // 起始日期 yyyyMM
	EndDate   string `json:"end_date"`   // 结束日期 yyyyMM
}

// VideoDownloadResult 视频下载结果
type VideoDownloadResult struct {
	GUID       string    `json:"guid"`
	Title      string    `json:"title"`
	Status     string    `json:"status"`     // success, failed, skipped
	Error      string    `json:"error"`      // 错误信息
	Timestamp  time.Time `json:"timestamp"`  // 完成时间
	OutputPath string    `json:"output_path"` // 输出文件路径
}

// AlbumDownloadState 专辑下载状态
type AlbumDownloadState struct {
	// 专辑标识
	AlbumID    string `json:"album_id"`    // 专辑ID（唯一标识）
	AlbumTitle string `json:"album_title"` // 专辑标题（显示用）
	SourceURL  string `json:"source_url"`  // 来源URL

	// 下载配置
	DateRange      DateRange `json:"date_range"`       // 日期范围
	TotalCount     int       `json:"total_count"`      // 视频总数
	AlbumOutputDir string    `json:"album_output_dir"` // 专辑输出目录（子目录路径）

	// 下载进度
	CompletedGUIDs []string `json:"completed_guids"` // 已完成视频GUID列表
	FailedGUIDs    []string `json:"failed_guids"`    // 失败视频GUID列表

	// 下载结果详情
	Results []VideoDownloadResult `json:"results"`

	// 时间戳
	StartTime   time.Time `json:"start_time"`   // 开始时间
	LastUpdated time.Time `json:"last_updated"` // 最后更新时间
	EndTime     time.Time `json:"end_time"`     // 结束时间（完成时）

	// 状态
	Status AlbumStatus `json:"status"` // 下载状态
}

// AlbumStateManager 专辑状态管理器
type AlbumStateManager struct {
	tempDir string        // 状态文件存储目录
	logger  *slog.Logger  // 日志记录器
	mu      sync.RWMutex  // 读写锁，保护并发访问
}

// NewAlbumStateManager 创建状态管理器
func NewAlbumStateManager(tempDir string, logger *slog.Logger) *AlbumStateManager {
	// 状态文件存储在 tempDir/albums 子目录下
	stateDir := filepath.Join(tempDir, "albums")
	return &AlbumStateManager{
		tempDir: stateDir,
		logger:  logger,
	}
}

// EnsureDir 确保状态文件目录存在
func (m *AlbumStateManager) EnsureDir() error {
	if err := os.MkdirAll(m.tempDir, 0755); err != nil {
		return fmt.Errorf("创建状态目录失败: %w", err)
	}
	return nil
}

// getStateFilePath 获取状态文件路径
func (m *AlbumStateManager) getStateFilePath(albumID string) string {
	// 使用专辑ID作为文件名，确保文件名安全
	safeID := safeFilename(albumID)
	return filepath.Join(m.tempDir, safeID+".json")
}

// Load 加载专辑下载状态
func (m *AlbumStateManager) Load(albumID string) (*AlbumDownloadState, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	filePath := m.getStateFilePath(albumID)

	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // 文件不存在，返回nil表示无状态
		}
		return nil, fmt.Errorf("读取状态文件失败: %w", err)
	}

	var state AlbumDownloadState
	if err := json.Unmarshal(data, &state); err != nil {
		m.logger.Warn("状态文件格式错误，将忽略", "path", filePath, "error", err)
		return nil, fmt.Errorf("解析状态文件失败: %w", err)
	}

	m.logger.Debug("加载专辑状态", "album_id", albumID, "completed", len(state.CompletedGUIDs))
	return &state, nil
}

// Save 保存专辑下载状态
func (m *AlbumStateManager) Save(state *AlbumDownloadState) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.EnsureDir(); err != nil {
		return err
	}

	state.LastUpdated = time.Now()

	filePath := m.getStateFilePath(state.AlbumID)

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化状态失败: %w", err)
	}

	// 使用临时文件+原子重命名，避免写入过程中断导致文件损坏
	tmpPath := filePath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("写入状态文件失败: %w", err)
	}

	if err := os.Rename(tmpPath, filePath); err != nil {
		os.Remove(tmpPath) // 清理临时文件
		return fmt.Errorf("保存状态文件失败: %w", err)
	}

	m.logger.Debug("保存专辑状态", "album_id", state.AlbumID, "completed", len(state.CompletedGUIDs))
	return nil
}

// Delete 删除专辑下载状态
func (m *AlbumStateManager) Delete(albumID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	filePath := m.getStateFilePath(albumID)

	if err := os.Remove(filePath); err != nil {
		if os.IsNotExist(err) {
			return nil // 文件不存在，无需删除
		}
		return fmt.Errorf("删除状态文件失败: %w", err)
	}

	m.logger.Debug("删除专辑状态", "album_id", albumID)
	return nil
}

// List 列出所有未完成的下载状态
func (m *AlbumStateManager) List() ([]*AlbumDownloadState, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if err := m.EnsureDir(); err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(m.tempDir)
	if err != nil {
		return nil, fmt.Errorf("读取状态目录失败: %w", err)
	}

	var states []*AlbumDownloadState
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		filePath := filepath.Join(m.tempDir, entry.Name())
		data, err := os.ReadFile(filePath)
		if err != nil {
			m.logger.Warn("读取状态文件失败", "file", entry.Name(), "error", err)
			continue
		}

		var state AlbumDownloadState
		if err := json.Unmarshal(data, &state); err != nil {
			m.logger.Warn("解析状态文件失败", "file", entry.Name(), "error", err)
			continue
		}

		// 只返回未完成的状态
		if state.Status == AlbumStatusInProgress {
			states = append(states, &state)
		}
	}

	return states, nil
}

// CreateNewState 创建新的下载状态
func (m *AlbumStateManager) CreateNewState(albumID, albumTitle, sourceURL string, dateRange DateRange, totalCount int) *AlbumDownloadState {
	return &AlbumDownloadState{
		AlbumID:        albumID,
		AlbumTitle:     albumTitle,
		SourceURL:      sourceURL,
		DateRange:      dateRange,
		TotalCount:     totalCount,
		CompletedGUIDs: make([]string, 0),
		FailedGUIDs:    make([]string, 0),
		Results:        make([]VideoDownloadResult, 0),
		StartTime:      time.Now(),
		LastUpdated:    time.Now(),
		Status:         AlbumStatusInProgress,
	}
}

// MarkVideoCompleted 标记视频下载完成
func (m *AlbumStateManager) MarkVideoCompleted(albumID, guid, title, outputPath string) error {
	state, err := m.Load(albumID)
	if err != nil {
		return err
	}
	if state == nil {
		return fmt.Errorf("专辑状态不存在: %s", albumID)
	}

	// 添加到已完成列表（避免重复）
	if !contains(state.CompletedGUIDs, guid) {
		state.CompletedGUIDs = append(state.CompletedGUIDs, guid)
	}

	// 从失败列表移除（如果存在）
	state.FailedGUIDs = remove(state.FailedGUIDs, guid)

	// 添加结果记录
	state.Results = append(state.Results, VideoDownloadResult{
		GUID:       guid,
		Title:      title,
		Status:     "success",
		Timestamp:  time.Now(),
		OutputPath: outputPath,
	})

	// 检查是否全部完成
	if len(state.CompletedGUIDs) == state.TotalCount {
		state.Status = AlbumStatusCompleted
		state.EndTime = time.Now()
	}

	return m.Save(state)
}

// MarkVideoFailed 标记视频下载失败
func (m *AlbumStateManager) MarkVideoFailed(albumID, guid, title, errMsg string) error {
	state, err := m.Load(albumID)
	if err != nil {
		return err
	}
	if state == nil {
		return fmt.Errorf("专辑状态不存在: %s", albumID)
	}

	// 添加到失败列表（避免重复）
	if !contains(state.FailedGUIDs, guid) {
		state.FailedGUIDs = append(state.FailedGUIDs, guid)
	}

	// 添加结果记录
	state.Results = append(state.Results, VideoDownloadResult{
		GUID:      guid,
		Title:     title,
		Status:    "failed",
		Error:     errMsg,
		Timestamp: time.Now(),
	})

	return m.Save(state)
}

// MarkVideoSkipped 标记视频被跳过（续传时已下载）
func (m *AlbumStateManager) MarkVideoSkipped(albumID, guid, title, outputPath string) error {
	state, err := m.Load(albumID)
	if err != nil {
		return err
	}
	if state == nil {
		return fmt.Errorf("专辑状态不存在: %s", albumID)
	}

	// 添加结果记录
	state.Results = append(state.Results, VideoDownloadResult{
		GUID:       guid,
		Title:      title,
		Status:     "skipped",
		Timestamp:  time.Now(),
		OutputPath: outputPath,
	})

	return m.Save(state)
}

// IsVideoCompleted 检查视频是否已完成
func (m *AlbumStateManager) IsVideoCompleted(albumID, guid string) bool {
	state, err := m.Load(albumID)
	if err != nil || state == nil {
		return false
	}
	return contains(state.CompletedGUIDs, guid)
}

// MarkVideoDownloading 标记视频正在下载
func (m *AlbumStateManager) MarkVideoDownloading(albumID, guid, title string) error {
	// 暂时不需要持久化，仅用于进度追踪
	return nil
}

// MarkVideoProcessing 标记视频正在处理（解密/合并）
func (m *AlbumStateManager) MarkVideoProcessing(albumID, guid, title string) error {
	// 暂时不需要持久化，仅用于进度追踪
	return nil
}

// GetProgress 获取下载进度
func (m *AlbumStateManager) GetProgress(albumID string) (completed, total int, err error) {
	state, err := m.Load(albumID)
	if err != nil {
		return 0, 0, err
	}
	if state == nil {
		return 0, 0, nil
	}
	return len(state.CompletedGUIDs), state.TotalCount, nil
}

// MarkAlbumCancelled 标记专辑下载已取消
func (m *AlbumStateManager) MarkAlbumCancelled(albumID string) error {
	state, err := m.Load(albumID)
	if err != nil {
		return err
	}
	if state == nil {
		return nil
	}

	state.Status = AlbumStatusCancelled
	state.EndTime = time.Now()
	return m.Save(state)
}

// ClearCompleted 删除所有已完成的下载状态
func (m *AlbumStateManager) ClearCompleted() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	entries, err := os.ReadDir(m.tempDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("读取状态目录失败: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		filePath := filepath.Join(m.tempDir, entry.Name())
		data, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}

		var state AlbumDownloadState
		if err := json.Unmarshal(data, &state); err != nil {
			continue
		}

		// 删除已完成或已取消的状态
		if state.Status == AlbumStatusCompleted || state.Status == AlbumStatusCancelled {
			os.Remove(filePath)
			m.logger.Debug("清理已完成状态", "album_id", state.AlbumID)
		}
	}

	return nil
}

// 辅助函数

// contains 检查切片中是否包含指定元素
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// remove 从切片中移除指定元素
func remove(slice []string, item string) []string {
	result := make([]string, 0, len(slice))
	for _, s := range slice {
		if s != item {
			result = append(result, s)
		}
	}
	return result
}

// safeFilename 将字符串转换为安全的文件名
func safeFilename(s string) string {
	// 替换不安全的字符
	result := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			result = append(result, r)
		case r >= 'A' && r <= 'Z':
			result = append(result, r)
		case r >= '0' && r <= '9':
			result = append(result, r)
		case r == '-' || r == '_':
			result = append(result, r)
		default:
			// 其他字符替换为下划线
			result = append(result, '_')
		}
	}
	return string(result)
}
