package main

import (
	"context"
	"testing"
	"time"
	
	"aria2go/internal/core"
)

func TestEngineStartStop(t *testing.T) {
	engine := core.NewDefaultEngine()
	
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	
	// 启动引擎
	err := engine.Start(ctx)
	if err != nil {
		t.Fatalf("启动引擎失败: %v", err)
	}
	
	// 等待一段时间确保引擎运行
	time.Sleep(100 * time.Millisecond)
	
	// 停止引擎
	err = engine.Stop()
	if err != nil {
		t.Fatalf("停止引擎失败: %v", err)
	}
}

func TestEngineAddTask(t *testing.T) {
	engine := core.NewDefaultEngine()
	
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	
	err := engine.Start(ctx)
	if err != nil {
		t.Fatalf("启动引擎失败: %v", err)
	}
	defer engine.Stop()
	
	// 创建一个简单的任务配置
	config := core.TaskConfig{
		URLs:        []string{"http://example.com/test"},
		OutputPath:  "/tmp/test-file",
		SegmentSize: 1024 * 1024, // 1MB
		Connections: 1,
		Protocol:    "http",
	}
	
	// 创建事件通道（用于任务事件）
	eventCh := make(chan core.Event, 10)
	
	// 创建任务
	task := core.NewBaseTask("test-task-1", config, eventCh)
	
	err = engine.AddTask(task)
	if err != nil {
		t.Fatalf("添加任务失败: %v", err)
	}
	
	// 获取任务状态
	status, err := engine.GetTaskStatus("test-task-1")
	if err != nil {
		t.Fatalf("获取任务状态失败: %v", err)
	}
	
	t.Logf("任务状态: %v", status)
}