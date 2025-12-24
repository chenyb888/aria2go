// Package bt 提供BitTorrent协议支持
package bt

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"sync"
	"time"
)

// PeerManagerConfig Peer管理器配置
type PeerManagerConfig struct {
	// 连接设置
	MaxConnections     int           // 最大连接数
	MaxPeerQueueSize   int           // Peer队列最大大小
	ConnectTimeout     time.Duration // 连接超时
	HandshakeTimeout   time.Duration // 握手超时
	KeepAliveInterval  time.Duration // 保活间隔
	
	// 选择策略
	PreferEncrypted    bool // 优先选择加密peer
	PreferUTP          bool // 优先选择UTP peer
	MinResponseTime    time.Duration // 最小响应时间
	
	// 黑名单设置
	BlacklistDuration  time.Duration // 黑名单持续时间
	MaxFailures        int           // 最大失败次数
}

// DefaultPeerManagerConfig 默认Peer管理器配置
func DefaultPeerManagerConfig() PeerManagerConfig {
	return PeerManagerConfig{
		MaxConnections:     50,
		MaxPeerQueueSize:   1000,
		ConnectTimeout:     10 * time.Second,
		HandshakeTimeout:   5 * time.Second,
		KeepAliveInterval:  2 * time.Minute,
		PreferEncrypted:    true,
		MinResponseTime:    100 * time.Millisecond,
		BlacklistDuration:  5 * time.Minute,
		MaxFailures:        3,
	}
}

// PeerStatus Peer状态
type PeerStatus struct {
	IP           net.IP
	Port         uint16
	Connected    bool
	Choked       bool
	Interested   bool
	DownloadRate float64 // 下载速率 (KB/s)
	UploadRate   float64 // 上传速率 (KB/s)
	LastActive   time.Time
	Failures     int
	Blacklisted  bool
	BlacklistedUntil time.Time
}

// PeerManager 管理peer连接
type PeerManager struct {
	infoHash  [20]byte
	peerID    [20]byte
	config    PeerManagerConfig
	
	// Peer存储
	peers        map[string]*Peer           // 所有peer
	connected    map[string]*Peer           // 已连接的peer
	connecting   map[string]bool            // 正在连接的peer
	disconnected map[string]*PeerStatus     // 断开连接的peer状态
	blacklist    map[string]time.Time       // 黑名单
	
	// Peer队列
	peerQueue    chan *PeerInfo             // 等待连接的peer队列
	trackerPeers chan []PeerInfo            // tracker返回的peer列表
	
	// 统计信息
	stats        PeerManagerStats
	
	// 待处理请求
	pendingRequests map[string]*PendingRequest // 待处理的块请求
	
	// 锁和同步
	mu           sync.RWMutex
	wg           sync.WaitGroup
	ctx          context.Context
	cancel       context.CancelFunc
	
	// 事件通道
	eventCh      chan PeerEvent
}

// PeerManagerStats Peer管理器统计信息
type PeerManagerStats struct {
	TotalPeers      int
	ConnectedPeers  int
	ConnectingPeers int
	Blacklisted     int
	TotalDownloads  int64
	TotalUploads    int64
	FailedConnects  int
}

// PendingRequest 待处理的块请求
type PendingRequest struct {
	ch       chan []byte      // 数据通道
	peer     *Peer            // 发送请求的peer
	deadline time.Time        // 超时时间
	canceled bool             // 是否已取消
}

// PeerEvent Peer事件
type PeerEvent struct {
	Type   PeerEventType
	Peer   *Peer
	PeerInfo PeerInfo
	Error  error
}

// PeerEventType Peer事件类型
type PeerEventType int

const (
	PeerConnected PeerEventType = iota
	PeerDisconnected
	PeerChoked
	PeerUnchoked
	PeerInterested
	PeerNotInterested
	PeerHavePiece
	PeerBitfieldUpdated
	PeerBlacklisted
)

// NewPeerManager 创建新的Peer管理器
func NewPeerManager(infoHash [20]byte, peerID [20]byte, config PeerManagerConfig) *PeerManager {
	ctx, cancel := context.WithCancel(context.Background())
	
	pm := &PeerManager{
		infoHash:        infoHash,
		peerID:          peerID,
		config:          config,
		peers:           make(map[string]*Peer),
		connected:       make(map[string]*Peer),
		connecting:      make(map[string]bool),
		disconnected:    make(map[string]*PeerStatus),
		blacklist:       make(map[string]time.Time),
		pendingRequests: make(map[string]*PendingRequest),
		peerQueue:       make(chan *PeerInfo, config.MaxPeerQueueSize),
		trackerPeers:    make(chan []PeerInfo, 10),
		ctx:             ctx,
		cancel:          cancel,
		eventCh:         make(chan PeerEvent, 100),
	}
	
	// 启动worker
	pm.startWorkers()
	
	return pm
}

// startWorkers 启动worker goroutine
func (pm *PeerManager) startWorkers() {
	// 启动连接worker
	for i := 0; i < pm.config.MaxConnections/2; i++ {
		pm.wg.Add(1)
		go pm.connectionWorker(i)
	}
	
	// 启动peer处理worker
	pm.wg.Add(1)
	go pm.peerProcessor()
	
	// 启动统计worker
	pm.wg.Add(1)
	go pm.statsWorker()
	
	// 启动黑名单清理worker
	pm.wg.Add(1)
	go pm.blacklistCleaner()
}

// connectionWorker 连接worker
func (pm *PeerManager) connectionWorker(id int) {
	defer pm.wg.Done()
	
	for {
		select {
		case <-pm.ctx.Done():
			return
		case peerInfo := <-pm.peerQueue:
			pm.connectToPeer(peerInfo)
		}
	}
}

// peerProcessor 处理tracker返回的peer列表
func (pm *PeerManager) peerProcessor() {
	defer pm.wg.Done()
	
	for {
		select {
		case <-pm.ctx.Done():
			return
		case peerInfos := <-pm.trackerPeers:
			pm.processTrackerPeers(peerInfos)
		}
	}
}

// statsWorker 统计worker
func (pm *PeerManager) statsWorker() {
	defer pm.wg.Done()
	
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	
	for {
		select {
		case <-pm.ctx.Done():
			return
		case <-ticker.C:
			pm.updateStats()
		}
	}
}

// blacklistCleaner 黑名单清理worker
func (pm *PeerManager) blacklistCleaner() {
	defer pm.wg.Done()
	
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	
	for {
		select {
		case <-pm.ctx.Done():
			return
		case <-ticker.C:
			pm.cleanBlacklist()
		}
	}
}

// AddPeerInfo 添加peer信息
func (pm *PeerManager) AddPeerInfo(peerInfo *PeerInfo) error {
	if peerInfo == nil {
		return errors.New("peer info is nil")
	}
	
	key := peerKey(peerInfo.IP, peerInfo.Port)
	
	pm.mu.RLock()
	
	// 检查是否在黑名单中
	if blacklistedUntil, exists := pm.blacklist[key]; exists {
		if time.Now().Before(blacklistedUntil) {
			pm.mu.RUnlock()
			return fmt.Errorf("peer %s is blacklisted until %v", key, blacklistedUntil)
		}
		// 黑名单已过期，移除
		delete(pm.blacklist, key)
	}
	
	// 检查是否已存在
	if _, exists := pm.peers[key]; exists {
		pm.mu.RUnlock()
		return nil // 已存在，静默返回
	}
	
	pm.mu.RUnlock()
	
	// 添加到队列
	select {
	case pm.peerQueue <- peerInfo:
		return nil
	default:
		return errors.New("peer queue is full")
	}
}

// AddTrackerPeers 添加tracker返回的peer列表
func (pm *PeerManager) AddTrackerPeers(peerInfos []PeerInfo) {
	select {
	case pm.trackerPeers <- peerInfos:
		// 成功添加到通道
	default:
		// 通道满，丢弃一些peer
		if len(peerInfos) > 10 {
			pm.trackerPeers <- peerInfos[:10]
		}
	}
}

// connectToPeer 连接到peer
func (pm *PeerManager) connectToPeer(peerInfo *PeerInfo) {
	key := peerKey(peerInfo.IP, peerInfo.Port)
	
	// 标记为正在连接
	pm.mu.Lock()
	if pm.connecting[key] {
		pm.mu.Unlock()
		return // 已经在连接中
	}
	
	// 检查连接数限制
	if len(pm.connected) >= pm.config.MaxConnections {
		pm.mu.Unlock()
		return // 已达到最大连接数
	}
	
	pm.connecting[key] = true
	pm.mu.Unlock()
	
	// 连接完成后清除连接状态
	defer func() {
		pm.mu.Lock()
		delete(pm.connecting, key)
		pm.mu.Unlock()
	}()
	
	// 创建peer
	peer := NewPeer(peerInfo.IP, peerInfo.Port)
	
	// 连接peer
	err := peer.Connect(pm.infoHash, pm.peerID, pm.config.ConnectTimeout)
	if err != nil {
		pm.handleConnectFailure(peerInfo, err)
		return
	}
	
	// 连接成功
	pm.mu.Lock()
	pm.peers[key] = peer
	pm.connected[key] = peer
	pm.stats.TotalPeers++
	pm.stats.ConnectedPeers++
	pm.mu.Unlock()
	
	// 发送连接成功事件
	pm.eventCh <- PeerEvent{
		Type:   PeerConnected,
		Peer:   peer,
		PeerInfo: *peerInfo,
	}
	
	// 启动peer监控
	pm.wg.Add(1)
	go pm.monitorPeer(peer)
}

// handleConnectFailure 处理连接失败
func (pm *PeerManager) handleConnectFailure(peerInfo *PeerInfo, err error) {
	key := peerKey(peerInfo.IP, peerInfo.Port)
	
	pm.mu.Lock()
	defer pm.mu.Unlock()
	
	// 更新失败统计
	pm.stats.FailedConnects++
	
	// 更新peer状态
	status, exists := pm.disconnected[key]
	if !exists {
		status = &PeerStatus{
			IP:   peerInfo.IP,
			Port: peerInfo.Port,
		}
		pm.disconnected[key] = status
	}
	
	status.Failures++
	status.LastActive = time.Now()
	
	// 检查是否需要加入黑名单
	if status.Failures >= pm.config.MaxFailures {
		blacklistedUntil := time.Now().Add(pm.config.BlacklistDuration)
		pm.blacklist[key] = blacklistedUntil
		status.Blacklisted = true
		status.BlacklistedUntil = blacklistedUntil
		pm.stats.Blacklisted++
		
		// 发送黑名单事件
		pm.eventCh <- PeerEvent{
			Type:   PeerBlacklisted,
			PeerInfo: *peerInfo,
			Error:  err,
		}
	}
}

// monitorPeer 监控peer连接
func (pm *PeerManager) monitorPeer(peer *Peer) {
	defer pm.wg.Done()
	defer pm.handlePeerDisconnect(peer)
	
	// 获取错误通道
	errorCh := peer.GetErrors()
	
	// 定时发送keep-alive
	keepAliveTicker := time.NewTicker(pm.config.KeepAliveInterval)
	defer keepAliveTicker.Stop()
	
	for {
		select {
		case <-pm.ctx.Done():
			return
			
		case err := <-errorCh:
			if err != nil {
				// peer错误，断开连接
				return
			}
			
		case <-keepAliveTicker.C:
			// 发送keep-alive
			if err := peer.SendKeepAlive(); err != nil {
				return
			}
			
		case msg := <-peer.GetMessages():
			// 处理peer消息
			pm.handlePeerMessage(peer, msg)
		}
	}
}

// handlePeerMessage 处理peer消息
func (pm *PeerManager) handlePeerMessage(peer *Peer, msg Message) {
	
	switch msg.Type {
	case MsgChoke:
		pm.mu.Lock()
		peer.choked = true
		pm.mu.Unlock()
		
		pm.eventCh <- PeerEvent{
			Type: PeerChoked,
			Peer: peer,
		}
		
	case MsgUnchoke:
		pm.mu.Lock()
		peer.choked = false
		pm.mu.Unlock()
		
		pm.eventCh <- PeerEvent{
			Type: PeerUnchoked,
			Peer: peer,
		}
		
	case MsgInterested:
		pm.mu.Lock()
		peer.interested = true
		pm.mu.Unlock()
		
		pm.eventCh <- PeerEvent{
			Type: PeerInterested,
			Peer: peer,
		}
		
	case MsgNotInterested:
		pm.mu.Lock()
		peer.interested = false
		pm.mu.Unlock()
		
		pm.eventCh <- PeerEvent{
			Type: PeerNotInterested,
			Peer: peer,
		}
		
	case MsgHave:
		pm.eventCh <- PeerEvent{
			Type: PeerHavePiece,
			Peer: peer,
		}
		
	case MsgBitfield:
		pm.eventCh <- PeerEvent{
			Type: PeerBitfieldUpdated,
			Peer: peer,
		}
	
	case MsgPiece:
		// 处理piece数据消息
		if msg.Index >= 0 && msg.Begin >= 0 && msg.Data != nil {
			key := fmt.Sprintf("%d:%d", msg.Index, msg.Begin)
			pm.mu.Lock()
			if req, exists := pm.pendingRequests[key]; exists && !req.canceled {
				// 发送数据到等待的通道
				select {
				case req.ch <- msg.Data:
					// 成功发送数据，关闭通道防止泄漏
					close(req.ch)
				default:
					// 通道已满，可能请求者已超时，关闭通道并清理
					close(req.ch)
				}
				// 删除待处理请求
				delete(pm.pendingRequests, key)
			} else if req != nil && req.canceled {
				// 请求已取消，清理资源
				close(req.ch)
				delete(pm.pendingRequests, key)
			}
			pm.mu.Unlock()
		}
	
	case MsgRequest:
		// 其他peer请求数据，暂不处理（未来实现上传）
		// 可以记录日志或忽略
		_ = msg
	}
}

// handlePeerDisconnect 处理peer断开连接
func (pm *PeerManager) handlePeerDisconnect(peer *Peer) {
	key := peerKey(peer.IP, peer.Port)
	
	pm.mu.Lock()
	defer pm.mu.Unlock()
	
	// 从已连接列表中移除
	delete(pm.connected, key)
	pm.stats.ConnectedPeers--
	
	// 保存peer状态
	status := &PeerStatus{
		IP:         peer.IP,
		Port:       peer.Port,
		Connected:  false,
		Choked:     peer.IsChoked(),
		Interested: peer.IsInterested(),
		LastActive: time.Now(),
	}
	
	pm.disconnected[key] = status
	
	// 发送断开连接事件
	pm.eventCh <- PeerEvent{
		Type: PeerDisconnected,
		Peer: peer,
	}
	
	// 关闭peer连接
	peer.Close()
}

// processTrackerPeers 处理tracker返回的peer列表
func (pm *PeerManager) processTrackerPeers(peerInfos []PeerInfo) {
	// 过滤和排序peer
	filteredPeers := pm.filterAndSortPeers(peerInfos)
	
	// 添加到连接队列
	for i := range filteredPeers {
		pm.AddPeerInfo(&filteredPeers[i])
	}
}

// filterAndSortPeers 过滤和排序peer
func (pm *PeerManager) filterAndSortPeers(peerInfos []PeerInfo) []PeerInfo {
	var filtered []PeerInfo
	
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	
	for _, peerInfo := range peerInfos {
		key := peerKey(peerInfo.IP, peerInfo.Port)
		
		// 检查是否在黑名单中
		if blacklistedUntil, exists := pm.blacklist[key]; exists {
			if time.Now().Before(blacklistedUntil) {
				continue // 跳过黑名单中的peer
			}
		}
		
		// 检查是否已连接或正在连接
		if _, exists := pm.peers[key]; exists {
			continue // 已存在
		}
		
		if _, exists := pm.connecting[key]; exists {
			continue // 正在连接中
		}
		
		filtered = append(filtered, peerInfo)
	}
	
	// 排序：优先选择没有peer ID的（可能是新peer）
	sort.Slice(filtered, func(i, j int) bool {
		// 检查peer ID是否全零
		iZero := isZeroPeerID(filtered[i].PeerID)
		jZero := isZeroPeerID(filtered[j].PeerID)
		
		if iZero && !jZero {
			return true
		}
		if !iZero && jZero {
			return false
		}
		
		// 其他排序标准可以在这里添加
		return true
	})
	
	return filtered
}

// isZeroPeerID 检查peer ID是否全零
func isZeroPeerID(peerID [20]byte) bool {
	for _, b := range peerID {
		if b != 0 {
			return false
		}
	}
	return true
}

// updateStats 更新统计信息
func (pm *PeerManager) updateStats() {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	
	// 更新速率统计
	for _, peer := range pm.connected {
		// 这里可以添加速率计算逻辑
		// 目前使用简单实现
		// peer.Downloaded, peer.Uploaded 可以用于计算速率
		_ = peer // 避免未使用变量错误
	}
}

// cleanBlacklist 清理黑名单
func (pm *PeerManager) cleanBlacklist() {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	
	now := time.Now()
	for key, blacklistedUntil := range pm.blacklist {
		if now.After(blacklistedUntil) {
			delete(pm.blacklist, key)
			
			// 更新peer状态
			if status, exists := pm.disconnected[key]; exists {
				status.Blacklisted = false
			}
			
			pm.stats.Blacklisted--
		}
	}
}

// GetConnectedPeers 获取已连接的peer列表
func (pm *PeerManager) GetConnectedPeers() []*Peer {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	
	peers := make([]*Peer, 0, len(pm.connected))
	for _, peer := range pm.connected {
		peers = append(peers, peer)
	}
	
	return peers
}

// GetPeerByPiece 获取拥有指定piece的peer
func (pm *PeerManager) GetPeerByPiece(pieceIndex int) []*Peer {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	
	var peers []*Peer
	for _, peer := range pm.connected {
		if peer.HasPiece(pieceIndex) {
			peers = append(peers, peer)
		}
	}
	
	return peers
}

// GetBestPeerForPiece 获取下载指定piece的最佳peer
func (pm *PeerManager) GetBestPeerForPiece(pieceIndex int) *Peer {
	peers := pm.GetPeerByPiece(pieceIndex)
	if len(peers) == 0 {
		return nil
	}
	
	// 简单实现：返回第一个未被阻塞的peer
	for _, peer := range peers {
		if !peer.IsChoked() && peer.IsConnected() {
			return peer
		}
	}
	
	// 如果没有未被阻塞的peer，返回第一个peer
	return peers[0]
}

// RemovePeer 移除peer
func (pm *PeerManager) RemovePeer(peer *Peer) {
	key := peerKey(peer.IP, peer.Port)
	
	pm.mu.Lock()
	defer pm.mu.Unlock()
	
	delete(pm.peers, key)
	delete(pm.connected, key)
	delete(pm.connecting, key)
	
	pm.stats.ConnectedPeers--
	
	peer.Close()
}

// Close 关闭Peer管理器
func (pm *PeerManager) Close() {
	// 发送取消信号
	pm.cancel()
	
	// 等待所有worker完成
	pm.wg.Wait()
	
	// 关闭所有peer连接
	pm.mu.Lock()
	defer pm.mu.Unlock()
	
	for _, peer := range pm.peers {
		peer.Close()
	}
	
	// 清空数据结构
	pm.peers = make(map[string]*Peer)
	pm.connected = make(map[string]*Peer)
	pm.connecting = make(map[string]bool)
	close(pm.eventCh)
}

// GetStats 获取统计信息
func (pm *PeerManager) GetStats() PeerManagerStats {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	
	return pm.stats
}

// GetEventChannel 获取事件通道
func (pm *PeerManager) GetEventChannel() <-chan PeerEvent {
	return pm.eventCh
}

// GetConfig 获取配置
func (pm *PeerManager) GetConfig() PeerManagerConfig {
	return pm.config
}

// SetConfig 更新配置
func (pm *PeerManager) SetConfig(config PeerManagerConfig) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	
	pm.config = config
}

// GetPeerStatus 获取peer状态
func (pm *PeerManager) GetPeerStatus(ip net.IP, port uint16) (*PeerStatus, bool) {
	key := peerKey(ip, port)
	
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	
	// 检查已连接的peer
	if peer, exists := pm.connected[key]; exists {
		return &PeerStatus{
			IP:           ip,
			Port:         port,
			Connected:    true,
			Choked:       peer.IsChoked(),
			Interested:   peer.IsInterested(),
			LastActive:   time.Now(),
		}, true
	}
	
	// 检查断开连接的peer
	if status, exists := pm.disconnected[key]; exists {
		return status, true
	}
	
	return nil, false
}

// GetActivePeerCount 获取活动peer数量
func (pm *PeerManager) GetActivePeerCount() int {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	
	return len(pm.connected)
}

// GetTotalPeerCount 获取总peer数量
func (pm *PeerManager) GetTotalPeerCount() int {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	
	return len(pm.peers)
}

// peerKey 生成peer键
func peerKey(ip net.IP, port uint16) string {
	return fmt.Sprintf("%s:%d", ip.String(), port)
}

// IsPeerConnected 检查peer是否已连接
func (pm *PeerManager) IsPeerConnected(ip net.IP, port uint16) bool {
	key := peerKey(ip, port)
	
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	
	_, exists := pm.connected[key]
	return exists
}

// GetPeer 获取peer
func (pm *PeerManager) GetPeer(ip net.IP, port uint16) (*Peer, bool) {
	key := peerKey(ip, port)
	
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	
	peer, exists := pm.connected[key]
	return peer, exists
}

// RequestBlock 实现BlockRequester接口，请求一个数据块
func (pm *PeerManager) RequestBlock(pieceIndex int, offset int, size int) ([]byte, error) {
	// 生成请求键
	key := fmt.Sprintf("%d:%d", pieceIndex, offset)
	
	// 创建待处理请求
	req := &PendingRequest{
		ch:       make(chan []byte, 1),
		peer:     nil, // 稍后设置
		deadline: time.Now().Add(30 * time.Second), // 30秒超时
		canceled: false,
	}
	
	// 选择peer并发送请求
	var selectedPeer *Peer
	pm.mu.RLock()
	for _, peer := range pm.connected {
		peer.mu.RLock()
		isChoked := peer.choked
		peer.mu.RUnlock()
		
		if !isChoked {
			selectedPeer = peer
			break
		}
	}
	pm.mu.RUnlock()
	
	if selectedPeer == nil {
		return nil, errors.New("no available peer to request block")
	}
	
	req.peer = selectedPeer
	
	// 注册待处理请求
	pm.mu.Lock()
	pm.pendingRequests[key] = req
	pm.mu.Unlock()
	
	// 发送请求消息
	if err := selectedPeer.SendRequest(pieceIndex, offset, size); err != nil {
		pm.mu.Lock()
		close(req.ch)
		delete(pm.pendingRequests, key)
		pm.mu.Unlock()
		return nil, fmt.Errorf("send request failed: %w", err)
	}
	
	// 等待响应或超时
	select {
	case data := <-req.ch:
		// 成功收到数据，通道已由handlePeerMessage关闭
		return data, nil
	
	case <-time.After(30 * time.Second):
		// 超时
		pm.mu.Lock()
		if _, exists := pm.pendingRequests[key]; exists {
			req.canceled = true
			close(req.ch)
			delete(pm.pendingRequests, key)
		}
		pm.mu.Unlock()
		return nil, errors.New("request timeout")
	
	case <-pm.ctx.Done():
		// PeerManager关闭
		pm.mu.Lock()
		if _, exists := pm.pendingRequests[key]; exists {
			close(req.ch)
			delete(pm.pendingRequests, key)
		}
		pm.mu.Unlock()
		return nil, pm.ctx.Err()
	}
}

// CancelBlock 取消块请求
func (pm *PeerManager) CancelBlock(pieceIndex int, offset int) error {
	key := fmt.Sprintf("%d:%d", pieceIndex, offset)
	
	pm.mu.Lock()
	defer pm.mu.Unlock()
	
	if req, exists := pm.pendingRequests[key]; exists {
		req.canceled = true
		// 关闭通道以避免goroutine泄漏
		close(req.ch)
		delete(pm.pendingRequests, key)
	}
	
	return nil
}