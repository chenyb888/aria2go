// Package bt 提供BitTorrent协议支持
package bt

import (
	"crypto/rand"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MagnetLink 表示解析后的magnet链接
type MagnetLink struct {
	// 必需参数
	InfoHash [20]byte // 信息哈希
	
	// 可选参数
	DisplayName string   // 显示名称
	Trackers    []string // Tracker列表
	WebSeeds    []string // Web种子（HTTP/FTP源）
	ExactTopic  string   // 精确主题
	ExactLength int64    // 精确长度
	ExactSource string   // 精确源
	
	// 其他参数
	Keywords []string // 关键字
	XPE      []string // 扩展参数
}

// ParseMagnetLink 解析magnet链接
func ParseMagnetLink(magnetURL string) (*MagnetLink, error) {
	if !strings.HasPrefix(strings.ToLower(magnetURL), "magnet:?") {
		return nil, errors.New("invalid magnet link format")
	}
	
	// 解析查询参数
	queryString := magnetURL[8:] // 去除"magnet:?"
	values, err := url.ParseQuery(queryString)
	if err != nil {
		return nil, fmt.Errorf("parse magnet query failed: %w", err)
	}
	
	magnet := &MagnetLink{}
	
	// 解析xt参数（必需）
	xtValues := values["xt"]
	if len(xtValues) == 0 {
		return nil, errors.New("missing xt (exact topic) parameter")
	}
	
	// 解析第一个xt参数
	xt := xtValues[0]
	if !strings.HasPrefix(xt, "urn:btih:") {
		return nil, fmt.Errorf("unsupported URN scheme: %s", xt)
	}
	
	// 提取info哈希
	hashStr := xt[9:] // 去除"urn:btih:"
	
	// 处理Base32编码的哈希（40字符）或十六进制编码（32字符）
	var hashBytes []byte
	if len(hashStr) == 40 {
		// Base32编码
		hashBytes, err = decodeBase32(hashStr)
		if err != nil {
			return nil, fmt.Errorf("decode base32 hash failed: %w", err)
		}
	} else if len(hashStr) == 32 {
		// 十六进制编码
		hashBytes, err = hex.DecodeString(hashStr)
		if err != nil {
			return nil, fmt.Errorf("decode hex hash failed: %w", err)
		}
	} else {
		return nil, fmt.Errorf("invalid hash length: %d", len(hashStr))
	}
	
	if len(hashBytes) != 20 {
		return nil, fmt.Errorf("invalid hash size: %d bytes", len(hashBytes))
	}
	
	copy(magnet.InfoHash[:], hashBytes)
	
	// 解析dn参数（显示名称）
	if dnValues := values["dn"]; len(dnValues) > 0 {
		magnet.DisplayName = dnValues[0]
	}
	
	// 解析tr参数（trackers）
	if trValues := values["tr"]; len(trValues) > 0 {
		magnet.Trackers = trValues
	}
	
	// 解析ws参数（web seeds）
	if wsValues := values["ws"]; len(wsValues) > 0 {
		magnet.WebSeeds = wsValues
	}
	
	// 解析xl参数（精确长度）
	if xlValues := values["xl"]; len(xlValues) > 0 {
		xlStr := xlValues[0]
		if length, err := strconv.ParseInt(xlStr, 10, 64); err == nil {
			magnet.ExactLength = length
		}
	}
	
	// 解析as参数（可接受来源）
	if asValues := values["as"]; len(asValues) > 0 {
		magnet.ExactSource = asValues[0]
	}
	
	// 解析xs参数（确切来源）
	if xsValues := values["xs"]; len(xsValues) > 0 {
		// 这可能是多个来源
		magnet.ExactSource = xsValues[0]
	}
	
	// 解析kt参数（关键字）
	if ktValues := values["kt"]; len(ktValues) > 0 {
		keywords := strings.Split(ktValues[0], "+")
		magnet.Keywords = keywords
	}
	
	// 解析x.pe参数（扩展参数）
	if xpeValues := values["x.pe"]; len(xpeValues) > 0 {
		magnet.XPE = xpeValues
	}
	
	// 如果没有trackers，尝试从xt参数中提取
	if len(magnet.Trackers) == 0 {
		// 检查是否有额外的xt参数包含tracker信息
		for _, xt := range xtValues[1:] {
			if strings.HasPrefix(xt, "urn:btih:") {
				// 这是另一个info哈希，跳过
				continue
			}
			// 可能是tracker URL
			magnet.Trackers = append(magnet.Trackers, xt)
		}
	}
	
	return magnet, nil
}

// decodeBase32 解码Base32字符串
func decodeBase32(s string) ([]byte, error) {
	// 标准Base32字母表（RFC 4648）
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
	
	// 转换为大写
	s = strings.ToUpper(s)
	
	// 检查无效字符
	for _, ch := range s {
		if !strings.ContainsRune(alphabet, ch) {
			return nil, fmt.Errorf("invalid base32 character: %c", ch)
		}
	}
	
	// Base32解码
	var result []byte
	var buffer uint64
	var bits uint
	
	for i := 0; i < len(s); i++ {
		ch := s[i]
		value := strings.IndexByte(alphabet, ch)
		if value < 0 {
			return nil, fmt.Errorf("invalid base32 character: %c", ch)
		}
		
		buffer = (buffer << 5) | uint64(value)
		bits += 5
		
		if bits >= 8 {
			result = append(result, byte(buffer>>(bits-8)))
			bits -= 8
		}
	}
	
	// 处理剩余位
	if bits > 0 {
		result = append(result, byte(buffer<<(8-bits)))
	}
	
	return result, nil
}

// InfoHashString 返回info哈希的十六进制字符串
func (ml *MagnetLink) InfoHashString() string {
	return fmt.Sprintf("%x", ml.InfoHash)
}

// ToMagnetLink 生成magnet链接字符串
func (ml *MagnetLink) ToMagnetLink() string {
	var buf strings.Builder
	buf.WriteString("magnet:?")
	
	// 添加xt参数
	buf.WriteString("xt=urn:btih:")
	buf.WriteString(ml.InfoHashString())
	
	// 添加dn参数
	if ml.DisplayName != "" {
		buf.WriteString("&dn=")
		buf.WriteString(url.QueryEscape(ml.DisplayName))
	}
	
	// 添加tr参数
	for _, tracker := range ml.Trackers {
		buf.WriteString("&tr=")
		buf.WriteString(url.QueryEscape(tracker))
	}
	
	// 添加ws参数
	for _, webseed := range ml.WebSeeds {
		buf.WriteString("&ws=")
		buf.WriteString(url.QueryEscape(webseed))
	}
	
	// 添加xl参数
	if ml.ExactLength > 0 {
		buf.WriteString("&xl=")
		buf.WriteString(strconv.FormatInt(ml.ExactLength, 10))
	}
	
	// 添加as参数
	if ml.ExactSource != "" {
		buf.WriteString("&as=")
		buf.WriteString(url.QueryEscape(ml.ExactSource))
	}
	
	// 添加xs参数
	if ml.ExactSource != "" {
		buf.WriteString("&xs=")
		buf.WriteString(url.QueryEscape(ml.ExactSource))
	}
	
	// 添加kt参数
	if len(ml.Keywords) > 0 {
		buf.WriteString("&kt=")
		buf.WriteString(url.QueryEscape(strings.Join(ml.Keywords, "+")))
	}
	
	return buf.String()
}

// HasTrackers 检查是否有trackers
func (ml *MagnetLink) HasTrackers() bool {
	return len(ml.Trackers) > 0
}

// HasWebSeeds 检查是否有web seeds
func (ml *MagnetLink) HasWebSeeds() bool {
	return len(ml.WebSeeds) > 0
}

// GetPrimaryTracker 获取主tracker
func (ml *MagnetLink) GetPrimaryTracker() string {
	if len(ml.Trackers) > 0 {
		return ml.Trackers[0]
	}
	return ""
}

// Validate 验证magnet链接
func (ml *MagnetLink) Validate() error {
	// 检查info哈希是否全为零
	var zeroHash [20]byte
	if ml.InfoHash == zeroHash {
		return errors.New("invalid info hash (all zeros)")
	}
	
	// 检查是否有trackers或web seeds
	if !ml.HasTrackers() && !ml.HasWebSeeds() {
		return errors.New("no trackers or web seeds specified")
	}
	
	return nil
}

// GenerateInfoHash 从数据生成info哈希
func GenerateInfoHash(data []byte) [20]byte {
	return sha1.Sum(data)
}

// MagnetFromTorrent 从torrent文件生成magnet链接
func MagnetFromTorrent(torrent *TorrentFile) *MagnetLink {
	magnet := &MagnetLink{
		InfoHash:    torrent.InfoHash,
		DisplayName: torrent.Info.Name,
	}
	
	// 添加trackers
	if torrent.Announce != "" {
		magnet.Trackers = append(magnet.Trackers, torrent.Announce)
	}
	
	// 添加tracker列表
	if len(torrent.AnnounceList) > 0 {
		for _, tier := range torrent.AnnounceList {
			magnet.Trackers = append(magnet.Trackers, tier...)
		}
	}
	
	// 设置文件长度
	if len(torrent.Files) == 1 {
		magnet.ExactLength = torrent.Files[0].Length
	} else {
		magnet.ExactLength = torrent.TotalSize()
	}
	
	return magnet
}

// IsMagnetLink 检查URL是否是magnet链接
func IsMagnetLink(url string) bool {
	return strings.HasPrefix(strings.ToLower(url), "magnet:")
}

// ExtractInfoHashFromURL 从URL中提取info哈希
func ExtractInfoHashFromURL(urlStr string) ([20]byte, error) {
	if !IsMagnetLink(urlStr) {
		return [20]byte{}, errors.New("not a magnet link")
	}
	
	magnet, err := ParseMagnetLink(urlStr)
	if err != nil {
		return [20]byte{}, err
	}
	
	return magnet.InfoHash, nil
}

// MagnetResolver 解析magnet链接并获取torrent文件
type MagnetResolver struct {
	// DHT客户端用于解析magnet链接，参考 aria2 的 DHTGetPeersCommand
	dhtClient *DHTClient
	
	// 缓存已解析的torrent文件
	cache map[[20]byte]*TorrentFile
}

// NewMagnetResolver 创建magnet解析器
func NewMagnetResolver() *MagnetResolver {
	return &MagnetResolver{
		dhtClient: NewDHTClient(),
		cache:     make(map[[20]byte]*TorrentFile),
	}
}

// Resolve 解析magnet链接
func (mr *MagnetResolver) Resolve(magnetURL string) (*TorrentFile, error) {
	// 解析magnet链接
	magnet, err := ParseMagnetLink(magnetURL)
	if err != nil {
		return nil, err
	}
	
	// 检查缓存
	if torrent, exists := mr.cache[magnet.InfoHash]; exists {
		return torrent, nil
	}
	
	// 从 DHT 或 tracker 获取 torrent 文件
	// 使用 MagnetDownloader 进行下载
	downloader := NewMagnetDownloader()
	return downloader.Download(magnetURL)
}

// CacheTorrent 缓存torrent文件
func (mr *MagnetResolver) CacheTorrent(torrent *TorrentFile) {
	mr.cache[torrent.InfoHash] = torrent
}

// GetCached 获取缓存的torrent文件
func (mr *MagnetResolver) GetCached(infoHash [20]byte) (*TorrentFile, bool) {
	torrent, exists := mr.cache[infoHash]
	return torrent, exists
}

// ClearCache 清除缓存
func (mr *MagnetResolver) ClearCache() {
	mr.cache = make(map[[20]byte]*TorrentFile)
}

// MagnetDownloader 专门处理magnet链接的下载器
type MagnetDownloader struct {
	resolver *MagnetResolver
}

// NewMagnetDownloader 创建magnet下载器
func NewMagnetDownloader() *MagnetDownloader {
	return &MagnetDownloader{
		resolver: NewMagnetResolver(),
	}
}

// Download 下载magnet链接
func (md *MagnetDownloader) Download(magnetURL string) (*TorrentFile, error) {
	// 解析magnet链接
	magnet, err := ParseMagnetLink(magnetURL)
	if err != nil {
		return nil, err
	}

	// 验证magnet链接
	if err := magnet.Validate(); err != nil {
		return nil, err
	}

	// 实际下载逻辑，参考 aria2 的 UTMetadata 扩展协议
	// 步骤：
	// 1. 通过 DHT 获取 torrent 文件
	// 2. 或者通过 tracker 获取 peer 列表并交换 torrent 文件
	// 3. 或者使用 web seeds 获取 torrent 文件

	log.Printf("MagnetDownloader[Download] 开始下载 magnet: %s", magnetURL)

	// 尝试方法 1: 通过 DHT 获取 peers
	if md.resolver.dhtClient != nil {
		log.Printf("MagnetDownloader[Download] 尝试通过 DHT 获取 peers")
		peers, err := md.resolver.dhtClient.GetPeers(magnet.InfoHash)
		if err == nil && len(peers) > 0 {
			log.Printf("MagnetDownloader[Download] 通过 DHT 获取到 %d 个 peers", len(peers))
			// 通过 UTMetadata 扩展协议从 peers 获取 torrent 文件
			// 参考 aria2 的 DefaultBtInteractive::doInteractionProcessing()
			torrent, err := md.downloadViaUTMetadata(magnet, peers)
			if err == nil {
				return torrent, nil
			}
			log.Printf("MagnetDownloader[Download] UTMetadata 下载失败: %v", err)
		}
	}

	// 尝试方法 2: 通过 tracker 获取 peers
	if len(magnet.Trackers) > 0 {
		log.Printf("MagnetDownloader[Download] 尝试通过 tracker 获取 peers")
		for _, tracker := range magnet.Trackers {
			peers, err := md.contactTracker(tracker, magnet.InfoHash)
			if err == nil && len(peers) > 0 {
				log.Printf("MagnetDownloader[Download] 从 tracker %s 获取到 %d 个 peers", tracker, len(peers))
				// 通过 UTMetadata 扩展协议从 peers 获取 torrent 文件
				torrent, err := md.downloadViaUTMetadata(magnet, peers)
				if err == nil {
					return torrent, nil
				}
				log.Printf("MagnetDownloader[Download] tracker UTMetadata 下载失败: %v", err)
			}
		}
	}

	// 尝试方法 3: 使用 web seeds
	if len(magnet.WebSeeds) > 0 {
		log.Printf("MagnetDownloader[Download] 尝试通过 web seeds 获取 torrent 文件")
		for _, webSeed := range magnet.WebSeeds {
			torrent, err := md.downloadViaWebSeed(webSeed)
			if err == nil {
				return torrent, nil
			}
			log.Printf("MagnetDownloader[Download] web seed %s 下载失败: %v", webSeed, err)
		}
	}

	return nil, errors.New("failed to download torrent file from magnet: no available method")
}

// GetTrackers 获取trackers
func (md *MagnetDownloader) GetTrackers(magnetURL string) ([]string, error) {
	magnet, err := ParseMagnetLink(magnetURL)
	if err != nil {
		return nil, err
	}
	
	return magnet.Trackers, nil
}

// GetWebSeeds 获取web seeds
func (md *MagnetDownloader) GetWebSeeds(magnetURL string) ([]string, error) {
	magnet, err := ParseMagnetLink(magnetURL)
	if err != nil {
		return nil, err
	}
	
	return magnet.WebSeeds, nil
}

// downloadViaUTMetadata 通过 UTMetadata 扩展协议从 peers 下载 torrent 文件
// 参考 aria2 的 DefaultBtInteractive::doInteractionProcessing()
func (md *MagnetDownloader) downloadViaUTMetadata(magnet *MagnetLink, peers []string) (*TorrentFile, error) {
	// 假设 metadataSize 已知（从握手消息获取）
	// 实际实现需要先与 peers 握手获取 metadata_size
	metadataSize := int64(len(magnet.InfoHash) * 1024) // 简化假设
	
	// 创建 UTMetadata 交换器
	exchange := NewUTMetadataExchange(magnet.InfoHash, metadataSize)
	
	// 遍历 peers 尝试获取 metadata
	for _, peer := range peers {
		// 简化实现：实际需要建立 BT 连接并进行握手
		// 参考 aria2 的 PeerInteractionCommand 和 HandshakeExtensionMessage
		log.Printf("MagnetDownloader[downloadViaUTMetadata] 尝试从 peer %s 获取 metadata", peer)
		
		// 获取缺失的 piece
		missingPieces := exchange.GetMissingPieces()
		if len(missingPieces) == 0 {
			// 所有 piece 已请求
			break
		}
		
		// 请求第一个缺失的 piece
		pieceIndex := missingPieces[0]
		_, err := exchange.CreateRequest(pieceIndex)
		if err != nil {
			log.Printf("MagnetDownloader[downloadViaUTMetadata] 创建请求失败: %v", err)
			continue
		}
		
		// 简化实现：实际需要发送消息并等待响应
		// 参考 aria2 的 UTMetadataRequestExtensionMessage 和 UTMetadataDataExtensionMessage
		log.Printf("MagnetDownloader[downloadViaUTMetadata] 请求 piece %d", pieceIndex)
		
		// 模拟收到数据消息
		dataMsg := &UTMetadataMessage{
			MsgType:   UTMetadataData,
			Piece:     pieceIndex,
			TotalSize: metadataSize,
			Data:      make([]byte, MetadataPieceSize),
		}
		
		if err := exchange.HandleDataMessage(dataMsg); err != nil {
			log.Printf("MagnetDownloader[downloadViaUTMetadata] 处理数据失败: %v", err)
			continue
		}
		
		// 检查是否完成
		metadata := exchange.GetMetadata()
		if metadata != nil {
			log.Printf("MagnetDownloader[downloadViaUTMetadata] metadata 下载完成")
			// 解析 torrent 文件
			torrent, err := ParseTorrentData(metadata)
			if err != nil {
				return nil, fmt.Errorf("parse torrent from metadata failed: %w", err)
			}
			return torrent, nil
		}
	}
	
	return nil, errors.New("failed to get metadata from all peers via UTMetadata")
}

// contactTracker 联系 tracker 获取 peers
// 参考 aria2 的 TrackerWatcherCommand::process() 和 DefaultBtAnnounce
func (md *MagnetDownloader) contactTracker(trackerURL string, infoHash [20]byte) ([]string, error) {
	// 简化实现：实际需要构建 HTTP/UDP tracker 请求
	// 参考 aria2 的 DefaultBtAnnounce::getAnnounceUrl()
	log.Printf("MagnetDownloader[contactTracker] 联系 tracker: %s", trackerURL)
	
	// 模拟返回 peers
	// 实际实现需要：
	// 1. 构建 announce URL（HTTP tracker）或 UDP 请求（UDP tracker）
	// 2. 发送请求
	// 3. 解析响应获取 peers
	// 4. 返回 peer 地址列表
	
	return []string{}, nil
}

// downloadViaWebSeed 通过 web seed 下载 torrent 文件
// 参考 aria2 的 web seeds 处理（作为普通 HTTP URL）
func (md *MagnetDownloader) downloadViaWebSeed(webSeedURL string) (*TorrentFile, error) {
	// 简化实现：实际需要发起 HTTP 请求
	// 参考 aria2 的 HttpRequestCommand 和 HttpResponseCommand
	log.Printf("MagnetDownloader[downloadViaWebSeed] 从 web seed 下载: %s", webSeedURL)
	
	// 模拟下载
	// 实际实现需要：
	// 1. 发起 HTTP GET 请求
	// 2. 接收响应数据
	// 3. 验证是否为有效的 torrent 文件
	// 4. 解析 torrent 文件
	
	return nil, errors.New("web seed download not fully implemented")
}

// DHTClient DHT客户端，参考 aria2 的 DHTRegistry 和相关组件
type DHTClient struct {
	running bool
	// DHT 节点 ID
	nodeID [20]byte
	// 路由表
	routingTable *DHTRoutingTable
	// 任务队列
	taskQueue chan DHTTask
	// 停止通道
	stopCh chan struct{}
}

// NewDHTClient 创建新的 DHT 客户端
func NewDHTClient() *DHTClient {
	// 生成随机节点 ID，参考 aria2 的 DHTNode::generateID()
	var nodeID [20]byte
	if _, err := rand.Read(nodeID[:]); err != nil {
		// 如果加密随机数生成失败，使用伪随机数
		log.Printf("DHTClient[NewDHTClient] crypto/rand 失败，使用伪随机数: %v", err)
		for i := range nodeID {
			nodeID[i] = byte(i)
		}
	}

	return &DHTClient{
		running:     false,
		nodeID:      nodeID,
		routingTable: NewDHTRoutingTable(),
		taskQueue:   make(chan DHTTask, 100),
		stopCh:      make(chan struct{}),
	}
}

// Start 启动 DHT 客户端
func (dc *DHTClient) Start() error {
	if dc.running {
		return nil
	}
	dc.running = true

	// 添加引导节点
	dc.bootstrapNodes()
	
	// 启动 DHT 任务处理循环
	go dc.taskLoop()
	
	// 启动路由表维护循环
	go dc.maintenanceLoop()

	return nil
}

// Stop 停止 DHT 客户端
func (dc *DHTClient) Stop() {
	if !dc.running {
		return
	}
	dc.running = false
	close(dc.stopCh)
}

// maintenanceLoop 路由表维护循环
func (dc *DHTClient) maintenanceLoop() {
	ticker := time.NewTicker(RefreshInterval)
	defer ticker.Stop()
	
	for {
		select {
		case <-dc.stopCh:
			return
		case <-ticker.C:
			// 定期刷新路由表
			dc.refreshRoutingTable()
		}
	}
}

// refreshRoutingTable 刷新路由表
func (dc *DHTClient) refreshRoutingTable() {
	// 获取所有节点
	allNodes := dc.routingTable.getAllNodes()
	
	if len(allNodes) == 0 {
		dc.bootstrapNodes()
		return
	}
	
	// 随机选择一些节点进行 ping
	for _, node := range allNodes {
		if err := dc.pingNode(node); err != nil {
			// ping 失败，节点可能已下线
			log.Printf("DHTClient[refreshRoutingTable] 节点 %s:%d ping 失败: %v", node.IP, node.Port, err)
		}
	}
}

// pingNode 向节点发送 ping 请求
func (dc *DHTClient) pingNode(node DHTNode) error {
	// 创建 ping 请求消息
	transactionID := generateTransactionID()
	
	msg := &DHTMessage{
		T: "q",
		Y: transactionID,
		Q: DHTMsgPing,
		A: map[string]interface{}{
			"id": dc.nodeID[:],
		},
	}
	
	// 编码消息
	data, err := encodeDHTMessage(msg)
	if err != nil {
		return fmt.Errorf("encode message failed: %w", err)
	}
	
	// 发送 UDP 请求
	conn, err := net.DialUDP("udp", nil, &net.UDPAddr{
		IP:   net.ParseIP(node.IP),
		Port: node.Port,
	})
	if err != nil {
		return fmt.Errorf("dial udp failed: %w", err)
	}
	defer conn.Close()
	
	// 设置超时
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	
	// 发送请求
	_, err = conn.Write(data)
	if err != nil {
		return fmt.Errorf("send request failed: %w", err)
	}
	
	// 接收响应
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		return fmt.Errorf("read response failed: %w", err)
	}
	
	// 解码响应
	resp, err := decodeDHTMessage(buf[:n])
	if err != nil {
		return fmt.Errorf("decode response failed: %w", err)
	}
	
	// 检查响应类型
	if resp.T != "r" {
		return fmt.Errorf("invalid response type: %s", resp.T)
	}
	
	// 更新节点的 ID（从响应中获取）
	if nodeID, ok := resp.R["id"]; ok {
		if nodeIDBytes, ok := nodeID.([]byte); ok && len(nodeIDBytes) == 20 {
			var id [20]byte
			copy(id[:], nodeIDBytes)
			// 更新路由表中的节点 ID
			dc.routingTable.addNode(DHTNode{
				ID:   id,
				IP:   node.IP,
				Port: node.Port,
			})
		}
	}
	
	return nil
}

// GetPeers 获取拥有指定 InfoHash 的 peers，参考 aria2 的 DHTGetPeersCommand
func (dc *DHTClient) GetPeers(infoHash [20]byte) ([]string, error) {
	// 如果路由表为空，先添加一些已知的 DHT 节点
	if len(dc.routingTable.getAllNodes()) == 0 {
		dc.bootstrapNodes()
	}
	
	// 从 K 桶中查找最接近 InfoHash 的节点
	closestNodes := dc.routingTable.getClosestNodes(infoHash)
	
	if len(closestNodes) == 0 {
		return nil, errors.New("no nodes in routing table")
	}
	
	log.Printf("DHTClient[GetPeers] 向 %d 个节点发送 get_peers 请求", len(closestNodes))
	
	// 向这些节点发送 get_peers 请求
	var peers []string
	var mu sync.Mutex
	
	// 使用 WaitGroup 等待所有请求完成
	var wg sync.WaitGroup
	
	for _, node := range closestNodes {
		wg.Add(1)
		go func(n DHTNode) {
			defer wg.Done()
			
			// 发送 get_peers 请求
			nodePeers, err := dc.sendGetPeersRequest(n, infoHash)
			if err != nil {
				log.Printf("DHTClient[GetPeers] 向节点 %s:%d 发送请求失败: %v", n.IP, n.Port, err)
				return
			}
			
			// 添加节点到路由表
			dc.routingTable.addNode(n)
			
			// 收集返回的 peers
			mu.Lock()
			peers = append(peers, nodePeers...)
			mu.Unlock()
		}(node)
	}
	
	wg.Wait()
	
	log.Printf("DHTClient[GetPeers] 找到 %d 个 peers", len(peers))
	return peers, nil
}

// sendGetPeersRequest 向单个节点发送 get_peers 请求
func (dc *DHTClient) sendGetPeersRequest(node DHTNode, infoHash [20]byte) ([]string, error) {
	// 创建 get_peers 请求消息
	// 参考 aria2 的 DHTGetPeersMessage::createMessage()
	transactionID := generateTransactionID()
	
	msg := &DHTMessage{
		T: "q",
		Y: transactionID,
		Q: DHTMsgGetPeers,
		A: map[string]interface{}{
			"id":        dc.nodeID[:],
			"info_hash": infoHash[:],
		},
	}
	
	// 编码消息
	data, err := encodeDHTMessage(msg)
	if err != nil {
		return nil, fmt.Errorf("encode message failed: %w", err)
	}
	
	// 发送 UDP 请求
	conn, err := net.DialUDP("udp", nil, &net.UDPAddr{
		IP:   net.ParseIP(node.IP),
		Port: node.Port,
	})
	if err != nil {
		return nil, fmt.Errorf("dial udp failed: %w", err)
	}
	defer conn.Close()
	
	// 设置超时
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	
	// 发送请求
	_, err = conn.Write(data)
	if err != nil {
		return nil, fmt.Errorf("send request failed: %w", err)
	}
	
	// 接收响应
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, fmt.Errorf("read response failed: %w", err)
	}
	
	// 解码响应
	resp, err := decodeDHTMessage(buf[:n])
	if err != nil {
		return nil, fmt.Errorf("decode response failed: %w", err)
	}
	
	// 解析 peers
	if resp.T != "r" {
		return nil, errors.New("invalid response type")
	}
	
	// 从响应中提取 peers
	return dc.extractPeersFromResponse(resp.R)
}

// extractPeersFromResponse 从响应中提取 peers
func (dc *DHTClient) extractPeersFromResponse(response map[string]interface{}) ([]string, error) {
	var peers []string
	
	// 检查是否有 peers 字段（紧凑格式）
	if peersData, ok := response["values"]; ok {
		// values 是一个列表，每个元素是一个 6 字节的紧凑 peer 信息
		if peerList, ok := peersData.([]interface{}); ok {
			for _, p := range peerList {
				if peerBytes, ok := p.([]byte); ok && len(peerBytes) == 6 {
					// 前 4 字节是 IP，后 2 字节是端口
					ip := net.IP(peerBytes[0:4]).String()
					port := binary.BigEndian.Uint16(peerBytes[4:6])
					peers = append(peers, fmt.Sprintf("%s:%d", ip, port))
				}
			}
		}
	}
	
	// 检查是否有 nodes 字段（用于继续查询）
	if nodesData, ok := response["nodes"]; ok {
		// nodes 是紧凑格式的节点信息
		if nodesBytes, ok := nodesData.([]byte); ok {
			// 每 26 字节是一个节点（20 字节 ID + 4 字节 IP + 2 字节端口）
			for i := 0; i+26 <= len(nodesBytes); i += 26 {
				var nodeID [20]byte
				copy(nodeID[:], nodesBytes[i:i+20])
				ip := net.IP(nodesBytes[i+20 : i+24]).String()
				port := binary.BigEndian.Uint16(nodesBytes[i+24 : i+26])
				
				// 将新节点添加到路由表
				dc.routingTable.addNode(DHTNode{
					ID:   nodeID,
					IP:   ip,
					Port: int(port),
				})
			}
		}
	}
	
	return peers, nil
}

// bootstrapNodes 添加已知的 DHT 引导节点
func (dc *DHTClient) bootstrapNodes() {
	// 一些公共的 DHT 引导节点
	bootstrapNodes := []DHTNode{
		{IP: "router.bittorrent.com", Port: 6881},
		{IP: "dht.transmissionbt.com", Port: 6881},
		{IP: "router.utorrent.com", Port: 6881},
		{IP: "dht.aelitis.com", Port: 6881},
	}
	
	for _, node := range bootstrapNodes {
		// 解析域名
		ips, err := net.LookupIP(node.IP)
		if err != nil {
			continue
		}
		
		for _, ip := range ips {
			if ip.To4() != nil {
				// 只使用 IPv4 地址
				dc.routingTable.addNode(DHTNode{
					ID:   [20]byte{}, // ID 会在 ping 后更新
					IP:   ip.String(),
					Port: node.Port,
				})
			}
		}
	}
}

// getAllNodes 获取路由表中的所有节点
func (rt *DHTRoutingTable) getAllNodes() []DHTNode {
	var nodes []DHTNode
	for _, bucket := range rt.buckets {
		if bucket == nil {
			continue
		}
		bucket.mu.RLock()
		nodes = append(nodes, bucket.nodes...)
		bucket.mu.RUnlock()
	}
	return nodes
}

// generateTransactionID 生成随机的事务 ID
func generateTransactionID() string {
	b := make([]byte, 2)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// AnnouncePeer 发布 peer 信息，参考 aria2 的 DHTAnnouncePeerMessage
func (dc *DHTClient) AnnouncePeer(infoHash [20]byte, port int) error {
	// 如果路由表为空，先添加一些已知的 DHT 节点
	if len(dc.routingTable.getAllNodes()) == 0 {
		dc.bootstrapNodes()
	}
	
	// 先执行 get_peers 获取 token
	_, err := dc.GetPeers(infoHash)
	if err != nil {
		log.Printf("DHTClient[AnnouncePeer] get_peers 失败: %v", err)
		// 继续尝试，可能已经有节点信息
	}
	
	// 获取 K 个最接近的节点
	closestNodes := dc.routingTable.getClosestNodes(infoHash)
	
	if len(closestNodes) == 0 {
		return errors.New("no nodes in routing table")
	}
	
	log.Printf("DHTClient[AnnouncePeer] 向 %d 个节点发送 announce_peer 请求", len(closestNodes))
	
	// 向这些节点发送 announce_peer 请求
	var wg sync.WaitGroup
	successCount := 0
	
	for _, node := range closestNodes {
		wg.Add(1)
		go func(n DHTNode) {
			defer wg.Done()
			
			// 发送 announce_peer 请求
			err := dc.sendAnnouncePeerRequest(n, infoHash, port)
			if err != nil {
				log.Printf("DHTClient[AnnouncePeer] 向节点 %s:%d 发送请求失败: %v", n.IP, n.Port, err)
				return
			}
			
			// 添加节点到路由表
			dc.routingTable.addNode(n)
			
			successCount++
		}(node)
	}
	
	wg.Wait()
	
	log.Printf("DHTClient[AnnouncePeer] 成功向 %d 个节点发布 peer 信息", successCount)
	return nil
}

// sendAnnouncePeerRequest 向单个节点发送 announce_peer 请求
func (dc *DHTClient) sendAnnouncePeerRequest(node DHTNode, infoHash [20]byte, port int) error {
	// 创建 announce_peer 请求消息
	// 参考 aria2 的 DHTAnnouncePeerMessage::createMessage()
	transactionID := generateTransactionID()
	
	// 注意：实际实现中需要从 get_peers 响应中获取 token
	// 这里使用一个简化的 token
	token := []byte("announce_token")
	
	msg := &DHTMessage{
		T: "q",
		Y: transactionID,
		Q: DHTMsgAnnouncePeer,
		A: map[string]interface{}{
			"id":        dc.nodeID[:],
			"info_hash": infoHash[:],
			"port":      port,
			"token":     token,
		},
	}
	
	// 编码消息
	data, err := encodeDHTMessage(msg)
	if err != nil {
		return fmt.Errorf("encode message failed: %w", err)
	}
	
	// 发送 UDP 请求
	conn, err := net.DialUDP("udp", nil, &net.UDPAddr{
		IP:   net.ParseIP(node.IP),
		Port: node.Port,
	})
	if err != nil {
		return fmt.Errorf("dial udp failed: %w", err)
	}
	defer conn.Close()
	
	// 设置超时
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	
	// 发送请求
	_, err = conn.Write(data)
	if err != nil {
		return fmt.Errorf("send request failed: %w", err)
	}
	
	// 接收响应
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		return fmt.Errorf("read response failed: %w", err)
	}
	
	// 解码响应
	resp, err := decodeDHTMessage(buf[:n])
	if err != nil {
		return fmt.Errorf("decode response failed: %w", err)
	}
	
	// 检查响应类型
	if resp.T != "r" {
		return fmt.Errorf("invalid response type: %s", resp.T)
	}
	
	// 检查是否有错误
	if len(resp.E) > 0 {
		return fmt.Errorf("announce_peer error: %v", resp.E)
	}
	
	return nil
}

// taskLoop DHT 任务处理循环
func (dc *DHTClient) taskLoop() {
	for {
		select {
		case <-dc.stopCh:
			return
		case task := <-dc.taskQueue:
			// 处理 DHT 任务
			task.Execute()
		}
	}
}

// DHTRoutingTable DHT 路由表，参考 aria2 的 DHTRoutingTable
type DHTRoutingTable struct {
	// K 桶
	buckets [160]*KBucket
}

// NewDHTRoutingTable 创建新的 DHT 路由表
func NewDHTRoutingTable() *DHTRoutingTable {
	rt := &DHTRoutingTable{
		buckets: [160]*KBucket{},
	}
	// 初始化所有 K 桶
	for i := 0; i < 160; i++ {
		rt.buckets[i] = &KBucket{
			nodes: make([]DHTNode, 0, K),
			lastSeen: make(map[string]time.Time),
		}
	}
	return rt
}

// getClosestNodes 获取最接近目标 ID 的节点
// 参考 aria2 的 DHTRoutingTable::getClosestNodes()
func (rt *DHTRoutingTable) getClosestNodes(targetID [20]byte) []DHTNode {
	var candidates []struct {
		node    DHTNode
		distance [20]byte
	}
	
	// 从所有 K 桶收集节点
	for _, bucket := range rt.buckets {
		if bucket == nil {
			continue
		}
		bucket.mu.RLock()
		for _, node := range bucket.nodes {
			distance := calculateXORDistance(node.ID, targetID)
			candidates = append(candidates, struct {
				node    DHTNode
				distance [20]byte
			}{node, distance})
		}
		bucket.mu.RUnlock()
	}
	
	// 按 XOR 距离排序
	sort.Slice(candidates, func(i, j int) bool {
		return compareXORDistance(candidates[i].distance, candidates[j].distance) < 0
	})
	
	// 返回最接近的 K 个节点
	result := make([]DHTNode, 0, K)
	for i := 0; i < len(candidates) && i < K; i++ {
		result = append(result, candidates[i].node)
	}
	
	return result
}

// calculateXORDistance 计算 XOR 距离
func calculateXORDistance(id1, id2 [20]byte) [20]byte {
	var distance [20]byte
	for i := 0; i < 20; i++ {
		distance[i] = id1[i] ^ id2[i]
	}
	return distance
}

// compareXORDistance 比较两个 XOR 距离
// 返回 -1 如果 d1 < d2，1 如果 d1 > d2，0 如果相等
func compareXORDistance(d1, d2 [20]byte) int {
	for i := 0; i < 20; i++ {
		if d1[i] < d2[i] {
			return -1
		} else if d1[i] > d2[i] {
			return 1
		}
	}
	return 0
}

// addNode 添加节点到路由表
// 参考 aria2 的 DHTRoutingTable::addNode()
func (rt *DHTRoutingTable) addNode(node DHTNode) {
	if node.Port <= 0 || node.Port > 65535 {
		return
	}
	
	// 计算节点 ID 与本地节点 ID 的前导零位数
	// 这决定了节点应该放入哪个 K 桶
	bucketIndex := rt.getBucketIndex(node.ID)
	
	if bucketIndex < 0 || bucketIndex >= 160 {
		return
	}
	
	bucket := rt.buckets[bucketIndex]
	bucket.mu.Lock()
	defer bucket.mu.Unlock()
	
	// 检查节点是否已存在
	nodeKey := fmt.Sprintf("%s:%d", node.IP, node.Port)
	for i, n := range bucket.nodes {
		if n.IP == node.IP && n.Port == node.Port {
			// 节点已存在，更新最后活跃时间
			bucket.lastSeen[nodeKey] = time.Now()
			// 将节点移到末尾（最近活跃）
			bucket.nodes = append(bucket.nodes[:i], bucket.nodes[i+1:]...)
			bucket.nodes = append(bucket.nodes, n)
			return
		}
	}
	
	// 如果 K 桶未满，直接添加
	if len(bucket.nodes) < K {
		bucket.nodes = append(bucket.nodes, node)
		bucket.lastSeen[nodeKey] = time.Now()
		return
	}
	
	// K 桶已满，检查是否有过期节点
	now := time.Now()
	var expiredIndex = -1
	for i, n := range bucket.nodes {
		key := fmt.Sprintf("%s:%d", n.IP, n.Port)
		if lastSeen, ok := bucket.lastSeen[key]; ok {
			if now.Sub(lastSeen) > NodeExpireTime {
				expiredIndex = i
				break
			}
		}
	}
	
	// 如果有过期节点，替换它
	if expiredIndex >= 0 {
		oldKey := fmt.Sprintf("%s:%d", bucket.nodes[expiredIndex].IP, bucket.nodes[expiredIndex].Port)
		delete(bucket.lastSeen, oldKey)
		bucket.nodes[expiredIndex] = node
		bucket.lastSeen[nodeKey] = time.Now()
		return
	}
	
	// 没有过期节点，丢弃新节点（实际实现中可以 ping 最旧的节点）
}

// getBucketIndex 计算节点 ID 应该放入哪个 K 桶
func (rt *DHTRoutingTable) getBucketIndex(nodeID [20]byte) int {
	// 这是一个简化实现，实际应该使用本地节点 ID
	// 这里假设本地节点 ID 是全 0
	for i := 0; i < 160; i++ {
		byteIndex := i / 8
		bitIndex := 7 - (i % 8)
		if (nodeID[byteIndex] & (1 << bitIndex)) != 0 {
			return i
		}
	}
	return 159
}

// KBucket K 桶
type KBucket struct {
	mu       sync.RWMutex
	nodes    []DHTNode
	lastSeen map[string]time.Time // 节点最后活跃时间
}

const (
	// K 桶的最大节点数
	K = 8
	// 节点过期时间
	NodeExpireTime = 15 * time.Minute
	// 刷新 K 桶的时间间隔
	RefreshInterval = 15 * time.Minute
)

// DHTNode DHT 节点
type DHTNode struct {
	ID   [20]byte
	IP   string
	Port int
}

// DHTMessage DHT 消息
type DHTMessage struct {
	T string // 消息类型: "q" (请求) 或 "r" (响应)
	Y string // 事务 ID
	Q string // 请求方法名: "ping", "find_node", "get_peers", "announce_peer"
	A map[string]interface{} // 请求参数
	R map[string]interface{} // 响应参数
	E [2]interface{} // 错误信息 [错误码, 错误描述]
}

// DHT 消息类型

const (

	DHTMsgPing         = "ping"

	DHTMsgFindNode     = "find_node"

	DHTMsgGetPeers     = "get_peers"

	DHTMsgAnnouncePeer = "announce_peer"

)



// encodeDHTMessage 编码 DHT 消息为 bencode 格式

// 参考 aria2 的 DHTMessage::createMessage()

func encodeDHTMessage(msg *DHTMessage) ([]byte, error) {

	dict := make(map[string]interface{})

	dict["t"] = msg.Y

	dict["y"] = msg.T

	

	if msg.T == "q" {

		// 请求消息

		dict["q"] = msg.Q

		dict["a"] = msg.A

	} else if msg.T == "r" {

		// 响应消息

		dict["r"] = msg.R

	} else if msg.T == "e" {

		// 错误消息

		dict["e"] = msg.E[:]

	}

	

	// 简化的 bencode 编码

	return encodeBencode(dict)

}



// decodeDHTMessage 解码 bencode 格式的 DHT 消息

func decodeDHTMessage(data []byte) (*DHTMessage, error) {

	dict, err := decodeBencode(data)

	if err != nil {

		return nil, err

	}

	

	msg := &DHTMessage{}

	

	// 解析事务 ID

	if t, ok := dict["t"].(string); ok {

		msg.Y = t

	}

	

	// 解析消息类型

	if y, ok := dict["y"].(string); ok {

		msg.T = y

	}

	

	// 解析请求方法名

	if q, ok := dict["q"].(string); ok {

		msg.Q = q

	}

	

	// 解析请求参数

	if a, ok := dict["a"].(map[string]interface{}); ok {

		msg.A = a

	}

	

	// 解析响应参数

	if r, ok := dict["r"].(map[string]interface{}); ok {

		msg.R = r

	}

	

	// 解析错误信息

	if e, ok := dict["e"].([]interface{}); ok && len(e) >= 2 {

		msg.E = [2]interface{}{e[0], e[1]}

	}

	

	return msg, nil

}



// 简化的 bencode 编码

func encodeBencode(v interface{}) ([]byte, error) {

	switch val := v.(type) {

	case string:

		return []byte(fmt.Sprintf("%d:%s", len(val), val)), nil

	case int:

		return []byte(fmt.Sprintf("i%de", val)), nil

	case []byte:

		return []byte(fmt.Sprintf("%d:%s", len(val), string(val))), nil

	case map[string]interface{}:

		var result []byte

		result = append(result, 'd')

		for key, value := range val {

			keyBytes, err := encodeBencode(key)

			if err != nil {

				return nil, err

			}

			valueBytes, err := encodeBencode(value)

			if err != nil {

				return nil, err

			}

			result = append(result, keyBytes...)

			result = append(result, valueBytes...)

		}

		result = append(result, 'e')

		return result, nil

	case []interface{}:

		var result []byte

		result = append(result, 'l')

		for _, item := range val {

			itemBytes, err := encodeBencode(item)

			if err != nil {

				return nil, err

			}

			result = append(result, itemBytes...)

		}

		result = append(result, 'e')

		return result, nil

	default:

		return nil, fmt.Errorf("unsupported type for bencode encoding: %T", v)

	}

}



// 简化的 bencode 解码

func decodeBencode(data []byte) (map[string]interface{}, error) {

	if len(data) == 0 || data[0] != 'd' {

		return nil, errors.New("invalid bencode dictionary")

	}

	

	result := make(map[string]interface{})

	i := 1

	

	for i < len(data) && data[i] != 'e' {

		// 解析 key (string)

		if data[i] < '0' || data[i] > '9' {

			return nil, errors.New("expected string key")

		}

		

		colonIndex := -1

		for j := i; j < len(data); j++ {

			if data[j] == ':' {

				colonIndex = j

				break

			}

		}

		if colonIndex == -1 {

			return nil, errors.New("invalid string format")

		}

		

		keyLen := 0

		fmt.Sscanf(string(data[i:colonIndex]), "%d", &keyLen)

		keyStart := colonIndex + 1

		keyEnd := keyStart + keyLen

		if keyEnd > len(data) {

			return nil, errors.New("string length exceeds data")

		}

		

		key := string(data[keyStart:keyEnd])

		i = keyEnd

		

		// 解析 value

		if data[i] == 'd' {

			// 嵌套字典 - 简化处理，返回空字典

			result[key] = make(map[string]interface{})

			// 跳过到字典结束

			depth := 1

			for i < len(data) && depth > 0 {

				i++

				if data[i] == 'd' {

					depth++

				} else if data[i] == 'e' {

					depth--

				}

			}

			i++

		} else if data[i] == 'l' {

			// 列表 - 简化处理，返回空列表

			result[key] = []interface{}{}

			// 跳过到列表结束

			depth := 1

			for i < len(data) && depth > 0 {

				i++

				if data[i] == 'l' {

					depth++

				} else if data[i] == 'e' {

					depth--

				}

			}

			i++

		} else if data[i] == 'i' {

			// 整数

			i++

			endIndex := -1

			for j := i; j < len(data); j++ {

				if data[j] == 'e' {

					endIndex = j

					break

				}

			}

			if endIndex == -1 {

				return nil, errors.New("invalid integer format")

			}

			var intVal int

			fmt.Sscanf(string(data[i:endIndex]), "%d", &intVal)

			result[key] = intVal

			i = endIndex + 1

		} else if data[i] >= '0' && data[i] <= '9' {

			// 字符串

			colonIndex = -1

			for j := i; j < len(data); j++ {

				if data[j] == ':' {

					colonIndex = j

					break

				}

			}

			if colonIndex == -1 {

				return nil, errors.New("invalid string format")

			}

			

			strLen := 0

			fmt.Sscanf(string(data[i:colonIndex]), "%d", &strLen)

			strStart := colonIndex + 1

			strEnd := strStart + strLen

			if strEnd > len(data) {

				return nil, errors.New("string length exceeds data")

			}

			

			result[key] = string(data[strStart:strEnd])

			i = strEnd

		} else {

			return nil, fmt.Errorf("unexpected character: %c", data[i])

		}

	}

	

	return result, nil

}



// DHTTask DHT 任务接口

type DHTTask interface {

	Execute() error

}