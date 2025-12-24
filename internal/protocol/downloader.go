// Package protocol 定义下载协议接口
package protocol

import (
	"context"
	
	"aria2go/internal/core"
)

// Downloader 下载器接口，所有协议下载器必须实现此接口
type Downloader interface {
	// Download 执行下载任务
	Download(ctx context.Context, task core.Task) error
	
	// CanHandle 检查是否可以处理指定的URL或协议
	CanHandle(url string) bool
	
	// Name 返回下载器名称
	Name() string
	
	// SupportsResume 是否支持断点续传
	SupportsResume() bool
	
	// SupportsConcurrent 是否支持并发下载
	SupportsConcurrent() bool
	
	// SupportsSegments 是否支持分段下载
	SupportsSegments() bool
}

// Factory 下载器工厂接口
type Factory interface {
	// CreateDownloader 创建下载器
	CreateDownloader(config interface{}) (Downloader, error)
	
	// SupportedProtocols 返回支持的协议列表
	SupportedProtocols() []string
}

// DownloaderFactory 下载器工厂
type DownloaderFactory struct {
	protocols map[string]Factory
}

// NewDownloaderFactory 创建下载器工厂
func NewDownloaderFactory() *DownloaderFactory {
	return &DownloaderFactory{
		protocols: make(map[string]Factory),
	}
}

// Register 注册协议工厂
func (df *DownloaderFactory) Register(protocol string, factory Factory) {
	df.protocols[protocol] = factory
}

// CreateDownloader 创建下载器
func (df *DownloaderFactory) CreateDownloader(protocol string, config interface{}) (Downloader, error) {
	factory, exists := df.protocols[protocol]
	if !exists {
		return nil, ErrUnsupportedProtocol
	}
	
	return factory.CreateDownloader(config)
}

// GetSupportedProtocols 获取支持的协议列表
func (df *DownloaderFactory) GetSupportedProtocols() []string {
	protocols := make([]string, 0, len(df.protocols))
	for protocol := range df.protocols {
		protocols = append(protocols, protocol)
	}
	return protocols
}

// CanHandleURL 检查是否有下载器可以处理指定的URL
func (df *DownloaderFactory) CanHandleURL(url string) bool {
	for _, factory := range df.protocols {
		downloader, err := factory.CreateDownloader(nil)
		if err != nil {
			continue
		}
		if downloader.CanHandle(url) {
			return true
		}
	}
	return false
}

// GetDownloaderForURL 获取适合处理指定URL的下载器
func (df *DownloaderFactory) GetDownloaderForURL(url string, config interface{}) (Downloader, error) {
	for _, factory := range df.protocols {
		downloader, err := factory.CreateDownloader(config)
		if err != nil {
			continue
		}
		if downloader.CanHandle(url) {
			return downloader, nil
		}
	}
	return nil, ErrUnsupportedProtocol
}

// 错误定义
var (
	ErrUnsupportedProtocol = NewProtocolError("unsupported protocol")
	ErrInvalidURL          = NewProtocolError("invalid URL")
	ErrDownloadFailed      = NewProtocolError("download failed")
)

// ProtocolError 协议错误
type ProtocolError struct {
	message string
}

// NewProtocolError 创建协议错误
func NewProtocolError(message string) *ProtocolError {
	return &ProtocolError{message: message}
}

// Error 实现error接口
func (e *ProtocolError) Error() string {
	return e.message
}

// Config 通用配置接口
type Config interface {
	// Validate 验证配置
	Validate() error
	
	// GetProtocol 获取协议类型
	GetProtocol() string
}