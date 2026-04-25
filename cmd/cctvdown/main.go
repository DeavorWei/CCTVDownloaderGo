package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/CCTVDownloadGo/cctvdown/internal/config"
	"github.com/CCTVDownloadGo/cctvdown/internal/notify"
	"github.com/CCTVDownloadGo/cctvdown/internal/utils"
	"github.com/spf13/cobra"
)

var (
	cfg     *config.Config
	Version = "dev" // 由构建时注入
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 信号处理：优雅关闭
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		slog.Warn("收到中断信号，正在清理...", "signal", sig)
		cancel()
	}()

	// run() 负责初始化日志，返回 logCloser 由 main() 管理
	logCloser, err := run(ctx)
	if logCloser != nil {
		defer logCloser.Close()
	}

	if err != nil {
		if errors.Is(err, context.Canceled) {
			slog.Info("用户取消操作")
		} else {
			slog.Error("执行失败", "error", err)
			os.Exit(1)
		}
	}
}

func run(ctx context.Context) (io.Closer, error) {
	// 首次启动检查：如果配置文件不存在则生成默认配置文件
	if err := config.EnsureConfigFile(); err != nil {
		slog.Warn("生成默认配置文件失败", "error", err)
	}

	// 加载配置：优先级 CLI参数 > 环境变量 > 配置文件 > 默认值
	loadedCfg, err := config.LoadConfig()
	if err != nil {
		// 配置加载失败时使用默认配置，但记录警告
		slog.Warn("配置加载失败，使用默认配置", "error", err)
		cfg = config.DefaultConfig()
	} else {
		cfg = loadedCfg
	}

	// 初始化日志系统（只初始化一次），返回 logCloser 由 main() 管理
	logger, logCloser := notify.SetupLogger(cfg.LogLevel, cfg.LogFile)
	slog.SetDefault(logger)

	rootCmd := &cobra.Command{
		Use:   "cctvdown",
		Short: "CCTV视频下载器 - 高性能Go语言实现",
		Long: `CCTV视频下载器 (cctvdown)
基于Go语言的高性能CCTV视频下载CLI工具，支持加密流解密和普通流直接下载。

支持两种输入方式：
  - URL自动提取PID: cctvdown get -u https://tv.cctv.com/...
  - 直接输入GUID:    cctvdown get -g <32位十六进制GUID>`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	// 全局标志
	rootCmd.PersistentFlags().StringVar(&cfg.OutputDir, "output", cfg.OutputDir, "输出目录")
	rootCmd.PersistentFlags().StringVar(&cfg.FFmpegPath, "ffmpeg", cfg.FFmpegPath, "FFmpeg可执行路径")
	rootCmd.PersistentFlags().StringVar(&cfg.NodePath, "node", cfg.NodePath, "Node.js可执行路径")
	rootCmd.PersistentFlags().IntVar(&cfg.DownloadWorkers, "workers", cfg.DownloadWorkers, "下载并发数")
	rootCmd.PersistentFlags().IntVar(&cfg.FFmpegConcurrency, "ffmpeg-concurrency", cfg.FFmpegConcurrency, "FFmpeg最大并发数")
	rootCmd.PersistentFlags().StringVar(&cfg.LogLevel, "log-level", cfg.LogLevel, "日志级别 (debug|info|warn|error)")
	rootCmd.PersistentFlags().StringVar(&cfg.LogFile, "log-file", cfg.LogFile, "日志文件路径")
	rootCmd.PersistentFlags().BoolVarP(&cfg.Verbose, "verbose", "v", cfg.Verbose, "详细输出（等同于--log-level=debug）")
	rootCmd.PersistentFlags().BoolVarP(&cfg.Quiet, "quiet", "q", cfg.Quiet, "静默模式（等同于--log-level=error）")

	// get 命令
	getCmd := &cobra.Command{
		Use:   "get",
		Short: "下载CCTV视频",
		Long: `下载CCTV视频，支持URL和GUID两种输入方式。

示例：
  cctvdown get -u https://tv.cctv.com/2024/01/01/VIDE1234567890.shtml
  cctvdown get -g 1234567890abcdef1234567890abcdef
  cctvdown get -u https://tv.cctv.com/... -o ./my_videos`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGet(ctx, cmd)
		},
	}

	getCmd.Flags().StringP("url", "u", "", "CCTV视频页面URL")
	getCmd.Flags().StringP("guid", "g", "", "视频GUID（32位十六进制字符）")

	rootCmd.AddCommand(getCmd)

	// album 命令
	albumCmd := &cobra.Command{
		Use:   "album",
		Short: "专辑视频批量下载",
		Long: `通过单个视频链接获取整个专辑的所有视频并批量下载。

示例：
	 cctvdown album get -u https://tv.cctv.com/...
	 cctvdown album get -u https://tv.cctv.com/... -s 202401 -e 202312
	 cctvdown album get -u https://tv.cctv.com/... --skip
	 cctvdown album get -u https://tv.cctv.com/... --restart  # 强制重新开始
	 cctvdown album list  # 列出未完成的下载任务
	 cctvdown album clear # 清理已完成的下载状态`,
	}

	// album get 子命令
	albumGetCmd := &cobra.Command{
		Use:   "get",
		Short: "获取并下载专辑视频",
		Long: `获取专辑视频列表并批量下载。

默认流程：
	 1. 获取并打印专辑视频列表
	 2. 等待用户输入 'y' 确认开始下载
	 3. 执行批量下载

使用 --skip 跳过确认直接下载。
使用 --restart 强制重新开始，忽略已有的下载进度。

断点续传：
	 如果存在未完成的下载任务，程序会提示用户选择：
	 [c] 继续下载 - 跳过已完成的视频
	 [r] 重新开始 - 删除进度，重新下载全部
	 [x] 取消退出`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAlbumGet(ctx, cmd)
		},
	}

	albumGetCmd.Flags().StringP("url", "u", "", "CCTV视频页面URL")
	albumGetCmd.Flags().StringP("start-date", "s", "", "起始日期 (格式: yyyyMM)")
	albumGetCmd.Flags().StringP("end-date", "e", "", "结束日期 (格式: yyyyMM)")
	albumGetCmd.Flags().Bool("skip", false, "跳过确认直接开始下载")
	albumGetCmd.Flags().Bool("restart", false, "强制重新开始，忽略已有的下载进度")
	albumGetCmd.Flags().Int("download-slots", cfg.AlbumDownloadSlots, "同时下载的视频数")
	albumGetCmd.Flags().Int("process-workers", cfg.AlbumProcessWorkers, "同时处理的视频数")
	albumGetCmd.Flags().Int("decrypt-workers", cfg.DecryptWorkers, "解密并行进程数")

	// album list 子命令
	albumListCmd := &cobra.Command{
		Use:   "list",
		Short: "列出未完成的下载任务",
		Long: `列出所有未完成的专辑下载任务。

显示信息包括：
	 - 专辑名称
	 - 下载进度
	 - 失败数量
	 - 来源URL
	 - 最后更新时间`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAlbumList(ctx)
		},
	}

	// album clear 子命令
	albumClearCmd := &cobra.Command{
		Use:   "clear",
		Short: "清理已完成的下载状态",
		Long: `清理所有已完成或已取消的专辑下载状态文件。

注意：清理后无法恢复下载进度。`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAlbumClear(ctx)
		},
	}

	albumCmd.AddCommand(albumGetCmd)
	albumCmd.AddCommand(albumListCmd)
	albumCmd.AddCommand(albumClearCmd)
	rootCmd.AddCommand(albumCmd)

	// version 命令
	versionCmd := &cobra.Command{
		Use:   "version",
		Short: "显示版本信息",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("cctvdown v%s\n", Version)
		},
	}
	rootCmd.AddCommand(versionCmd)

	// check 命令：检查外部依赖
	checkCmd := &cobra.Command{
		Use:   "check",
		Short: "检查外部依赖是否安装",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCheck()
		},
	}
	rootCmd.AddCommand(checkCmd)

	return logCloser, rootCmd.ExecuteContext(ctx)
}

func runGet(ctx context.Context, cmd *cobra.Command) error {
	// 处理日志级别覆盖（CLI参数覆盖配置）
	if cfg.Verbose {
		cfg.LogLevel = "verbose"
	}
	if cfg.Quiet {
		cfg.LogLevel = "error"
	}

	// 检查外部依赖
	deps, err := utils.CheckDependencies(cfg.FFmpegPath, cfg.NodePath)
	if err != nil {
		return fmt.Errorf("依赖检查失败: %w", err)
	}

	for _, dep := range deps {
		if dep.Path != "" {
			slog.Info("依赖检测通过", "name", dep.DisplayName, "path", dep.Path, "version", dep.Version)
		} else {
			slog.Warn("依赖未安装", "name", dep.DisplayName, "required", dep.Required)
		}
	}

	// 获取输入参数
	url, _ := cmd.Flags().GetString("url")
	guid, _ := cmd.Flags().GetString("guid")

	if url == "" && guid == "" {
		return fmt.Errorf("请指定视频URL(-u)或GUID(-g)")
	}

	// 确保目录存在
	if err := cfg.EnsureDirs(); err != nil {
		return err
	}

	// 创建清理管理器
	cleanup := utils.NewCleanupManager(slog.Default())
	cleanup.CleanupOnCancel(ctx)

	// 创建控制器并执行
	controller := NewController(cfg, slog.Default(), cleanup)
	return controller.Run(ctx, url, guid)
}

func runAlbumGet(ctx context.Context, cmd *cobra.Command) error {
	// 处理日志级别覆盖（CLI参数覆盖配置）
	if cfg.Verbose {
		cfg.LogLevel = "verbose"
	}
	if cfg.Quiet {
		cfg.LogLevel = "error"
	}

	// 检查外部依赖
	deps, err := utils.CheckDependencies(cfg.FFmpegPath, cfg.NodePath)
	if err != nil {
		return fmt.Errorf("依赖检查失败: %w", err)
	}

	for _, dep := range deps {
		if dep.Path != "" {
			slog.Info("依赖检测通过", "name", dep.DisplayName, "path", dep.Path, "version", dep.Version)
		} else {
			slog.Warn("依赖未安装", "name", dep.DisplayName, "required", dep.Required)
		}
	}

	// 获取输入参数
	url, _ := cmd.Flags().GetString("url")
	startDate, _ := cmd.Flags().GetString("start-date")
	endDate, _ := cmd.Flags().GetString("end-date")
	skipConfirm, _ := cmd.Flags().GetBool("skip")
	restart, _ := cmd.Flags().GetBool("restart")
	downloadSlots, _ := cmd.Flags().GetInt("download-slots")
	processWorkers, _ := cmd.Flags().GetInt("process-workers")
	decryptWorkers, _ := cmd.Flags().GetInt("decrypt-workers")

	if url == "" {
		return fmt.Errorf("请指定视频URL(-u)")
	}

	// 设置流水线参数
	if downloadSlots > 0 {
		cfg.AlbumDownloadSlots = downloadSlots
	}
	if processWorkers > 0 {
		cfg.AlbumProcessWorkers = processWorkers
	}
	if decryptWorkers > 0 {
		cfg.DecryptWorkers = decryptWorkers
	}

	// 确保目录存在
	if err := cfg.EnsureDirs(); err != nil {
		return err
	}

	// 创建清理管理器
	cleanup := utils.NewCleanupManager(slog.Default())
	cleanup.CleanupOnCancel(ctx)

	// 创建专辑控制器并执行
	controller := NewAlbumController(cfg, slog.Default(), cleanup)
	return controller.RunGet(ctx, url, startDate, endDate, skipConfirm, restart)
}

func runAlbumList(ctx context.Context) error {
	// 确保目录存在
	if err := cfg.EnsureDirs(); err != nil {
		return err
	}

	// 创建专辑控制器
	cleanup := utils.NewCleanupManager(slog.Default())
	controller := NewAlbumController(cfg, slog.Default(), cleanup)
	return controller.ListPendingDownloads()
}

func runAlbumClear(ctx context.Context) error {
	// 确保目录存在
	if err := cfg.EnsureDirs(); err != nil {
		return err
	}

	// 创建专辑控制器
	cleanup := utils.NewCleanupManager(slog.Default())
	controller := NewAlbumController(cfg, slog.Default(), cleanup)
	return controller.ClearCompletedStates()
}

func runCheck() error {
	deps, err := utils.CheckDependencies("ffmpeg", "node")

	fmt.Println("=== 依赖检查 ===")
	for _, dep := range deps {
		status := "❌ 未安装"
		if dep.Path != "" {
			status = fmt.Sprintf("✅ 已安装 (%s)", dep.Version)
		}
		required := "必须"
		if !dep.Required {
			required = "可选（加密流需要）"
		}
		fmt.Printf("  %s: %s [%s]\n", dep.DisplayName, status, required)
	}
	fmt.Println()

	return err
}