// Package core 提供任务管理功能
package core

import (
	"context"
	"errors"
	"log"
	"sync"
)

// TaskManager 任务管理器接口
type TaskManager interface {
	// Start 启动任务管理器
	Start(ctx context.Context) error
	
	// Stop 停止任务管理器
	Stop() error
	
	// AddTask 添加任务
	AddTask(task Task) error
	
	// RemoveTask 移除任务
	RemoveTask(taskID string) error
	
	// GetTask 获取任务
	GetTask(taskID string) (Task, error)
	
	// ListTasks 列出所有任务
	ListTasks() []Task
	
	// GetTaskCount 获取任务数量
	GetTaskCount() int
}

// DefaultTaskManager 是 TaskManager 的默认实现
type DefaultTaskManager struct {
	mu       sync.RWMutex
	tasks    map[string]Task
	sched    Scheduler
	eventCh  chan<- Event
	running  bool
	stopCh   chan struct{}
}

// NewDefaultTaskManager 创建新的默认任务管理器
func NewDefaultTaskManager(sched Scheduler, eventCh chan<- Event) *DefaultTaskManager {
	return &DefaultTaskManager{
		tasks:   make(map[string]Task),
		sched:   sched,
		eventCh: eventCh,
		stopCh:  make(chan struct{}),
	}
}

// Start 启动任务管理器
func (tm *DefaultTaskManager) Start(ctx context.Context) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	
	if tm.running {
		log.Printf("任务管理器[Start] 任务管理器已经在运行")
		return nil
	}
	
	tm.running = true
	log.Printf("任务管理器[Start] 启动任务管理器")
	
	// 启动事件处理循环
	go tm.eventLoop(ctx)
	log.Printf("任务管理器[Start] 事件处理循环已启动")
	
	return nil
}

// Stop 停止任务管理器
func (tm *DefaultTaskManager) Stop() error {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	
	if !tm.running {
		return nil
	}
	
	close(tm.stopCh)
	tm.running = false
	
	// 停止所有任务
	for _, task := range tm.tasks {
		task.Stop()
	}
	
	// 清空任务列表
	tm.tasks = make(map[string]Task)
	
	return nil
}

// AddTask 添加任务
func (tm *DefaultTaskManager) AddTask(task Task) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	
	taskID := task.ID()
	log.Printf("任务管理器[AddTask] 添加任务: %s", taskID)
	
	if _, exists := tm.tasks[taskID]; exists {
		log.Printf("任务管理器[AddTask] 任务已存在: %s", taskID)
		return ErrTaskMgrAlreadyExists
	}
	
	tm.tasks[taskID] = task
	log.Printf("任务管理器[AddTask] 任务已添加到任务列表: %s", taskID)
	
	// 发送任务添加事件
	if tm.eventCh != nil {
		log.Printf("任务管理器[AddTask] 发送任务添加事件: %s", taskID)
		tm.eventCh <- Event{
			Type:    EventTaskAdded,
			TaskID:  taskID,
			Payload: task,
		}
	}
	
	// 调度任务执行
	if tm.sched != nil {
		log.Printf("任务管理器[AddTask] 调用调度器Schedule: %s", taskID)
		if err := tm.sched.Schedule(task); err != nil {
			log.Printf("任务管理器[AddTask] 调度失败: %s, 错误: %v", taskID, err)
			return err
		}
		log.Printf("任务管理器[AddTask] 调度成功: %s", taskID)
	} else {
		log.Printf("任务管理器[AddTask] 警告: 调度器为nil")
	}
	
	return nil
}

// RemoveTask 移除任务
func (tm *DefaultTaskManager) RemoveTask(taskID string) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	
	task, exists := tm.tasks[taskID]
	if !exists {
		return ErrTaskMgrNotFound
	}
	
	// 停止任务
	task.Stop()
	
	// 取消调度
	if tm.sched != nil {
		if err := tm.sched.Unschedule(taskID); err != nil {
			// 即使取消调度失败，也继续移除任务
		}
	}
	
	// 从任务列表中移除
	delete(tm.tasks, taskID)
	
	// 发送任务移除事件
	if tm.eventCh != nil {
		tm.eventCh <- Event{
			Type:   EventTaskRemoved,
			TaskID: taskID,
		}
	}
	
	return nil
}

// GetTask 获取任务
func (tm *DefaultTaskManager) GetTask(taskID string) (Task, error) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	
	task, exists := tm.tasks[taskID]
	if !exists {
		return nil, ErrTaskMgrNotFound
	}
	
	return task, nil
}

// ListTasks 列出所有任务
func (tm *DefaultTaskManager) ListTasks() []Task {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	
	tasks := make([]Task, 0, len(tm.tasks))
	for _, task := range tm.tasks {
		tasks = append(tasks, task)
	}
	
	return tasks
}

// GetTaskCount 获取任务数量
func (tm *DefaultTaskManager) GetTaskCount() int {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	
	return len(tm.tasks)
}

// eventLoop 事件处理循环
func (tm *DefaultTaskManager) eventLoop(ctx context.Context) {
	// 简化实现，实际可能需要处理特定事件
	select {
	case <-ctx.Done():
		return
	case <-tm.stopCh:
		return
	}
}

// 错误定义
var (
	ErrTaskMgrAlreadyExists = errors.New("task already exists")
	ErrTaskMgrNotFound      = errors.New("task not found")
)