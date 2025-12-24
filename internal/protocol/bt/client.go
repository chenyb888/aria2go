// Package bt 提供BitTorrent协议支持
package bt

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	
	"aria2go/internal/core"
	"aria2go/internal/protocol"
)

// Client BitTorrent客户端
type Client struct {
	config Config
}

// Config BitTorrent客户端配置
type Config struct {
	// 监听端口
	ListenPort int
	
	// DHT设置
	EnableDHT     bool
	DHTListenPort int
	
	// PEX设置
	EnablePEX bool
	
	// 加密设置
	EnableEncryption bool
	
	// 上传限制
	MaxUploadRate int64
	
	// 下载限制
	MaxDownloadRate int64
	
	// 同时下载任务数
	MaxActiveTasks int
	
	// 每个任务的最大连接数
	MaxConnectionsPerTask int
	
	// 下载目录
	DownloadDir string
	
	// Piece管理器配置
	PieceManagerConfig PieceManagerConfig
}

// NewClient 创建BitTorrent客户端
func NewClient(config Config) (*Client, error) {
	// 验证配置
	if config.ListenPort <= 0 || config.ListenPort > 65535 {
		return nil, fmt.Errorf("invalid listen port: %d", config.ListenPort)
	}
	
	if config.MaxActiveTasks <= 0 {
		config.MaxActiveTasks = 5
	}
	
	if config.MaxConnectionsPerTask <= 0 {
		config.MaxConnectionsPerTask = 55
	}
	
	return &Client{
		config: config,
	}, nil
}

// BTDownloader BitTorrent下载器实现
type BTDownloader struct {
	client       *Client
	config       Config
	torrent      *TorrentFile
	pieceManager *PieceManager
	peerManager  *PeerManager
	trackerManager *TrackerManager
	peerID       [20]byte
}

// NewBTDownloader 创建BitTorrent下载器
func NewBTDownloader(config Config) (*BTDownloader, error) {
	client, err := NewClient(config)
	if err != nil {
		return nil, err
	}
	
	// 生成peer ID
	peerID := GeneratePeerID()
	
	return &BTDownloader{
		client:  client,
		config:  config,
		peerID:  peerID,
	}, nil
}

// Download 实现protocol.Downloader接口
func (bd *BTDownloader) Download(ctx context.Context, task core.Task) error {
	// 获取任务配置
	config := task.Config()
	
	if len(config.URLs) == 0 {
		return fmt.Errorf("no URLs provided")
	}
	
	url := config.URLs[0]
	outputPath := config.OutputPath
	
	// 解析torrent文件或magnet链接
	var torrent *TorrentFile
	var err error
	
	if strings.HasSuffix(strings.ToLower(url), ".torrent") {
		// 本地.torrent文件
		torrent, err = ParseTorrentFile(url)
		if err != nil {
			return fmt.Errorf("parse torrent file failed: %w", err)
		}
	} else if IsMagnetLink(url) {
		// magnet链接
		_, err := ParseMagnetLink(url)
		if err != nil {
			return fmt.Errorf("parse magnet link failed: %w", err)
		}
		
		// TODO: 实现从DHT或tracker获取torrent文件
		return fmt.Errorf("magnet link support not fully implemented yet")
	} else {
		return fmt.Errorf("unsupported URL: %s", url)
	}
	
	bd.torrent = torrent
	
	// 创建下载目录
	downloadDir := bd.config.DownloadDir
	if downloadDir == "" {
		downloadDir = filepath.Dir(outputPath)
	}
	
	if err := os.MkdirAll(downloadDir, 0755); err != nil {
		return fmt.Errorf("create download directory failed: %w", err)
	}
	
	// 初始化peer管理器
	peerManager := NewPeerManager(torrent.InfoHash, bd.peerID, DefaultPeerManagerConfig())
	bd.peerManager = peerManager
	
	// 初始化piece管理器
	pieceManager, err := NewPieceManager(torrent, bd.config.PieceManagerConfig, bd)
	if err != nil {
		return fmt.Errorf("create piece manager failed: %w", err)
	}
	bd.pieceManager = pieceManager
	
	// 初始化tracker管理器
	trackerManager := NewTrackerManager(torrent.InfoHash, bd.peerID, uint16(bd.config.ListenPort))
	
	// 添加trackers
	if torrent.Announce != "" {
		trackerManager.AddTracker(torrent.Announce)
	}
	
	for _, tier := range torrent.AnnounceList {
		for _, tracker := range tier {
			trackerManager.AddTracker(tracker)
		}
	}
	
	bd.trackerManager = trackerManager
	
	// 开始下载
	return bd.startDownload(ctx)
}

// startDownload 开始下载
func (bd *BTDownloader) startDownload(ctx context.Context) error {
	// 向trackers宣告
	peers, err := bd.trackerManager.AnnounceAll(ctx, 0, 0, bd.torrent.TotalSize())
	if err != nil {
		// 记录错误但继续
		fmt.Printf("Tracker announce failed: %v\n", err)
	}
	
	// 添加peers到peer管理器
	for _, peer := range peers {
		bd.peerManager.AddPeerInfo(&peer)
	}
	
	// 开始下载pieces
	go bd.downloadPieces(ctx)
	
	// 监控进度
	go bd.monitorProgress(ctx)
	
	// 等待完成或上下文取消
	<-ctx.Done()
	
	return nil
}

// downloadPieces 下载pieces
func (bd *BTDownloader) downloadPieces(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			// 获取下一个piece
			piece := bd.pieceManager.GetNextPiece()
			if piece == nil {
				// 没有更多piece，等待
				time.Sleep(1 * time.Second)
				continue
			}
			
			// 将piece加入下载队列
			if err := bd.pieceManager.QueuePiece(piece); err != nil {
				fmt.Printf("Queue piece %d failed: %v\n", piece.Index, err)
				time.Sleep(100 * time.Millisecond)
			}
		}
	}
}

// monitorProgress 监控下载进度
func (bd *BTDownloader) monitorProgress(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			progress := bd.pieceManager.GetProgress()
			downloaded := bd.pieceManager.GetDownloadedBytes()
			total := bd.torrent.TotalSize()
			
			fmt.Printf("Progress: %.2f%%, Downloaded: %s / %s\n",
				progress*100,
				formatBytes(downloaded),
				formatBytes(total))
		}
	}
}

// formatBytes 格式化字节大小
func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// CanHandle 检查是否可以处理BitTorrent URL
func (bd *BTDownloader) CanHandle(urlStr string) bool {
	// 检查.torrent文件扩展名
	if strings.HasSuffix(strings.ToLower(urlStr), ".torrent") {
		return true
	}
	
	// 检查magnet链接
	if IsMagnetLink(urlStr) {
		return true
	}
	
	// 检查URL协议
	parsed, err := url.Parse(urlStr)
	if err != nil {
		return false
	}
	
	// 检查协议是否为bittorrent相关
	scheme := strings.ToLower(parsed.Scheme)
	return scheme == "bittorrent" || scheme == "torrent" || scheme == "magnet"
}

// Name 返回下载器名称
func (bd *BTDownloader) Name() string {
	return "bittorrent"
}

// SupportsResume 是否支持断点续传
func (bd *BTDownloader) SupportsResume() bool {
	return true
}

// SupportsConcurrent 是否支持并发下载
func (bd *BTDownloader) SupportsConcurrent() bool {
	return true
}

// SupportsSegments 是否支持分段下载
func (bd *BTDownloader) SupportsSegments() bool {
	return true
}

// Factory BitTorrent下载器工厂
type Factory struct{}

// CreateDownloader 创建BitTorrent下载器
func (f *Factory) CreateDownloader(config interface{}) (protocol.Downloader, error) {
	var btConfig Config
	if config != nil {
		if c, ok := config.(Config); ok {
			btConfig = c
		} else {
			// 使用默认配置
			btConfig = DefaultConfig()
		}
	} else {
		btConfig = DefaultConfig()
	}
	
	return NewBTDownloader(btConfig)
}

// SupportedProtocols 返回支持的协议列表
func (f *Factory) SupportedProtocols() []string {
	return []string{"bittorrent", "torrent", "magnet"}
}

// DefaultConfig 返回默认配置
func DefaultConfig() Config {
	return Config{
		ListenPort:            6881,
		EnableDHT:             true,
		DHTListenPort:         6881,
		EnablePEX:             true,
		EnableEncryption:      true,
		MaxUploadRate:         0, // 无限制
		MaxDownloadRate:       0, // 无限制
		MaxActiveTasks:        5,
		MaxConnectionsPerTask: 55,
		DownloadDir:           ".",
		PieceManagerConfig:    DefaultPieceManagerConfig(),
	}
}

// RequestBlock 实现BlockRequester接口，请求一个数据块
func (bd *BTDownloader) RequestBlock(pieceIndex int, offset int, size int) ([]byte, error) {
	if bd.peerManager == nil {
		return nil, errors.New("peer manager not initialized")
	}
	return bd.peerManager.RequestBlock(pieceIndex, offset, size)
}

// CancelBlock 实现BlockRequester接口，取消块请求
func (bd *BTDownloader) CancelBlock(pieceIndex int, offset int) error {
	if bd.peerManager == nil {
		return errors.New("peer manager not initialized")
	}
	return bd.peerManager.CancelBlock(pieceIndex, offset)
}