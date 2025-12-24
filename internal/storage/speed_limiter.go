// Package storage 提供速度限制功能
package storage

import (
	"io"
	"sync"
	"time"
)

// SpeedLimiter 速度限制器
type SpeedLimiter struct {
	mu sync.RWMutex
	
	// 速度限制（字节/秒）
	limit int64
	
	// 当前窗口已传输的字节数
	currentBytes int64
	
	// 当前窗口开始时间
	windowStart time.Time
	
	// 窗口大小（秒）
	windowSize time.Duration
	
	// 是否启用限制
	enabled bool
}

// NewSpeedLimiter 创建速度限制器
func NewSpeedLimiter(limit int64) *SpeedLimiter {
	return &SpeedLimiter{
		limit:       limit,
		enabled:     limit > 0,
		windowSize:  1 * time.Second,
		windowStart: time.Now(),
	}
}

// Wait 等待直到可以传输指定字节数
func (sl *SpeedLimiter) Wait(bytes int64) time.Duration {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	
	if !sl.enabled || sl.limit <= 0 {
		return 0
	}
	
	now := time.Now()
	elapsed := now.Sub(sl.windowStart)
	
	// 如果超过窗口大小，重置窗口
	if elapsed >= sl.windowSize {
		sl.currentBytes = 0
		sl.windowStart = now
		elapsed = 0
	}
	
	// 计算当前窗口剩余可传输的字节数
	allowedBytes := int64(float64(sl.limit) * elapsed.Seconds())
	remainingBytes := sl.limit - (sl.currentBytes + allowedBytes)
	
	if remainingBytes <= 0 {
		// 需要等待到下一个窗口
		waitTime := sl.windowSize - elapsed
		sl.currentBytes = 0
		sl.windowStart = now.Add(waitTime)
		return waitTime
	}
	
	// 检查是否可以立即传输
	if bytes <= remainingBytes {
		sl.currentBytes += bytes
		return 0
	}
	
	// 需要等待
	// 计算需要等待的时间
	neededBytes := bytes - remainingBytes
	waitTime := time.Duration(float64(neededBytes) / float64(sl.limit) * float64(time.Second))
	
	// 重置窗口
	sl.currentBytes = bytes
	sl.windowStart = now.Add(waitTime)
	
	return waitTime
}

// SetLimit 设置速度限制
func (sl *SpeedLimiter) SetLimit(limit int64) {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	
	sl.limit = limit
	sl.enabled = limit > 0
	
	// 重置窗口
	sl.currentBytes = 0
	sl.windowStart = time.Now()
}

// GetLimit 获取当前速度限制
func (sl *SpeedLimiter) GetLimit() int64 {
	sl.mu.RLock()
	defer sl.mu.RUnlock()
	return sl.limit
}

// IsEnabled 检查是否启用限制
func (sl *SpeedLimiter) IsEnabled() bool {
	sl.mu.RLock()
	defer sl.mu.RUnlock()
	return sl.enabled
}

// Enable 启用速度限制
func (sl *SpeedLimiter) Enable() {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	sl.enabled = true
}

// Disable 禁用速度限制
func (sl *SpeedLimiter) Disable() {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	sl.enabled = false
}

// Reset 重置限制器
func (sl *SpeedLimiter) Reset() {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	sl.currentBytes = 0
	sl.windowStart = time.Now()
}

// ThrottledReader 带速度限制的Reader
type ThrottledReader struct {
	reader      io.Reader
	speedLimiter *SpeedLimiter
}

// NewThrottledReader 创建带速度限制的Reader
func NewThrottledReader(reader io.Reader, speedLimiter *SpeedLimiter) *ThrottledReader {
	return &ThrottledReader{
		reader:       reader,
		speedLimiter: speedLimiter,
	}
}

// Read 实现io.Reader接口
func (tr *ThrottledReader) Read(p []byte) (int, error) {
	n, err := tr.reader.Read(p)
	
	if n > 0 && tr.speedLimiter != nil && tr.speedLimiter.IsEnabled() {
		waitTime := tr.speedLimiter.Wait(int64(n))
		if waitTime > 0 {
			time.Sleep(waitTime)
		}
	}
	
	return n, err
}

// ThrottledWriter 带速度限制的Writer
type ThrottledWriter struct {
	writer      io.Writer
	speedLimiter *SpeedLimiter
}

// NewThrottledWriter 创建带速度限制的Writer
func NewThrottledWriter(writer io.Writer, speedLimiter *SpeedLimiter) *ThrottledWriter {
	return &ThrottledWriter{
		writer:       writer,
		speedLimiter: speedLimiter,
	}
}

// Write 实现io.Writer接口
func (tw *ThrottledWriter) Write(p []byte) (int, error) {
	if len(p) > 0 && tw.speedLimiter != nil && tw.speedLimiter.IsEnabled() {
		waitTime := tw.speedLimiter.Wait(int64(len(p)))
		if waitTime > 0 {
			time.Sleep(waitTime)
		}
	}
	
	return tw.writer.Write(p)
}

// GlobalSpeedLimiter 全局速度限制器
type GlobalSpeedLimiter struct {
	downloadLimiter *SpeedLimiter
	uploadLimiter   *SpeedLimiter
}

// NewGlobalSpeedLimiter 创建全局速度限制器
func NewGlobalSpeedLimiter(downloadLimit, uploadLimit int64) *GlobalSpeedLimiter {
	return &GlobalSpeedLimiter{
		downloadLimiter: NewSpeedLimiter(downloadLimit),
		uploadLimiter:   NewSpeedLimiter(uploadLimit),
	}
}

// GetDownloadLimiter 获取下载限制器
func (gsl *GlobalSpeedLimiter) GetDownloadLimiter() *SpeedLimiter {
	return gsl.downloadLimiter
}

// GetUploadLimiter 获取上传限制器
func (gsl *GlobalSpeedLimiter) GetUploadLimiter() *SpeedLimiter {
	return gsl.uploadLimiter
}

// SetDownloadLimit 设置下载限制
func (gsl *GlobalSpeedLimiter) SetDownloadLimit(limit int64) {
	gsl.downloadLimiter.SetLimit(limit)
}

// SetUploadLimit 设置上传限制
func (gsl *GlobalSpeedLimiter) SetUploadLimit(limit int64) {
	gsl.uploadLimiter.SetLimit(limit)
}