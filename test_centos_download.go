package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"
	
	"aria2go/internal/core"
	"aria2go/internal/protocol/http"
)

func main() {
	// CentOS ISO下载链接 - 使用腾讯云镜像
	centosURL := "https://mirrors.cloud.tencent.com/centos/8/isos/x86_64/CentOS-8.5.2111-x86_64-boot.iso"
	
	// 预期的SHA256值
	expectedSHA256 := "9602c69c52d93f51295c0199af395ca0edbe35e36506e32b8e749ce6c8f5b60a"
	
	// 创建输出目录
	outputDir := "/tmp/centos_test_tencent"
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		log.Fatalf("创建输出目录失败: %v", err)
	}
	
	outputPath := filepath.Join(outputDir, "CentOS-8.5.2111-x86_64-boot.iso")
	
	// 创建事件通道
	eventCh := make(chan core.Event, 100)
	
	// 创建任务配置
	config := core.TaskConfig{
		URLs:        []string{centosURL},
		OutputPath:  outputPath,
		Connections: 4,              // 4个连接
		SegmentSize: 10 * 1024 * 1024, // 10MB分段
		Options: map[string]interface{}{
			"http": map[string]interface{}{
				"user-agent":  "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
				"timeout":     float64(30),
				"max-retries": float64(5),
			},
		},
	}
	
	// 创建HTTP任务
	task, err := http.NewHTTPTask("centos-download-test", config, eventCh)
	if err != nil {
		log.Fatalf("创建HTTP任务失败: %v", err)
	}
	
	fmt.Printf("开始下载CentOS ISO文件:\n")
	fmt.Printf("URL: %s\n", centosURL)
	fmt.Printf("输出路径: %s\n", outputPath)
	fmt.Printf("连接数: %d\n", config.Connections)
	fmt.Printf("分段大小: %d MB\n", config.SegmentSize/(1024*1024))
	fmt.Println("=" * 60)
	
	// 启动事件处理goroutine
	go func() {
		for event := range eventCh {
			switch event.Type {
			case core.EventTypeTaskProgress:
				progress := event.Data.(core.TaskProgress)
				fmt.Printf("进度: %.1f%% | 已下载: %.2f MB / %.2f MB | 速度: %.2f MB/s\n",
					progress.Progress,
					float64(progress.DownloadedBytes)/(1024*1024),
					float64(progress.TotalBytes)/(1024*1024),
					float64(progress.DownloadSpeed)/(1024*1024))
			case core.EventTypeTaskCompleted:
				fmt.Println("✓ 下载完成!")
			case core.EventTypeTaskError:
				fmt.Printf("✗ 下载错误: %v\n", event.Data)
			}
		}
	}()
	
	// 启动任务
	ctx := context.Background()
	startTime := time.Now()
	
	err = task.Start(ctx)
	if err != nil {
		log.Fatalf("启动任务失败: %v", err)
	}
	
	// 等待任务完成
	timeout := time.After(30 * time.Minute) // 给大文件足够的时间
	done := false
	
	for !done {
		select {
		case <-timeout:
			fmt.Println("⏰ 下载超时")
			done = true
		default:
			status := task.Status()
			if status.State == core.TaskStateCompleted || status.State == core.TaskStateError || status.State == core.TaskStateStopped {
				done = true
			}
			time.Sleep(1 * time.Second)
		}
	}
	
	elapsed := time.Since(startTime)
	
	// 获取最终状态
	status := task.Status()
	progress := task.Progress()
	
	fmt.Println("=" * 60)
	fmt.Printf("下载结果:\n")
	fmt.Printf("状态: %v\n", status.State)
	fmt.Printf("总耗时: %v\n", elapsed)
	
	if progress.TotalBytes > 0 {
		fmt.Printf("文件大小: %.2f MB\n", float64(progress.TotalBytes)/(1024*1024))
		fmt.Printf("下载速度: %.2f MB/s\n", float64(progress.DownloadedBytes)/elapsed.Seconds()/(1024*1024))
	}
	
	if status.Error != nil {
		fmt.Printf("错误: %v\n", status.Error)
	}
	
	// 关闭事件通道
	close(eventCh)
	
	// 检查文件是否存在
	if _, err := os.Stat(outputPath); err == nil {
		fmt.Printf("✓ 文件已保存到: %s\n", outputPath)
		
		// 验证SHA256
		fmt.Println("\n" + "=" * 60)
		fmt.Println("开始SHA256完整性验证...")
		fmt.Printf("预期SHA256: %s\n", expectedSHA256)
		
		calculatedSHA256, err := calculateSHA256(outputPath)
		if err != nil {
			fmt.Printf("✗ 计算SHA256失败: %v\n", err)
		} else {
			fmt.Printf("计算SHA256: %s\n", calculatedSHA256)
			
			if calculatedSHA256 == expectedSHA256 {
				fmt.Println("✅ SHA256验证通过！文件完整性确认。")
			} else {
				fmt.Println("❌ SHA256验证失败！文件可能已损坏或被篡改。")
				fmt.Printf("差异: 预期值 vs 计算值\n")
			}
		}
	} else {
		fmt.Printf("✗ 文件未找到: %v\n", err)
	}
}

// calculateSHA256 计算文件的SHA256哈希值
func calculateSHA256(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()
	
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	
	return hex.EncodeToString(hash.Sum(nil)), nil
}