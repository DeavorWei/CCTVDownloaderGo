package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/viper"
)

// ConfigFileName 配置文件名
const ConfigFileName = "cctvdown.yaml"

// Config 应用配置
type Config struct {
	// 下载设置
	DownloadWorkers   int    `mapstructure:"download_workers"`
	FFmpegConcurrency int    `mapstructure:"ffmpeg_concurrency"`
	OutputDir         string `mapstructure:"output_dir"`
	TempDir           string `mapstructure:"temp_dir"`

	// 专辑流水线配置
	AlbumDownloadSlots  int `mapstructure:"album_download_slots"`  // 同时下载的视频数
	AlbumProcessWorkers int `mapstructure:"album_process_workers"` // 同时处理的视频数
	DecryptWorkers      int `mapstructure:"decrypt_workers"`       // 解密并行进程数

	// 工具路径
	FFmpegPath string `mapstructure:"ffmpeg_path"`
	NodePath   string `mapstructure:"node_path"`

	// 网络设置
	Timeout    time.Duration `mapstructure:"timeout"`
	MaxRetries int           `mapstructure:"max_retries"`
	UserAgent  string        `mapstructure:"user_agent"`

	// 日志
	LogLevel string `mapstructure:"log_level"`
	LogFile  string `mapstructure:"log_file"`
	Verbose  bool   `mapstructure:"verbose"`
	Quiet    bool   `mapstructure:"quiet"`
}

// DefaultConfig 返回默认配置
func DefaultConfig() *Config {
	outputDir := filepath.Join(".", "videos")
	tempDir := filepath.Join(os.TempDir(), "cctvdown")
	logFile := filepath.Join(".", "logs", "app.log")

	return &Config{
		DownloadWorkers:     8,
		FFmpegConcurrency:   8,
		OutputDir:           outputDir,
		TempDir:             tempDir,
		AlbumDownloadSlots:  1, // 同时下载1个视频，下载完成后立即开始下一个
		AlbumProcessWorkers: 2, // 同时处理2个视频
		DecryptWorkers:      8, // 解密并行进程数
		FFmpegPath:          "ffmpeg",
		NodePath:            "node",
		Timeout:             30 * time.Second,
		MaxRetries:          3,
		UserAgent:           "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/147.0.0.0 Safari/537.36",
		LogLevel:            "info",
		LogFile:             logFile,
		Verbose:             false,
		Quiet:               false,
	}
}

// EnsureConfigFile 确保配置文件存在，如果不存在则生成默认配置文件
func EnsureConfigFile() error {
	// 检查配置文件是否已存在
	if _, err := os.Stat(ConfigFileName); err == nil {
		return nil // 文件已存在
	}

	// 生成默认配置文件
	content := generateDefaultConfigContent()
	if err := os.WriteFile(ConfigFileName, []byte(content), 0644); err != nil {
		return fmt.Errorf("写入配置文件失败: %w", err)
	}

	return nil
}

// generateDefaultConfigContent 生成带注释的默认配置文件内容
func generateDefaultConfigContent() string {
	return `# ============================================================
# CCTV视频下载器配置文件
# ============================================================
# 本配置文件包含所有可配置参数及其默认值
# 首次启动时自动生成，可根据需要修改
# 配置优先级: CLI参数 > 环境变量(CCTVDOWN_*) > 配置文件 > 默认值
# ============================================================

# ----------------------
# 日志配置
# ----------------------

# 日志级别
# 可选值: verbose, debug, info, warn, error
# - verbose: 最详细日志，包含分片级别的下载进度
# - debug:   详细调试信息，包含所有请求和响应
# - info:    常规信息，显示下载进度和关键步骤（推荐）
# - warn:    警告信息，显示潜在问题
# - error:   仅显示错误信息
log_level: info

# 日志文件路径
# 日志会同时输出到控制台和文件
# 默认: ./logs/app.log
log_file: ./logs/app.log

# 详细输出模式
# 设置为true时等同于 log_level=debug
verbose: false

# 静默模式
# 设置为true时等同于 log_level=error
quiet: false

# ----------------------
# 下载配置
# ----------------------

# 下载并发数
# 范围: 1-32
# 建议值: 8-16，过高可能导致服务器限速
download_workers: 8

# FFmpeg最大并发数
# 范围: 1-16
# 建议值: 4-8，取决于CPU性能
ffmpeg_concurrency: 8

# 输出目录
# 下载的视频文件保存路径
# 支持相对路径和绝对路径
output_dir: ./videos

# 临时目录
# 下载过程中的临时文件存放路径
# 留空使用系统临时目录
temp_dir: ""

# ----------------------
# 专辑流水线配置
# ----------------------

# 同时下载的视频数
# 范围: 1-8
# 控制专辑批量下载时的并行下载数
# 设置为1时：前一个视频下载完成后立即开始下一个，无需等待处理完成
# 建议值: 1，可最大化网络利用率
album_download_slots: 1

# 同时处理的视频数
# 范围: 1-8
# 控制解密和合并的并行处理数
# 建议值: 2-4，取决于CPU和磁盘性能
album_process_workers: 2

# 解密并行进程数
# 范围: 1-16
# 每个视频解密时的并行度
# 建议值: 4-8，取决于CPU性能
decrypt_workers: 8

# ----------------------
# 工具路径
# ----------------------

# FFmpeg可执行文件路径
# 留空则使用系统PATH中的ffmpeg
# Windows示例: C:/ffmpeg/bin/ffmpeg.exe
# Linux/macOS示例: /usr/local/bin/ffmpeg
ffmpeg_path: ffmpeg

# Node.js可执行文件路径
# 留空则使用系统PATH中的node
# 用于执行解密脚本
# Windows示例: C:/Program Files/nodejs/node.exe
# Linux/macOS示例: /usr/bin/node
node_path: node

# ----------------------
# 网络配置
# ----------------------

# 请求超时时间
# 格式: 数值+单位，如 30s, 1m, 2h
# 建议值: 30s-60s
timeout: 30s

# 最大重试次数
# 范围: 0-10
# 网络请求失败时的自动重试次数
max_retries: 3

# User-Agent
# 模拟浏览器请求头，用于绕过简单的反爬检测
# 一般无需修改
user_agent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/147.0.0.0 Safari/537.36"
`
}

// LoadConfig 加载配置，优先级：CLI参数 > 环境变量(CCTVDOWN_*) > 配置文件 > 默认值
func LoadConfig() (*Config, error) {
	cfg := DefaultConfig()

	v := viper.New()

	// 设置默认值
	v.SetDefault("download_workers", cfg.DownloadWorkers)
	v.SetDefault("ffmpeg_concurrency", cfg.FFmpegConcurrency)
	v.SetDefault("output_dir", cfg.OutputDir)
	v.SetDefault("temp_dir", cfg.TempDir)
	v.SetDefault("album_download_slots", cfg.AlbumDownloadSlots)
	v.SetDefault("album_process_workers", cfg.AlbumProcessWorkers)
	v.SetDefault("decrypt_workers", cfg.DecryptWorkers)
	v.SetDefault("ffmpeg_path", cfg.FFmpegPath)
	v.SetDefault("node_path", cfg.NodePath)
	v.SetDefault("timeout", cfg.Timeout)
	v.SetDefault("max_retries", cfg.MaxRetries)
	v.SetDefault("user_agent", cfg.UserAgent)
	v.SetDefault("log_level", cfg.LogLevel)
	v.SetDefault("log_file", cfg.LogFile)
	v.SetDefault("verbose", cfg.Verbose)
	v.SetDefault("quiet", cfg.Quiet)

	// 环境变量绑定
	v.SetEnvPrefix("CCTVDOWN")
	v.AutomaticEnv()

	// 配置文件搜索（仅当前目录）
	v.SetConfigName("cctvdown")
	v.SetConfigType("yaml")
	v.AddConfigPath(".")

	// 读取配置文件（忽略不存在的情况）
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("读取配置文件失败: %w", err)
		}
	}

	// 反序列化到Config结构
	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("解析配置失败: %w", err)
	}

	// 后处理：修正空值（配置文件中显式设置为空字符串的情况）
	// Viper 会用配置文件中的显式空值覆盖默认值，需要重新设置
	if cfg.TempDir == "" {
		cfg.TempDir = filepath.Join(os.TempDir(), "cctvdown")
	}
	if cfg.OutputDir == "" {
		cfg.OutputDir = filepath.Join(".", "videos")
	}
	if cfg.FFmpegPath == "" {
		cfg.FFmpegPath = "ffmpeg"
	}
	if cfg.NodePath == "" {
		cfg.NodePath = "node"
	}
	if cfg.LogFile == "" {
		cfg.LogFile = filepath.Join(".", "logs", "app.log")
	}

	return cfg, nil
}

// GetTempWorkDir 获取本次任务的临时工作目录
func (c *Config) GetTempWorkDir(guid string) string {
	return filepath.Join(c.TempDir, guid)
}

// EnsureDirs 确保输出目录和临时目录存在
func (c *Config) EnsureDirs() error {
	if err := os.MkdirAll(c.OutputDir, 0755); err != nil {
		return fmt.Errorf("创建输出目录失败: %w", err)
	}
	if err := os.MkdirAll(c.TempDir, 0755); err != nil {
		return fmt.Errorf("创建临时目录失败: %w", err)
	}
	return nil
}
