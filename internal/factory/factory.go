// Package factory 提供任务工厂功能
package factory

import (
	"fmt"
	"log"
	"strings"
	
	core "aria2go/internal/core"
	http "aria2go/internal/protocol/http"
	bt "aria2go/internal/protocol/bt"
)

// TaskFactory 任务工厂接口
type TaskFactory interface {
	// CreateTask 创建下载任务
	CreateTask(id string, config core.TaskConfig, eventCh chan<- core.Event) (core.Task, error)
}

// DefaultTaskFactory 默认任务工厂实现
type DefaultTaskFactory struct{}

// NewDefaultTaskFactory 创建新的默认任务工厂
func NewDefaultTaskFactory() *DefaultTaskFactory {
	return &DefaultTaskFactory{}
}

// CreateTask 创建下载任务
func (f *DefaultTaskFactory) CreateTask(id string, config core.TaskConfig, eventCh chan<- core.Event) (core.Task, error) {
	log.Printf("任务工厂[CreateTask] 创建任务: %s, URL: %v", id, config.URLs)
	
	// 如果指定了协议类型，使用该协议
	if config.Protocol != "" {
		log.Printf("任务工厂[CreateTask] 使用指定协议: %s", config.Protocol)
		return f.createByProtocol(id, config, eventCh)
	}
	
	// 否则根据URL自动判断
	if len(config.URLs) == 0 {
		log.Printf("任务工厂[CreateTask] 错误: 没有提供URL")
		return nil, fmt.Errorf("no URLs provided")
	}
	
	// 使用第一个URL判断协议
	url := config.URLs[0]
	protocol := detectProtocol(url)
	log.Printf("任务工厂[CreateTask] 检测到协议: %s (URL: %s)", protocol, url)
	
	// 创建协议特定的配置
	protoConfig := config
	protoConfig.Protocol = protocol
	
	return f.createByProtocol(id, protoConfig, eventCh)
}

// createByProtocol 根据协议创建任务
func (f *DefaultTaskFactory) createByProtocol(id string, config core.TaskConfig, eventCh chan<- core.Event) (core.Task, error) {
	log.Printf("任务工厂[createByProtocol] 根据协议创建任务: %s, 协议: %s", id, config.Protocol)
	log.Printf("任务工厂[createByProtocol] 详细配置: URLs=%v, OutputPath=%s", config.URLs, config.OutputPath)
	
	switch strings.ToLower(config.Protocol) {
	case "http", "https":
		log.Printf("任务工厂[createByProtocol] 创建HTTPTask: %s", id)
		return http.NewHTTPTask(id, config, eventCh)
	case "bt", "bittorrent":
		log.Printf("任务工厂[createByProtocol] 创建BTTask: %s", id)
		return bt.NewBTTask(id, config, eventCh)
	default:
		// 其他协议暂时返回错误，后续可以扩展
		log.Printf("任务工厂[createByProtocol] 错误: 协议未实现: %s", config.Protocol)
		return nil, fmt.Errorf("protocol not implemented yet: %s", config.Protocol)
	}
}

// detectProtocol 检测URL协议
func detectProtocol(url string) string {
	url = strings.ToLower(url)
	
	switch {
	case strings.HasPrefix(url, "http://"):
		return "http"
	case strings.HasPrefix(url, "https://"):
		return "https"
	case strings.HasPrefix(url, "ftp://"):
		return "ftp"
	case strings.HasPrefix(url, "ftps://"):
		return "ftps"
	case strings.HasPrefix(url, "magnet:"):
		return "bt"
	case strings.HasSuffix(url, ".torrent"):
		return "bt"
	case strings.HasSuffix(url, ".metalink"):
		return "metalink"
	default:
		// 默认使用HTTP
		return "http"
	}
}

// GlobalTaskFactory 全局任务工厂实例
var GlobalTaskFactory TaskFactory = NewDefaultTaskFactory()

// CreateTask 全局函数，使用全局工厂创建任务
func CreateTask(id string, config core.TaskConfig, eventCh chan<- core.Event) (core.Task, error) {
	return GlobalTaskFactory.CreateTask(id, config, eventCh)
}