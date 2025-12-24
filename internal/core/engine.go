// Package core 提供下载引擎核心功能
package core

import (
	"context"
	"errors"
	"log"
	"sync"
)

// Engine 是下载引擎的核心接口，对应 aria2 的 DownloadEngine
type Engine interface {
	// Start 启动下载引擎
	Start(ctx context.Context) error
	
	// Stop 停止下载引擎
	Stop() error
	
	// AddTask 添加下载任务
	AddTask(task Task) error
	
	// RemoveTask 移除下载任务
	RemoveTask(taskID string) error
	
	// PauseTask 暂停任务
	PauseTask(taskID string) error
	
	// ResumeTask 恢复任务
	ResumeTask(taskID string) error
	
	// GetTaskStatus 获取任务状态
	GetTaskStatus(taskID string) (TaskStatus, error)
	
	// GetTaskProgress 获取任务进度
	GetTaskProgress(taskID string) (TaskProgress, error)
	
	// GetGlobalStat 获取全局统计信息
	GetGlobalStat() GlobalStat
}

// DownloadEngine 是 Engine 接口的具体实现
type DownloadEngine struct {
	mu       sync.RWMutex
	tasks    map[string]Task
	taskMan  TaskManager
	sched    Scheduler
	eventCh  chan Event
	stopCh   chan struct{}
	running  bool
	started  bool
	
	// 统计信息
	stat     GlobalStat
}

// GlobalStat 包含全局统计信息
type GlobalStat struct {
	DownloadSpeed int64 // 下载速度 (bytes/sec)
	UploadSpeed   int64 // 上传速度 (bytes/sec)
	NumActive     int   // 活跃任务数
	NumWaiting    int   // 等待任务数
	NumStopped    int   // 停止任务数
	NumTotal      int   // 总任务数
}

// NewDownloadEngine 创建新的下载引擎实例
func NewDownloadEngine() *DownloadEngine {
	return &DownloadEngine{
		tasks:   make(map[string]Task),
		eventCh: make(chan Event, 100),
		stopCh:  make(chan struct{}),
		stat:    GlobalStat{},
	}
}

// Start 启动下载引擎
func (e *DownloadEngine) Start(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	
	if e.running {
		log.Printf("引擎[Start] 引擎已经在运行")
		return ErrEngineAlreadyRunning
	}
	
	e.running = true
	e.started = true
	log.Printf("引擎[Start] 启动下载引擎")
	
	// 启动调度器
	if e.sched == nil {
		e.sched = NewDefaultScheduler(e.eventCh)
		log.Printf("引擎[Start] 创建新调度器")
	}
	
	// 启动任务管理器
	if e.taskMan == nil {
		e.taskMan = NewDefaultTaskManager(e.sched, e.eventCh)
		log.Printf("引擎[Start] 创建新任务管理器")
	}
	
	// 启动事件处理循环
	go e.eventLoop(ctx)
	log.Printf("引擎[Start] 启动事件处理循环")
	
	// 启动任务管理器
	log.Printf("引擎[Start] 启动任务管理器")
	if err := e.taskMan.Start(ctx); err != nil {
		e.running = false
		e.started = false
		log.Printf("引擎[Start] 任务管理器启动失败: %v", err)
		return err
	}
	
	// 启动调度器
	log.Printf("引擎[Start] 启动调度器")
	go e.sched.Start(ctx)
	
	log.Printf("引擎[Start] 引擎启动完成")
	return nil
}

// Stop 停止下载引擎
func (e *DownloadEngine) Stop() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	
	if !e.running {
		return nil
	}
	
	close(e.stopCh)
	e.running = false
	
	// 停止所有任务
	for _, task := range e.tasks {
		task.Stop()
	}
	
	// 清空任务列表
	e.tasks = make(map[string]Task)
	
	return nil
}

// AddTask 添加下载任务
func (e *DownloadEngine) AddTask(task Task) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	
	if !e.started {
		log.Printf("引擎[AddTask] 错误: 引擎未启动")
		return ErrEngineNotStarted
	}
	
	taskID := task.ID()
	log.Printf("引擎[AddTask] 添加任务: %s", taskID)
	
	if _, exists := e.tasks[taskID]; exists {
		log.Printf("引擎[AddTask] 错误: 任务已存在: %s", taskID)
		return ErrTaskAlreadyExists
	}
	
	// 通过任务管理器添加任务（任务管理器会处理调度）
	if e.taskMan != nil {
		log.Printf("引擎[AddTask] 通过任务管理器添加任务: %s", taskID)
		if err := e.taskMan.AddTask(task); err != nil {
			log.Printf("引擎[AddTask] 任务管理器添加失败: %s, 错误: %v", taskID, err)
			return err
		}
		log.Printf("引擎[AddTask] 任务管理器添加成功: %s", taskID)
	} else {
		log.Printf("引擎[AddTask] 警告: 任务管理器为nil")
	}
	
	e.tasks[taskID] = task
	e.stat.NumTotal++
	e.stat.NumWaiting++
	log.Printf("引擎[AddTask] 任务已添加到引擎: %s, 总任务数: %d, 等待任务数: %d", taskID, e.stat.NumTotal, e.stat.NumWaiting)
	
	return nil
}

// RemoveTask 移除下载任务
func (e *DownloadEngine) RemoveTask(taskID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	
	task, exists := e.tasks[taskID]
	if !exists {
		return ErrTaskNotFound
	}
	
	// 停止任务
	task.Stop()
	
	// 更新统计
	e.updateStatOnRemove(task.Status())
	
	delete(e.tasks, taskID)
	
	e.eventCh <- Event{
		Type:   EventTaskRemoved,
		TaskID: taskID,
	}
	
	return nil
}

// updateStatOnRemove 根据任务状态更新统计信息
func (e *DownloadEngine) updateStatOnRemove(status TaskStatus) {
	switch status.State {
	case TaskStateActive:
		e.stat.NumActive--
	case TaskStateWaiting:
		e.stat.NumWaiting--
	case TaskStatePaused:
		e.stat.NumStopped--
	case TaskStateStopped:
		e.stat.NumStopped--
	}
	e.stat.NumTotal--
}

// PauseTask 暂停任务
func (e *DownloadEngine) PauseTask(taskID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	
	task, exists := e.tasks[taskID]
	if !exists {
		return ErrTaskNotFound
	}
	
	if err := task.Pause(); err != nil {
		return err
	}
	
	// 更新统计
	if task.Status().State == TaskStateActive {
		e.stat.NumActive--
		e.stat.NumStopped++
	}
	
	return nil
}

// ResumeTask 恢复任务
func (e *DownloadEngine) ResumeTask(taskID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	
	task, exists := e.tasks[taskID]
	if !exists {
		return ErrTaskNotFound
	}
	
	if err := task.Resume(); err != nil {
		return err
	}
	
	// 更新统计
	if task.Status().State == TaskStateActive {
		e.stat.NumStopped--
		e.stat.NumActive++
	}
	
	return nil
}

// GetTaskStatus 获取任务状态
func (e *DownloadEngine) GetTaskStatus(taskID string) (TaskStatus, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	
	task, exists := e.tasks[taskID]
	if !exists {
		return TaskStatus{}, ErrTaskNotFound
	}
	
	return task.Status(), nil
}

// GetTaskProgress 获取任务进度
func (e *DownloadEngine) GetTaskProgress(taskID string) (TaskProgress, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	
	task, exists := e.tasks[taskID]
	if !exists {
		return TaskProgress{}, ErrTaskNotFound
	}
	
	return task.Progress(), nil
}

// GetGlobalStat 获取全局统计信息
func (e *DownloadEngine) GetGlobalStat() GlobalStat {
	e.mu.RLock()
	defer e.mu.RUnlock()
	
	return e.stat
}

// eventLoop 事件处理循环
func (e *DownloadEngine) eventLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-e.stopCh:
			return
		case event := <-e.eventCh:
			e.handleEvent(event)
		}
	}
}

// handleEvent 处理事件
func (e *DownloadEngine) handleEvent(event Event) {
	switch event.Type {
	case EventTaskStateChanged:
		e.handleTaskStateChange(event)
	case EventTaskProgress:
		e.handleTaskProgress(event)
	case EventTaskCompleted:
		e.handleTaskCompletion(event)
	case EventTaskError:
		e.handleTaskError(event)
	}
}

// handleTaskStateChange 处理任务状态变化
func (e *DownloadEngine) handleTaskStateChange(event Event) {
	// 更新全局统计
	// 这里需要根据任务状态变化更新 NumActive, NumWaiting 等统计
	// 简化实现，后续完善
}

// handleTaskProgress 处理任务进度更新
func (e *DownloadEngine) handleTaskProgress(event Event) {
	// 更新速度统计等
}

// handleTaskCompletion 处理任务完成
func (e *DownloadEngine) handleTaskCompletion(event Event) {
	e.mu.Lock()
	defer e.mu.Unlock()
	
	if task, exists := e.tasks[event.TaskID]; exists {
		// 更新统计
		if task.Status().State == TaskStateActive {
			e.stat.NumActive--
		}
		e.stat.NumStopped++
	}
}

// handleTaskError 处理任务错误
func (e *DownloadEngine) handleTaskError(event Event) {
	// 错误处理逻辑
}

// EventCh 返回引擎的事件通道
func (e *DownloadEngine) EventCh() chan<- Event {
	return e.eventCh
}

// 错误定义
var (
	ErrEngineAlreadyRunning = errors.New("download engine is already running")
	ErrEngineNotStarted     = errors.New("download engine is not started")
	ErrTaskAlreadyExists    = errors.New("task already exists")
	ErrTaskNotFound         = errors.New("task not found")
)

// NewDefaultEngine 创建默认的下载引擎
func NewDefaultEngine() *DownloadEngine {
	eventCh := make(chan Event, 100)
	sched := NewDefaultScheduler(eventCh)
	taskMan := NewDefaultTaskManager(sched, eventCh)
	
	return &DownloadEngine{
		tasks:   make(map[string]Task),
		eventCh: eventCh,
		stopCh:  make(chan struct{}),
		stat:    GlobalStat{},
		sched:   sched,
		taskMan: taskMan,
	}
}