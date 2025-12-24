package http_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
	
	"aria2go/internal/core"
	"aria2go/internal/protocol/http"
)

// TestNewHTTPTask 测试创建HTTP任务
func TestNewHTTPTask(t *testing.T) {
	// 创建事件通道
	eventCh := make(chan core.Event, 10)
	
	// 创建任务配置
	config := core.TaskConfig{
		URLs:        []string{"http://example.com/test.txt"},
		OutputPath:  "/tmp/test.txt",
		Connections: 3,
		SegmentSize: 1024 * 1024, // 1MB
		MaxSpeed:    1024 * 1024, // 1MB/s
		Options: map[string]interface{}{
			"http": map[string]interface{}{
				"user-agent":  "TestAgent",
				"timeout":     float64(30),
				"max-retries": float64(5),
			},
		},
	}
	
	// 创建HTTP任务
	task, err := http.NewHTTPTask("test-task-1", config, eventCh)
	if err != nil {
		t.Fatalf("创建HTTP任务失败: %v", err)
	}
	
	if task == nil {
		t.Fatal("HTTP任务为nil")
	}
	
	if task.ID() != "test-task-1" {
		t.Errorf("期望任务ID为'test-task-1'，实际为'%s'", task.ID())
	}
	
	// 测试无效URL
	invalidConfig := core.TaskConfig{
		URLs:       []string{"ftp://example.com/test.txt"}, // 非HTTP URL
		OutputPath: "/tmp/test.txt",
	}
	
	_, err = http.NewHTTPTask("test-task-2", invalidConfig, eventCh)
	if err == nil {
		t.Error("期望无效URL错误，但实际成功创建任务")
	}
	
	// 测试无URL
	noURLConfig := core.TaskConfig{
		URLs:       []string{},
		OutputPath: "/tmp/test.txt",
	}
	
	_, err = http.NewHTTPTask("test-task-3", noURLConfig, eventCh)
	if err == nil {
		t.Error("期望无URL错误，但实际成功创建任务")
	}
}

// TestHTTPTaskLifecycle 测试HTTP任务生命周期
func TestHTTPTaskLifecycle(t *testing.T) {
	// 创建测试服务器
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusOK)
		w.Write(make([]byte, 100))
	}))
	defer server.Close()
	
	// 创建事件通道
	eventCh := make(chan core.Event, 10)
	
	// 创建临时目录
	tempDir := t.TempDir()
	outputPath := filepath.Join(tempDir, "lifecycle_test.txt")
	
	// 创建任务配置
	config := core.TaskConfig{
		URLs:        []string{server.URL},
		OutputPath:  outputPath,
		Connections: 1,
		SegmentSize: 50 * 1024, // 50KB
	}
	
	// 创建HTTP任务
	task, err := http.NewHTTPTask("lifecycle-task", config, eventCh)
	if err != nil {
		t.Fatalf("创建HTTP任务失败: %v", err)
	}
	
	// 测试初始状态
	if task.Status() != core.TaskStatusPending {
		t.Errorf("期望初始状态为Pending，实际为%s", task.Status())
	}
	
	// 启动任务
	ctx := context.Background()
	err = task.Start(ctx)
	if err != nil {
		t.Fatalf("启动任务失败: %v", err)
	}
	
	// 等待任务完成或超时
	timeout := time.After(5 * time.Second)
	done := false
	
	for !done {
		select {
		case event := <-eventCh:
			t.Logf("收到事件: %v", event)
			if event.Type == core.EventTypeTaskCompleted {
				done = true
			}
		case <-timeout:
			t.Fatal("任务执行超时")
		}
	}
	
	// 验证任务状态
	if task.Status() != core.TaskStatusCompleted {
		t.Errorf("期望任务状态为Completed，实际为%s", task.Status())
	}
	
	// 获取进度
	progress := task.Progress()
	if progress.TotalBytes != 100 {
		t.Errorf("期望总字节数100，实际%d", progress.TotalBytes)
	}
	
	if progress.DownloadedBytes != 100 {
		t.Errorf("期望已下载字节数100，实际%d", progress.DownloadedBytes)
	}
	
	if progress.Progress != 100.0 {
		t.Errorf("期望进度100%%，实际%f%%", progress.Progress)
	}
}

// TestHTTPTaskPauseResume 测试HTTP任务暂停和恢复
func TestHTTPTaskPauseResume(t *testing.T) {
	// 创建慢速测试服务器
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Length", "1000")
		w.WriteHeader(http.StatusOK)
		
		// 缓慢写入数据
		for i := 0; i < 10; i++ {
			select {
			case <-r.Context().Done():
				// 客户端取消
				return
			default:
				w.Write(make([]byte, 100))
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
				time.Sleep(50 * time.Millisecond)
			}
		}
	}))
	defer server.Close()
	
	// 创建事件通道
	eventCh := make(chan core.Event, 10)
	
	// 创建临时目录
	tempDir := t.TempDir()
	outputPath := filepath.Join(tempDir, "pause_resume_test.txt")
	
	// 创建任务配置
	config := core.TaskConfig{
		URLs:        []string{server.URL},
		OutputPath:  outputPath,
		Connections: 1,
		SegmentSize: 500,
	}
	
	// 创建HTTP任务
	task, err := http.NewHTTPTask("pause-resume-task", config, eventCh)
	if err != nil {
		t.Fatalf("创建HTTP任务失败: %v", err)
	}
	
	// 启动任务
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	err = task.Start(ctx)
	if err != nil {
		t.Fatalf("启动任务失败: %v", err)
	}
	
	// 等待一段时间后暂停
	time.Sleep(200 * time.Millisecond)
	
	// 暂停任务
	err = task.Pause()
	if err != nil {
		t.Fatalf("暂停任务失败: %v", err)
	}
	
	// 验证任务状态
	if task.Status() != core.TaskStatusPaused {
		t.Errorf("期望任务状态为Paused，实际为%s", task.Status())
	}
	
	// 获取暂停时的进度
	pausedProgress := task.Progress()
	t.Logf("暂停时进度: %d/%d 字节", pausedProgress.DownloadedBytes, pausedProgress.TotalBytes)
	
	// 等待一段时间确保任务确实暂停了
	time.Sleep(300 * time.Millisecond)
	
	// 恢复任务
	err = task.Resume()
	if err != nil {
		t.Fatalf("恢复任务失败: %v", err)
	}
	
	// 等待任务完成
	timeout := time.After(5 * time.Second)
	done := false
	
	for !done {
		select {
		case event := <-eventCh:
			t.Logf("收到事件: %v", event)
			if event.Type == core.EventTypeTaskCompleted {
				done = true
			}
		case <-timeout:
			t.Fatal("任务执行超时")
		}
	}
	
	// 验证最终进度
	finalProgress := task.Progress()
	if finalProgress.DownloadedBytes != 1000 {
		t.Errorf("期望最终下载1000字节，实际%d字节", finalProgress.DownloadedBytes)
	}
	
	if finalProgress.Progress != 100.0 {
		t.Errorf("期望最终进度100%%，实际%f%%", finalProgress.Progress)
	}
}

// TestHTTPTaskStop 测试HTTP任务停止
func TestHTTPTaskStop(t *testing.T) {
	// 创建慢速测试服务器
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Length", "10000")
		w.WriteHeader(http.StatusOK)
		
		// 非常缓慢地写入数据
		for i := 0; i < 100; i++ {
			select {
			case <-r.Context().Done():
				// 客户端取消
				return
			default:
				w.Write(make([]byte, 100))
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
				time.Sleep(100 * time.Millisecond)
			}
		}
	}))
	defer server.Close()
	
	// 创建事件通道
	eventCh := make(chan core.Event, 10)
	
	// 创建临时目录
	tempDir := t.TempDir()
	outputPath := filepath.Join(tempDir, "stop_test.txt")
	
	// 创建任务配置
	config := core.TaskConfig{
		URLs:        []string{server.URL},
		OutputPath:  outputPath,
		Connections: 1,
		SegmentSize: 1000,
	}
	
	// 创建HTTP任务
	task, err := http.NewHTTPTask("stop-task", config, eventCh)
	if err != nil {
		t.Fatalf("创建HTTP任务失败: %v", err)
	}
	
	// 启动任务
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	err = task.Start(ctx)
	if err != nil {
		t.Fatalf("启动任务失败: %v", err)
	}
	
	// 等待一段时间后停止
	time.Sleep(300 * time.Millisecond)
	
	// 停止任务
	err = task.Stop()
	if err != nil {
		t.Fatalf("停止任务失败: %v", err)
	}
	
	// 验证任务状态
	if task.Status() != core.TaskStatusStopped {
		t.Errorf("期望任务状态为Stopped，实际为%s", task.Status())
	}
	
	// 获取停止时的进度
	stoppedProgress := task.Progress()
	t.Logf("停止时进度: %d/%d 字节", stoppedProgress.DownloadedBytes, stoppedProgress.TotalBytes)
	
	// 进度应该小于100%
	if stoppedProgress.Progress >= 100.0 {
		t.Errorf("期望进度小于100%%，实际%f%%", stoppedProgress.Progress)
	}
}

// TestHTTPTaskErrorHandling 测试HTTP任务错误处理
func TestHTTPTaskErrorHandling(t *testing.T) {
	// 创建返回错误的测试服务器
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("404 Not Found"))
	}))
	defer server.Close()
	
	// 创建事件通道
	eventCh := make(chan core.Event, 10)
	
	// 创建临时目录
	tempDir := t.TempDir()
	outputPath := filepath.Join(tempDir, "error_test.txt")
	
	// 创建任务配置
	config := core.TaskConfig{
		URLs:        []string{server.URL},
		OutputPath:  outputPath,
		Connections: 1,
		SegmentSize: 1024,
	}
	
	// 创建HTTP任务
	task, err := http.NewHTTPTask("error-task", config, eventCh)
	if err != nil {
		t.Fatalf("创建HTTP任务失败: %v", err)
	}
	
	// 启动任务
	ctx := context.Background()
	err = task.Start(ctx)
	if err != nil {
		t.Fatalf("启动任务失败: %v", err)
	}
	
	// 等待错误事件
	timeout := time.After(5 * time.Second)
	errorReceived := false
	
	for !errorReceived {
		select {
		case event := <-eventCh:
			t.Logf("收到事件: %v", event)
			if event.Type == core.EventTypeTaskError {
				errorReceived = true
			}
		case <-timeout:
			t.Fatal("等待错误事件超时")
		}
	}
	
	// 验证任务状态
	if task.Status() != core.TaskStatusError {
		t.Errorf("期望任务状态为Error，实际为%s", task.Status())
	}
	
	// 验证错误信息
	if task.Error() == nil {
		t.Error("期望任务有错误信息，但实际为nil")
	} else {
		t.Logf("任务错误: %v", task.Error())
	}
}

// TestHTTPTaskSegmentedDownload 测试HTTP分段下载
func TestHTTPTaskSegmentedDownload(t *testing.T) {
	// 创建支持范围请求的测试服务器
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		content := make([]byte, 1000)
		for i := range content {
			content[i] = byte(i % 256)
		}
		
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Accept-Ranges", "bytes")
		
		// 处理范围请求
		rangeHeader := r.Header.Get("Range")
		if rangeHeader != "" {
			// 简化处理：只支持单个范围
			var start, end int64
			fmt.Sscanf(rangeHeader, "bytes=%d-%d", &start, &end)
			
			if end == 0 {
				end = 999
			}
			
			if start < 0 || start > 999 || end < start || end > 999 {
				w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
				return
			}
			
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/1000", start, end))
			w.Header().Set("Content-Length", fmt.Sprintf("%d", end-start+1))
			w.WriteHeader(http.StatusPartialContent)
			w.Write(content[start : end+1])
		} else {
			// 完整文件请求
			w.Header().Set("Content-Length", "1000")
			w.WriteHeader(http.StatusOK)
			w.Write(content)
		}
	}))
	defer server.Close()
	
	// 创建事件通道
	eventCh := make(chan core.Event, 10)
	
	// 创建临时目录
	tempDir := t.TempDir()
	outputPath := filepath.Join(tempDir, "segmented_test.bin")
	
	// 创建任务配置（使用分段下载）
	config := core.TaskConfig{
		URLs:        []string{server.URL},
		OutputPath:  outputPath,
		Connections: 4,              // 4个连接
		SegmentSize: 250,            // 每个分段250字节
	}
	
	// 创建HTTP任务
	task, err := http.NewHTTPTask("segmented-task", config, eventCh)
	if err != nil {
		t.Fatalf("创建HTTP任务失败: %v", err)
	}
	
	// 启动任务
	ctx := context.Background()
	err = task.Start(ctx)
	if err != nil {
		t.Fatalf("启动任务失败: %v", err)
	}
	
	// 等待任务完成
	timeout := time.After(5 * time.Second)
	done := false
	
	for !done {
		select {
		case event := <-eventCh:
			t.Logf("收到事件: %v", event)
			if event.Type == core.EventTypeTaskCompleted {
				done = true
			}
		case <-timeout:
			t.Fatal("任务执行超时")
		}
	}
	
	// 验证任务状态
	if task.Status() != core.TaskStatusCompleted {
		t.Errorf("期望任务状态为Completed，实际为%s", task.Status())
	}
	
	// 验证进度
	progress := task.Progress()
	if progress.TotalBytes != 1000 {
		t.Errorf("期望总字节数1000，实际%d", progress.TotalBytes)
	}
	
	if progress.DownloadedBytes != 1000 {
		t.Errorf("期望已下载字节数1000，实际%d", progress.DownloadedBytes)
	}
	
	if progress.Progress != 100.0 {
		t.Errorf("期望进度100%%，实际%f%%", progress.Progress)
	}
}

// TestHTTPTaskConfigValidation 测试HTTP任务配置验证
func TestHTTPTaskConfigValidation(t *testing.T) {
	testCases := []struct {
		name        string
		config      core.TaskConfig
		expectError bool
	}{
		{
			name: "有效配置",
			config: core.TaskConfig{
				URLs:       []string{"http://example.com/file.txt"},
				OutputPath: "/tmp/file.txt",
			},
			expectError: false,
		},
		{
			name: "HTTPS URL",
			config: core.TaskConfig{
				URLs:       []string{"https://example.com/file.txt"},
				OutputPath: "/tmp/file.txt",
			},
			expectError: false,
		},
		{
			name: "多个URL（只取第一个）",
			config: core.TaskConfig{
				URLs:       []string{"http://example.com/file1.txt", "http://example.com/file2.txt"},
				OutputPath: "/tmp/file.txt",
			},
			expectError: false,
		},
		{
			name: "无效协议",
			config: core.TaskConfig{
				URLs:       []string{"ftp://example.com/file.txt"},
				OutputPath: "/tmp/file.txt",
			},
			expectError: true,
		},
		{
			name: "无URL",
			config: core.TaskConfig{
				URLs:       []string{},
				OutputPath: "/tmp/file.txt",
			},
			expectError: true,
		},
		{
			name: "空URL",
			config: core.TaskConfig{
				URLs:       []string{""},
				OutputPath: "/tmp/file.txt",
			},
			expectError: true,
		},
		{
			name: "无效URL格式",
			config: core.TaskConfig{
				URLs:       []string{"not-a-url"},
				OutputPath: "/tmp/file.txt",
			},
			expectError: true,
		},
	}
	
	eventCh := make(chan core.Event, 10)
	
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			taskID := fmt.Sprintf("test-%s", tc.name)
			_, err := http.NewHTTPTask(taskID, tc.config, eventCh)
			
			if tc.expectError && err == nil {
				t.Errorf("期望错误，但实际成功创建任务")
			} else if !tc.expectError && err != nil {
				t.Errorf("期望成功，但实际错误: %v", err)
			}
		})
	}
}

// TestHTTPTaskProgressUpdates 测试HTTP任务进度更新
func TestHTTPTaskProgressUpdates(t *testing.T) {
	// 创建测试服务器
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Length", "500")
		w.WriteHeader(http.StatusOK)
		
		// 分块写入，以便测试进度更新
		for i := 0; i < 5; i++ {
			w.Write(make([]byte, 100))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			time.Sleep(50 * time.Millisecond)
		}
	}))
	defer server.Close()
	
	// 创建事件通道
	eventCh := make(chan core.Event, 100)
	
	// 创建临时目录
	tempDir := t.TempDir()
	outputPath := filepath.Join(tempDir, "progress_test.txt")
	
	// 创建任务配置
	config := core.TaskConfig{
		URLs:        []string{server.URL},
		OutputPath:  outputPath,
		Connections: 1,
		SegmentSize: 1024,
	}
	
	// 创建HTTP任务
	task, err := http.NewHTTPTask("progress-task", config, eventCh)
	if err != nil {
		t.Fatalf("创建HTTP任务失败: %v", err)
	}
	
	// 收集进度事件
	var progressEvents []core.Event
	go func() {
		for event := range eventCh {
			if event.Type == core.EventTypeTaskProgress {
				progressEvents = append(progressEvents, event)
				t.Logf("进度事件: 已下载%d/%d字节 (%.1f%%)", 
					event.Data.(core.TaskProgress).DownloadedBytes,
					event.Data.(core.TaskProgress).TotalBytes,
					event.Data.(core.TaskProgress).Progress)
			}
		}
	}()
	
	// 启动任务
	ctx := context.Background()
	err = task.Start(ctx)
	if err != nil {
		t.Fatalf("启动任务失败: %v", err)
	}
	
	// 等待任务完成
	timeout := time.After(5 * time.Second)
	done := false
	
	for !done {
		select {
		case event := <-eventCh:
			if event.Type == core.EventTypeTaskCompleted {
				done = true
			}
		case <-timeout:
			t.Fatal("任务执行超时")
		}
	}
	
	// 关闭事件通道（给goroutine时间处理）
	time.Sleep(100 * time.Millisecond)
	
	// 验证进度事件数量
	if len(progressEvents) == 0 {
		t.Error("期望收到进度事件，但实际没有收到")
	} else {
		t.Logf("收到%d个进度事件", len(progressEvents))
	}
	
	// 验证最终进度
	finalProgress := task.Progress()
	if finalProgress.DownloadedBytes != 500 {
		t.Errorf("期望最终下载500字节，实际%d字节", finalProgress.DownloadedBytes)
	}
	
	if finalProgress.Progress != 100.0 {
		t.Errorf("期望最终进度100%%，实际%f%%", finalProgress.Progress)
	}
}