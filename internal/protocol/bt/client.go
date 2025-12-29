// Package bt 提供BitTorrent协议支持
package bt

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"aria2go/internal/core"
	"aria2go/internal/protocol"
)

// DefaultTrackers 默认的公共 tracker 列表（来自 ngosang/trackerslist）
// 这些 tracker 按照流行度和延迟排序，定期更新
var DefaultTrackers = []string{
	// UDP Trackers
	"udp://tracker.opentrackr.org:1337/announce",
	"udp://open.tracker.cl:1337/announce",
	"udp://open.stealth.si:80/announce",
	"udp://tracker.torrent.eu.org:451/announce",
	"udp://exodus.desync.com:6969/announce",
	"udp://tracker1.bt.moack.co.kr:80/announce",
	"udp://tracker2.bt.moack.co.kr:80/announce",
	"udp://tracker3.bt.moack.co.kr:6969/announce",
	"udp://tracker4.bt.moack.co.kr:80/announce",
	"udp://tracker6.dler.org:2710/announce",
	"udp://tracker.dler.org:6969/announce",
	"udp://retracker.netbynet.ru:2710/announce",
	"udp://retracker.lanta-net.ru:2710/announce",
	"udp://opentracker.i2p.rocks:6969/announce",
	"udp://open.demonii.com:1337/announce",
	"udp://denis.stalker.upeer.me:6969/announce",
	"udp://tracker.nyaa.uk:6969/announce",
	"udp://tracker.cyberia.is:6969/announce",
	"udp://tracker.tiny-vps.com:6969/announce",
	"udp://tracker.moeking.me:6969/announce",
	
	// HTTP/HTTPS Trackers
	"http://tracker.opentrackr.org:1337/announce",
	"https://tracker.opentrackr.org:443/announce",
	"http://open.tracker.cl:1337/announce",
	"http://open.stealth.si:80/announce",
	"http://tracker.torrent.eu.org:451/announce",
	"http://exodus.desync.com:6969/announce",
	"http://tracker.dler.org:6969/announce",
	"https://tracker.dler.org:443/announce",
	"http://retracker.netbynet.ru:2710/announce",
	"http://retracker.lanta-net.ru:2710/announce",
	
	// Archive.org Trackers
	"udp://bt1.archive.org:6969/announce",
	"udp://bt2.archive.org:6969/announce",
	"http://bt1.archive.org:6969/announce",
	"http://bt2.archive.org:6969/announce",
}

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
	dhtClient    *DHTClient
	peerID       [20]byte
	id           string
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
		id:      fmt.Sprintf("%x", peerID[:8]),
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
		// magnet链接，参考 aria2 的 UTMetadata 扩展协议
		_, err := ParseMagnetLink(url)
		if err != nil {
			return fmt.Errorf("parse magnet link failed: %w", err)
		}

		// 尝试通过 DHT 或 tracker 获取 torrent 文件
		log.Printf("BTClient[%s] 尝试从 magnet 获取 torrent 文件", bd.id)

		// 创建 magnet 解析器
		magnetResolver := NewMagnetResolver()

		// 解析 magnet 链接获取 torrent 文件
		torrent, err = magnetResolver.Resolve(url)
		if err != nil {
			return fmt.Errorf("resolve magnet link failed: %w", err)
		}

		log.Printf("BTClient[%s] 成功从 magnet 获取 torrent 文件", bd.id)
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
	
	// 添加torrent文件中的trackers
	if torrent.Announce != "" {
		trackerManager.AddTracker(torrent.Announce)
	}
	
	for _, tier := range torrent.AnnounceList {
		for _, tracker := range tier {
			trackerManager.AddTracker(tracker)
		}
	}
	
	// 添加默认的公共 trackers（如果torrent文件中没有足够的tracker）
	if len(torrent.AnnounceList) == 0 && torrent.Announce == "" {
		log.Printf("BTClient[%s] torrent文件中没有tracker，添加默认公共trackers", bd.id)
		for _, tracker := range DefaultTrackers {
			trackerManager.AddTracker(tracker)
		}
	} else {
		// 即使有tracker，也添加一些默认的公共tracker作为备份
		log.Printf("BTClient[%s] 添加默认公共trackers作为备份", bd.id)
		for _, tracker := range DefaultTrackers {
			trackerManager.AddTracker(tracker)
		}
	}
	
	bd.trackerManager = trackerManager
	
	// 初始化DHT客户端（如果启用）
	if bd.config.EnableDHT {
		bd.dhtClient = NewDHTClient()
		if err := bd.dhtClient.Start(); err != nil {
			log.Printf("BTClient[%s] 启动DHT客户端失败: %v", bd.id, err)
			bd.dhtClient = nil
		} else {
			log.Printf("BTClient[%s] DHT客户端已启动", bd.id)
		}
	}
	
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
	
	// 如果tracker没有返回peers且启用了DHT，尝试通过DHT获取peers
	if len(peers) == 0 && bd.dhtClient != nil {
		fmt.Printf("No peers from trackers, trying DHT...\n")
		dhtPeers, err := bd.dhtClient.GetPeers(bd.torrent.InfoHash)
		if err != nil {
			fmt.Printf("DHT get peers failed: %v\n", err)
		} else {
			fmt.Printf("DHT returned %d peers\n", len(dhtPeers))
			for _, peerAddr := range dhtPeers {
				// 解析 peer 地址
				parts := strings.Split(peerAddr, ":")
				if len(parts) == 2 {
					port, err := strconv.ParseUint(parts[1], 10, 16)
					if err != nil {
						continue
					}
					peer := PeerInfo{
						IP:   net.ParseIP(parts[0]),
						Port: uint16(port),
					}
					bd.peerManager.AddPeerInfo(&peer)
				}
			}
		}
	}
	
	// 开始下载pieces
	go bd.downloadPieces(ctx)
	
	// 监控进度
	go bd.monitorProgress(ctx)
	
	// 等待完成或上下文取消
	<-ctx.Done()
	
	// 停止DHT客户端
	if bd.dhtClient != nil {
		bd.dhtClient.Stop()
	}
	
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