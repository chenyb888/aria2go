package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
	
	"aria2go/internal/core"
	"aria2go/internal/protocol/http"
)

func main() {
	// 测试不同的下载配置
	testConfigs := []struct {
		name        string
		connections int
		segmentSize int64
	}{
		{"单连接", 1, 10 * 1024 * 1024},
		{"4连接-10MB分段", 4, 10 * 1024 * 1024},
		{"8连接-5MB分段", 8, 5 * 1024 * 1024},
		{"16连接-2MB分段", 16, 2 * 1024 * 1024},
	}
	
	// 使用一个可访问的小文件进行测试
	testURLs := []string{
		"https://httpbin.org/bytes/1048576", // 1MB测试文件
		"https://httpbin.org/bytes/5242880", // 5MB测试文件
	}
	
	outputDir := "/tmp/performance_test"
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		log.Fatalf("创建输出目录失败: %v", err)
	}
	
	fmt.Println("=== 下载性能分析测试 ===")
	fmt.Println("测试不同的连接数和分段大小对下载性能的影响")
	fmt.Println("=" * 60)
	
	results := []performanceResult{}
	
	for _, testURL := range testURLs {
		fmt.Printf("\n测试URL: %s\n", testURL)
		fmt.Println("-" * 40)
		
		for _, config := range testConfigs {
			fmt.Printf("\n配置: %s\n", config.name)
			
			// 创建唯一的输出文件名
			outputFile := filepath.Join(outputDir, fmt.Sprintf("test_%s_%d_%d.tmp", 
				filepath.Base(testURL), config.connections, config.segmentSize))
			
			// 运行性能测试
			result := runPerformanceTest(testURL, outputFile, config.connections, config.segmentSize)
			results = append(results, result)
			
			// 显示结果
			fmt.Printf("  状态: %s\n", result.status)
			if result.duration > 0 {
				fmt.Printf("  耗时: %v\n", result.duration)
				if result.fileSize > 0 {
					speedMBps := float64(result.fileSize) / result.duration.Seconds() / (1024 * 1024)
					fmt.Printf("  文件大小: %.2f MB\n", float64(result.fileSize)/(1024*1024))
					fmt.Printf("  平均速度: %.2f MB/s\n", speedMBps)
					fmt.Printf("  效率: %.1f%%\n", result.efficiency)
				}
			}
			
			// 清理测试文件
			os.Remove(outputFile)
		}
	}
	
	// 生成性能分析报告
	fmt.Println("\n" + "=" * 60)
	fmt.Println("性能分析总结:")
	fmt.Println("=" * 60)
	
	for _, result := range results {
		if result.status == "成功" && result.duration > 0 {
			speedMBps := float64(result.fileSize) / result.duration.Seconds() / (1024 * 1024)
			fmt.Printf("%s | 连接数: %d | 分段: %.1fMB | 速度: %.2f MB/s | 效率: %.1f%%\n",
				result.url, result.connections, float64(result.segmentSize)/(1024*1024),
				speedMBps, result.efficiency)
		}
	}
	
	// 清理目录
	os.RemoveAll(outputDir)
	
	fmt.Println("\n测试完成!")
}

type performanceResult struct {
	url         string
	connections int
	segmentSize int64
	status      string
	duration    time.Duration
	fileSize    int64
	efficiency  float64 // 效率百分比（实际速度/理论最大速度）
}

func runPerformanceTest(url, outputPath string, connections int, segmentSize int64) performanceResult {
	result := performanceResult{
		url:         url,
		connections: connections,
		segmentSize: segmentSize,
		status:      "失败",
	}
	
	// 创建事件通道
	eventCh := make(chan core.Event, 100)
	
	// 创建任务配置
	config := core.TaskConfig{
		URLs:        []string{url},
		OutputPath:  outputPath,
		Connections: connections,
		SegmentSize: segmentSize,
		Options: map[string]interface{}{
			"http": map[string]interface{}{
				"user-agent":  "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36",
				"timeout":     float64(30),
				"max-retries": float64(3),
			},
		},
	}
	
	// 创建HTTP任务
	task, err := http.NewHTTPTask("performance-test", config, eventCh)
	if err != nil {
		result.status = fmt.Sprintf("创建任务失败: %v", err)
		return result
	}
	
	// 启动事件处理goroutine
	progressUpdates := make(chan core.TaskProgress, 10)
	go func() {
		for event := range eventCh {
			if event.Type == core.EventTypeTaskProgress {
				progressUpdates <- event.Data.(core.TaskProgress)
			}
		}
	}()
	
	// 启动任务并计时
	startTime := time.Now()
	
	err = task.Start(nil)
	if err != nil {
		result.status = fmt.Sprintf("启动任务失败: %v", err)
		close(eventCh)
		return result
	}
	
	// 等待任务完成
	timeout := time.After(2 * time.Minute)
	completed := false
	
	for !completed {
		select {
		case <-timeout:
			result.status = "超时"
			completed = true
		default:
			status := task.Status()
			if status.State == core.TaskStateCompleted {
				result.status = "成功"
				completed = true
			} else if status.State == core.TaskStateError || status.State == core.TaskStateStopped {
				result.status = fmt.Sprintf("失败: %v", status.Error)
				completed = true
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	
	result.duration = time.Since(startTime)
	
	// 获取文件信息
	if fi, err := os.Stat(outputPath); err == nil {
		result.fileSize = fi.Size()
	}
	
	// 计算效率（简化版）
	// 假设理论最大速度为10 MB/s（80 Mbps）
	theoreticalMaxSpeed := 10.0 // MB/s
	if result.duration > 0 && result.fileSize > 0 {
		actualSpeed := float64(result.fileSize) / result.duration.Seconds() / (1024 * 1024)
		result.efficiency = (actualSpeed / theoreticalMaxSpeed) * 100
		if result.efficiency > 100 {
			result.efficiency = 100
		}
	}
	
	// 关闭通道
	close(eventCh)
	close(progressUpdates)
	
	return result
}