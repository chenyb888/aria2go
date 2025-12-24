// Package http 提供完整的HTTP下载器实现
package http

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
	
	"aria2go/internal/core"
)

// CompleteHTTPDownloader 完整的HTTP下载器实现
type CompleteHTTPDownloader struct {
	client      *ImprovedClient
	config      ImprovedConfig
	segmentSize int64
	concurrency int
	mu          sync.RWMutex
	
	// 任务状态跟踪
	activeTasks map[string]*DownloadTaskContext
}

// DownloadTaskContext 下载任务上下文
type DownloadTaskContext struct {
	TaskID      string
	URL         string
	OutputPath  string
	TotalSize   int64
	Downloaded  int64
	StartTime   time.Time
	CancelFunc  context.CancelFunc
	ProgressCh  chan core.TaskProgress
	ErrorCh     chan error
	mu          sync.RWMutex
}

// segmentTask 表示一个分段下载任务
type segmentTask struct {
	index int
	start int64
	end   int64
}

// segmentResult 表示分段下载结果
type segmentResult struct {
	index    int
	bytes    int64
	tempFile string
	err      error
	duration time.Duration
}

// NewCompleteHTTPDownloader 创建完整的HTTP下载器
func NewCompleteHTTPDownloader(config ImprovedConfig) (*CompleteHTTPDownloader, error) {
	client, err := NewImprovedClient(config)
	if err != nil {
		return nil, fmt.Errorf("create HTTP client failed: %w", err)
	}
	
	return &CompleteHTTPDownloader{
		client:      client,
		config:      config,
		segmentSize: 5 * 1024 * 1024, // 默认5MB分段
		concurrency: 5,               // 默认5个并发
		activeTasks: make(map[string]*DownloadTaskContext),
	}, nil
}

// Download 执行下载任务
func (d *CompleteHTTPDownloader) Download(ctx context.Context, task core.Task) error {
	taskID := task.ID()
	config := task.Config()
	
	if len(config.URLs) == 0 {
		return fmt.Errorf("no URLs provided for task %s", taskID)
	}
	
	url := config.URLs[0]
	outputPath := config.OutputPath
	
	// 创建任务上下文
	taskCtx := &DownloadTaskContext{
		TaskID:     taskID,
		URL:        url,
		OutputPath: outputPath,
		StartTime:  time.Now(),
		ProgressCh: make(chan core.TaskProgress, 10),
		ErrorCh:    make(chan error, 1),
	}
	
	// 注册任务
	d.mu.Lock()
	d.activeTasks[taskID] = taskCtx
	d.mu.Unlock()
	
	// 清理函数
	defer func() {
		d.mu.Lock()
		delete(d.activeTasks, taskID)
		d.mu.Unlock()
		close(taskCtx.ProgressCh)
		close(taskCtx.ErrorCh)
	}()
	
	// 创建带取消的上下文
	downloadCtx, cancel := context.WithCancel(ctx)
	taskCtx.CancelFunc = cancel
	defer cancel()
	
	// 启动进度报告goroutine
	go d.reportProgress(taskCtx, task)
	
	// 执行下载
	err := d.executeDownload(downloadCtx, taskCtx, config)
	if err != nil {
		// 发送错误
		select {
		case taskCtx.ErrorCh <- err:
		default:
		}
		return fmt.Errorf("download failed for task %s: %w", taskID, err)
	}
	
	return nil
}

// executeDownload 执行实际的下载逻辑
func (d *CompleteHTTPDownloader) executeDownload(ctx context.Context, taskCtx *DownloadTaskContext, config core.TaskConfig) error {
	// 获取文件信息
	fileInfo, err := d.client.Head(ctx, taskCtx.URL, nil)
	if err != nil {
		return fmt.Errorf("get file info failed: %w", err)
	}
	
	taskCtx.TotalSize = fileInfo.TotalSize
	
	// 决定下载策略
	if taskCtx.TotalSize <= 0 || config.Connections <= 1 {
		// 单连接下载
		return d.downloadSingle(ctx, taskCtx, config)
	}
	
	// 分段下载
	return d.downloadSegmented(ctx, taskCtx, config)
}

// downloadSingle 单连接下载
func (d *CompleteHTTPDownloader) downloadSingle(ctx context.Context, taskCtx *DownloadTaskContext, config core.TaskConfig) error {
	options := DownloadOptions{
		Headers:      nil,
		ResumeFrom:   0,
		ExpectedSize: taskCtx.TotalSize,
		MaxSpeed:     config.MaxSpeed,
	}
	
	// 进度回调
	progressCB := func(downloaded, total int64, speed int64) {
		taskCtx.mu.Lock()
		taskCtx.Downloaded = downloaded
		taskCtx.mu.Unlock()
		
		// 发送进度更新
		progress := core.TaskProgress{
			TotalBytes:      total,
			DownloadedBytes: downloaded,
			DownloadSpeed:   speed,
			Progress:        0,
		}
		if total > 0 {
			progress.Progress = float64(downloaded) / float64(total) * 100
		}
		
		select {
		case taskCtx.ProgressCh <- progress:
		default:
			// 通道已满，跳过
		}
	}
	
	// 执行下载
	result, err := d.client.DownloadWithProgress(ctx, taskCtx.URL, taskCtx.OutputPath, options, progressCB)
	if err != nil {
		return err
	}
	
	// 更新最终进度
	taskCtx.mu.Lock()
	taskCtx.Downloaded = result.BytesRead
	taskCtx.mu.Unlock()
	
	return nil
}

// downloadSegmented 分段下载
func (d *CompleteHTTPDownloader) downloadSegmented(ctx context.Context, taskCtx *DownloadTaskContext, config core.TaskConfig) error {
	// 计算分段
	segments := d.calculateSegments(taskCtx.TotalSize, config)
	
	// 创建临时目录
	tempDir := filepath.Join(filepath.Dir(taskCtx.OutputPath), ".aria2go_temp_"+taskCtx.TaskID)
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return fmt.Errorf("create temp directory failed: %w", err)
	}
	defer os.RemoveAll(tempDir)
	
	// 创建分段下载任务
	tasks := make([]segmentTask, len(segments))
	for i, seg := range segments {
		tasks[i] = segmentTask{
			index: i,
			start: seg.start,
			end:   seg.end,
		}
	}
	
	// 执行并发下载
	err := d.downloadSegmentsConcurrently(ctx, taskCtx, tasks, tempDir, config)
	if err != nil {
		return err
	}
	
	// 合并分段
	if err := d.mergeSegments(ctx, taskCtx, tasks, tempDir); err != nil {
		return fmt.Errorf("merge segments failed: %w", err)
	}
	
	return nil
}

// calculateSegments 计算分段
func (d *CompleteHTTPDownloader) calculateSegments(totalSize int64, config core.TaskConfig) []segmentRange {
	// 使用配置的分段大小和连接数
	segmentSize := config.SegmentSize
	if segmentSize <= 0 {
		segmentSize = d.segmentSize
	}
	
	connections := config.Connections
	if connections <= 0 {
		connections = d.concurrency
	}
	
	// 如果文件太小或连接数为1，返回单个分段
	if totalSize <= 0 || connections == 1 || totalSize < segmentSize {
		return []segmentRange{{start: 0, end: totalSize - 1}}
	}
	
	// 计算分段数量
	numSegments := (totalSize + segmentSize - 1) / segmentSize // 向上取整
	if numSegments > int64(connections) {
		numSegments = int64(connections)
	}
	
	// 重新计算分段大小
	segmentSize = (totalSize + numSegments - 1) / numSegments
	
	segments := make([]segmentRange, 0, numSegments)
	var start int64 = 0
	
	for start < totalSize {
		end := start + segmentSize - 1
		if end >= totalSize {
			end = totalSize - 1
		}
		segments = append(segments, segmentRange{start: start, end: end})
		start = end + 1
	}
	
	return segments
}

// downloadSegmentsConcurrently 并发下载分段
func (d *CompleteHTTPDownloader) downloadSegmentsConcurrently(ctx context.Context, taskCtx *DownloadTaskContext, 
	tasks []segmentTask, tempDir string, config core.TaskConfig) error {
	
	// 任务通道
	taskChan := make(chan segmentTask, len(tasks))
	resultChan := make(chan segmentResult, len(tasks))
	errorChan := make(chan error, len(tasks))
	
	// 发送任务
	for _, task := range tasks {
		taskChan <- task
	}
	close(taskChan)
	
	// 启动worker
	var wg sync.WaitGroup
	workerCount := config.Connections
	if workerCount <= 0 {
		workerCount = d.concurrency
	}
	
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go d.segmentWorker(ctx, &wg, taskChan, resultChan, errorChan, taskCtx, tempDir, config)
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
	
	// 收集结果并更新进度
	var totalDownloaded int64
	for result := range resultChan {
		totalDownloaded += result.bytes
	}
	
	taskCtx.mu.Lock()
	taskCtx.Downloaded = totalDownloaded
	taskCtx.mu.Unlock()
	
	return nil
}

// segmentWorker 分段下载worker
func (d *CompleteHTTPDownloader) segmentWorker(ctx context.Context, wg *sync.WaitGroup, 
	taskChan <-chan segmentTask, resultChan chan<- segmentResult, 
	errorChan chan<- error, taskCtx *DownloadTaskContext, 
	tempDir string, config core.TaskConfig) {
	
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
		tempFile := filepath.Join(tempDir, fmt.Sprintf("segment_%d.tmp", task.index))
		
		// 下载选项
		options := DownloadOptions{
			Headers:      nil,
			ResumeFrom:   0,
			ExpectedSize: task.end - task.start + 1,
			MaxSpeed:     config.MaxSpeed,
		}
		
		// 进度回调（用于这个分段）
		progressCB := func(downloaded, total int64, speed int64) {
			// 可以在这里实现分段级别的进度跟踪
		}
		
		// 执行下载
		result, err := d.client.DownloadWithProgress(ctx, taskCtx.URL, tempFile, options, progressCB)
		if err != nil {
			errorChan <- fmt.Errorf("segment %d download failed: %w", task.index, err)
			return
		}
		
		resultChan <- segmentResult{
			index:    task.index,
			bytes:    result.BytesRead,
			tempFile: tempFile,
		}
	}
}

// mergeSegments 合并分段文件
func (d *CompleteHTTPDownloader) mergeSegments(ctx context.Context, taskCtx *DownloadTaskContext, 
	tasks []segmentTask, tempDir string) error {
	
	// 创建输出文件
	outputFile, err := os.Create(taskCtx.OutputPath)
	if err != nil {
		return fmt.Errorf("create output file failed: %w", err)
	}
	defer outputFile.Close()
	
	// 预分配空间
	if taskCtx.TotalSize > 0 {
		if err := outputFile.Truncate(taskCtx.TotalSize); err != nil {
			// 如果预分配失败，继续尝试合并
		}
	}
	
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
		
		// 更新进度
		taskCtx.mu.Lock()
		// 这里可以更新已合并的字节数
		taskCtx.mu.Unlock()
	}
	
	return nil
}

// reportProgress 报告进度
func (d *CompleteHTTPDownloader) reportProgress(taskCtx *DownloadTaskContext, task core.Task) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	
	var lastProgress core.TaskProgress
	
	for {
		select {
		case <-ticker.C:
			taskCtx.mu.RLock()
			downloaded := taskCtx.Downloaded
			total := taskCtx.TotalSize
			elapsed := time.Since(taskCtx.StartTime)
			taskCtx.mu.RUnlock()
			
			// 计算速度
			speed := int64(0)
			if elapsed.Seconds() > 0 {
				speed = int64(float64(downloaded) / elapsed.Seconds())
			}
			
			// 计算进度百分比
			progress := 0.0
			if total > 0 {
				progress = float64(downloaded) / float64(total) * 100
			}
			
			currentProgress := core.TaskProgress{
				TotalBytes:      total,
				DownloadedBytes: downloaded,
				DownloadSpeed:   speed,
				Progress:        progress,
				ETA:             calculateETA(downloaded, total, speed),
			}
			
			// 只有当进度有变化时才发送
			if currentProgress != lastProgress {
				// 这里应该调用task的UpdateProgress方法
				// 由于接口限制，我们通过通道发送
				select {
				case taskCtx.ProgressCh <- currentProgress:
					lastProgress = currentProgress
				default:
					// 通道已满，跳过
				}
			}
			
		case progress := <-taskCtx.ProgressCh:
			// 直接转发进度更新
			lastProgress = progress
			
		case err := <-taskCtx.ErrorCh:
			// 处理错误
			if err != nil {
				log.Printf("下载任务错误: %v", err)
			}
			return
			
		case <-taskCtx.ProgressCh: // 通道关闭
			return
		}
	}
}

// CancelTask 取消任务
func (d *CompleteHTTPDownloader) CancelTask(taskID string) error {
	d.mu.RLock()
	taskCtx, exists := d.activeTasks[taskID]
	d.mu.RUnlock()
	
	if !exists {
		return fmt.Errorf("task %s not found", taskID)
	}
	
	if taskCtx.CancelFunc != nil {
		taskCtx.CancelFunc()
	}
	
	return nil
}

// GetTaskProgress 获取任务进度
func (d *CompleteHTTPDownloader) GetTaskProgress(taskID string) (core.TaskProgress, error) {
	d.mu.RLock()
	taskCtx, exists := d.activeTasks[taskID]
	d.mu.RUnlock()
	
	if !exists {
		return core.TaskProgress{}, fmt.Errorf("task %s not found", taskID)
	}
	
	taskCtx.mu.RLock()
	defer taskCtx.mu.RUnlock()
	
	elapsed := time.Since(taskCtx.StartTime)
	speed := int64(0)
	if elapsed.Seconds() > 0 {
		speed = int64(float64(taskCtx.Downloaded) / elapsed.Seconds())
	}
	
	progress := 0.0
	if taskCtx.TotalSize > 0 {
		progress = float64(taskCtx.Downloaded) / float64(taskCtx.TotalSize) * 100
	}
	
	return core.TaskProgress{
		TotalBytes:      taskCtx.TotalSize,
		DownloadedBytes: taskCtx.Downloaded,
		DownloadSpeed:   speed,
		Progress:        progress,
		ETA:             calculateETA(taskCtx.Downloaded, taskCtx.TotalSize, speed),
	}, nil
}

// SetSegmentSize 设置分段大小
func (d *CompleteHTTPDownloader) SetSegmentSize(size int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	
	if size > 0 {
		d.segmentSize = size
	}
}

// SetConcurrency 设置并发数
func (d *CompleteHTTPDownloader) SetConcurrency(n int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	
	if n > 0 {
		d.concurrency = n
	}
}

// GetStats 获取下载器统计信息
func (d *CompleteHTTPDownloader) GetStats() interface{} {
	// 这里可以返回各种统计信息
	return map[string]interface{}{
		"active_tasks": len(d.activeTasks),
		"segment_size": d.segmentSize,
		"concurrency":  d.concurrency,
	}
}

// calculateETA 计算预计剩余时间
func calculateETA(downloaded, total int64, speed int64) time.Duration {
	if speed <= 0 || total <= 0 || downloaded >= total {
		return 0
	}
	
	remainingBytes := total - downloaded
	seconds := float64(remainingBytes) / float64(speed)
	
	return time.Duration(seconds * float64(time.Second))
}