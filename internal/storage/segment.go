// Package storage 提供文件分片和断点续传功能
package storage

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// SegmentManager 分段管理器
type SegmentManager struct {
	filePath    string
	segmentSize int64
	totalSize   int64
	segments    []Segment
	mu          sync.RWMutex
}

// Segment 表示一个文件分段
type Segment struct {
	Index      int
	Start      int64
	End        int64
	Downloaded int64
	Completed  bool
}

// NewSegmentManager 创建分段管理器
func NewSegmentManager(filePath string, segmentSize, totalSize int64) (*SegmentManager, error) {
	if segmentSize <= 0 {
		return nil, fmt.Errorf("segment size must be positive")
	}
	
	if totalSize < 0 {
		return nil, fmt.Errorf("total size must be non-negative")
	}
	
	sm := &SegmentManager{
		filePath:    filePath,
		segmentSize: segmentSize,
		totalSize:   totalSize,
	}
	
	// 初始化分段
	sm.initSegments()
	
	return sm, nil
}

// initSegments 初始化分段
func (sm *SegmentManager) initSegments() {
	if sm.totalSize == 0 {
		// 文件大小为0，创建一个空分段
		sm.segments = []Segment{{Index: 0, Start: 0, End: 0, Downloaded: 0, Completed: true}}
		return
	}
	
	// 计算分段数
	numSegments := sm.totalSize / sm.segmentSize
	if sm.totalSize%sm.segmentSize != 0 {
		numSegments++
	}
	
	// 创建分段
	sm.segments = make([]Segment, numSegments)
	for i := int64(0); i < numSegments; i++ {
		start := i * sm.segmentSize
		end := start + sm.segmentSize - 1
		if end >= sm.totalSize-1 {
			end = sm.totalSize - 1
		}
		
		sm.segments[i] = Segment{
			Index:      int(i),
			Start:      start,
			End:        end,
			Downloaded: 0,
			Completed:  false,
		}
	}
}

// GetSegment 获取指定索引的分段
func (sm *SegmentManager) GetSegment(index int) (*Segment, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	
	if index < 0 || index >= len(sm.segments) {
		return nil, fmt.Errorf("segment index out of range: %d", index)
	}
	
	segment := sm.segments[index]
	return &segment, nil
}

// GetNextIncompleteSegment 获取下一个未完成的分段
func (sm *SegmentManager) GetNextIncompleteSegment() (*Segment, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	
	for i := range sm.segments {
		if !sm.segments[i].Completed {
			segment := sm.segments[i]
			return &segment, nil
		}
	}
	
	return nil, nil // 所有分段都已完成
}

// UpdateSegmentProgress 更新分段进度
func (sm *SegmentManager) UpdateSegmentProgress(index int, downloaded int64) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	
	if index < 0 || index >= len(sm.segments) {
		return fmt.Errorf("segment index out of range: %d", index)
	}
	
	segment := &sm.segments[index]
	segment.Downloaded = downloaded
	
	// 检查是否完成
	segmentSize := segment.End - segment.Start + 1
	if segment.Downloaded >= segmentSize {
		segment.Completed = true
		segment.Downloaded = segmentSize
	}
	
	return nil
}

// MarkSegmentCompleted 标记分段完成
func (sm *SegmentManager) MarkSegmentCompleted(index int) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	
	if index < 0 || index >= len(sm.segments) {
		return fmt.Errorf("segment index out of range: %d", index)
	}
	
	segment := &sm.segments[index]
	segment.Completed = true
	segmentSize := segment.End - segment.Start + 1
	segment.Downloaded = segmentSize
	
	return nil
}

// GetProgress 获取整体进度
func (sm *SegmentManager) GetProgress() (downloaded, total int64, percentage float64) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	
	total = sm.totalSize
	for _, segment := range sm.segments {
		downloaded += segment.Downloaded
	}
	
	if total > 0 {
		percentage = float64(downloaded) / float64(total) * 100
	}
	
	return downloaded, total, percentage
}

// IsComplete 检查是否所有分段都已完成
func (sm *SegmentManager) IsComplete() bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	
	for _, segment := range sm.segments {
		if !segment.Completed {
			return false
		}
	}
	
	return true
}

// WriteSegmentData 写入分段数据
func (sm *SegmentManager) WriteSegmentData(index int, data []byte) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	
	if index < 0 || index >= len(sm.segments) {
		return fmt.Errorf("segment index out of range: %d", index)
	}
	
	// 打开文件（创建目录）
	dir := filepath.Dir(sm.filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create directory failed: %w", err)
	}
	
	file, err := os.OpenFile(sm.filePath, os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		return fmt.Errorf("open file failed: %w", err)
	}
	defer file.Close()
	
	// 定位到分段起始位置
	segment := sm.segments[index]
	if _, err := file.Seek(segment.Start, io.SeekStart); err != nil {
		return fmt.Errorf("seek file failed: %w", err)
	}
	
	// 写入数据
	n, err := file.Write(data)
	if err != nil {
		return fmt.Errorf("write data failed: %w", err)
	}
	
	// 更新进度
	sm.segments[index].Downloaded += int64(n)
	if sm.segments[index].Downloaded >= (segment.End - segment.Start + 1) {
		sm.segments[index].Completed = true
	}
	
	return nil
}

// SaveState 保存状态到文件（用于断点续传）
func (sm *SegmentManager) SaveState(stateFile string) error {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	
	// 创建状态数据结构
	state := struct {
		FilePath    string    `json:"file_path"`
		SegmentSize int64     `json:"segment_size"`
		TotalSize   int64     `json:"total_size"`
		Segments    []Segment `json:"segments"`
	}{
		FilePath:    sm.filePath,
		SegmentSize: sm.segmentSize,
		TotalSize:   sm.totalSize,
		Segments:    sm.segments,
	}
	
	// 序列化为JSON
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal state failed: %w", err)
	}
	
	// 写入文件
	if err := os.WriteFile(stateFile, data, 0644); err != nil {
		return fmt.Errorf("write state file failed: %w", err)
	}
	
	return nil
}

// LoadState 从文件加载状态
func LoadState(stateFile string) (*SegmentManager, error) {
	data, err := os.ReadFile(stateFile)
	if err != nil {
		return nil, fmt.Errorf("read state file failed: %w", err)
	}
	
	var state struct {
		FilePath    string    `json:"file_path"`
		SegmentSize int64     `json:"segment_size"`
		TotalSize   int64     `json:"total_size"`
		Segments    []Segment `json:"segments"`
	}
	
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("unmarshal state failed: %w", err)
	}
	
	sm := &SegmentManager{
		filePath:    state.FilePath,
		segmentSize: state.SegmentSize,
		totalSize:   state.TotalSize,
		segments:    state.Segments,
	}
	
	return sm, nil
}

// GetSegmentCount 获取分段数量
func (sm *SegmentManager) GetSegmentCount() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return len(sm.segments)
}

// GetCompletedSegments 获取已完成的分段数
func (sm *SegmentManager) GetCompletedSegments() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	
	count := 0
	for _, segment := range sm.segments {
		if segment.Completed {
			count++
		}
	}
	
	return count
}