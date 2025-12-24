package http_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
	
	"aria2go/internal/core"
	"aria2go/internal/protocol/http"
)

// TestHTTPIntegration 测试HTTP下载集成
func TestHTTPIntegration(t *testing.T) {
	// 创建测试服务器
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 生成测试数据
		data := make([]byte, 1024*1024) // 1MB
		for i := range data {
			data[i] = byte(i % 256)
		}
		
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
		w.Header().Set("Accept-Ranges", "bytes")
		
		// 处理范围请求
		rangeHeader := r.Header.Get("Range")
		if rangeHeader != "" {
			var start, end int64
			fmt.Sscanf(rangeHeader, "bytes=%d-%d", &start, &end)
			
			if end == 0 {
				end = int64(len(data) - 1)
			}
			
			if start < 0 || start >= int64(len(data)) || end < start || end >= int64(len(data)) {
				w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
				return
			}
			
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(data)))
			w.Header().Set("Content-Length", fmt.Sprintf("%d", end-start+1))
			w.WriteHeader(http.StatusPartialContent)
			w.Write(data[start : end+1])
		} else {
			// 完整文件请求
			w.WriteHeader(http.StatusOK)
			w.Write(data)
		}
	}))
	defer server.Close()
	
	// 创建临时目录
	tempDir := t.TempDir()
	
	// 测试用例
	testCases := []struct {
		name        string
		connections int
		segmentSize int64
		expectError bool
	}{
		{
			name:        "单连接下载",
			connections: 1,
			segmentSize: 0, // 不分段
			expectError: false,
		},
		{
			name:        "2连接分段下载",
			connections: 2,
			segmentSize: 512 * 1024, // 512KB分段
			expectError: false,
		},
		{
			name:        "4连接分段下载",
			connections: 4,
			segmentSize: 256 * 1024, // 256KB分段
			expectError: false,
		},
		{
			name:        "8连接分段下载",
			connections: 8,
			segmentSize: 128 * 1024, // 128KB分段
			expectError: false,
		},
	}
	
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// 创建事件通道
			eventCh := make(chan core.Event, 100)
			
			// 输出文件路径
			outputPath := filepath.Join(tempDir, fmt.Sprintf("integration_%s.bin", tc.name))
			
			// 创建任务配置
			config := core.TaskConfig{
				URLs:        []string{server.URL},
				OutputPath:  outputPath,
				Connections: tc.connections,
				SegmentSize: tc.segmentSize,
			}
			
			// 创建HTTP任务
			task, err := http.NewHTTPTask(fmt.Sprintf("integration-%s", tc.name), config, eventCh)
			if err != nil {
				t.Fatalf("创建HTTP任务失败: %v", err)
			}
			
			// 收集事件
			var events []core.Event
			var wg sync.WaitGroup
			wg.Add(1)
			
			go func() {
				defer wg.Done()
				for event := range eventCh {
					events = append(events, event)
					switch event.Type {
					case core.EventTypeTaskProgress:
						progress := event.Data.(core.TaskProgress)
						t.Logf("[%s] 进度: %.1f%% (%d/%d) 速度: %d B/s", 
							tc.name, progress.Progress, 
							progress.DownloadedBytes, progress.TotalBytes,
							progress.DownloadSpeed)
					case core.EventTypeTaskCompleted:
						t.Logf("[%s] 任务完成", tc.name)
					case core.EventTypeTaskError:
						t.Logf("[%s] 任务错误: %v", tc.name, event.Data)
					}
				}
			}()
			
			// 启动任务
			ctx := context.Background()
			startTime := time.Now()
			err = task.Start(ctx)
			if err != nil {
				t.Fatalf("启动任务失败: %v", err)
			}
			
			// 等待任务完成
			timeout := time.After(30 * time.Second)
			done := false
			
			for !done {
				select {
				case <-timeout:
					t.Fatalf("[%s] 任务执行超时", tc.name)
				default:
					status := task.Status()
					if status == core.TaskStatusCompleted || status == core.TaskStatusError || status == core.TaskStatusStopped {
						done = true
					}
					time.Sleep(100 * time.Millisecond)
				}
			}
			
			// 关闭事件通道并等待goroutine完成
			close(eventCh)
			wg.Wait()
			
			elapsed := time.Since(startTime)
			t.Logf("[%s] 总耗时: %v", tc.name, elapsed)
			
			// 验证结果
			if tc.expectError {
				if task.Status() != core.TaskStatusError {
					t.Errorf("[%s] 期望任务状态为Error，实际为%s", tc.name, task.Status())
				}
			} else {
				if task.Status() != core.TaskStatusCompleted {
					t.Errorf("[%s] 期望任务状态为Completed，实际为%s", tc.name, task.Status())
				}
				
				// 验证文件大小
				fileInfo, err := os.Stat(outputPath)
				if err != nil {
					t.Fatalf("[%s] 获取文件信息失败: %v", tc.name, err)
				}
				
				expectedSize := int64(1024 * 1024) // 1MB
				if fileInfo.Size() != expectedSize {
					t.Errorf("[%s] 期望文件大小%d字节，实际%d字节", tc.name, expectedSize, fileInfo.Size())
				}
				
				// 验证进度
				progress := task.Progress()
				if progress.DownloadedBytes != expectedSize {
					t.Errorf("[%s] 期望下载%d字节，实际%d字节", tc.name, expectedSize, progress.DownloadedBytes)
				}
				
				if progress.Progress != 100.0 {
					t.Errorf("[%s] 期望进度100%%，实际%f%%", tc.name, progress.Progress)
				}
				
				// 计算下载速度
				if elapsed.Seconds() > 0 {
					speed := float64(expectedSize) / elapsed.Seconds()
					t.Logf("[%s] 平均下载速度: %.2f KB/s", tc.name, speed/1024)
				}
			}
			
			// 验证事件序列
			t.Logf("[%s] 收到%d个事件", tc.name, len(events))
			
			// 检查是否有进度事件
			hasProgressEvents := false
			for _, event := range events {
				if event.Type == core.EventTypeTaskProgress {
					hasProgressEvents = true
					break
				}
			}
			
			if !hasProgressEvents {
				t.Errorf("[%s] 期望收到进度事件，但实际没有收到", tc.name)
			}
		})
	}
}

// TestHTTPConcurrentTasks 测试并发HTTP任务
func TestHTTPConcurrentTasks(t *testing.T) {
	// 创建测试服务器
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 每个文件100KB
		data := make([]byte, 100*1024)
		for i := range data {
			data[i] = byte(i % 256)
		}
		
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
		w.WriteHeader(http.StatusOK)
		w.Write(data)
	}))
	defer server.Close()
	
	// 创建临时目录
	tempDir := t.TempDir()
	
	// 并发任务数量
	numTasks := 5
	
	// 创建任务并启动
	var tasks []core.Task
	var eventsChannels []chan core.Event
	var wg sync.WaitGroup
	
	// 启动所有任务
	for i := 0; i < numTasks; i++ {
		// 创建事件通道
		eventCh := make(chan core.Event, 10)
		eventsChannels = append(eventsChannels, eventCh)
		
		// 输出文件路径
		outputPath := filepath.Join(tempDir, fmt.Sprintf("concurrent_%d.bin", i))
		
		// 创建任务配置
		config := core.TaskConfig{
			URLs:        []string{server.URL},
			OutputPath:  outputPath,
			Connections: 2,
			SegmentSize: 50 * 1024, // 50KB分段
		}
		
		// 创建HTTP任务
		task, err := http.NewHTTPTask(fmt.Sprintf("concurrent-task-%d", i), config, eventCh)
		if err != nil {
			t.Fatalf("创建任务%d失败: %v", i, err)
		}
		
		tasks = append(tasks, task)
		
		// 启动任务
		ctx := context.Background()
		err = task.Start(ctx)
		if err != nil {
			t.Fatalf("启动任务%d失败: %v", i, err)
		}
		
		t.Logf("启动任务%d: %s", i, task.ID())
	}
	
	// 收集所有任务的事件
	allEvents := make(chan struct {
		taskID string
		event  core.Event
	}, 100)
	
	for i, eventCh := range eventsChannels {
		wg.Add(1)
		go func(taskID string, ch <-chan core.Event) {
			defer wg.Done()
			for event := range ch {
				allEvents <- struct {
					taskID string
					event  core.Event
				}{
					taskID: taskID,
					event:  event,
				}
			}
		}(tasks[i].ID(), eventCh)
	}
	
	// 等待所有任务完成
	timeout := time.After(30 * time.Second)
	completedTasks := 0
	
	for completedTasks < numTasks {
		select {
		case <-timeout:
			t.Fatal("并发任务执行超时")
		case evt := <-allEvents:
			if evt.event.Type == core.EventTypeTaskCompleted {
				completedTasks++
				t.Logf("任务完成: %s", evt.taskID)
			} else if evt.event.Type == core.EventTypeTaskError {
				t.Errorf("任务错误: %s - %v", evt.taskID, evt.event.Data)
				completedTasks++
			}
		default:
			time.Sleep(100 * time.Millisecond)
		}
	}
	
	// 关闭所有事件通道
	for _, eventCh := range eventsChannels {
		close(eventCh)
	}
	
	// 等待所有事件收集goroutine完成
	wg.Wait()
	close(allEvents)
	
	// 验证所有任务状态
	for i, task := range tasks {
		if task.Status() != core.TaskStatusCompleted {
			t.Errorf("任务%d状态应为Completed，实际为%s", i, task.Status())
		}
		
		// 验证文件大小
		outputPath := filepath.Join(tempDir, fmt.Sprintf("concurrent_%d.bin", i))
		fileInfo, err := os.Stat(outputPath)
		if err != nil {
			t.Errorf("获取任务%d文件信息失败: %v", i, err)
			continue
		}
		
		expectedSize := int64(100 * 1024) // 100KB
		if fileInfo.Size() != expectedSize {
			t.Errorf("任务%d期望文件大小%d字节，实际%d字节", i, expectedSize, fileInfo.Size())
		}
		
		// 验证进度
		progress := task.Progress()
		if progress.DownloadedBytes != expectedSize {
			t.Errorf("任务%d期望下载%d字节，实际%d字节", i, expectedSize, progress.DownloadedBytes)
		}
		
		if progress.Progress != 100.0 {
			t.Errorf("任务%d期望进度100%%，实际%f%%", i, progress.Progress)
		}
	}
	
	t.Logf("所有%d个并发任务完成", numTasks)
}

// TestHTTPResumeDownload 测试HTTP断点续传
func TestHTTPResumeDownload(t *testing.T) {
	// 创建测试服务器
	downloadCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downloadCount++
		
		// 生成测试数据（500KB）
		data := make([]byte, 500*1024)
		for i := range data {
			data[i] = byte(i % 256)
		}
		
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
		w.Header().Set("Accept-Ranges", "bytes")
		
		// 第一次下载时模拟中断
		if downloadCount == 1 {
			// 只发送前200KB
			w.WriteHeader(http.StatusOK)
			w.Write(data[:200*1024])
			// 不关闭连接，模拟网络中断
			return
		}
		
		// 第二次下载（续传）
		rangeHeader := r.Header.Get("Range")
		if rangeHeader != "" {
			var start int64
			fmt.Sscanf(rangeHeader, "bytes=%d-", &start)
			
			if start < 0 || start >= int64(len(data)) {
				w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
				return
			}
			
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(data)-1, len(data)))
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)-int(start)))
			w.WriteHeader(http.StatusPartialContent)
			w.Write(data[start:])
		} else {
			// 完整文件请求
			w.WriteHeader(http.StatusOK)
			w.Write(data)
		}
	}))
	defer server.Close()
	
	// 创建临时目录
	tempDir := t.TempDir()
	outputPath := filepath.Join(tempDir, "resume_test.bin")
	
	// 第一次下载（会中断）
	t.Log("第一次下载（模拟中断）")
	eventCh1 := make(chan core.Event, 10)
	
	config1 := core.TaskConfig{
		URLs:        []string{server.URL},
		OutputPath:  outputPath,
		Connections: 1,
		SegmentSize: 100 * 1024,
	}
	
	task1, err := http.NewHTTPTask("resume-task-1", config1, eventCh1)
	if err != nil {
		t.Fatalf("创建任务1失败: %v", err)
	}
	
	ctx1, cancel1 := context.WithCancel(context.Background())
	err = task1.Start(ctx1)
	if err != nil {
		t.Fatalf("启动任务1失败: %v", err)
	}
	
	// 等待下载部分数据
	time.Sleep(1 * time.Second)
	
	// 取消下载（模拟中断）
	cancel1()
	
	// 等待任务停止
	time.Sleep(500 * time.Millisecond)
	
	if task1.Status() != core.TaskStatusStopped {
		t.Errorf("任务1状态应为Stopped，实际为%s", task1.Status())
	}
	
	// 检查已下载的数据
	fileInfo1, err := os.Stat(outputPath)
	if err != nil {
		t.Fatalf("获取文件信息失败: %v", err)
	}
	
	t.Logf("第一次下载后文件大小: %d 字节", fileInfo1.Size())
	
	// 第二次下载（续传）
	t.Log("第二次下载（续传）")
	eventCh2 := make(chan core.Event, 10)
	
	config2 := core.TaskConfig{
		URLs:        []string{server.URL},
		OutputPath:  outputPath,
		Connections: 1,
		SegmentSize: 100 * 1024,
	}
	
	task2, err := http.NewHTTPTask("resume-task-2", config2, eventCh2)
	if err != nil {
		t.Fatalf("创建任务2失败: %v", err)
	}
	
	ctx2 := context.Background()
	err = task2.Start(ctx2)
	if err != nil {
		t.Fatalf("启动任务2失败: %v", err)
	}
	
	// 等待任务完成
	timeout := time.After(10 * time.Second)
	done := false
	
	for !done {
		select {
		case event := <-eventCh2:
			if event.Type == core.EventTypeTaskCompleted {
				done = true
			}
		case <-timeout:
			t.Fatal("续传任务执行超时")
		}
	}
	
	// 验证最终文件大小
	fileInfo2, err := os.Stat(outputPath)
	if err != nil {
		t.Fatalf("获取最终文件信息失败: %v", err)
	}
	
	expectedSize := int64(500 * 1024) // 500KB
	if fileInfo2.Size() != expectedSize {
		t.Errorf("期望最终文件大小%d字节，实际%d字节", expectedSize, fileInfo2.Size())
	}
	
	// 验证任务状态
	if task2.Status() != core.TaskStatusCompleted {
		t.Errorf("任务2状态应为Completed，实际为%s", task2.Status())
	}
	
	// 验证进度
	progress := task2.Progress()
	if progress.DownloadedBytes != expectedSize {
		t.Errorf("期望下载%d字节，实际%d字节", expectedSize, progress.DownloadedBytes)
	}
	
	if progress.Progress != 100.0 {
		t.Errorf("期望进度100%%，实际%f%%", progress.Progress)
	}
	
	t.Logf("断点续传测试完成，总下载次数: %d", downloadCount)
}

// TestHTTPErrorRecovery 测试HTTP错误恢复
func TestHTTPErrorRecovery(t *testing.T) {
	// 创建测试服务器，模拟各种错误
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		
		switch requestCount {
		case 1:
			// 第一次请求：服务器错误
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("Internal Server Error"))
		case 2:
			// 第二次请求：服务不可用
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("Service Unavailable"))
		case 3:
			// 第三次请求：成功
			data := []byte("Success after retries")
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
			w.WriteHeader(http.StatusOK)
			w.Write(data)
		default:
			// 其他请求：成功
			data := []byte("Additional data")
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
			w.WriteHeader(http.StatusOK)
			w.Write(data)
		}
	}))
	defer server.Close()
	
	// 创建临时目录
	tempDir := t.TempDir()
	outputPath := filepath.Join(tempDir, "recovery_test.txt")
	
	// 创建事件通道
	eventCh := make(chan core.Event, 10)
	
	// 创建任务配置（启用重试）
	config := core.TaskConfig{
		URLs:        []string{server.URL},
		OutputPath:  outputPath,
		Connections: 1,
		SegmentSize: 1024,
		Options: map[string]interface{}{
			"http": map[string]interface{}{
				"max-retries":   float64(5),
				"retry-wait":    float64(100), // 100ms
				"timeout":       float64(5),
			},
		},
	}
	
	// 创建HTTP任务
	task, err := http.NewHTTPTask("recovery-task", config, eventCh)
	if err != nil {
		t.Fatalf("创建任务失败: %v", err)
	}
	
	// 启动任务
	ctx := context.Background()
	err = task.Start(ctx)
	if err != nil {
		t.Fatalf("启动任务失败: %v", err)
	}
	
	// 等待任务完成
	timeout := time.After(10 * time.Second)
	done := false
	
	for !done {
		select {
		case event := <-eventCh:
			t.Logf("收到事件: %v", event)
			if event.Type == core.EventTypeTaskCompleted {
				done = true
			} else if event.Type == core.EventTypeTaskError {
				t.Fatalf("任务错误: %v", event.Data)
			}
		case <-timeout:
			t.Fatal("任务执行超时")
		}
	}
	
	// 验证任务状态
	if task.Status() != core.TaskStatusCompleted {
		t.Errorf("期望任务状态为Completed，实际为%s", task.Status())
	}
	
	// 验证请求次数（应该重试了）
	if requestCount < 3 {
		t.Errorf("期望至少3次请求（包括重试），实际%d次", requestCount)
	}
	
	t.Logf("错误恢复测试完成，总请求次数: %d", requestCount)
}

// TestHTTPPerformance 测试HTTP性能
func TestHTTPPerformance(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过性能测试（短模式）")
	}
	
	// 创建大文件测试服务器（10MB）
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 生成10MB测试数据
		const fileSize = 10 * 1024 * 1024
		
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", fmt.Sprintf("%d", fileSize))
		w.Header().Set("Accept-Ranges", "bytes")
		
		// 处理范围请求
		rangeHeader := r.Header.Get("Range")
		if rangeHeader != "" {
			var start, end int64
			fmt.Sscanf(rangeHeader, "bytes=%d-%d", &start, &end)
			
			if end == 0 {
				end = fileSize - 1
			}
			
			chunkSize := end - start + 1
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, fileSize))
			w.Header().Set("Content-Length", fmt.Sprintf("%d", chunkSize))
			w.WriteHeader(http.StatusPartialContent)
			
			// 生成数据块
			chunk := make([]byte, chunkSize)
			for i := range chunk {
				chunk[i] = byte((int64(i) + start) % 256)
			}
			w.Write(chunk)
		} else {
			// 完整文件请求（流式写入）
			w.WriteHeader(http.StatusOK)
			
			// 分块写入以减少内存使用
			const chunkSize = 64 * 1024 // 64KB
			for written := 0; written < fileSize; written += chunkSize {
				chunk := make([]byte, chunkSize)
				for i := range chunk {
					chunk[i] = byte((written + i) % 256)
				}
				w.Write(chunk)
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}
		}
	}))
	defer server.Close()
	
	// 测试不同配置的性能
	testCases := []struct {
		name        string
		connections int
		segmentSize int64
	}{
		{
			name:        "单连接",
			connections: 1,
			segmentSize: 0,
		},
		{
			name:        "4连接-1MB分段",
			connections: 4,
			segmentSize: 1024 * 1024,
		},
		{
			name:        "8连接-512KB分段",
			connections: 8,
			segmentSize: 512 * 1024,
		},
	}
	
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// 创建临时目录
			tempDir := t.TempDir()
			outputPath := filepath.Join(tempDir, fmt.Sprintf("perf_%s.bin", tc.name))
			
			// 创建事件通道
			eventCh := make(chan core.Event, 100)
			
			// 创建任务配置
			config := core.TaskConfig{
				URLs:        []string{server.URL},
				OutputPath:  outputPath,
				Connections: tc.connections,
				SegmentSize: tc.segmentSize,
			}
			
			// 创建HTTP任务
			task, err := http.NewHTTPTask(fmt.Sprintf("perf-%s", tc.name), config, eventCh)
			if err != nil {
				t.Fatalf("创建任务失败: %v", err)
			}
			
			// 启动任务并计时
			ctx := context.Background()
			startTime := time.Now()
			
			err = task.Start(ctx)
			if err != nil {
				t.Fatalf("启动任务失败: %v", err)
			}
			
			// 等待任务完成
			timeout := time.After(60 * time.Second) // 给大文件更多时间
			done := false
			
			for !done {
				select {
				case event := <-eventCh:
					if event.Type == core.EventTypeTaskCompleted {
						done = true
					} else if event.Type == core.EventTypeTaskError {
						t.Fatalf("任务错误: %v", event.Data)
					}
				case <-timeout:
					t.Fatalf("任务执行超时")
				}
			}
			
			elapsed := time.Since(startTime)
			
			// 验证文件大小
			fileInfo, err := os.Stat(outputPath)
			if err != nil {
				t.Fatalf("获取文件信息失败: %v", err)
			}
			
			const expectedSize = 10 * 1024 * 1024 // 10MB
			if fileInfo.Size() != expectedSize {
				t.Errorf("期望文件大小%d字节，实际%d字节", expectedSize, fileInfo.Size())
			}
			
			// 计算性能指标
			downloadSpeed := float64(expectedSize) / elapsed.Seconds()
			t.Logf("%s - 耗时: %v, 速度: %.2f MB/s (%.2f Mbps)", 
				tc.name, elapsed, downloadSpeed/(1024*1024), downloadSpeed*8/(1024*1024))
			
			// 验证任务状态
			if task.Status() != core.TaskStatusCompleted {
				t.Errorf("期望任务状态为Completed，实际为%s", task.Status())
			}
		})
	}
}