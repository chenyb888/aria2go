// Package ftp 提供FTP协议支持
package ftp

import (
	"context"
	"fmt"
	
	"aria2go/internal/core"
	"aria2go/internal/protocol"
)

// Client FTP客户端
type Client struct {
	config Config
}

// Config FTP客户端配置
type Config struct {
	Host     string
	Port     int
	Username string
	Password string
	Timeout  int
	Passive  bool
}

// NewClient 创建FTP客户端
func NewClient(config Config) (*Client, error) {
	return &Client{
		config: config,
	}, nil
}

// FTPDownloader FTP下载器实现
type FTPDownloader struct {
	client *Client
	config Config
}

// NewFTPDownloader 创建FTP下载器
func NewFTPDownloader(config Config) (*FTPDownloader, error) {
	client, err := NewClient(config)
	if err != nil {
		return nil, err
	}
	
	return &FTPDownloader{
		client: client,
		config: config,
	}, nil
}

// Download 实现protocol.Downloader接口
func (fd *FTPDownloader) Download(ctx context.Context, task core.Task) error {
	// TODO: 实现FTP下载逻辑
	return fmt.Errorf("FTP protocol not implemented yet")
}

// CanHandle 检查是否可以处理FTP URL
func (fd *FTPDownloader) CanHandle(url string) bool {
	// TODO: 实现URL检测
	return false
}

// Name 返回下载器名称
func (fd *FTPDownloader) Name() string {
	return "ftp"
}

// SupportsResume 是否支持断点续传
func (fd *FTPDownloader) SupportsResume() bool {
	return true
}

// SupportsConcurrent 是否支持并发下载
func (fd *FTPDownloader) SupportsConcurrent() bool {
	return false
}

// SupportsSegments 是否支持分段下载
func (fd *FTPDownloader) SupportsSegments() bool {
	return false
}

// Factory FTP下载器工厂
type Factory struct{}

// CreateDownloader 创建FTP下载器
func (f *Factory) CreateDownloader(config interface{}) (protocol.Downloader, error) {
	var ftpConfig Config
	if config != nil {
		if c, ok := config.(Config); ok {
			ftpConfig = c
		} else {
			// 使用默认配置
			ftpConfig = DefaultConfig()
		}
	} else {
		ftpConfig = DefaultConfig()
	}
	
	return NewFTPDownloader(ftpConfig)
}

// SupportedProtocols 返回支持的协议列表
func (f *Factory) SupportedProtocols() []string {
	return []string{"ftp", "ftps"}
}

// DefaultConfig 返回默认配置
func DefaultConfig() Config {
	return Config{
		Host:     "localhost",
		Port:     21,
		Username: "anonymous",
		Password: "anonymous@example.com",
		Timeout:  30,
		Passive:  true,
	}
}