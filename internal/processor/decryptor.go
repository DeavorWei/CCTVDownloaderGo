package processor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/CCTVDownloadGo/cctvdown/internal/notify"
	"golang.org/x/sync/errgroup"
)

// Decryptor 解密引擎接口
type Decryptor interface {
	// DecryptSegment 解密单个TS分片的H264数据
	DecryptSegment(h264Data []byte) ([]byte, error)
	Close() error
}

// DecryptProgressCallback 解密进度回调函数类型
type DecryptProgressCallback func(current, total int64)

// NodeDecryptor Node.js子进程解密实现
type NodeDecryptor struct {
	nodePath  string // Node.js可执行路径
	scriptDir string // 解密脚本目录
	logger    *slog.Logger
}

// NewNodeDecryptor 创建Node.js解密器
func NewNodeDecryptor(nodePath, scriptDir string, logger *slog.Logger) *NodeDecryptor {
	return &NodeDecryptor{
		nodePath:  nodePath,
		scriptDir: scriptDir,
		logger:    logger,
	}
}

// DecryptSegment 解密单个分片
// 注意：Node.js解密脚本需要完整的TS文件路径，这里提供接口适配
func (d *NodeDecryptor) DecryptSegment(h264Data []byte) ([]byte, error) {
	// Node.js解密脚本需要文件路径，这里需要临时文件
	// 实际实现中，解密是在ProcessEncrypted中批量处理的
	return nil, fmt.Errorf("NodeDecryptor需要通过文件路径解密，请使用DecryptFiles方法")
}

// DecryptFiles 批量解密TS文件
// 调用方式对齐cctvguid-nw: node dec.mjs list.txt output.mp4
func (d *NodeDecryptor) DecryptFiles(ctx context.Context, tsFiles []string, outputPath string) error {
	// 生成临时list.txt
	listFile := filepath.Join(d.scriptDir, "temp_list.txt")
	listContent := buildFFconcatList(tsFiles)
	if err := os.WriteFile(listFile, []byte(listContent), 0644); err != nil {
		return fmt.Errorf("创建列表文件失败: %w", err)
	}
	defer os.Remove(listFile)

	// 调用Node.js执行解密脚本
	scriptPath := filepath.Join(d.scriptDir, "dec.mjs")
	cmd := exec.CommandContext(ctx, d.nodePath, scriptPath, listFile, outputPath)
	cmd.Dir = d.scriptDir

	output, err := cmd.CombinedOutput()
	if err != nil {
		d.logger.Error("解密失败", "error", err, "output", string(output))
		return fmt.Errorf("解密失败: %w, output: %s", err, string(output))
	}

	d.logger.Log(ctx, notify.LevelVerbose, "解密完成", "output", outputPath)
	return nil
}

// DecryptBatchMapped 批量映射解密H264文件（带实时进度回调）
// 调用方式：node dec.mjs --batch-mapped tasks.json
// tasks.json 包含映射数组，如 [{"in": "seg_0.264", "out": "seg_0_dec.264"}, ...]
func (d *NodeDecryptor) DecryptBatchMapped(ctx context.Context, tasksFilePath string, progressCb DecryptProgressCallback) error {
	scriptPath := filepath.Join(d.scriptDir, "dec.mjs")
	cmd := exec.CommandContext(ctx, d.nodePath, scriptPath, "--batch-mapped", tasksFilePath)
	cmd.Dir = d.scriptDir

	// 创建管道实时读取stderr（进度输出）
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("创建stderr管道失败: %w", err)
	}

	// 同时捕获stdout
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("创建stdout管道失败: %w", err)
	}

	// 启动命令
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动Node.js进程失败: %w", err)
	}

	// 使用WaitGroup等待所有goroutine完成
	var wg sync.WaitGroup

	// 实时解析进度（从stderr）
	wg.Add(1)
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			// 解析进度格式: PROGRESS:current:total
			if strings.HasPrefix(line, "PROGRESS:") {
				parts := strings.Split(line, ":")
				if len(parts) == 3 {
					current, err1 := strconv.ParseInt(parts[1], 10, 64)
					total, err2 := strconv.ParseInt(parts[2], 10, 64)
					if err1 == nil && err2 == nil && progressCb != nil {
						progressCb(current, total)
					}
				}
			} else {
				// 其他stderr输出记录到日志
				d.logger.Debug("Node.js stderr", "output", line)
			}
		}
	}()

	// 读取stdout输出
	wg.Add(1)
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			d.logger.Debug("Node.js stdout", "output", line)
		}
	}()

	// 等待命令完成
	err = cmd.Wait()

	// 等待所有输出读取完成
	wg.Wait()

	if err != nil {
		return fmt.Errorf("批量独立解密失败: %w", err)
	}

	d.logger.Log(ctx, notify.LevelVerbose, "批量独立解密完成", "tasks", tasksFilePath)
	return nil
}

// DecryptBatchMappedParallel 多进程并行解密（核心优化）
// 将任务拆分为多个子集，每个子集由独立的Node.js进程处理
// workerCount: 并行进程数，建议设置为CPU核心数
func (d *NodeDecryptor) DecryptBatchMappedParallel(ctx context.Context, tasks []DecryptTask, workerCount int, progressCb DecryptProgressCallback) error {
	if workerCount <= 0 {
		workerCount = runtime.NumCPU()
	}
	if workerCount > len(tasks) {
		workerCount = len(tasks)
	}
	if workerCount == 0 {
		return nil
	}

	d.logger.Info("启动多进程并行解密", "workers", workerCount, "tasks", len(tasks))

	// 将任务均匀分配给各个worker
	taskGroups := make([][]DecryptTask, workerCount)
	for i, task := range tasks {
		idx := i % workerCount
		taskGroups[idx] = append(taskGroups[idx], task)
	}

	// 进度追踪
	var completedCount atomic.Int64
	totalTasks := int64(len(tasks))

	// 每个worker的上一次进度值（用于计算增量）
	workerLastProgress := make([]atomic.Int64, workerCount)

	// 使用errgroup管理并行goroutine
	g, ctx := errgroup.WithContext(ctx)

	// 为每个worker创建独立的tasks.json并启动进程
	for workerIdx, group := range taskGroups {
		if len(group) == 0 {
			continue
		}

		workerIdx := workerIdx // 捕获循环变量
		group := group

		g.Go(func() error {
			// 为此worker创建临时tasks.json
			tasksFile := filepath.Join(d.scriptDir, fmt.Sprintf("tasks_worker_%d.json", workerIdx))
			tasksData, err := json.Marshal(group)
			if err != nil {
				return fmt.Errorf("序列化worker %d任务失败: %w", workerIdx, err)
			}
			if err := os.WriteFile(tasksFile, tasksData, 0644); err != nil {
				return fmt.Errorf("写入worker %d任务文件失败: %w", workerIdx, err)
			}
			defer os.Remove(tasksFile)

			// 启动独立的Node.js进程
			scriptPath := filepath.Join(d.scriptDir, "dec.mjs")
			cmd := exec.CommandContext(ctx, d.nodePath, scriptPath, "--batch-mapped", tasksFile)
			cmd.Dir = d.scriptDir

			// 创建管道实时读取stderr（进度输出）
			stderr, err := cmd.StderrPipe()
			if err != nil {
				return fmt.Errorf("创建worker %d stderr管道失败: %w", workerIdx, err)
			}

			// 同时捕获stdout
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				return fmt.Errorf("创建worker %d stdout管道失败: %w", workerIdx, err)
			}

			// 启动命令
			if err := cmd.Start(); err != nil {
				return fmt.Errorf("启动worker %d Node.js进程失败: %w", workerIdx, err)
			}

			// 使用WaitGroup等待输出读取完成
			var wg sync.WaitGroup

			// 实时解析进度（从stderr）
			wg.Add(1)
			go func() {
				defer wg.Done()
				scanner := bufio.NewScanner(stderr)
				for scanner.Scan() {
					line := scanner.Text()
					// 解析进度格式: PROGRESS:current:total
					if strings.HasPrefix(line, "PROGRESS:") {
						parts := strings.Split(line, ":")
						if len(parts) == 3 {
							// 解析worker的当前进度
							workerCurrent, err1 := strconv.ParseInt(parts[1], 10, 64)
							if err1 == nil && progressCb != nil {
								// 计算增量：当前进度 - 上一次进度
								lastProgress := workerLastProgress[workerIdx].Swap(workerCurrent)
								increment := workerCurrent - lastProgress
								if increment > 0 {
									completed := completedCount.Add(increment)
									progressCb(completed, totalTasks)
								}
							}
						}
					} else {
						// 其他stderr输出记录到日志
						d.logger.Debug("Worker stderr", "worker", workerIdx, "output", line)
					}
				}
			}()

			// 读取stdout输出
			wg.Add(1)
			go func() {
				defer wg.Done()
				scanner := bufio.NewScanner(stdout)
				for scanner.Scan() {
					line := scanner.Text()
					d.logger.Debug("Worker stdout", "worker", workerIdx, "output", line)
				}
			}()

			// 等待命令完成
			err = cmd.Wait()

			// 等待所有输出读取完成
			wg.Wait()

			if err != nil {
				d.logger.Error("Worker进程失败", "worker", workerIdx, "error", err)
				return fmt.Errorf("worker %d解密失败: %w", workerIdx, err)
			}

			d.logger.Debug("Worker完成", "worker", workerIdx, "tasks", len(group))
			return nil
		})
	}

	// 等待所有worker完成
	if err := g.Wait(); err != nil {
		return err
	}

	// 确保进度显示完成
	if progressCb != nil {
		progressCb(totalTasks, totalTasks)
	}

	d.logger.Info("多进程并行解密完成", "total", totalTasks)
	return nil
}

// DecryptTask 解密任务结构
type DecryptTask struct {
	In  string `json:"in"`
	Out string `json:"out"`
}

// BuildTasksJSON 构建批量映射解密的tasks.json内容
func BuildTasksJSON(tasks []map[string]string) ([]byte, error) {
	return json.Marshal(tasks)
}

// BuildDecryptTasks 从map构建DecryptTask切片
func BuildDecryptTasks(tasks []map[string]string) []DecryptTask {
	result := make([]DecryptTask, len(tasks))
	for i, t := range tasks {
		result[i] = DecryptTask{In: t["in"], Out: t["out"]}
	}
	return result
}

// Close 关闭解密器
func (d *NodeDecryptor) Close() error {
	return nil
}

// buildFFconcatList 构建FFmpeg concat列表
func buildFFconcatList(files []string) string {
	var buf bytes.Buffer
	for _, f := range files {
		buf.WriteString(fmt.Sprintf("file '%s'\n", f))
	}
	return buf.String()
}

// ProcessNALUnits 处理NAL单元解密（有状态序列操作）
// ⚠️ 约束：同一TS分片内的NAL必须串行解密
// 此函数用于Go原生解密实现（wazero方案），Node.js方案不使用此函数
func ProcessNALUnits(units []*NALUnit, decryptFunc func([]byte) ([]byte, error)) ([]byte, error) {
	var output bytes.Buffer
	shouldDecrypt := false

	for _, unit := range units {
		newData := append([]byte{unit.Header}, unit.Data...)

		switch {
		case unit.NalUnitType == 25:
			// Type 25: 加密标记NAL
			shouldDecrypt = len(unit.Data) > 0 && unit.Data[0] == 1
			decrypted, err := decryptFunc(newData)
			if err != nil {
				return nil, fmt.Errorf("解密Type 25失败: %w", err)
			}
			unit.Reload(decrypted)
			continue // Type 25不输出

		case (unit.NalUnitType == 1 || unit.NalUnitType == 5) && shouldDecrypt:
			// Type 1/5: 需要解密
			decrypted, err := decryptFunc(newData)
			if err != nil {
				return nil, fmt.Errorf("解密Type %d失败: %w", unit.NalUnitType, err)
			}
			unit.Reload(decrypted)
		}

		output.Write(unit.Dump())
	}

	return output.Bytes(), nil
}
