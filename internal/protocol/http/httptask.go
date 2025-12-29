// Package http 提供HTTP任务实现
package http

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"
	
	"aria2go/internal/core"
)

// HTTPTask 是HTTP下载任务的实现
type HTTPTask struct {
	*core.BaseTask
	client     *Client
	downloader *SegmentDownloader
	url        string
	outputPath string
	cancelFunc context.CancelFunc
	// 分段下载配置
	connections int    // 每个服务器的连接数
	segmentSize int64  // 分段大小（字节）
	maxSpeed    int64  // 最大下载速度（字节/秒）
}

// NewHTTPTask 创建新的HTTP下载任务
func NewHTTPTask(id string, config core.TaskConfig, eventCh chan<- core.Event) (*HTTPTask, error) {
	// 验证配置
	if len(config.URLs) == 0 {
		return nil, fmt.Errorf("no URLs provided")
	}
	
	// 使用第一个URL（HTTP任务通常只有一个URL）
	url := config.URLs[0]
	
	// 验证协议
	if !IsHTTPURL(url) {
		return nil, fmt.Errorf("invalid HTTP URL: %s", url)
	}
	
	// 创建HTTP配置
	httpConfig := Config{
		UserAgent:       "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		Timeout:         30 * time.Second,
		MaxRetries:      3,
		RetryInterval:   2 * time.Second,
		MaxConnsPerHost: 10,
	}
	
	// 从任务配置中提取HTTP选项
	if opts, ok := config.Options["http"].(map[string]interface{}); ok {
		if userAgent, ok := opts["user-agent"].(string); ok {
			httpConfig.UserAgent = userAgent
		}
		if timeout, ok := opts["timeout"].(float64); ok {
			httpConfig.Timeout = time.Duration(timeout) * time.Second
		}
		if maxRetries, ok := opts["max-retries"].(float64); ok {
			httpConfig.MaxRetries = int(maxRetries)
		}
		if proxy, ok := opts["proxy"].(string); ok {
			httpConfig.ProxyURL = proxy
		}
	}
	
	// 创建HTTP客户端
	client, err := NewClient(httpConfig)
	if err != nil {
		return nil, fmt.Errorf("create HTTP client failed: %w", err)
	}
	
	// 创建基础任务
	baseTask := core.NewBaseTask(id, config, eventCh)
	
	// 设置分段下载配置
	connections := config.Connections
	if connections <= 0 {
		connections = 1 // 默认至少1个连接
	}
	
	segmentSize := config.SegmentSize
	if segmentSize <= 0 {
		segmentSize = 20 * 1024 * 1024 // 默认20MB
	}
	
	maxSpeed := config.MaxSpeed
	
	return &HTTPTask{
		BaseTask:   baseTask,
		client:     client,
		downloader: nil,
		url:        url,
		outputPath: config.OutputPath,
		connections: connections,
		segmentSize: segmentSize,
		maxSpeed:    maxSpeed,
	}, nil
}

// IsHTTPURL 检查URL是否是HTTP或HTTPS协议
func IsHTTPURL(url string) bool {
	return strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://")
}

// Start 启动HTTP下载任务
func (t *HTTPTask) Start(ctx context.Context) error {
	// 调用父类的Start方法更新状态
	if err := t.BaseTask.Start(ctx); err != nil {
		return err
	}
	
	// 创建子上下文用于取消控制
	downloadCtx, cancel := context.WithCancel(ctx)
	t.cancelFunc = cancel
	
	// 在goroutine中执行下载
	go t.download(downloadCtx)
	
	// 记录日志
	log.Printf("HTTPTask[%s] 开始下载: %s -> %s", t.ID(), t.url, t.outputPath)
	
	return nil
}

// Stop 停止HTTP下载任务
func (t *HTTPTask) Stop() error {
	// 调用取消函数
	if t.cancelFunc != nil {
		t.cancelFunc()
	}
	
	// 调用父类的Stop方法
	return t.BaseTask.Stop()
}

// Pause 暂停HTTP下载任务
func (t *HTTPTask) Pause() error {
	// 调用取消函数
	if t.cancelFunc != nil {
		t.cancelFunc()
	}
	
	// 调用父类的Pause方法
	return t.BaseTask.Pause()
}

// Resume 恢复HTTP下载任务
func (t *HTTPTask) Resume() error {
	// 调用父类的Resume方法（只改变状态，实际恢复由调度器处理）
	return t.BaseTask.Resume()
}

// download 执行实际的下载逻辑
func (t *HTTPTask) download(ctx context.Context) {
	log.Printf("HTTPTask[%s] 开始执行下载", t.ID())
	log.Printf("HTTPTask[%s] 配置: connections=%d, segmentSize=%d, maxSpeed=%d", 
		t.ID(), t.connections, t.segmentSize, t.maxSpeed)
	
	// 获取文件总大小（通过HEAD请求）
	var totalBytes int64
	headResult, err := t.client.Head(ctx, t.url, nil)
	if err == nil && headResult.TotalSize > 0 {
		totalBytes = headResult.TotalSize
		log.Printf("HTTPTask[%s] 文件总大小: %d 字节", t.ID(), totalBytes)
	} else {
		log.Printf("HTTPTask[%s] 无法获取文件大小: %v", t.ID(), err)
		totalBytes = 0
	}
	
	// 决定是否使用分段下载
	useSegmented := false
	if totalBytes > 0 && t.connections > 1 && totalBytes > t.segmentSize {
		useSegmented = true
		log.Printf("HTTPTask[%s] 启用分段下载: 文件大小=%d, 分段大小=%d, 连接数=%d", 
			t.ID(), totalBytes, t.segmentSize, t.connections)
	} else {
		log.Printf("HTTPTask[%s] 使用单连接下载", t.ID())
	}
	
	// 用于进度更新的通道
	progressDone := make(chan struct{})
	
	// 启动进度更新goroutine
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		
		var lastBytes int64
		var lastTime time.Time
		
		for {
			select {
			case <-ctx.Done():
				return
			case <-progressDone:
				return
			case <-ticker.C:
				// 检查输出文件大小
				downloadedBytes := int64(0)
				if info, err := os.Stat(t.outputPath); err == nil {
					downloadedBytes = info.Size()
				}
				
				// 计算下载速度
				now := time.Now()
				speed := int64(0)
				if !lastTime.IsZero() && downloadedBytes > lastBytes {
					elapsed := now.Sub(lastTime).Seconds()
					if elapsed > 0 {
						speed = int64(float64(downloadedBytes-lastBytes) / elapsed)
					}
				}
				
				// 更新进度
				progress := core.TaskProgress{
					TotalBytes:      totalBytes,
					DownloadedBytes: downloadedBytes,
					DownloadSpeed:   speed,
					Progress:        0,
				}
				if totalBytes > 0 {
					progress.Progress = float64(downloadedBytes) / float64(totalBytes) * 100
				}
				
				t.BaseTask.UpdateProgress(progress)
				
				// 保存本次统计
				lastBytes = downloadedBytes
				lastTime = now
			}
		}
	}()
	
	var result *DownloadResult
	
	if useSegmented {
		// 分段下载
		result, err = t.downloadSegmented(ctx, totalBytes, progressDone)
	} else {
		// 单连接下载
		// 创建下载任务
		task := DownloadTask{
			URL:        t.url,
			OutputPath: t.outputPath,
		}
		result, err = t.client.Download(ctx, task)
	}
	
	// 停止进度更新
	close(progressDone)
	
	if err != nil {
		log.Printf("HTTPTask[%s] 下载失败: %v", t.ID(), err)
		// 设置错误状态
		t.BaseTask.SetError(fmt.Errorf("HTTP download failed: %w", err))
	} else {
		log.Printf("HTTPTask[%s] 下载完成，大小: %d 字节", t.ID(), result.BytesRead)
		// 标记任务完成
		t.BaseTask.SetComplete()
	}
}

// downloadSegmented 执行分段下载
func (t *HTTPTask) downloadSegmented(ctx context.Context, totalBytes int64, progressDone chan struct{}) (*DownloadResult, error) {
	log.Printf("HTTPTask[%s] 开始分段下载，总大小: %d 字节", t.ID(), totalBytes)
	
	// 计算分段
	segments := calculateSegments(totalBytes, t.segmentSize, t.connections)
	log.Printf("HTTPTask[%s] 分成 %d 个分段", t.ID(), len(segments))
	
	// 创建输出文件（如果不存在）
	file, err := os.OpenFile(t.outputPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return nil, fmt.Errorf("创建输出文件失败: %w", err)
	}
	defer file.Close()
	
	// 预分配文件空间（可选）
	if err := file.Truncate(totalBytes); err != nil {
		log.Printf("HTTPTask[%s] 预分配文件空间失败: %v，继续下载", t.ID(), err)
	}
	
	// 用于协调分段下载的通道
	type segmentResult struct {
		index    int
		start    int64
		end      int64
		bytes    int64
		err      error
	}
	
	results := make(chan segmentResult, len(segments))
	
	// 启动分段下载goroutine
	semaphore := make(chan struct{}, t.connections) // 并发控制
	
	for i, seg := range segments {
		go func(idx int, start, end int64) {
			// 控制并发数
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			
			// 创建分段下载任务
			task := DownloadTask{
				URL:        t.url,
				OutputPath: t.outputPath, // 注意：所有分段写入同一个文件的不同位置
				RangeStart: start,
				RangeEnd:   end,
			}
			
			log.Printf("HTTPTask[%s] 开始下载分段 %d: bytes=%d-%d", t.ID(), idx, start, end)
			
			// 下载分段
			result, err := t.client.Download(ctx, task)
			if err != nil {
				log.Printf("HTTPTask[%s] 分段 %d 下载失败: %v", t.ID(), idx, err)
				results <- segmentResult{index: idx, start: start, end: end, err: err}
				return
			}
			
			log.Printf("HTTPTask[%s] 分段 %d 下载完成: %d 字节", t.ID(), idx, result.BytesRead)
			results <- segmentResult{index: idx, start: start, end: end, bytes: result.BytesRead}
		}(i, seg.start, seg.end)
	}
	
	// 收集结果
	var totalDownloaded int64
	var segmentErrors []error
	
	for i := 0; i < len(segments); i++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case result := <-results:
			if result.err != nil {
				segmentErrors = append(segmentErrors, fmt.Errorf("分段 %d (bytes %d-%d): %w", 
					result.index, result.start, result.end, result.err))
			} else {
				totalDownloaded += result.bytes
			}
		}
	}
	
	// 检查错误
	if len(segmentErrors) > 0 {
		// 合并所有错误
		errMsg := "分段下载失败:\n"
		for _, err := range segmentErrors {
			errMsg += fmt.Sprintf("  - %v\n", err)
		}
		return nil, fmt.Errorf("%s", errMsg)
	}
	
	log.Printf("HTTPTask[%s] 所有分段下载完成，总下载量: %d 字节", t.ID(), totalDownloaded)
	
	// 验证文件大小
	fileInfo, err := os.Stat(t.outputPath)
	if err != nil {
		return nil, fmt.Errorf("获取文件信息失败: %w", err)
	}
	
	if fileInfo.Size() != totalBytes {
		log.Printf("HTTPTask[%s] 警告: 文件大小不匹配，期望=%d，实际=%d", 
			t.ID(), totalBytes, fileInfo.Size())
	}
	
	return &DownloadResult{
		BytesRead:   totalDownloaded,
		TotalSize:   totalBytes,
		StatusCode:  200, // 假设成功
	}, nil
}

// segmentRange 表示一个分段范围
type segmentRange struct {
	start int64
	end   int64
}

// calculateSegments 计算分段
func calculateSegments(totalBytes, segmentSize int64, maxConnections int) []segmentRange {
	if totalBytes <= 0 || segmentSize <= 0 {
		return []segmentRange{{start: 0, end: -1}} // 无范围限制
	}
	
	// 计算分段数量
	numSegments := (totalBytes + segmentSize - 1) / segmentSize // 向上取整
	if numSegments > int64(maxConnections) {
		numSegments = int64(maxConnections)
	}
	
	// 计算每个分段的大小（尽量均匀）
	segmentSize = (totalBytes + numSegments - 1) / numSegments // 向上取整
	
	segments := make([]segmentRange, 0, numSegments)
	var start int64 = 0
	
	for start < totalBytes {
		end := start + segmentSize - 1
		if end >= totalBytes {
			end = totalBytes - 1
		}
		segments = append(segments, segmentRange{start: start, end: end})
		start = end + 1
	}
	
	return segments
}

// updateProgress 更新任务进度（需要定期调用）
func (t *HTTPTask) updateProgress(totalBytes, downloadedBytes int64) {
	progress := core.TaskProgress{
		TotalBytes:     totalBytes,
		DownloadedBytes: downloadedBytes,
		Progress:       0,
	}

	if totalBytes > 0 {
		progress.Progress = float64(downloadedBytes) / float64(totalBytes) * 100
	}

	t.BaseTask.UpdateProgress(progress)
}

// GetURL 获取下载URL
func (t *HTTPTask) GetURL() string {
	return t.url
}

// GetOutputPath 获取输出文件路径
func (t *HTTPTask) GetOutputPath() string {
	return t.outputPath
}

// GetProgressCallback 获取进度回调函数
func (t *HTTPTask) GetProgressCallback() func(progress core.TaskProgress) {
	return func(progress core.TaskProgress) {
		t.UpdateProgress(progress)
	}
}