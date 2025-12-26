// Package core 提供下载引擎核心功能
package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"time"
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

	// EventCh 返回引擎的事件通道
	EventCh() chan<- Event

	// PauseAllTasks 暂停所有任务
	PauseAllTasks() error

	// ForcePauseAllTasks 强制暂停所有任务
	ForcePauseAllTasks() error

	// ResumeAllTasks 恢复所有任务
	ResumeAllTasks() error

	// GetActiveTasks 获取活跃任务列表
	GetActiveTasks() []Task

	// GetWaitingTasks 获取等待任务列表
	GetWaitingTasks() []Task

	// GetStoppedTasks 获取停止任务列表
	GetStoppedTasks() []Task

	// GetTask 获取任务
	GetTask(taskID string) (Task, error)

	// GetGlobalOption 获取全局配置选项
	GetGlobalOption() map[string]string

	// ChangeGlobalOption 修改全局配置选项
	ChangeGlobalOption(options map[string]string) error

	// Shutdown 关闭下载引擎
	Shutdown(force bool) error

	// SaveSession 保存会话到文件
	SaveSession(filename string) error
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
	
	// 全局配置
	globalConfig map[string]string
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
		tasks:        make(map[string]Task),
		eventCh:      make(chan Event, 100),
		stopCh:       make(chan struct{}),
		stat:         GlobalStat{},
		globalConfig: getDefaultGlobalConfig(),
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

// PauseAllTasks 暂停所有任务
func (e *DownloadEngine) PauseAllTasks() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	log.Printf("引擎[PauseAllTasks] 暂停所有任务")

	for taskID, task := range e.tasks {
		status := task.Status()
		if status.State == TaskStateActive {
			if err := task.Pause(); err != nil {
				log.Printf("引擎[PauseAllTasks] 暂停任务失败: %s, 错误: %v", taskID, err)
				continue
			}
			e.stat.NumActive--
			e.stat.NumStopped++
		}
	}

	log.Printf("引擎[PauseAllTasks] 完成，剩余活跃任务: %d", e.stat.NumActive)
	return nil
}

// ForcePauseAllTasks 强制暂停所有任务
func (e *DownloadEngine) ForcePauseAllTasks() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	log.Printf("引擎[ForcePauseAllTasks] 强制暂停所有任务")

	for taskID, task := range e.tasks {
		status := task.Status()
		// 强制停止所有活跃任务
		if status.State == TaskStateActive || status.State == TaskStateWaiting {
			// 先停止任务
			if err := task.Stop(); err != nil {
				log.Printf("引擎[ForcePauseAllTasks] 停止任务失败: %s, 错误: %v", taskID, err)
				continue
			}
			// 然后暂停
			if err := task.Pause(); err != nil {
				log.Printf("引擎[ForcePauseAllTasks] 暂停任务失败: %s, 错误: %v", taskID, err)
			}
			// 更新统计
			if status.State == TaskStateActive {
				e.stat.NumActive--
			} else if status.State == TaskStateWaiting {
				e.stat.NumWaiting--
			}
			e.stat.NumStopped++
		}
	}

	log.Printf("引擎[ForcePauseAllTasks] 完成，剩余活跃任务: %d, 等待任务: %d", e.stat.NumActive, e.stat.NumWaiting)
	return nil
}

// ResumeAllTasks 恢复所有暂停的任务
func (e *DownloadEngine) ResumeAllTasks() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	log.Printf("引擎[ResumeAllTasks] 恢复所有暂停的任务")

	for taskID, task := range e.tasks {
		status := task.Status()
		if status.State == TaskStatePaused {
			if err := task.Resume(); err != nil {
				log.Printf("引擎[ResumeAllTasks] 恢复任务失败: %s, 错误: %v", taskID, err)
				continue
			}
			e.stat.NumStopped--
			e.stat.NumWaiting++
		}
	}

	log.Printf("引擎[ResumeAllTasks] 完成，恢复的任务数已加入等待队列")
	return nil
}

// GetActiveTasks 获取活跃任务列表
func (e *DownloadEngine) GetActiveTasks() []Task {
	e.mu.RLock()
	defer e.mu.RUnlock()

	var activeTasks []Task
	for _, task := range e.tasks {
		if task.Status().State == TaskStateActive {
			activeTasks = append(activeTasks, task)
		}
	}

	return activeTasks
}

// GetWaitingTasks 获取等待任务列表
func (e *DownloadEngine) GetWaitingTasks() []Task {
	e.mu.RLock()
	defer e.mu.RUnlock()

	var waitingTasks []Task
	for _, task := range e.tasks {
		if task.Status().State == TaskStateWaiting {
			waitingTasks = append(waitingTasks, task)
		}
	}

	return waitingTasks
}

// GetStoppedTasks 获取停止任务列表
func (e *DownloadEngine) GetStoppedTasks() []Task {
	e.mu.RLock()
	defer e.mu.RUnlock()

	var stoppedTasks []Task
	for _, task := range e.tasks {
		status := task.Status()
		if status.State == TaskStateStopped || status.State == TaskStatePaused || status.State == TaskStateCompleted || status.State == TaskStateError {
			stoppedTasks = append(stoppedTasks, task)
		}
	}

	return stoppedTasks
}

// GetTask 获取任务
func (e *DownloadEngine) GetTask(taskID string) (Task, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	task, exists := e.tasks[taskID]
	if !exists {
		return nil, ErrTaskNotFound
	}

	return task, nil
}

// GetGlobalOption 获取全局配置选项
func (e *DownloadEngine) GetGlobalOption() map[string]string {
	e.mu.RLock()
	defer e.mu.RUnlock()

	// 返回全局配置的副本
	result := make(map[string]string)
	for k, v := range e.globalConfig {
		result[k] = v
	}
	return result
}

// ChangeGlobalOption 修改全局配置选项
func (e *DownloadEngine) ChangeGlobalOption(options map[string]string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// 更新全局配置
	for k, v := range options {
		e.globalConfig[k] = v
	}

	// 处理特定选项
	if speed, ok := options["max-overall-download-limit"]; ok {
		// TODO: 应用到任务管理器
		log.Printf("引擎[ChangeGlobalOption] max-overall-download-limit = %s", speed)
	}
	if speed, ok := options["max-overall-upload-limit"]; ok {
		// TODO: 应用到任务管理器
		log.Printf("引擎[ChangeGlobalOption] max-overall-upload-limit = %s", speed)
	}
	if maxConcurrent, ok := options["max-concurrent-downloads"]; ok {
		// TODO: 应用到任务管理器
		log.Printf("引擎[ChangeGlobalOption] max-concurrent-downloads = %s", maxConcurrent)
	}
	if logLevel, ok := options["log-level"]; ok {
		// TODO: 设置日志级别
		log.Printf("引擎[ChangeGlobalOption] log-level = %s", logLevel)
	}

	return nil
}

// Shutdown 关闭下载引擎
func (e *DownloadEngine) Shutdown(force bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.running {
		log.Printf("引擎[Shutdown] 引擎未运行")
		return nil
	}

	log.Printf("引擎[Shutdown] 开始关闭引擎, force=%v", force)

	if force {
		// 强制关闭：立即停止所有任务
		log.Printf("引擎[Shutdown] 强制停止所有任务")
		for _, task := range e.tasks {
			if err := task.Stop(); err != nil {
				log.Printf("引擎[Shutdown] 停止任务失败: %s, 错误: %v", task.ID(), err)
			}
		}
	} else {
		// 优雅关闭：等待活跃任务完成
		log.Printf("引擎[Shutdown] 等待活跃任务完成")
		activeTasks := e.GetActiveTasks()
		if len(activeTasks) > 0 {
			log.Printf("引擎[Shutdown] 还有 %d 个活跃任务，等待完成", len(activeTasks))
			// TODO: 实现等待任务完成的逻辑
			// 当前简化处理，直接停止
			for _, task := range activeTasks {
				if err := task.Stop(); err != nil {
					log.Printf("引擎[Shutdown] 停止任务失败: %s, 错误: %v", task.ID(), err)
				}
			}
		}
	}

	// 停止引擎
	e.running = false
	close(e.stopCh)

	log.Printf("引擎[Shutdown] 引擎已关闭")
	return nil
}

// SaveSession 保存会话到文件
func (e *DownloadEngine) SaveSession(filename string) error {
	e.mu.RLock()
	defer e.mu.RUnlock()

	log.Printf("引擎[SaveSession] 保存会话到文件: %s", filename)

	// 创建会话数据
	session := SessionData{
		Version:    "1.0",
		SavedAt:    time.Now().Unix(),
		GlobalStat: e.stat,
		GlobalOption: make(map[string]string),
	}

	// 保存全局配置
	for k, v := range e.globalConfig {
		session.GlobalOption[k] = v
	}

	// 保存所有任务
	for _, task := range e.tasks {
		status := task.Status()
		progress := task.Progress()
		config := task.Config()

		taskData := TaskData{
			GID:         task.ID(),
			State:       status.State.String(),
			StartTime:   status.StartTime.Unix(),
			EndTime:     status.EndTime.Unix(),
			TotalBytes:  progress.TotalBytes,
			DownloadedBytes: progress.DownloadedBytes,
			UploadedBytes:   progress.UploadedBytes,
			URLs:        config.URLs,
			OutputPath:  config.OutputPath,
			Options:     make(map[string]string),
		}

		// 转换选项为字符串
		for k, v := range config.Options {
			if str, ok := v.(string); ok {
				taskData.Options[k] = str
			}
		}

		// 如果有错误，保存错误信息
		if status.Error != nil {
			taskData.ErrorMessage = status.Error.Error()
		}

		session.Tasks = append(session.Tasks, taskData)
	}

	// 序列化为 JSON
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		log.Printf("引擎[SaveSession] 序列化失败: %v", err)
		return fmt.Errorf("serialize session failed: %w", err)
	}

	// 写入文件
	if err := os.WriteFile(filename, data, 0644); err != nil {
		log.Printf("引擎[SaveSession] 写入文件失败: %v", err)
		return fmt.Errorf("write session file failed: %w", err)
	}

	log.Printf("引擎[SaveSession] 会话已保存，任务数: %d", len(session.Tasks))
	return nil
}

// SessionData 会话数据结构
type SessionData struct {
	Version      string            `json:"version"`
	SavedAt      int64             `json:"savedAt"`
	GlobalStat   GlobalStat        `json:"globalStat"`
	GlobalOption map[string]string `json:"globalOption"`
	Tasks        []TaskData        `json:"tasks"`
}

// TaskData 任务数据结构
type TaskData struct {
	GID              string            `json:"gid"`
	State            string            `json:"state"`
	StartTime        int64             `json:"startTime"`
	EndTime          int64             `json:"endTime"`
	TotalBytes       int64             `json:"totalBytes"`
	DownloadedBytes  int64             `json:"downloadedBytes"`
	UploadedBytes    int64             `json:"uploadedBytes"`
	URLs             []string          `json:"urls"`
	OutputPath       string            `json:"outputPath"`
	Options          map[string]string `json:"options"`
	ErrorMessage     string            `json:"errorMessage,omitempty"`
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
		tasks:        make(map[string]Task),
		eventCh:      eventCh,
		stopCh:       make(chan struct{}),
		stat:         GlobalStat{},
		taskMan:      taskMan,
		sched:        sched,
		globalConfig: getDefaultGlobalConfig(),
	}
}

// getDefaultGlobalConfig 返回默认全局配置
func getDefaultGlobalConfig() map[string]string {
	return map[string]string{
		"dir":                        "/downloads",
		"max-overall-download-limit": "0",
		"max-overall-upload-limit":   "0",
		"max-concurrent-downloads":   "5",
		"continue":                   "true",
		"auto-file-renaming":         "true",
		"log-level":                  "notice",
		"max-download-result":        "1000",
	}
}