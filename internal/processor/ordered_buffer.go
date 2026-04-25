package processor

import (
	"sync"
)

// ProcessedSegment 已处理的分片
type ProcessedSegment struct {
	Index      int
	H264Path   string // 解密后的H264文件路径
	AACPath    string // AAC音频文件路径
	MP4Path    string // remux后的MP4文件路径
	DecSuccess bool   // 解密是否成功
}

// OrderedBuffer 有序合并缓冲区
// 保证Pipeline模式下分片按原始顺序合并
type OrderedBuffer struct {
	mu           sync.Mutex
	nextExpected int                   // 下一个期望输出的序号
	pending      map[int]*ProcessedSegment // 待输出的分片
	outputChan   chan *ProcessedSegment    // 输出通道
	closed       bool
}

// NewOrderedBuffer 创建有序缓冲区
func NewOrderedBuffer(totalSegments int) *OrderedBuffer {
	return &OrderedBuffer{
		nextExpected: 0,
		pending:      make(map[int]*ProcessedSegment, totalSegments),
		outputChan:   make(chan *ProcessedSegment, totalSegments),
		closed:       false,
	}
}

// Submit 提交处理完成的分片
// 分片可能乱序到达，但会按序输出
func (b *OrderedBuffer) Submit(segment *ProcessedSegment) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return
	}

	b.pending[segment.Index] = segment

	// 按顺序输出所有连续就绪的分片
	for {
		seg, ok := b.pending[b.nextExpected]
		if !ok {
			break
		}
		delete(b.pending, b.nextExpected)
		b.nextExpected++
		b.outputChan <- seg
	}
}

// Output 获取输出通道
func (b *OrderedBuffer) Output() <-chan *ProcessedSegment {
	return b.outputChan
}

// Close 关闭缓冲区
func (b *OrderedBuffer) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.closed {
		b.closed = true
		close(b.outputChan)
	}
}

// NextExpected 获取下一个期望的序号
func (b *OrderedBuffer) NextExpected() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.nextExpected
}

// PendingCount 获取待处理的分片数量
func (b *OrderedBuffer) PendingCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.pending)
}
