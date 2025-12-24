// Package metalink 提供Metalink协议支持
package metalink

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	
	"aria2go/internal/core"
	"aria2go/internal/protocol"
)

// Client Metalink客户端
type Client struct {
	config Config
}

// Config Metalink客户端配置
type Config struct {
	// 验证校验和
	VerifyChecksum bool
	
	// 使用最强校验和
	UseStrongestChecksum bool
	
	// 允许从不同协议下载
	AllowMixedProtocols bool
	
	// 首选协议
	PreferredProtocols []string
	
	// 首选位置
	PreferredLocations []string
	
	// 下载目录
	DownloadDir string
}

// NewClient 创建Metalink客户端
func NewClient(config Config) (*Client, error) {
	return &Client{
		config: config,
	}, nil
}

// MetalinkDownloader Metalink下载器实现
type MetalinkDownloader struct {
	client *Client
	config Config
}

// NewMetalinkDownloader 创建Metalink下载器
func NewMetalinkDownloader(config Config) (*MetalinkDownloader, error) {
	client, err := NewClient(config)
	if err != nil {
		return nil, err
	}
	
	return &MetalinkDownloader{
		client: client,
		config: config,
	}, nil
}

// Download 实现protocol.Downloader接口
func (md *MetalinkDownloader) Download(ctx context.Context, task core.Task) error {
	// 获取任务配置
	config := task.Config()
	
	if len(config.URLs) == 0 {
		return fmt.Errorf("no URLs provided")
	}
	
	url := config.URLs[0]
	outputPath := config.OutputPath
	
	// 解析metalink文件
	var metalink *MetalinkFile
	var err error
	
	if strings.HasSuffix(strings.ToLower(url), ".metalink") || 
	   strings.HasSuffix(strings.ToLower(url), ".meta4") {
		// 本地.metalink文件
		metalink, err = ParseMetalinkFile(url)
		if err != nil {
			return fmt.Errorf("parse metalink file failed: %w", err)
		}
	} else {
		// 可能是远程URL，需要先下载metalink文件
		// TODO: 实现从远程URL下载metalink文件
		return fmt.Errorf("remote metalink URLs not implemented yet")
	}
	
	if len(metalink.Files) == 0 {
		return fmt.Errorf("no files found in metalink")
	}
	
	// 创建下载目录
	downloadDir := md.config.DownloadDir
	if downloadDir == "" {
		downloadDir = filepath.Dir(outputPath)
	}
	
	if err := os.MkdirAll(downloadDir, 0755); err != nil {
		return fmt.Errorf("create download directory failed: %w", err)
	}
	
	// 下载所有文件
	for i, file := range metalink.Files {
		// 确定输出路径
		var fileOutputPath string
		if len(metalink.Files) == 1 {
			// 单个文件，使用用户指定的输出路径
			fileOutputPath = outputPath
		} else {
			// 多个文件，使用文件名
			fileOutputPath = filepath.Join(downloadDir, file.Name)
		}
		
		// 选择最佳资源
		resource := metalink.GetBestResource(i, "")
		if resource == nil {
			return fmt.Errorf("no suitable resource found for file %s", file.Name)
		}
		
		// 根据资源类型选择下载器
		// TODO: 实现实际的下载逻辑，这里只是占位符
		fmt.Printf("Downloading file %s from %s using %s protocol\n", 
			file.Name, resource.URL, resource.Type)
		
		// 创建占位符文件
		if err := createPlaceholderFile(fileOutputPath, file.Size); err != nil {
			return fmt.Errorf("create placeholder file failed: %w", err)
		}
	}
	
	return nil
}

// createPlaceholderFile 创建占位符文件（实际实现中应该真正下载）
func createPlaceholderFile(path string, size int64) error {
	// 在实际实现中，这里应该调用相应的协议下载器
	// 现在只是创建空文件作为占位符
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	
	if size > 0 {
		// 如果需要，可以预分配空间
		file.Truncate(size)
	}
	
	return nil
}

// CanHandle 检查是否可以处理Metalink URL
func (md *MetalinkDownloader) CanHandle(url string) bool {
	// 检查文件扩展名
	urlLower := strings.ToLower(url)
	return strings.HasSuffix(urlLower, ".metalink") || 
	       strings.HasSuffix(urlLower, ".meta4") ||
	       strings.Contains(urlLower, "metalink=") ||
	       strings.HasPrefix(urlLower, "metalink:")
}

// Name 返回下载器名称
func (md *MetalinkDownloader) Name() string {
	return "metalink"
}

// SupportsResume 是否支持断点续传
func (md *MetalinkDownloader) SupportsResume() bool {
	return true
}

// SupportsConcurrent 是否支持并发下载
func (md *MetalinkDownloader) SupportsConcurrent() bool {
	return true
}

// SupportsSegments 是否支持分段下载
func (md *MetalinkDownloader) SupportsSegments() bool {
	return true
}

// Factory Metalink下载器工厂
type Factory struct{}

// CreateDownloader 创建Metalink下载器
func (f *Factory) CreateDownloader(config interface{}) (protocol.Downloader, error) {
	var metalinkConfig Config
	if config != nil {
		if c, ok := config.(Config); ok {
			metalinkConfig = c
		} else {
			// 使用默认配置
			metalinkConfig = DefaultConfig()
		}
	} else {
		metalinkConfig = DefaultConfig()
	}
	
	return NewMetalinkDownloader(metalinkConfig)
}

// SupportedProtocols 返回支持的协议列表
func (f *Factory) SupportedProtocols() []string {
	return []string{"metalink", "meta4"}
}

// DefaultConfig 返回默认配置
func DefaultConfig() Config {
	return Config{
		VerifyChecksum:       true,
		UseStrongestChecksum: true,
		AllowMixedProtocols:  true,
		PreferredProtocols:   []string{"https", "http", "ftp"},
		PreferredLocations:   []string{},
		DownloadDir:          ".",
	}
}