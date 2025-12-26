// Package http 提供HTTP/HTTPS协议支持
package http

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// CookieStorage Cookie 存储，参考 aria2 的 CookieStorage
type CookieStorage struct {
	mu      sync.RWMutex
	cookies map[string][]*http.Cookie // key: domain
}

// NewCookieStorage 创建新的 Cookie 存储
func NewCookieStorage() *CookieStorage {
	return &CookieStorage{
		cookies: make(map[string][]*http.Cookie),
	}
}

// Add 添加 Cookie
func (cs *CookieStorage) Add(cookie *http.Cookie) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	
	domain := cookie.Domain
	if domain == "" {
		domain = "localhost"
	}
	
	// 检查是否已存在相同的 Cookie
	for i, c := range cs.cookies[domain] {
		if c.Name == cookie.Name && c.Path == cookie.Path {
			cs.cookies[domain][i] = cookie
			return
		}
	}
	
	cs.cookies[domain] = append(cs.cookies[domain], cookie)
}

// Get 获取指定域名的 Cookies
func (cs *CookieStorage) Get(domain string) []*http.Cookie {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	
	if cookies, exists := cs.cookies[domain]; exists {
		return cookies
	}
	return nil
}

// GetAll 获取所有 Cookies
func (cs *CookieStorage) GetAll() []*http.Cookie {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	
	var all []*http.Cookie
	for _, cookies := range cs.cookies {
		all = append(all, cookies...)
	}
	return all
}

// Clear 清除所有 Cookies
func (cs *CookieStorage) Clear() {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.cookies = make(map[string][]*http.Cookie)
}

// ClearDomain 清除指定域名的 Cookies
func (cs *CookieStorage) ClearDomain(domain string) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	delete(cs.cookies, domain)
}

// Client HTTP客户端配置
type Client struct {
	// HTTP客户端
	httpClient *http.Client
	
	// 用户代理
	userAgent string
	
	// 超时设置
	timeout time.Duration
	
	// 最大重试次数
	maxRetries int
	
	// 重试间隔
	retryInterval time.Duration
	
	// 连接池大小
	maxConnsPerHost int
	
	// 是否启用压缩
	enableCompression bool
	
	// Cookie存储，参考 aria2 的 CookieStorage
	cookieStorage *CookieStorage
	
	// 基本认证
	username string
	password string
	
	mu sync.RWMutex
}

// Config HTTP客户端配置
type Config struct {
	UserAgent         string
	Timeout           time.Duration
	MaxRetries        int
	RetryInterval     time.Duration
	MaxConnsPerHost   int
	EnableCompression bool
	ProxyURL          string
	Insecure          bool // 跳过TLS验证
}

// NewClient 创建新的HTTP客户端
func NewClient(config Config) (*Client, error) {
	// 创建传输层
	transport := &http.Transport{
		MaxIdleConns:        config.MaxConnsPerHost * 2,
		MaxIdleConnsPerHost: config.MaxConnsPerHost,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
		DisableCompression:  !config.EnableCompression,
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
	}
	
	return &Client{
		httpClient:        httpClient,
		userAgent:         config.UserAgent,
		timeout:           config.Timeout,
		maxRetries:        config.MaxRetries,
		retryInterval:     config.RetryInterval,
		maxConnsPerHost:   config.MaxConnsPerHost,
		enableCompression: config.EnableCompression,
		cookieStorage:     NewCookieStorage(),
	}, nil
}

// DownloadTask 下载任务
type DownloadTask struct {
	URL         string
	OutputPath  string
	RangeStart  int64
	RangeEnd    int64
	Headers     map[string]string
}

// DownloadResult 下载结果
type DownloadResult struct {
	BytesRead     int64
	TotalSize     int64
	StatusCode    int
	ContentType   string
	ETag          string
	LastModified  string
	SupportsRange bool
}

// Download 执行下载
func (c *Client) Download(ctx context.Context, task DownloadTask) (*DownloadResult, error) {
	fmt.Printf("HTTP Client[Download] 开始下载: %s -> %s\n", task.URL, task.OutputPath)
	var lastErr error
	
	for retry := 0; retry <= c.maxRetries; retry++ {
		if retry > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(c.retryInterval):
				// 重试等待
			}
		}
		
		result, err := c.downloadOnce(ctx, task)
		if err == nil {
			fmt.Printf("HTTP Client[Download] 下载成功: %s, 大小: %d 字节\n", task.URL, result.BytesRead)
			return result, nil
		}
		
		lastErr = err
		
		// 如果是致命错误，不重试
		if isFatalError(err) {
			return nil, err
		}
	}
	
	return nil, fmt.Errorf("download failed after %d retries: %w", c.maxRetries, lastErr)
}

// downloadOnce 单次下载尝试
func (c *Client) downloadOnce(ctx context.Context, task DownloadTask) (*DownloadResult, error) {
	// 创建请求
	req, err := http.NewRequestWithContext(ctx, "GET", task.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}
	
	// 设置用户代理
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}
	
	// 设置范围请求
	if task.RangeStart > 0 || task.RangeEnd > 0 {
		rangeHeader := fmt.Sprintf("bytes=%d-", task.RangeStart)
		if task.RangeEnd > 0 {
			rangeHeader = fmt.Sprintf("bytes=%d-%d", task.RangeStart, task.RangeEnd)
		}
		req.Header.Set("Range", rangeHeader)
	}
	
	// 设置自定义头部
	for key, value := range task.Headers {
		req.Header.Set(key, value)
	}
	
	// 基本认证
	if c.username != "" && c.password != "" {
		req.SetBasicAuth(c.username, c.password)
	}
	
	// 发送请求
	resp, err := c.httpClient.Do(req)
	if err != nil {
		fmt.Printf("HTTP Client[downloadOnce] 请求失败: %v\n", err)
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()
	
	// 检查响应状态
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return nil, fmt.Errorf("HTTP error: %s", resp.Status)
	}
	
	// 创建输出目录
	outputDir := filepath.Dir(task.OutputPath)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("create output directory failed: %w", err)
	}
	
	// 打开输出文件
	// 对于范围请求，不使用O_TRUNC标志，以免截断其他分段的数据
	openFlags := os.O_WRONLY | os.O_CREATE
	if task.RangeStart == 0 && task.RangeEnd == 0 {
		// 非范围请求（完整文件下载）使用O_TRUNC
		openFlags |= os.O_TRUNC
	}
	file, err := os.OpenFile(task.OutputPath, openFlags, 0644)
	if err != nil {
		return nil, fmt.Errorf("open output file failed: %w", err)
	}
	defer file.Close()
	
	// 定位到指定位置（对于范围请求）
	if task.RangeStart > 0 {
		if _, err := file.Seek(task.RangeStart, io.SeekStart); err != nil {
			return nil, fmt.Errorf("seek file failed: %w", err)
		}
	}
	
	// 复制数据
	bytesRead, err := io.Copy(file, resp.Body)
	if err != nil {
		return nil, fmt.Errorf("copy data failed: %w", err)
	}
	
	// 获取文件信息
	fileInfo, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("get file info failed: %w", err)
	}
	
	// 构建结果
	result := &DownloadResult{
		BytesRead:   bytesRead,
		TotalSize:   fileInfo.Size(),
		StatusCode:  resp.StatusCode,
		ContentType: resp.Header.Get("Content-Type"),
		ETag:        resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
	}
	
	return result, nil
}

// Head 发送HEAD请求获取文件信息
func (c *Client) Head(ctx context.Context, url string, headers map[string]string) (*DownloadResult, error) {
	req, err := http.NewRequestWithContext(ctx, "HEAD", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}
	
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}
	
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HEAD request failed: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP error: %s", resp.Status)
	}
	
	// 获取文件大小
	contentLength := resp.Header.Get("Content-Length")
	totalSize, _ := strconv.ParseInt(contentLength, 10, 64)
	
	return &DownloadResult{
		TotalSize:   totalSize,
		StatusCode:  resp.StatusCode,
		ContentType: resp.Header.Get("Content-Type"),
		ETag:        resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
	}, nil
}

// SetBasicAuth 设置基本认证
func (c *Client) SetBasicAuth(username, password string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.username = username
	c.password = password
}

// SetCookies 设置Cookie，参考 aria2 的 CookieStorage
func (c *Client) SetCookies(cookies []*http.Cookie) {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	for _, cookie := range cookies {
		c.cookieStorage.Add(cookie)
	}
}

// GetCookies 获取指定域名的 Cookies
func (c *Client) GetCookies(domain string) []*http.Cookie {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	return c.cookieStorage.Get(domain)
}

// GetAllCookies 获取所有 Cookies
func (c *Client) GetAllCookies() []*http.Cookie {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	return c.cookieStorage.GetAll()
}

// ClearCookies 清除所有 Cookies
func (c *Client) ClearCookies() {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	c.cookieStorage.Clear()
}

// ClearCookiesForDomain 清除指定域名的 Cookies
func (c *Client) ClearCookiesForDomain(domain string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	c.cookieStorage.ClearDomain(domain)
}

// isFatalError 判断是否为致命错误（不需要重试）
func isFatalError(err error) bool {
	if err == nil {
		return false
	}
	
	errStr := err.Error()
	// 这些错误通常不需要重试
	fatalErrors := []string{
		"404", "Not Found",
		"403", "Forbidden",
		"401", "Unauthorized",
		"400", "Bad Request",
		"501", "Not Implemented",
	}
	
	for _, fatalErr := range fatalErrors {
		if strings.Contains(errStr, fatalErr) {
			return true
		}
	}
	
	return false
}

// DefaultConfig 返回默认配置
func DefaultConfig() Config {
	return Config{
		UserAgent:         "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		Timeout:           30 * time.Second,
		MaxRetries:        3,
		RetryInterval:     2 * time.Second,
		MaxConnsPerHost:   10,
		EnableCompression: true,
	}
}