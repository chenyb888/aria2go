// Package core 提供任务调度功能
package core

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"
)

// Scheduler 调度器接口，负责管理任务的执行
type Scheduler interface {
	// Start 启动调度器
	Start(ctx context.Context) error
	
	// Stop 停止调度器
	Stop() error
	
	// Schedule 调度任务执行
	Schedule(task Task) error
	
	// Unschedule 取消任务调度
	Unschedule(taskID string) error
	
	// SetMaxConcurrent 设置最大并发任务数
	SetMaxConcurrent(n int)
	
	// GetQueueStatus 获取队列状态
	GetQueueStatus() QueueStatus
}

// QueueStatus 表示队列状态
type QueueStatus struct {
	TotalTasks    int
	ActiveTasks   int
	WaitingTasks  int
	MaxConcurrent int
}

// DefaultScheduler 是 Scheduler 的默认实现
type DefaultScheduler struct {
	mu             sync.RWMutex
	activeTasks    map[string]Task
	waitingQueue   []Task
	maxConcurrent  int
	eventCh        chan<- Event
	stopCh         chan struct{}
	running        bool
	taskCancelMap  map[string]context.CancelFunc
}

// NewDefaultScheduler 创建新的默认调度器
func NewDefaultScheduler(eventCh chan<- Event) *DefaultScheduler {
	return &DefaultScheduler{
		activeTasks:   make(map[string]Task),
		waitingQueue:  make([]Task, 0),
		maxConcurrent: 5, // 默认最大并发数
		eventCh:       eventCh,
		stopCh:        make(chan struct{}),
		taskCancelMap: make(map[string]context.CancelFunc),
	}
}

// Start 启动调度器
func (s *DefaultScheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	if s.running {
		log.Printf("调度器[Start] 调度器已经在运行")
		return nil
	}
	
	s.running = true
	log.Printf("调度器[Start] 启动调度器")
	
	// 启动调度循环
	go s.scheduleLoop(ctx)
	log.Printf("调度器[Start] 调度循环已启动")
	
	return nil
}

// Stop 停止调度器
func (s *DefaultScheduler) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	if !s.running {
		return nil
	}
	
	close(s.stopCh)
	s.running = false
	
	// 取消所有任务
	for taskID, cancel := range s.taskCancelMap {
		cancel()
		delete(s.taskCancelMap, taskID)
	}
	
	// 清空队列
	s.activeTasks = make(map[string]Task)
	s.waitingQueue = make([]Task, 0)
	
	return nil
}

// Schedule 调度任务执行
func (s *DefaultScheduler) Schedule(task Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	taskID := task.ID()
	log.Printf("调度器[Schedule] 调度任务: %s", taskID)
	
	// 检查任务是否已经在调度中
	if _, active := s.activeTasks[taskID]; active {
		log.Printf("调度器[Schedule] 任务已在活跃任务中: %s", taskID)
		return ErrSchedTaskAlreadyScheduled
	}
	
	for _, waitingTask := range s.waitingQueue {
		if waitingTask.ID() == taskID {
			log.Printf("调度器[Schedule] 任务已在等待队列中: %s", taskID)
			return ErrSchedTaskAlreadyScheduled
		}
	}
	
	// 将任务加入等待队列
	s.waitingQueue = append(s.waitingQueue, task)
	log.Printf("调度器[Schedule] 任务加入等待队列: %s, 等待队列长度: %d", taskID, len(s.waitingQueue))
	
	// 尝试立即调度
	s.trySchedule()
	
	return nil
}

// Unschedule 取消任务调度
func (s *DefaultScheduler) Unschedule(taskID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	// 检查是否在活跃任务中
	if task, exists := s.activeTasks[taskID]; exists {
		// 停止任务
		task.Stop()
		
		// 取消上下文
		if cancel, exists := s.taskCancelMap[taskID]; exists {
			cancel()
			delete(s.taskCancelMap, taskID)
		}
		
		delete(s.activeTasks, taskID)
		
		// 尝试调度等待队列中的任务
		s.trySchedule()
		
		return nil
	}
	
	// 检查是否在等待队列中
	for i, task := range s.waitingQueue {
		if task.ID() == taskID {
			// 从等待队列中移除
			s.waitingQueue = append(s.waitingQueue[:i], s.waitingQueue[i+1:]...)
			return nil
		}
	}
	
	return ErrSchedTaskNotFound
}

// SetMaxConcurrent 设置最大并发任务数
func (s *DefaultScheduler) SetMaxConcurrent(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	if n < 1 {
		n = 1
	}
	
	s.maxConcurrent = n
	
	// 调整并发数
	s.adjustConcurrency()
}

// GetQueueStatus 获取队列状态
func (s *DefaultScheduler) GetQueueStatus() QueueStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	return QueueStatus{
		TotalTasks:    len(s.activeTasks) + len(s.waitingQueue),
		ActiveTasks:   len(s.activeTasks),
		WaitingTasks:  len(s.waitingQueue),
		MaxConcurrent: s.maxConcurrent,
	}
}

// trySchedule 尝试调度等待队列中的任务
func (s *DefaultScheduler) trySchedule() {
	// 如果已达到最大并发数，则不调度
	if len(s.activeTasks) >= s.maxConcurrent {
		log.Printf("调度器[trySchedule] 已达到最大并发数: %d/%d", len(s.activeTasks), s.maxConcurrent)
		return
	}
	
	log.Printf("调度器[trySchedule] 当前活跃任务数: %d, 最大并发数: %d, 等待队列长度: %d", len(s.activeTasks), s.maxConcurrent, len(s.waitingQueue))
	
	// 调度等待队列中的任务
	for len(s.waitingQueue) > 0 && len(s.activeTasks) < s.maxConcurrent {
		// 从等待队列头部取出任务
		task := s.waitingQueue[0]
		s.waitingQueue = s.waitingQueue[1:]
		taskID := task.ID()
		
		log.Printf("调度器[trySchedule] 从等待队列取出任务: %s", taskID)
		
		// 将任务加入活跃任务
		s.activeTasks[taskID] = task
		
		// 创建任务上下文
		ctx, cancel := context.WithCancel(context.Background())
		s.taskCancelMap[taskID] = cancel
		
		log.Printf("调度器[trySchedule] 启动executeTask: %s", taskID)
		// 启动任务执行
		go s.executeTask(ctx, task)
	}
}

// adjustConcurrency 调整并发度
func (s *DefaultScheduler) adjustConcurrency() {
	// 如果减少了最大并发数，可能需要停止一些任务
	if len(s.activeTasks) > s.maxConcurrent {
		// 计算需要停止的任务数
		stopCount := len(s.activeTasks) - s.maxConcurrent
		
		// 停止部分活跃任务（简化实现，实际可能需要更复杂的策略）
		stopped := 0
		for taskID, task := range s.activeTasks {
			if stopped >= stopCount {
				break
			}
			
			// 将任务移回等待队列
			task.Stop()
			s.waitingQueue = append(s.waitingQueue, task)
			delete(s.activeTasks, taskID)
			
			// 取消上下文
			if cancel, exists := s.taskCancelMap[taskID]; exists {
				cancel()
				delete(s.taskCancelMap, taskID)
			}
			
			stopped++
		}
	} else {
		// 如果增加了最大并发数，尝试调度更多任务
		s.trySchedule()
	}
}

// executeTask 执行任务
func (s *DefaultScheduler) executeTask(ctx context.Context, task Task) {
	taskID := task.ID()
	
	log.Printf("调度器[executeTask] 开始执行任务: %s", taskID)
	
	// 任务执行前发送状态变化事件
	s.sendTaskEvent(taskID, EventTaskStateChanged, TaskStateChangedPayload{
		OldState: TaskStateWaiting,
		NewState: TaskStateActive,
	})
	
	// 启动任务
	log.Printf("调度器[executeTask] 调用task.Start: %s", taskID)
	if err := task.Start(ctx); err != nil {
		log.Printf("调度器[executeTask] 任务启动失败: %s, 错误: %v", taskID, err)
		s.handleTaskError(taskID, err)
		return
	}
	log.Printf("调度器[executeTask] 任务已启动: %s", taskID)
	
	// 创建ticker定期检查任务状态
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	
	// 等待任务完成、取消或超时
	for {
		select {
		case <-ctx.Done():
			// 任务被取消
			s.handleTaskCancelled(taskID)
			s.cleanupTask(taskID)
			return
		case <-s.stopCh:
			// 调度器停止
			task.Stop()
			s.cleanupTask(taskID)
			return
		case <-ticker.C:
			// 定期检查任务状态
			status := task.Status()
			log.Printf("调度器[executeTask] 检查任务状态: %s, 状态: %v", taskID, status.State)
			if status.State == TaskStateCompleted || status.State == TaskStateError {
				// 任务已完成或有错误，清理资源
				log.Printf("调度器[executeTask] 任务已完成或出错，清理: %s, 状态: %v", taskID, status.State)
				s.cleanupTask(taskID)
				return
			}
		}
	}
}

// handleTaskError 处理任务错误
func (s *DefaultScheduler) handleTaskError(taskID string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	// 发送错误事件
	s.sendTaskEvent(taskID, EventTaskError, TaskErrorPayload{
		Error: err,
	})
	
	// 清理任务
	s.cleanupTask(taskID)
	
	// 尝试调度新任务
	s.trySchedule()
}

// handleTaskCancelled 处理任务取消
func (s *DefaultScheduler) handleTaskCancelled(taskID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	// 发送状态变化事件
	s.sendTaskEvent(taskID, EventTaskStateChanged, TaskStateChangedPayload{
		OldState: TaskStateActive,
		NewState: TaskStateStopped,
	})
	
	// 清理任务
	s.cleanupTask(taskID)
	
	// 尝试调度新任务
	s.trySchedule()
}

// cleanupTask 清理任务资源
func (s *DefaultScheduler) cleanupTask(taskID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	// 从活跃任务中移除
	delete(s.activeTasks, taskID)
	
	// 清理取消函数
	if cancel, exists := s.taskCancelMap[taskID]; exists {
		delete(s.taskCancelMap, taskID)
		cancel()
	}
}

// sendTaskEvent 发送任务事件
func (s *DefaultScheduler) sendTaskEvent(taskID string, eventType EventType, payload interface{}) {
	if s.eventCh != nil {
		s.eventCh <- Event{
			Type:    eventType,
			TaskID:  taskID,
			Payload: payload,
		}
	}
}

// scheduleLoop 调度循环（处理定时任务等）
func (s *DefaultScheduler) scheduleLoop(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		case <-ticker.C:
			// 定期检查任务状态等
			s.checkTasks()
		}
	}
}

// checkTasks 检查任务状态
func (s *DefaultScheduler) checkTasks() {
	// 收集需要清理的任务ID
	var completedTasks []string
	
	s.mu.RLock()
	for taskID, task := range s.activeTasks {
		status := task.Status()
		if status.State == TaskStateCompleted || status.State == TaskStateError {
			completedTasks = append(completedTasks, taskID)
		}
	}
	s.mu.RUnlock()
	
	// 清理已完成的任务
	if len(completedTasks) > 0 {
		s.mu.Lock()
		for _, taskID := range completedTasks {
			delete(s.activeTasks, taskID)
			// 清理取消函数
			if cancel, exists := s.taskCancelMap[taskID]; exists {
				delete(s.taskCancelMap, taskID)
				cancel()
			}
		}
		// 尝试调度新任务
		s.trySchedule()
		s.mu.Unlock()
	}
}

// 错误定义
var (
	ErrSchedTaskAlreadyScheduled = errors.New("task is already scheduled")
	ErrSchedTaskNotFound         = errors.New("task not found")
)