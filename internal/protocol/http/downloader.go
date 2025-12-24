// Package http 提供HTTP分段下载器
package http

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	
	"aria2go/internal/core"
)

// SegmentDownloader 分段下载器
type SegmentDownloader struct {
	client      *Client
	config      Config
	segmentSize int64
	concurrency int
}

// NewSegmentDownloader 创建分段下载器
func NewSegmentDownloader(config Config) (*SegmentDownloader, error) {
	client, err := NewClient(config)
	if err != nil {
		return nil, err
	}
	
	return &SegmentDownloader{
		client:      client,
		config:      config,
		segmentSize: 1 * 1024 * 1024, // 1MB默认分段大小
		concurrency: 5,               // 默认并发数
	}, nil
}

// DownloadFile 下载整个文件（支持分段和并发）
func (sd *SegmentDownloader) DownloadFile(ctx context.Context, url, outputPath string) error {
	// 获取文件信息
	fileInfo, err := sd.client.Head(ctx, url, nil)
	if err != nil {
		return fmt.Errorf("get file info failed: %w", err)
	}
	
	// 如果文件大小为0，创建空文件
	if fileInfo.TotalSize == 0 {
		file, err := os.Create(outputPath)
		if err != nil {
			return fmt.Errorf("create empty file failed: %w", err)
		}
		file.Close()
		return nil
	}
	
	// 计算分段
	segments := sd.calculateSegments(fileInfo.TotalSize)
	
	// 创建分段下载任务
	tasks := make([]SegmentTask, len(segments))
	for i, seg := range segments {
		tasks[i] = SegmentTask{
			Index:      i,
			URL:        url,
			OutputPath: outputPath,
			Start:      seg.Start,
			End:        seg.End,
		}
	}
	
	// 执行分段下载
	err = sd.downloadSegments(ctx, tasks)
	if err != nil {
		return fmt.Errorf("segment download failed: %w", err)
	}
	
	// 验证文件大小
	downloadedFile, err := os.Stat(outputPath)
	if err != nil {
		return fmt.Errorf("stat downloaded file failed: %w", err)
	}
	
	if downloadedFile.Size() != fileInfo.TotalSize {
		return fmt.Errorf("file size mismatch: expected %d, got %d", fileInfo.TotalSize, downloadedFile.Size())
	}
	
	return nil
}

// Segment 表示一个下载分段
type Segment struct {
	Start int64
	End   int64
	Size  int64
}

// SegmentTask 分段下载任务
type SegmentTask struct {
	Index      int
	URL        string
	OutputPath string
	Start      int64
	End        int64
}

// calculateSegments 计算分段
func (sd *SegmentDownloader) calculateSegments(totalSize int64) []Segment {
	var segments []Segment
	
	if totalSize <= sd.segmentSize {
		// 文件太小，不需要分段
		segments = append(segments, Segment{
			Start: 0,
			End:   totalSize - 1,
			Size:  totalSize,
		})
		return segments
	}
	
	// 计算分段数
	numSegments := totalSize / sd.segmentSize
	if totalSize%sd.segmentSize != 0 {
		numSegments++
	}
	
	// 创建分段
	for i := int64(0); i < numSegments; i++ {
		start := i * sd.segmentSize
		end := start + sd.segmentSize - 1
		if end >= totalSize-1 {
			end = totalSize - 1
		}
		
		segments = append(segments, Segment{
			Start: start,
			End:   end,
			Size:  end - start + 1,
		})
	}
	
	return segments
}

// downloadSegments 并发下载分段
func (sd *SegmentDownloader) downloadSegments(ctx context.Context, tasks []SegmentTask) error {
	// 创建任务通道
	taskChan := make(chan SegmentTask, len(tasks))
	resultChan := make(chan SegmentResult, len(tasks))
	errorChan := make(chan error, len(tasks))
	
	// 发送任务到通道
	for _, task := range tasks {
		taskChan <- task
	}
	close(taskChan)
	
	// 创建临时文件目录
	tempDir := filepath.Join(filepath.Dir(tasks[0].OutputPath), ".aria2go_temp")
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return fmt.Errorf("create temp directory failed: %w", err)
	}
	defer os.RemoveAll(tempDir) // 清理临时文件
	
	// 启动worker
	var wg sync.WaitGroup
	for i := 0; i < sd.concurrency; i++ {
		wg.Add(1)
		go sd.segmentWorker(ctx, &wg, taskChan, resultChan, errorChan, tempDir)
	}
	
	// 等待所有worker完成
	wg.Wait()
	close(resultChan)
	close(errorChan)
	
	// 检查错误
	select {
	case err := <-errorChan:
		return err
	default:
		// 没有错误
	}
	
	// 合并分段文件
	err := sd.mergeSegments(ctx, tasks, tempDir, tasks[0].OutputPath)
	if err != nil {
		return fmt.Errorf("merge segments failed: %w", err)
	}
	
	return nil
}

// segmentWorker 分段下载worker
func (sd *SegmentDownloader) segmentWorker(ctx context.Context, wg *sync.WaitGroup, 
	taskChan <-chan SegmentTask, resultChan chan<- SegmentResult, 
	errorChan chan<- error, tempDir string) {
	defer wg.Done()
	
	for task := range taskChan {
		select {
		case <-ctx.Done():
			errorChan <- ctx.Err()
			return
		default:
			// 继续执行
		}
		
		// 创建临时文件
		tempFile := filepath.Join(tempDir, fmt.Sprintf("segment_%d.tmp", task.Index))
		
		// 下载分段
		result, err := sd.client.Download(ctx, DownloadTask{
			URL:        task.URL,
			OutputPath: tempFile,
			RangeStart: task.Start,
			RangeEnd:   task.End,
			Headers:    nil,
		})
		
		if err != nil {
			errorChan <- fmt.Errorf("segment %d download failed: %w", task.Index, err)
			return
		}
		
		resultChan <- SegmentResult{
			Index:      task.Index,
			BytesRead:  result.BytesRead,
			TempFile:   tempFile,
		}
	}
}

// SegmentResult 分段下载结果
type SegmentResult struct {
	Index     int
	BytesRead int64
	TempFile  string
}

// mergeSegments 合并分段文件
func (sd *SegmentDownloader) mergeSegments(ctx context.Context, tasks []SegmentTask, tempDir, outputPath string) error {
	// 创建输出文件
	outputFile, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("create output file failed: %w", err)
	}
	defer outputFile.Close()
	
	// 按顺序合并分段
	for i := 0; i < len(tasks); i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			// 继续执行
		}
		
		tempFile := filepath.Join(tempDir, fmt.Sprintf("segment_%d.tmp", i))
		
		// 打开临时文件
		tempFileHandle, err := os.Open(tempFile)
		if err != nil {
			return fmt.Errorf("open temp file %s failed: %w", tempFile, err)
		}
		
		// 复制数据
		_, err = io.Copy(outputFile, tempFileHandle)
		tempFileHandle.Close()
		
		if err != nil {
			return fmt.Errorf("copy segment %d failed: %w", i, err)
		}
		
		// 删除临时文件
		os.Remove(tempFile)
	}
	
	return nil
}

// SetSegmentSize 设置分段大小
func (sd *SegmentDownloader) SetSegmentSize(size int64) {
	if size > 0 {
		sd.segmentSize = size
	}
}

// SetConcurrency 设置并发数
func (sd *SegmentDownloader) SetConcurrency(n int) {
	if n > 0 {
		sd.concurrency = n
	}
}

// HTTPDownloader 实现core.Downloader接口
type HTTPDownloader struct {
	segmentDownloader *SegmentDownloader
	config            Config
}

// NewHTTPDownloader 创建HTTP下载器
func NewHTTPDownloader(config Config) (*HTTPDownloader, error) {
	segmentDownloader, err := NewSegmentDownloader(config)
	if err != nil {
		return nil, err
	}
	
	return &HTTPDownloader{
		segmentDownloader: segmentDownloader,
		config:            config,
	}, nil
}

// Download 实现core.Downloader接口
func (hd *HTTPDownloader) Download(ctx context.Context, task core.Task) error {
	// 获取任务配置
	config := task.Config()
	
	// 目前只处理第一个URL
	if len(config.URLs) == 0 {
		return fmt.Errorf("no URLs provided")
	}
	
	url := config.URLs[0]
	outputPath := config.OutputPath
	
	// 设置分段大小
	if config.SegmentSize > 0 {
		hd.segmentDownloader.SetSegmentSize(config.SegmentSize)
	}
	
	// 设置并发数
	if config.Connections > 0 {
		hd.segmentDownloader.SetConcurrency(config.Connections)
	}
	
	// 执行下载
	return hd.segmentDownloader.DownloadFile(ctx, url, outputPath)
}

// CanHandle 检查是否可以处理该协议
func (hd *HTTPDownloader) CanHandle(url string) bool {
	return strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://")
}

// GetConfig 返回配置
func (hd *HTTPDownloader) GetConfig() Config {
	return hd.config
}