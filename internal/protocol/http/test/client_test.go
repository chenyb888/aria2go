package http_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
	
	"aria2go/internal/protocol/http"
)

// TestNewClient 测试创建客户端
func TestNewClient(t *testing.T) {
	config := http.Config{
		UserAgent:         "TestAgent",
		Timeout:           10 * time.Second,
		MaxRetries:        3,
		RetryInterval:     1 * time.Second,
		MaxConnsPerHost:   5,
		EnableCompression: true,
	}
	
	client, err := http.NewClient(config)
	if err != nil {
		t.Fatalf("创建客户端失败: %v", err)
	}
	
	if client == nil {
		t.Fatal("客户端为nil")
	}
}

// TestNewImprovedClient 测试创建改进的客户端
func TestNewImprovedClient(t *testing.T) {
	config := http.DefaultImprovedConfig()
	config.UserAgent = "TestImprovedAgent"
	config.Timeout = 5 * time.Second
	
	client, err := http.NewImprovedClient(config)
	if err != nil {
		t.Fatalf("创建改进客户端失败: %v", err)
	}
	
	if client == nil {
		t.Fatal("改进客户端为nil")
	}
}

// TestDownloadSmallFile 测试下载小文件
func TestDownloadSmallFile(t *testing.T) {
	// 创建测试服务器
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Length", "13")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Hello, World!"))
	}))
	defer server.Close()
	
	// 创建临时目录
	tempDir := t.TempDir()
	outputPath := filepath.Join(tempDir, "test.txt")
	
	// 创建客户端
	config := http.DefaultImprovedConfig()
	config.Timeout = 5 * time.Second
	client, err := http.NewImprovedClient(config)
	if err != nil {
		t.Fatalf("创建客户端失败: %v", err)
	}
	
	// 执行下载
	ctx := context.Background()
	options := http.DownloadOptions{
		Headers: nil,
	}
	
	var progressCalled bool
	progressCB := func(downloaded, total int64, speed int64) {
		progressCalled = true
		t.Logf("进度: %d/%d, 速度: %d B/s", downloaded, total, speed)
	}
	
	result, err := client.DownloadWithProgress(ctx, server.URL, outputPath, options, progressCB)
	if err != nil {
		t.Fatalf("下载失败: %v", err)
	}
	
	// 验证结果
	if result.BytesRead != 13 {
		t.Errorf("期望下载13字节，实际下载%d字节", result.BytesRead)
	}
	
	if result.StatusCode != http.StatusOK {
		t.Errorf("期望状态码200，实际状态码%d", result.StatusCode)
	}
	
	// 验证文件内容
	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("读取文件失败: %v", err)
	}
	
	if string(content) != "Hello, World!" {
		t.Errorf("文件内容不匹配: %s", string(content))
	}
	
	// 验证进度回调被调用
	if !progressCalled {
		t.Error("进度回调未被调用")
	}
}

// TestHeadRequest 测试HEAD请求
func TestHeadRequest(t *testing.T) {
	// 创建测试服务器
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "HEAD" {
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Length", "1024")
			w.Header().Set("Accept-Ranges", "bytes")
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()
	
	// 创建客户端
	config := http.DefaultImprovedConfig()
	client, err := http.NewImprovedClient(config)
	if err != nil {
		t.Fatalf("创建客户端失败: %v", err)
	}
	
	// 执行HEAD请求
	ctx := context.Background()
	result, err := client.Head(ctx, server.URL, nil)
	if err != nil {
		t.Fatalf("HEAD请求失败: %v", err)
	}
	
	// 验证结果
	if result.TotalSize != 1024 {
		t.Errorf("期望文件大小1024字节，实际%d字节", result.TotalSize)
	}
	
	if !result.SupportsRange {
		t.Error("期望支持范围请求，但实际不支持")
	}
}

// TestRangeSupport 测试范围请求支持检测
func TestRangeSupport(t *testing.T) {
	// 创建测试服务器（不支持范围请求）
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "2048")
		// 不设置Accept-Ranges头
		w.WriteHeader(http.StatusOK)
		if r.Method == "GET" {
			w.Write(make([]byte, 2048))
		}
	}))
	defer server.Close()
	
	// 创建客户端
	config := http.DefaultImprovedConfig()
	client, err := http.NewImprovedClient(config)
	if err != nil {
		t.Fatalf("创建客户端失败: %v", err)
	}
	
	// 检测范围请求支持
	ctx := context.Background()
	supportsRange, err := client.supportsRangeRequest(ctx, server.URL, nil)
	if err != nil {
		t.Fatalf("检测范围请求支持失败: %v", err)
	}
	
	if supportsRange {
		t.Error("期望不支持范围请求，但检测结果为支持")
	}
}

// TestRetryOnFailure 测试失败重试
func TestRetryOnFailure(t *testing.T) {
	retryCount := 0
	maxRetries := 2
	
	// 创建测试服务器，前两次请求失败，第三次成功
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		retryCount++
		if retryCount <= maxRetries {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("Internal Server Error"))
		} else {
			w.Header().Set("Content-Length", "10")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("Success!"))
		}
	}))
	defer server.Close()
	
	// 创建客户端，配置重试
	config := http.DefaultImprovedConfig()
	config.MaxRetries = maxRetries
	config.RetryInterval = 100 * time.Millisecond // 缩短重试间隔以便测试
	client, err := http.NewImprovedClient(config)
	if err != nil {
		t.Fatalf("创建客户端失败: %v", err)
	}
	
	// 创建临时目录
	tempDir := t.TempDir()
	outputPath := filepath.Join(tempDir, "retry_test.txt")
	
	// 执行下载
	ctx := context.Background()
	options := http.DownloadOptions{
		Headers: nil,
	}
	
	result, err := client.DownloadWithProgress(ctx, server.URL, outputPath, options, nil)
	if err != nil {
		t.Fatalf("下载失败: %v", err)
	}
	
	// 验证重试次数
	if retryCount != maxRetries+1 {
		t.Errorf("期望重试%d次，实际重试%d次", maxRetries+1, retryCount)
	}
	
	// 验证下载结果
	if result.BytesRead != 10 {
		t.Errorf("期望下载10字节，实际下载%d字节", result.BytesRead)
	}
}

// TestCancelDownload 测试取消下载
func TestCancelDownload(t *testing.T) {
	// 创建慢速测试服务器
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000000") // 1MB
		w.WriteHeader(http.StatusOK)
		
		// 缓慢写入数据
		for i := 0; i < 100; i++ {
			select {
			case <-r.Context().Done():
				// 客户端取消
				return
			default:
				w.Write(make([]byte, 10000))
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
				time.Sleep(10 * time.Millisecond)
			}
		}
	}))
	defer server.Close()
	
	// 创建客户端
	config := http.DefaultImprovedConfig()
	config.Timeout = 30 * time.Second
	client, err := http.NewImprovedClient(config)
	if err != nil {
		t.Fatalf("创建客户端失败: %v", err)
	}
	
	// 创建临时目录
	tempDir := t.TempDir()
	outputPath := filepath.Join(tempDir, "cancel_test.txt")
	
	// 创建可取消的上下文
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	
	// 执行下载（应该被取消）
	options := http.DownloadOptions{
		Headers: nil,
	}
	
	_, err = client.DownloadWithProgress(ctx, server.URL, outputPath, options, nil)
	if err == nil {
		t.Error("期望下载被取消，但实际成功完成")
	}
	
	// 验证错误是上下文取消
	if ctx.Err() == nil {
		t.Error("上下文应该被取消")
	}
}

// TestInvalidURL 测试无效URL
func TestInvalidURL(t *testing.T) {
	config := http.DefaultImprovedConfig()
	client, err := http.NewImprovedClient(config)
	if err != nil {
		t.Fatalf("创建客户端失败: %v", err)
	}
	
	ctx := context.Background()
	options := http.DownloadOptions{
		Headers: nil,
	}
	
	// 使用无效URL
	_, err = client.DownloadWithProgress(ctx, "://invalid-url", "/tmp/test.txt", options, nil)
	if err == nil {
		t.Error("期望无效URL错误，但实际成功")
	}
}

// TestFileCreation 测试文件创建
func TestFileCreation(t *testing.T) {
	// 创建测试服务器
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "50")
		w.WriteHeader(http.StatusOK)
		w.Write(make([]byte, 50))
	}))
	defer server.Close()
	
	// 创建客户端
	config := http.DefaultImprovedConfig()
	client, err := http.NewImprovedClient(config)
	if err != nil {
		t.Fatalf("创建客户端失败: %v", err)
	}
	
	// 创建临时目录
	tempDir := t.TempDir()
	
	// 测试1: 正常文件创建
	outputPath1 := filepath.Join(tempDir, "test1.txt")
	ctx := context.Background()
	options := http.DownloadOptions{
		Headers: nil,
	}
	
	result1, err := client.DownloadWithProgress(ctx, server.URL, outputPath1, options, nil)
	if err != nil {
		t.Fatalf("下载失败: %v", err)
	}
	
	if result1.BytesRead != 50 {
		t.Errorf("期望下载50字节，实际下载%d字节", result1.BytesRead)
	}
	
	// 验证文件存在
	if _, err := os.Stat(outputPath1); os.IsNotExist(err) {
		t.Error("文件未创建")
	}
	
	// 测试2: 目录不存在，应该自动创建
	outputPath2 := filepath.Join(tempDir, "subdir", "test2.txt")
	result2, err := client.DownloadWithProgress(ctx, server.URL, outputPath2, options, nil)
	if err != nil {
		t.Fatalf("下载失败（自动创建目录）: %v", err)
	}
	
	if result2.BytesRead != 50 {
		t.Errorf("期望下载50字节，实际下载%d字节", result2.BytesRead)
	}
	
	// 验证文件存在
	if _, err := os.Stat(outputPath2); os.IsNotExist(err) {
		t.Error("文件未创建（自动创建目录失败）")
	}
}

// TestClientStats 测试客户端统计信息
func TestClientStats(t *testing.T) {
	// 创建测试服务器
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusOK)
		w.Write(make([]byte, 100))
	}))
	defer server.Close()
	
	// 创建客户端
	config := http.DefaultImprovedConfig()
	client, err := http.NewImprovedClient(config)
	if err != nil {
		t.Fatalf("创建客户端失败: %v", err)
	}
	
	// 初始统计信息
	initialStats := client.GetStats()
	if initialStats.TotalRequests != 0 {
		t.Errorf("初始总请求数应为0，实际为%d", initialStats.TotalRequests)
	}
	
	// 执行多次下载
	ctx := context.Background()
	tempDir := t.TempDir()
	options := http.DownloadOptions{
		Headers: nil,
	}
	
	for i := 0; i < 3; i++ {
		outputPath := filepath.Join(tempDir, fmt.Sprintf("test%d.txt", i))
		_, err := client.DownloadWithProgress(ctx, server.URL, outputPath, options, nil)
		if err != nil {
			t.Fatalf("下载%d失败: %v", i, err)
		}
	}
	
	// 验证统计信息
	stats := client.GetStats()
	if stats.TotalRequests != 3 {
		t.Errorf("期望总请求数3，实际%d", stats.TotalRequests)
	}
	
	if stats.SuccessfulRequests != 3 {
		t.Errorf("期望成功请求数3，实际%d", stats.SuccessfulRequests)
	}
	
	if stats.TotalBytes != 300 {
		t.Errorf("期望总字节数300，实际%d", stats.TotalBytes)
	}
	
	// 重置统计信息
	client.ResetStats()
	resetStats := client.GetStats()
	if resetStats.TotalRequests != 0 {
		t.Errorf("重置后总请求数应为0，实际为%d", resetStats.TotalRequests)
	}
}

// TestProxyConfiguration 测试代理配置
func TestProxyConfiguration(t *testing.T) {
	// 创建代理测试服务器
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 代理服务器应该看到CONNECT请求或转发请求
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Proxy Response"))
	}))
	defer proxyServer.Close()
	
	// 创建客户端并配置代理
	config := http.DefaultImprovedConfig()
	config.ProxyURL = proxyServer.URL
	client, err := http.NewImprovedClient(config)
	if err != nil {
		t.Fatalf("创建带代理的客户端失败: %v", err)
	}
	
	// 注意：这个测试主要验证代理配置不会导致错误
	// 实际代理行为需要更复杂的测试环境
	t.Log("代理配置测试通过")
}

// TestTLSConfiguration 测试TLS配置
func TestTLSConfiguration(t *testing.T) {
	// 创建HTTPS测试服务器
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "20")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("HTTPS Response"))
	}))
	defer server.Close()
	
	// 测试1: 默认配置（应该失败，因为证书不被信任）
	config1 := http.DefaultImprovedConfig()
	client1, err := http.NewImprovedClient(config1)
	if err != nil {
		t.Fatalf("创建客户端1失败: %v", err)
	}
	
	ctx := context.Background()
	tempDir := t.TempDir()
	outputPath1 := filepath.Join(tempDir, "https1.txt")
	options := http.DownloadOptions{
		Headers: nil,
	}
	
	_, err = client1.DownloadWithProgress(ctx, server.URL, outputPath1, options, nil)
	if err == nil {
		t.Error("期望TLS证书验证失败，但实际成功")
	}
	
	// 测试2: 跳过TLS验证（应该成功）
	config2 := http.DefaultImprovedConfig()
	config2.Insecure = true
	client2, err := http.NewImprovedClient(config2)
	if err != nil {
		t.Fatalf("创建客户端2失败: %v", err)
	}
	
	outputPath2 := filepath.Join(tempDir, "https2.txt")
	result, err := client2.DownloadWithProgress(ctx, server.URL, outputPath2, options, nil)
	if err != nil {
		t.Fatalf("HTTPS下载失败（跳过验证）: %v", err)
	}
	
	if result.BytesRead != 20 {
		t.Errorf("期望下载20字节，实际下载%d字节", result.BytesRead)
	}
}