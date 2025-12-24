// Package core 提供事件系统
package core

// EventType 表示事件类型
type EventType int

const (
	// EventTaskAdded 表示任务添加事件
	EventTaskAdded EventType = iota
	
	// EventTaskRemoved 表示任务移除事件
	EventTaskRemoved
	
	// EventTaskStateChanged 表示任务状态变化事件
	EventTaskStateChanged
	
	// EventTaskProgress 表示任务进度更新事件
	EventTaskProgress
	
	// EventTaskCompleted 表示任务完成事件
	EventTaskCompleted
	
	// EventTaskError 表示任务错误事件
	EventTaskError
	
	// EventEngineStarted 表示引擎启动事件
	EventEngineStarted
	
	// EventEngineStopped 表示引擎停止事件
	EventEngineStopped
	
	// EventSpeedLimitChanged 表示速度限制变化事件
	EventSpeedLimitChanged
)

// Event 表示一个事件
type Event struct {
	Type    EventType
	TaskID  string
	Payload interface{}
}

// TaskStateChangedPayload 表示任务状态变化事件的负载
type TaskStateChangedPayload struct {
	OldState TaskState
	NewState TaskState
}

// TaskProgressPayload 表示任务进度事件的负载
type TaskProgressPayload struct {
	Progress TaskProgress
}

// TaskErrorPayload 表示任务错误事件的负载
type TaskErrorPayload struct {
	Error error
}

// EngineEventPayload 表示引擎事件的负载
type EngineEventPayload struct {
	Message string
	Data    interface{}
}