// Package bt 提供BitTorrent任务实现
package bt

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"aria2go/internal/core"
)

// BtRuntime BitTorrent运行时状态，参考 aria2 的 BtRuntime
type BtRuntime struct {
	mu        sync.RWMutex
	halt      bool          // 是否停止
	uploadLengthAtStartup int64 // 启动时的上传长度
}

// NewBtRuntime 创建新的 BtRuntime
func NewBtRuntime() *BtRuntime {
	return &BtRuntime{
		halt: false,
	}
}

// SetHalt 设置停止状态
func (br *BtRuntime) SetHalt(halt bool) {
	br.mu.Lock()
	defer br.mu.Unlock()
	br.halt = halt
}

// IsHalt 检查是否停止
func (br *BtRuntime) IsHalt() bool {
	br.mu.RLock()
	defer br.mu.RUnlock()
	return br.halt
}

// SetUploadLengthAtStartup 设置启动时的上传长度
func (br *BtRuntime) SetUploadLengthAtStartup(length int64) {
	br.mu.Lock()
	defer br.mu.Unlock()
	br.uploadLengthAtStartup = length
}

// GetUploadLengthAtStartup 获取启动时的上传长度
func (br *BtRuntime) GetUploadLengthAtStartup() int64 {
	br.mu.RLock()
	defer br.mu.RUnlock()
	return br.uploadLengthAtStartup
}

// BTTask 是BitTorrent下载任务的实现
type BTTask struct {
	*core.BaseTask
	downloader *AnacrolixDownloader // 下载器引用（用于断点续传）
	config     Config
	cancelFunc context.CancelFunc
	btRuntime  *BtRuntime // BitTorrent运行时状态
}

// NewBTTask 创建新的BitTorrent下载任务
func NewBTTask(id string, config core.TaskConfig, eventCh chan<- core.Event) (*BTTask, error) {
	// 验证配置
	if len(config.URLs) == 0 {
		return nil, fmt.Errorf("no URLs provided")
	}

	// 使用第一个URL
	url := config.URLs[0]

	// 验证协议
	if !IsBitTorrentURL(url) {
		return nil, fmt.Errorf("invalid BitTorrent URL: %s", url)
	}

	// 创建BitTorrent配置
	btConfig := DefaultConfig()

	// 从任务配置中提取BitTorrent选项
	if opts, ok := config.Options["bt"].(map[string]interface{}); ok {
		// 监听端口
		if listenPort, ok := opts["listen-port"].(float64); ok {
			btConfig.ListenPort = int(listenPort)
		}
		// DHT设置
		if enableDHT, ok := opts["enable-dht"].(bool); ok {
			btConfig.EnableDHT = enableDHT
		}
		if dhtPort, ok := opts["dht-listen-port"].(float64); ok {
			btConfig.DHTListenPort = int(dhtPort)
		}
		// PEX设置
		if enablePEX, ok := opts["enable-pex"].(bool); ok {
			btConfig.EnablePEX = enablePEX
		}
		// 加密设置
		if enableEncryption, ok := opts["enable-encryption"].(bool); ok {
			btConfig.EnableEncryption = enableEncryption
		}
		// 上传限制
		if maxUploadRate, ok := opts["max-upload-rate"].(float64); ok {
			btConfig.MaxUploadRate = int64(maxUploadRate)
		}
		// 下载限制
		if maxDownloadRate, ok := opts["max-download-rate"].(float64); ok {
			btConfig.MaxDownloadRate = int64(maxDownloadRate)
		}
		// 同时下载任务数
		if maxActiveTasks, ok := opts["max-active-tasks"].(float64); ok {
			btConfig.MaxActiveTasks = int(maxActiveTasks)
		}
		// 每个任务的最大连接数
		if maxConnections, ok := opts["max-connections-per-task"].(float64); ok {
			btConfig.MaxConnectionsPerTask = int(maxConnections)
		}
		// 下载目录
		if downloadDir, ok := opts["download-dir"].(string); ok {
			btConfig.DownloadDir = downloadDir
		}
		// 写入磁盘间隔
		if writeInterval, ok := opts["write-interval"].(time.Duration); ok {
			btConfig.WriteInterval = writeInterval
		}
	}

	// 设置输出路径
	if config.OutputPath != "" {
		// 如果指定了输出路径，使用该路径所在的目录作为下载目录
		btConfig.DownloadDir = filepath.Dir(config.OutputPath)
	} else {
		// 默认下载目录为当前目录
		btConfig.DownloadDir = "."
	}

	// 创建基础任务
	baseTask := core.NewBaseTask(id, config, eventCh)

	return &BTTask{
		BaseTask:  baseTask,
		config:    btConfig,
		btRuntime: NewBtRuntime(), // 初始化 BtRuntime
	}, nil
}

// IsBitTorrentURL 检查URL是否是BitTorrent协议
func IsBitTorrentURL(url string) bool {
	// 检查.torrent文件扩展名
	if strings.HasSuffix(strings.ToLower(url), ".torrent") {
		return true
	}

	// 检查magnet链接
	if IsMagnetLink(url) {
		return true
	}

	// 检查协议前缀
	return strings.HasPrefix(strings.ToLower(url), "bittorrent:") ||
		strings.HasPrefix(strings.ToLower(url), "magnet:")
}

// Start 启动BitTorrent下载任务
func (t *BTTask) Start(ctx context.Context) error {
	// 调用父类的Start方法更新状态
	if err := t.BaseTask.Start(ctx); err != nil {
		return err
	}

	// 创建子上下文用于取消控制
	downloadCtx, cancel := context.WithCancel(ctx)
	t.cancelFunc = cancel

	// 在goroutine中执行下载
	go t.download(downloadCtx)

	// 记录日志
	log.Printf("BTTask[%s] 开始下载: %s", t.ID(), t.Config().URLs[0])

	return nil
}

// Stop 停止BitTorrent下载任务，参考 aria2 的 BtStopDownloadCommand
func (t *BTTask) Stop() error {
	log.Printf("BTTask[%s] 停止任务", t.ID())

	// 1. 设置 BtRuntime 的停止状态
	if t.btRuntime != nil {
		t.btRuntime.SetHalt(true)
		log.Printf("BTTask[%s] 已设置 BtRuntime 停止状态", t.ID())
	}

	// 2. 调用取消函数，取消所有正在进行的操作
	if t.cancelFunc != nil {
		t.cancelFunc()
		log.Printf("BTTask[%s] 已调用取消函数", t.ID())
	}

	// 3. 停止下载器（保留已下载的数据以支持断点续传）
	if t.downloader != nil {
		if err := t.downloader.Stop(); err != nil {
			log.Printf("BTTask[%s] 停止下载器失败: %v", t.ID(), err)
		} else {
			log.Printf("BTTask[%s] 下载器已停止（保留已下载数据）", t.ID())
		}
	}

	// 4. 调用父类的 Stop 方法
	return t.BaseTask.Stop()
}

// Pause 暂停BitTorrent下载任务
func (t *BTTask) Pause() error {
	log.Printf("BTTask[%s] 暂停任务", t.ID())

	// 调用下载器的 Pause 方法
	if t.downloader != nil {
		if err := t.downloader.Pause(); err != nil {
			log.Printf("BTTask[%s] 暂停下载器失败: %v", t.ID(), err)
			return err
		}
		log.Printf("BTTask[%s] 下载器已暂停", t.ID())
	}

	// 调用父类的Pause方法
	return t.BaseTask.Pause()
}

// Resume 恢复BitTorrent下载任务
func (t *BTTask) Resume() error {
	log.Printf("BTTask[%s] 恢复任务", t.ID())

	// 调用下载器的 Resume 方法
	if t.downloader != nil {
		if err := t.downloader.Resume(); err != nil {
			log.Printf("BTTask[%s] 恢复下载器失败: %v", t.ID(), err)
			return err
		}
		log.Printf("BTTask[%s] 下载器已恢复", t.ID())
	}

	// 调用父类的Resume方法（只改变状态，实际恢复由调度器处理）
	return t.BaseTask.Resume()
}

// download 执行实际的下载逻辑
func (t *BTTask) download(ctx context.Context) {
	log.Printf("BTTask[%s] 开始执行下载", t.ID())

	// 检查是否已有下载器（断点续传）
	var downloader *AnacrolixDownloader
	var err error

	if t.downloader != nil && t.downloader.client != nil {
		// 复用已有的下载器
		downloader = t.downloader
		log.Printf("BTTask[%s] 复用已有下载器（断点续传）", t.ID())
	} else {
		// 创建新的 BitTorrent 下载器，使用 anacrolix/torrent 库
		downloader, err = NewAnacrolixDownloader(t.config)
		if err != nil {
			log.Printf("BTTask[%s] 创建下载器失败: %v", t.ID(), err)
			t.BaseTask.SetError(fmt.Errorf("create BitTorrent downloader failed: %w", err))
			return
		}
		// 保存下载器引用（用于停止、恢复等操作）
		t.downloader = downloader
	}

	downloader.id = t.ID()
	downloader.task = t

	// 用于进度更新的通道
	progressDone := make(chan struct{})

// 启动进度更新goroutine
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-progressDone:
				return
			case <-ticker.C:
				// 注意：进度更新由 AnacrolixDownloader 的 monitorProgress 方法处理
				// 这里不再重复更新进度，避免冲突
			}
		}
	}()

	// 执行下载
	err = downloader.Download(ctx, t)

	// 停止进度更新
	close(progressDone)

	if err != nil {
		log.Printf("BTTask[%s] 下载失败: %v", t.ID(), err)
		// 设置错误状态
		t.BaseTask.SetError(fmt.Errorf("BitTorrent download failed: %w", err))
	} else {
		log.Printf("BTTask[%s] 下载完成", t.ID())
		// 标记任务完成
		t.BaseTask.SetComplete()
	}
}

// GetURL 获取下载URL
func (t *BTTask) GetURL() string {
	config := t.BaseTask.Config()
	if len(config.URLs) > 0 {
		return config.URLs[0]
	}
	return ""
}

// GetOutputPath 获取输出文件路径
func (t *BTTask) GetOutputPath() string {
	return t.BaseTask.Config().OutputPath
}

// GetProgressCallback 获取进度回调函数
func (t *BTTask) GetProgressCallback() func(progress core.TaskProgress) {
	return func(progress core.TaskProgress) {
		t.UpdateProgress(progress)
	}
}

// tempTask 临时任务实现，用于传递给BTDownloader
type tempTask struct {
	id         string
	config     core.TaskConfig
	outputPath string
}

func (t *tempTask) ID() string {
	return t.id
}

func (t *tempTask) Start(ctx context.Context) error {
	return nil
}

func (t *tempTask) Stop() error {
	return nil
}

func (t *tempTask) Pause() error {
	return nil
}

func (t *tempTask) Resume() error {
	return nil
}

func (t *tempTask) Status() core.TaskStatus {
	return core.TaskStatus{}
}

func (t *tempTask) Progress() core.TaskProgress {
	return core.TaskProgress{}
}

func (t *tempTask) Config() core.TaskConfig {
	return t.config
}

func (t *tempTask) GetFiles() []core.FileInfo {
	return []core.FileInfo{}
}

func (t *tempTask) GetURIs() []core.URIInfo {
	return []core.URIInfo{}
}

func (t *tempTask) GetPeers() []core.PeerInfo {
	return []core.PeerInfo{}
}

func (t *tempTask) GetServers() []core.ServerInfo {
	return []core.ServerInfo{}
}

func (t *tempTask) GetOption() map[string]string {
	return map[string]string{}
}

func (t *tempTask) ChangeOption(options map[string]string) error {
	return nil
}

func (t *tempTask) GetURL() string {
	return ""
}

func (t *tempTask) GetOutputPath() string {
	return ""
}

func (t *tempTask) GetProgressCallback() func(progress core.TaskProgress) {
	return nil
}

// DefaultConfig 返回默认配置
func DefaultConfig() Config {
	return Config{
		ListenPort:            6881,
		EnableDHT:             true,
		DHTListenPort:         6881,
		EnablePEX:             true,
		EnableEncryption:      true,
		MaxUploadRate:         0,
		MaxDownloadRate:       0,
		MaxActiveTasks:        5,
		MaxConnectionsPerTask: 55,
		DownloadDir:           ".",
		WriteInterval:         1 * time.Second, // 默认每秒写入一次
	}
}