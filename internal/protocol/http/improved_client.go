// Package http 提供改进的HTTP客户端功能
package http

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	
	"golang.org/x/net/publicsuffix"
)

// Checksum 校验和，参考 aria2 的 Checksum 类
type Checksum struct {
	HashType string // md5, sha-1, sha-256 等
	Digest   string // 十六进制格式的校验和
}

// NewChecksum 创建新的校验和
func NewChecksum(hashType, digest string) *Checksum {
	return &Checksum{
		HashType: hashType,
		Digest:   digest,
	}
}

// Verify 验证文件的校验和
func (cs *Checksum) Verify(filePath string) error {
	// 读取文件内容
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read file %s: %w", filePath, err)
	}
	
	var hash []byte
	
	// 根据类型计算哈希
	switch strings.ToLower(cs.HashType) {
	case "md5":
		h := md5.Sum(data)
		hash = h[:]
	case "sha-1", "sha1":
		h := sha1.Sum(data)
		hash = h[:]
	case "sha-256", "sha256":
		h := sha256.Sum256(data)
		hash = h[:]
	default:
		return fmt.Errorf("unsupported hash type: %s", cs.HashType)
	}
	
	// 计算十六进制表示
	actualDigest := hex.EncodeToString(hash)
	
	// 比较校验和
	if actualDigest != cs.Digest {
		return fmt.Errorf("checksum mismatch for %s: expected %s, got %s", 
			filePath, cs.Digest, actualDigest)
	}
	
	return nil
}

// ImprovedClient 改进的HTTP客户端，支持更多功能
type ImprovedClient struct {
	httpClient *http.Client
	config     ImprovedConfig
	mu         sync.RWMutex
	
	// 统计信息
	stats *ClientStats
}

// ImprovedConfig 改进的客户端配置
type ImprovedConfig struct {
	UserAgent         string
	Timeout           time.Duration
	MaxRetries        int
	RetryInterval     time.Duration
	MaxConnsPerHost   int
	EnableCompression bool
	ProxyURL          string
	Insecure          bool // 跳过TLS验证
	FollowRedirects   bool // 是否跟随重定向
	MaxRedirects      int  // 最大重定向次数
	KeepAlive         bool // 是否保持连接
	IdleConnTimeout   time.Duration
	TLSHandshakeTimeout time.Duration
	DialTimeout       time.Duration
	ReadBufferSize    int
	WriteBufferSize   int
}

// ClientStats 客户端统计信息
type ClientStats struct {
	TotalRequests    int64
	SuccessfulRequests int64
	FailedRequests   int64
	TotalBytes       int64
	AvgResponseTime  time.Duration
	mu               sync.RWMutex
}

// DownloadOptions 下载选项
type DownloadOptions struct {
	Headers          map[string]string
	ResumeFrom       int64 // 断点续传起始位置
	ExpectedSize     int64 // 期望的文件大小
	Checksum         string // 校验和（用于验证）
	ChecksumType     string // 校验和类型（md5, sha1, sha256等）
	Timeout          time.Duration // 单个下载超时
	MaxSpeed         int64  // 最大下载速度（字节/秒）
}

// DownloadProgress 下载进度回调
type DownloadProgress func(downloaded, total int64, speed int64)

// NewImprovedClient 创建改进的HTTP客户端
func NewImprovedClient(config ImprovedConfig) (*ImprovedClient, error) {
	// 创建Cookie Jar
	jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	if err != nil {
		return nil, fmt.Errorf("create cookie jar failed: %w", err)
	}
	
	// 创建传输层配置
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   config.DialTimeout,
			KeepAlive: 30 * time.Second,
			DualStack: true,
		}).DialContext,
		MaxIdleConns:          config.MaxConnsPerHost * 2,
		MaxIdleConnsPerHost:   config.MaxConnsPerHost,
		IdleConnTimeout:       config.IdleConnTimeout,
		TLSHandshakeTimeout:   config.TLSHandshakeTimeout,
		ExpectContinueTimeout: 1 * time.Second,
		DisableCompression:    !config.EnableCompression,
		ReadBufferSize:        config.ReadBufferSize,
		WriteBufferSize:       config.WriteBufferSize,
	}
	
	// 配置TLS
	if config.Insecure {
		transport.TLSClientConfig = &tls.Config{
			InsecureSkipVerify: true,
		}
	}
	
	// 配置代理
	if config.ProxyURL != "" {
		proxyURL, err := url.Parse(config.ProxyURL)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy URL: %w", err)
		}
		transport.Proxy = http.ProxyURL(proxyURL)
	}
	
	// 创建HTTP客户端
	httpClient := &http.Client{
		Transport: transport,
		Timeout:   config.Timeout,
		Jar:       jar,
	}
	
	// 配置重定向策略
	if !config.FollowRedirects {
		httpClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}
	} else if config.MaxRedirects > 0 {
		httpClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			if len(via) >= config.MaxRedirects {
				return fmt.Errorf("stopped after %d redirects", config.MaxRedirects)
			}
			return nil
		}
	}
	
	return &ImprovedClient{
		httpClient: httpClient,
		config:     config,
		stats: &ClientStats{
			TotalRequests:    0,
			SuccessfulRequests: 0,
			FailedRequests:   0,
			TotalBytes:       0,
			AvgResponseTime:  0,
		},
	}, nil
}

// DefaultImprovedConfig 返回默认的改进配置
func DefaultImprovedConfig() ImprovedConfig {
	return ImprovedConfig{
		UserAgent:           "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		Timeout:             30 * time.Second,
		MaxRetries:          3,
		RetryInterval:       2 * time.Second,
		MaxConnsPerHost:     10,
		EnableCompression:   true,
		FollowRedirects:     true,
		MaxRedirects:        10,
		KeepAlive:           true,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
		DialTimeout:         30 * time.Second,
		ReadBufferSize:      4096,
		WriteBufferSize:     4096,
		Insecure:            false,
	}
}

// DownloadWithProgress 带进度回调的下载
func (c *ImprovedClient) DownloadWithProgress(ctx context.Context, urlStr, outputPath string, 
	options DownloadOptions, progress DownloadProgress) (*DownloadResult, error) {
	
	startTime := time.Now()
	
	// 获取文件信息
	fileInfo, err := c.Head(ctx, urlStr, options.Headers)
	if err != nil {
		return nil, fmt.Errorf("get file info failed: %w", err)
	}
	
	totalSize := fileInfo.TotalSize
	if options.ExpectedSize > 0 {
		totalSize = options.ExpectedSize
	}
	
	// 检查是否支持断点续传
	resumeFrom := options.ResumeFrom
	if resumeFrom > 0 {
		supportsRange, err := c.supportsRangeRequest(ctx, urlStr, options.Headers)
		if err != nil {
			return nil, fmt.Errorf("check range support failed: %w", err)
		}
		if !supportsRange {
			resumeFrom = 0 // 不支持断点续传，从头开始
		}
	}
	
	// 创建或打开输出文件
	var file *os.File
	if resumeFrom > 0 {
		// 断点续传：以追加模式打开
		file, err = os.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE, 0644)
		if err != nil {
			return nil, fmt.Errorf("open file for resume failed: %w", err)
		}
		// 定位到续传位置
		if _, err := file.Seek(resumeFrom, io.SeekStart); err != nil {
			file.Close()
			return nil, fmt.Errorf("seek to resume position failed: %w", err)
		}
	} else {
		// 新下载：创建文件
		file, err = os.Create(outputPath)
		if err != nil {
			return nil, fmt.Errorf("create output file failed: %w", err)
		}
	}
	defer file.Close()
	
	// 创建请求
	req, err := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}
	
	// 设置请求头
	c.setRequestHeaders(req, options.Headers)
	
	// 设置范围请求（断点续传）
	if resumeFrom > 0 {
		rangeHeader := fmt.Sprintf("bytes=%d-", resumeFrom)
		if totalSize > 0 {
			rangeHeader = fmt.Sprintf("bytes=%d-%d", resumeFrom, totalSize-1)
		}
		req.Header.Set("Range", rangeHeader)
	}
	
	// 发送请求
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()
	
	// 检查响应状态
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return nil, fmt.Errorf("HTTP error: %s", resp.Status)
	}
	
	// 创建带限速的reader（如果设置了最大速度）
	var reader io.Reader = resp.Body
	if options.MaxSpeed > 0 {
		reader = NewRateLimitedReader(resp.Body, options.MaxSpeed)
	}
	
	// 创建进度跟踪器
	progressReader := &progressTrackingReader{
		reader:   reader,
		progress: progress,
		total:    totalSize,
		downloaded: resumeFrom,
	}
	
	// 复制数据
	bytesRead, err := io.Copy(file, progressReader)
	if err != nil {
		return nil, fmt.Errorf("copy data failed: %w", err)
	}
	
	totalDownloaded := resumeFrom + bytesRead
	
	// 更新统计信息
	c.updateStats(true, bytesRead, time.Since(startTime))
	
	// 验证校验和（如果提供了）
	if options.Checksum != "" && options.ChecksumType != "" {
		if err := c.verifyChecksum(outputPath, options.Checksum, options.ChecksumType); err != nil {
			return nil, fmt.Errorf("checksum verification failed: %w", err)
		}
	}
	
	return &DownloadResult{
		BytesRead:   totalDownloaded,
		TotalSize:   totalSize,
		StatusCode:  resp.StatusCode,
		ContentType: resp.Header.Get("Content-Type"),
		ETag:        resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
	}, nil
}

// supportsRangeRequest 检查服务器是否支持范围请求
func (c *ImprovedClient) supportsRangeRequest(ctx context.Context, urlStr string, headers map[string]string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, "HEAD", urlStr, nil)
	if err != nil {
		return false, fmt.Errorf("create HEAD request failed: %w", err)
	}
	
	c.setRequestHeaders(req, headers)
	
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("HEAD request failed: %w", err)
	}
	defer resp.Body.Close()
	
	// 检查Accept-Ranges头
	acceptRanges := resp.Header.Get("Accept-Ranges")
	if strings.ToLower(acceptRanges) == "bytes" {
		return true, nil
	}
	
	// 检查Content-Range头（对于部分内容响应）
	contentRange := resp.Header.Get("Content-Range")
	if contentRange != "" && strings.HasPrefix(contentRange, "bytes") {
		return true, nil
	}
	
	return false, nil
}

// setRequestHeaders 设置请求头
func (c *ImprovedClient) setRequestHeaders(req *http.Request, headers map[string]string) {
	// 设置用户代理
	if c.config.UserAgent != "" {
		req.Header.Set("User-Agent", c.config.UserAgent)
	}
	
	// 设置Accept-Encoding（如果启用压缩）
	if c.config.EnableCompression {
		req.Header.Set("Accept-Encoding", "gzip, deflate")
	}
	
	// 设置自定义头部
	for key, value := range headers {
		req.Header.Set(key, value)
	}
}

// verifyChecksum 验证文件校验和，参考 aria2 的 ChecksumCheckIntegrityEntry
func (c *ImprovedClient) verifyChecksum(filePath, expectedChecksum, checksumType string) error {
	// 创建校验和对象
	checksum := NewChecksum(checksumType, expectedChecksum)
	
	// 验证校验和
	if err := checksum.Verify(filePath); err != nil {
		return fmt.Errorf("checksum verification failed: %w", err)
	}
	
	return nil
}

// VerifyChecksum 验证文件校验和（公共方法）
func (c *ImprovedClient) VerifyChecksum(filePath, expectedChecksum, checksumType string) error {
	return c.verifyChecksum(filePath, expectedChecksum, checksumType)
}

// updateStats 更新统计信息
func (c *ImprovedClient) updateStats(success bool, bytesRead int64, responseTime time.Duration) {
	c.stats.mu.Lock()
	defer c.stats.mu.Unlock()
	
	c.stats.TotalRequests++
	if success {
		c.stats.SuccessfulRequests++
		c.stats.TotalBytes += bytesRead
		
		// 更新平均响应时间
		if c.stats.SuccessfulRequests == 1 {
			c.stats.AvgResponseTime = responseTime
		} else {
			// 加权平均
			c.stats.AvgResponseTime = (c.stats.AvgResponseTime*time.Duration(c.stats.SuccessfulRequests-1) + responseTime) / 
				time.Duration(c.stats.SuccessfulRequests)
		}
	} else {
		c.stats.FailedRequests++
	}
}

// GetStats 获取统计信息
func (c *ImprovedClient) GetStats() ClientStats {
	c.stats.mu.RLock()
	defer c.stats.mu.RUnlock()
	return *c.stats
}

// ResetStats 重置统计信息
func (c *ImprovedClient) ResetStats() {
	c.stats.mu.Lock()
	defer c.stats.mu.Unlock()
	
	c.stats.TotalRequests = 0
	c.stats.SuccessfulRequests = 0
	c.stats.FailedRequests = 0
	c.stats.TotalBytes = 0
	c.stats.AvgResponseTime = 0
}

// Head 发送HEAD请求（改进版本）
func (c *ImprovedClient) Head(ctx context.Context, urlStr string, headers map[string]string) (*DownloadResult, error) {
	req, err := http.NewRequestWithContext(ctx, "HEAD", urlStr, nil)
	if err != nil {
		return nil, fmt.Errorf("create HEAD request failed: %w", err)
	}
	
	c.setRequestHeaders(req, headers)
	
	startTime := time.Now()
	resp, err := c.httpClient.Do(req)
	responseTime := time.Since(startTime)
	
	if err != nil {
		c.updateStats(false, 0, responseTime)
		return nil, fmt.Errorf("HEAD request failed: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		c.updateStats(false, 0, responseTime)
		return nil, fmt.Errorf("HTTP error: %s", resp.Status)
	}
	
	// 获取文件大小
	contentLength := resp.Header.Get("Content-Length")
	totalSize, _ := strconv.ParseInt(contentLength, 10, 64)
	
	// 检查是否支持断点续传
	supportsRange := false
	acceptRanges := resp.Header.Get("Accept-Ranges")
	if strings.ToLower(acceptRanges) == "bytes" {
		supportsRange = true
	}
	
	c.updateStats(true, 0, responseTime)
	
	return &DownloadResult{
		TotalSize:      totalSize,
		StatusCode:     resp.StatusCode,
		ContentType:    resp.Header.Get("Content-Type"),
		ETag:           resp.Header.Get("ETag"),
		LastModified:   resp.Header.Get("Last-Modified"),
		SupportsRange:  supportsRange,
	}, nil
}

// SetProxy 设置代理
func (c *ImprovedClient) SetProxy(proxyURL string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	if proxyURL == "" {
		// 清除代理
		if transport, ok := c.httpClient.Transport.(*http.Transport); ok {
			transport.Proxy = nil
		}
		return nil
	}
	
	parsedURL, err := url.Parse(proxyURL)
	if err != nil {
		return fmt.Errorf("invalid proxy URL: %w", err)
	}
	
	if transport, ok := c.httpClient.Transport.(*http.Transport); ok {
		transport.Proxy = http.ProxyURL(parsedURL)
	}
	
	return nil
}

// SetCookies 设置Cookie
func (c *ImprovedClient) SetCookies(urlStr string, cookies []*http.Cookie) error {
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	
	c.httpClient.Jar.SetCookies(parsedURL, cookies)
	return nil
}

// GetCookies 获取Cookie
func (c *ImprovedClient) GetCookies(urlStr string) ([]*http.Cookie, error) {
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	
	return c.httpClient.Jar.Cookies(parsedURL), nil
}

// progressTrackingReader 跟踪进度的Reader
type progressTrackingReader struct {
	reader     io.Reader
	progress   DownloadProgress
	total      int64
	downloaded int64
	lastUpdate time.Time
	lastBytes  int64
	mu         sync.Mutex
}

func (ptr *progressTrackingReader) Read(p []byte) (n int, err error) {
	n, err = ptr.reader.Read(p)
	
	if n > 0 && ptr.progress != nil {
		ptr.mu.Lock()
		ptr.downloaded += int64(n)
		
		// 计算速度
		now := time.Now()
		speed := int64(0)
		if !ptr.lastUpdate.IsZero() {
			elapsed := now.Sub(ptr.lastUpdate).Seconds()
			if elapsed > 0 {
				speed = int64(float64(ptr.downloaded-ptr.lastBytes) / elapsed)
			}
		}
		
		// 更新进度（限制更新频率）
		if now.Sub(ptr.lastUpdate) > 100*time.Millisecond || ptr.lastUpdate.IsZero() {
			ptr.progress(ptr.downloaded, ptr.total, speed)
			ptr.lastUpdate = now
			ptr.lastBytes = ptr.downloaded
		}
		ptr.mu.Unlock()
	}
	
	return n, err
}

// RateLimitedReader 限速Reader
type RateLimitedReader struct {
	reader      io.Reader
	maxSpeed    int64 // 字节/秒
	lastRead    time.Time
	bytesRead   int64
	mu          sync.Mutex
}

// NewRateLimitedReader 创建限速Reader
func NewRateLimitedReader(reader io.Reader, maxSpeed int64) *RateLimitedReader {
	return &RateLimitedReader{
		reader:   reader,
		maxSpeed: maxSpeed,
		lastRead: time.Now(),
	}
}

func (rlr *RateLimitedReader) Read(p []byte) (n int, err error) {
	if rlr.maxSpeed <= 0 {
		return rlr.reader.Read(p)
	}
	
	// 计算允许读取的字节数
	rlr.mu.Lock()
	now := time.Now()
	elapsed := now.Sub(rlr.lastRead).Seconds()
	
	// 计算这个时间窗口内允许读取的字节数
	allowedBytes := int64(elapsed * float64(rlr.maxSpeed))
	if allowedBytes <= 0 {
		// 需要等待
		waitTime := time.Duration(float64(time.Second) / float64(rlr.maxSpeed))
		rlr.mu.Unlock()
		time.Sleep(waitTime)
		rlr.mu.Lock()
		now = time.Now()
		elapsed = now.Sub(rlr.lastRead).Seconds()
		allowedBytes = int64(elapsed * float64(rlr.maxSpeed))
	}
	
	// 限制读取大小
	maxRead := len(p)
	if int64(maxRead) > allowedBytes {
		maxRead = int(allowedBytes)
	}
	rlr.mu.Unlock()
	
	// 读取数据
	if maxRead > 0 {
		n, err = rlr.reader.Read(p[:maxRead])
		if n > 0 {
			rlr.mu.Lock()
			rlr.bytesRead += int64(n)
			rlr.lastRead = time.Now()
			rlr.mu.Unlock()
		}
	}
	
	return n, err
}