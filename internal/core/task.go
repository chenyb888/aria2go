// Package core 提供下载任务相关功能
package core

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Task 表示一个下载任务，对应 aria2 的 RequestGroup
type Task interface {
	// ID 返回任务唯一标识
	ID() string
	
	// Start 启动任务
	Start(ctx context.Context) error
	
	// Stop 停止任务
	Stop() error
	
	// Pause 暂停任务
	Pause() error
	
	// Resume 恢复任务
	Resume() error
	
	// Status 返回任务当前状态
	Status() TaskStatus
	
	// Progress 返回任务进度信息
	Progress() TaskProgress
	
	// Config 返回任务配置
	Config() TaskConfig
}

// TaskStatus 表示任务状态
type TaskStatus struct {
	State     TaskState
	Error     error
	StartTime time.Time
	EndTime   time.Time
}

// TaskProgress 表示任务进度
type TaskProgress struct {
	TotalBytes     int64   // 总字节数
	DownloadedBytes int64   // 已下载字节数
	UploadedBytes  int64   // 已上传字节数
	DownloadSpeed  int64   // 下载速度 (bytes/sec)
	UploadSpeed    int64   // 上传速度 (bytes/sec)
	Progress       float64 // 进度百分比 (0-100)
	ETA            time.Duration // 预计剩余时间
}

// TaskConfig 表示任务配置
type TaskConfig struct {
	URLs         []string      // 下载URL列表
	OutputPath   string        // 输出文件路径
	SegmentSize  int64         // 分段大小
	Connections  int           // 每个服务器的连接数
	MaxSpeed     int64         // 最大下载速度 (bytes/sec)
	MaxUploadSpeed int64       // 最大上传速度 (bytes/sec)
	Checksum     string        // 文件校验和
	ChecksumType string        // 校验和类型 (md5, sha1, etc.)
	Protocol     string        // 协议类型 (http, ftp, bt, metalink)
	Options      map[string]interface{} // 其他选项
}

// TaskState 表示任务状态枚举
type TaskState int

const (
	TaskStateWaiting TaskState = iota // 等待中
	TaskStateActive                   // 活跃中
	TaskStatePaused                   // 已暂停
	TaskStateStopped                  // 已停止
	TaskStateCompleted                // 已完成
	TaskStateError                    // 错误
)

// String 返回任务状态的字符串表示
func (s TaskState) String() string {
	switch s {
	case TaskStateWaiting:
		return "waiting"
	case TaskStateActive:
		return "active"
	case TaskStatePaused:
		return "paused"
	case TaskStateStopped:
		return "stopped"
	case TaskStateCompleted:
		return "completed"
	case TaskStateError:
		return "error"
	default:
		return "unknown"
	}
}

// BaseTask 是 Task 接口的基础实现
type BaseTask struct {
	id       string
	config   TaskConfig
	status   TaskStatus
	progress TaskProgress
	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.RWMutex
	eventCh  chan<- Event
}

// NewBaseTask 创建新的基础任务
func NewBaseTask(id string, config TaskConfig, eventCh chan<- Event) *BaseTask {
	return &BaseTask{
		id:      id,
		config:  config,
		status:  TaskStatus{State: TaskStateWaiting},
		eventCh: eventCh,
	}
}

// ID 返回任务ID
func (t *BaseTask) ID() string {
	return t.id
}

// Start 启动任务
func (t *BaseTask) Start(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	
	if t.status.State != TaskStateWaiting && t.status.State != TaskStatePaused {
		return ErrTaskNotStartable
	}
	
	t.ctx, t.cancel = context.WithCancel(ctx)
	t.status.State = TaskStateActive
	t.status.StartTime = time.Now()
	
	// 发送状态变化事件
	if t.eventCh != nil {
		t.eventCh <- Event{
			Type:   EventTaskStateChanged,
			TaskID: t.id,
			Payload: TaskStateChangedPayload{
				OldState: TaskStateWaiting,
				NewState: TaskStateActive,
			},
		}
	}
	
	return nil
}

// Stop 停止任务
func (t *BaseTask) Stop() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	
	if t.status.State == TaskStateStopped || t.status.State == TaskStateCompleted {
		return nil
	}
	
	oldState := t.status.State
	t.status.State = TaskStateStopped
	t.status.EndTime = time.Now()
	
	if t.cancel != nil {
		t.cancel()
	}
	
	// 发送状态变化事件
	if t.eventCh != nil && oldState != TaskStateStopped {
		t.eventCh <- Event{
			Type:   EventTaskStateChanged,
			TaskID: t.id,
			Payload: TaskStateChangedPayload{
				OldState: oldState,
				NewState: TaskStateStopped,
			},
		}
	}
	
	return nil
}

// Pause 暂停任务
func (t *BaseTask) Pause() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	
	if t.status.State != TaskStateActive {
		return ErrTaskNotPausable
	}
	
	oldState := t.status.State
	t.status.State = TaskStatePaused
	
	if t.cancel != nil {
		t.cancel()
	}
	
	// 发送状态变化事件
	if t.eventCh != nil {
		t.eventCh <- Event{
			Type:   EventTaskStateChanged,
			TaskID: t.id,
			Payload: TaskStateChangedPayload{
				OldState: oldState,
				NewState: TaskStatePaused,
			},
		}
	}
	
	return nil
}

// Resume 恢复任务
func (t *BaseTask) Resume() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	
	if t.status.State != TaskStatePaused {
		return ErrTaskNotResumable
	}
	
	// 注意：Resume 只改变状态，实际的恢复由调度器处理
	oldState := t.status.State
	t.status.State = TaskStateWaiting
	
	// 发送状态变化事件
	if t.eventCh != nil {
		t.eventCh <- Event{
			Type:   EventTaskStateChanged,
			TaskID: t.id,
			Payload: TaskStateChangedPayload{
				OldState: oldState,
				NewState: TaskStateWaiting,
			},
		}
	}
	
	return nil
}

// Status 返回任务状态
func (t *BaseTask) Status() TaskStatus {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.status
}

// Progress 返回任务进度
func (t *BaseTask) Progress() TaskProgress {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.progress
}

// Config 返回任务配置
func (t *BaseTask) Config() TaskConfig {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.config
}

// updateProgress 更新任务进度（内部方法）
func (t *BaseTask) updateProgress(progress TaskProgress) {
	t.mu.Lock()
	defer t.mu.Unlock()
	
	t.progress = progress
	
	// 发送进度更新事件
	if t.eventCh != nil {
		t.eventCh <- Event{
			Type:   EventTaskProgress,
			TaskID: t.id,
			Payload: progress,
		}
	}
}

// setError 设置任务错误（内部方法）
func (t *BaseTask) setError(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	
	t.status.State = TaskStateError
	t.status.Error = err
	t.status.EndTime = time.Now()
	
	if t.cancel != nil {
		t.cancel()
	}
	
	// 发送错误事件
	if t.eventCh != nil {
		t.eventCh <- Event{
			Type:   EventTaskError,
			TaskID: t.id,
			Payload: TaskErrorPayload{
				Error: err,
			},
		}
	}
}

// complete 标记任务完成（内部方法）
func (t *BaseTask) complete() {
	t.mu.Lock()
	defer t.mu.Unlock()
	
	t.status.State = TaskStateCompleted
	t.status.EndTime = time.Now()
	t.progress.Progress = 100.0
	
	// 发送完成事件
	if t.eventCh != nil {
		t.eventCh <- Event{
			Type:   EventTaskCompleted,
			TaskID: t.id,
		}
	}
}

// SetError 设置任务错误（公共方法，供嵌入类型使用）
func (t *BaseTask) SetError(err error) {
	t.setError(err)
}

// SetComplete 标记任务完成（公共方法，供嵌入类型使用）
func (t *BaseTask) SetComplete() {
	t.complete()
}

// UpdateProgress 更新任务进度（公共方法，供嵌入类型使用）
func (t *BaseTask) UpdateProgress(progress TaskProgress) {
	t.updateProgress(progress)
}

// 错误定义
var (
	ErrTaskNotStartable = errors.New("task is not in a startable state")
	ErrTaskNotPausable  = errors.New("task is not in a pausable state")
	ErrTaskNotResumable = errors.New("task is not in a resumable state")
)