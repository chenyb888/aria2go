// Package core 提供下载任务相关功能
package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

	// GetFiles 获取文件列表
	GetFiles() []FileInfo

	// GetURIs 获取 URI 列表
	GetURIs() []URIInfo

	// GetPeers 获取 Peer 列表 (BitTorrent)
	GetPeers() []PeerInfo

	// GetServers 获取服务器列表 (HTTP/FTP)
	GetServers() []ServerInfo

	// GetOption 获取任务配置选项
	GetOption() map[string]string

	// ChangeOption 修改任务配置选项
	ChangeOption(options map[string]string) error

	// GetURL 获取下载URL
	GetURL() string

	// GetOutputPath 获取输出文件路径
	GetOutputPath() string

	// GetProgressCallback 获取进度回调函数
	GetProgressCallback() func(progress TaskProgress)
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

// GetFiles 获取文件列表（默认实现）
func (t *BaseTask) GetFiles() []FileInfo {
	t.mu.RLock()
	defer t.mu.RUnlock()

	// 默认实现：根据配置创建单文件信息
	fileInfo := FileInfo{
		Index:           1,
		Path:            t.config.OutputPath,
		Length:          t.progress.TotalBytes,
		CompletedLength: t.progress.DownloadedBytes,
		Selected:        true,
		URIs:            t.getURIsFromConfig(),
	}

	return []FileInfo{fileInfo}
}

// GetURIs 获取 URI 列表（默认实现）
func (t *BaseTask) GetURIs() []URIInfo {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return t.getURIsFromConfig()
}

// GetPeers 获取 Peer 列表（默认实现，返回空列表）
func (t *BaseTask) GetPeers() []PeerInfo {
	return []PeerInfo{}
}

// GetServers 获取服务器列表 (HTTP/FTP)
// 默认实现返回空列表，协议特定任务可以覆盖此方法
func (t *BaseTask) GetServers() []ServerInfo {
	return []ServerInfo{}
}

// GetOption 获取任务配置选项
func (t *BaseTask) GetOption() map[string]string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	// 将 Config.Options 转换为 map[string]string
	result := make(map[string]string)
	for k, v := range t.config.Options {
		if str, ok := v.(string); ok {
			result[k] = str
		}
	}

	// 添加常用配置选项
	if t.config.OutputPath != "" {
		result["out"] = t.config.OutputPath
	}
	if t.config.MaxSpeed > 0 {
		result["max-download-limit"] = formatSpeed(t.config.MaxSpeed)
	}
	if t.config.MaxUploadSpeed > 0 {
		result["max-upload-limit"] = formatSpeed(t.config.MaxUploadSpeed)
	}
	if t.config.Connections > 0 {
		result["split"] = fmt.Sprintf("%d", t.config.Connections)
	}

	return result
}

// ChangeOption 修改任务配置选项
func (t *BaseTask) ChangeOption(options map[string]string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.config.Options == nil {
		t.config.Options = make(map[string]interface{})
	}

	// 更新选项
	for k, v := range options {
		t.config.Options[k] = v

		// 处理特定选项
		switch k {
		case "out":
			t.config.OutputPath = v
		case "max-download-limit":
			t.config.MaxSpeed = parseSpeed(v)
		case "max-upload-limit":
			t.config.MaxUploadSpeed = parseSpeed(v)
		case "split":
			if split, err := parseNumber(v); err == nil {
				t.config.Connections = int(split)
			}
		case "dir":
			// 更新输出路径的目录部分
			if t.config.OutputPath != "" {
				t.config.OutputPath = v + "/" + getFileName(t.config.OutputPath)
			}
		}
	}

	return nil
}

// GetURL 获取下载URL
func (t *BaseTask) GetURL() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if len(t.config.URLs) > 0 {
		return t.config.URLs[0]
	}
	return ""
}

// GetOutputPath 获取输出文件路径
func (t *BaseTask) GetOutputPath() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.config.OutputPath
}

// GetProgressCallback 获取进度回调函数
func (t *BaseTask) GetProgressCallback() func(progress TaskProgress) {
	return func(progress TaskProgress) {
		t.UpdateProgress(progress)
	}
}

// formatSpeed 格式化速度为字符串
func formatSpeed(speed int64) string {
	return fmt.Sprintf("%d", speed)
}

// parseSpeed 解析速度字符串
func parseSpeed(speed string) int64 {
	var val int64
	fmt.Sscanf(speed, "%d", &val)
	return val
}

// getFileName 从路径中获取文件名
func getFileName(path string) string {
	// 简化实现，实际应该使用 filepath.Base
	parts := strings.Split(path, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return path
}

// getURIsFromConfig 从配置中创建 URI 列表
func (t *BaseTask) getURIsFromConfig() []URIInfo {
	var uris []URIInfo
	for i, url := range t.config.URLs {
		status := "waiting"
		if i == 0 {
			status = "used"
		}
		uris = append(uris, URIInfo{
			URI:    url,
			Status: status,
		})
	}
	return uris
}

// 错误定义
var (
	ErrTaskNotStartable = errors.New("task is not in a startable state")
	ErrTaskNotPausable  = errors.New("task is not in a pausable state")
	ErrTaskNotResumable = errors.New("task is not in a resumable state")
)

// FileInfo 表示文件信息，对应 aria2 的 FileEntry
type FileInfo struct {
	Index           int       // 文件索引（从1开始）
	Path            string    // 文件路径
	Length          int64     // 文件总大小
	CompletedLength int64     // 已下载大小
	Selected        bool      // 是否选中下载
	URIs            []URIInfo // URI 列表
}

// URIInfo 表示 URI 信息
type URIInfo struct {
	URI    string // URI 地址
	Status string // 状态: "used", "waiting", "error"
}

// PeerInfo 表示 BitTorrent Peer 信息
type PeerInfo struct {
	PeerId         string  // Peer ID
	Ip             string  // IP 地址
	Port           int     // 端口号
	Bitfield       int64   // 位图
	AmChoking      bool    // 是否阻塞对方
	AmInterested   bool    // 是否对对方感兴趣
	PeerChoking    bool    // 对方是否阻塞
	PeerInterested bool    // 对方是否感兴趣
	DownloadSpeed  int64   // 下载速度 (bytes/sec)
	UploadSpeed    int64   // 上传速度 (bytes/sec)
	Seeder         bool    // 是否为种子
}

// ServerInfo 表示服务器信息 (HTTP/FTP)
type ServerInfo struct {
	URI           string // 服务器 URI
	CurrentUri    string // 当前使用的 URI
	DownloadSpeed int64  // 下载速度 (bytes/sec)
}