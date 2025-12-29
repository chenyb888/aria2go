// Package bt 提供BitTorrent协议支持
package bt

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	mrand "math/rand"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Tracker 表示BitTorrent tracker
type Tracker struct {
	URL          string        // Tracker URL
	InfoHash     [20]byte      // info哈希
	PeerID       [20]byte      // peer ID
	Port         uint16        // 监听端口
	Uploaded     int64         // 已上传字节数
	Downloaded   int64         // 已下载字节数
	Left         int64         // 剩余字节数
	Compact      bool          // 是否使用compact格式
	NumWant      int           // 请求的peer数量
	Event        string        // 事件类型
	Key          string        // 客户端密钥
	TrackerID    string        // tracker ID
	NoPeerID     bool          // 是否不包含peer ID
	SupportCrypto bool         // 是否支持加密
	Interval      int64        // 下次请求间隔（秒）
	MinInterval   int64        // 最小间隔
	
	httpClient   *http.Client  // HTTP客户端
	udpConn      *net.UDPConn  // UDP连接
	
	mu           sync.RWMutex
}

// TrackerResponse Tracker响应
type TrackerResponse struct {
	FailureReason  string        `bencode:"failure reason,omitempty"`
	WarningMessage string        `bencode:"warning message,omitempty"`
	Interval       int64         `bencode:"interval"`          // 下次请求间隔（秒）
	MinInterval    int64         `bencode:"min interval,omitempty"` // 最小间隔
	TrackerID      string        `bencode:"tracker id,omitempty"`   // tracker ID
	Complete       int64         `bencode:"complete,omitempty"`     // 完成下载的peer数
	Incomplete     int64         `bencode:"incomplete,omitempty"`   // 未完成的peer数
	Peers          interface{}   `bencode:"peers"`             // peer列表
	Peers6         interface{}   `bencode:"peers6,omitempty"`  // IPv6 peer列表
}

// PeerInfo 从tracker获取的peer信息
type PeerInfo struct {
	IP       net.IP
	Port     uint16
	PeerID   [20]byte
}

// UDPTrackerResponse UDP tracker响应
type UDPTrackerResponse struct {
	Action        uint32
	TransactionID uint32
	Interval      uint32
	Leechers      uint32
	Seeders       uint32
	Peers         []PeerInfo
}

// NewTracker 创建新的tracker
func NewTracker(trackerURL string, infoHash [20]byte, peerID [20]byte, port uint16) *Tracker {
	return &Tracker{
		URL:        trackerURL,
		InfoHash:   infoHash,
		PeerID:     peerID,
		Port:       port,
		Compact:    true,
		NumWant:    50,
		Event:      "started",
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// Announce 向tracker宣告
func (t *Tracker) Announce(ctx context.Context, uploaded, downloaded, left int64) ([]PeerInfo, error) {
	t.mu.Lock()
	t.Uploaded = uploaded
	t.Downloaded = downloaded
	t.Left = left
	t.mu.Unlock()
	
	// 根据URL协议选择通信方式
	if strings.HasPrefix(t.URL, "http://") || strings.HasPrefix(t.URL, "https://") {
		return t.announceHTTP(ctx)
	} else if strings.HasPrefix(t.URL, "udp://") {
		return t.announceUDP(ctx)
	}
	
	return nil, fmt.Errorf("unsupported tracker protocol: %s", t.URL)
}

// announceHTTP 使用HTTP协议与tracker通信
func (t *Tracker) announceHTTP(ctx context.Context) ([]PeerInfo, error) {
	// 构建查询参数
	params := url.Values{}
	
	// 必需参数
	params.Set("info_hash", string(t.InfoHash[:]))
	params.Set("peer_id", string(t.PeerID[:]))
	params.Set("port", strconv.Itoa(int(t.Port)))
	params.Set("uploaded", strconv.FormatInt(t.Uploaded, 10))
	params.Set("downloaded", strconv.FormatInt(t.Downloaded, 10))
	params.Set("left", strconv.FormatInt(t.Left, 10))
	
	// 可选参数
	params.Set("compact", "1")
	params.Set("numwant", strconv.Itoa(t.NumWant))
	params.Set("event", t.Event)
	
	if t.Key != "" {
		params.Set("key", t.Key)
	}
	
	if t.TrackerID != "" {
		params.Set("trackerid", t.TrackerID)
	}
	
	if t.NoPeerID {
		params.Set("no_peer_id", "1")
	}
	
	if t.SupportCrypto {
		params.Set("supportcrypto", "1")
	}
	
	// 构建完整URL
	var announceURL string
	if strings.Contains(t.URL, "?") {
		announceURL = t.URL + "&" + params.Encode()
	} else {
		announceURL = t.URL + "?" + params.Encode()
	}
	
	// 发送HTTP GET请求
	req, err := http.NewRequestWithContext(ctx, "GET", announceURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}
	
	req.Header.Set("User-Agent", "aria2go/1.0")
	req.Header.Set("Accept", "text/plain, text/html")
	
	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request failed: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tracker returned status %d", resp.StatusCode)
	}
	
	// 读取响应体
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response failed: %w", err)
	}
	
	// 解析B编码响应
	return t.parseHTTPResponse(body)
}

// parseHTTPResponse 解析HTTP tracker响应
func (t *Tracker) parseHTTPResponse(data []byte) ([]PeerInfo, error) {
	// 解码B编码数据
	decoder := NewBDecoder(bytes.NewReader(data))
	decoded, err := decoder.Decode()
	if err != nil {
		return nil, fmt.Errorf("bdecode failed: %w", err)
	}
	
	dict, ok := decoded.(map[string]interface{})
	if !ok {
		return nil, errors.New("invalid tracker response format")
	}
	
	// 检查错误
	if failure, ok := dict["failure reason"].(string); ok {
		return nil, fmt.Errorf("tracker failure: %s", failure)
	}
	
	if warning, ok := dict["warning message"].(string); ok {
		// 警告信息，可以记录但继续处理
		fmt.Printf("Tracker warning: %s\n", warning)
	}
	
	// 解析interval
	if interval, ok := dict["interval"].(int64); ok {
		t.mu.Lock()
		t.Interval = interval
		t.mu.Unlock()
	}
	
	if minInterval, ok := dict["min interval"].(int64); ok {
		t.mu.Lock()
		t.MinInterval = minInterval
		t.mu.Unlock()
	}
	
	// 解析tracker ID
	if trackerID, ok := dict["tracker id"].(string); ok {
		t.mu.Lock()
		t.TrackerID = trackerID
		t.mu.Unlock()
	}
	
	// 解析peer列表
	peers, ok := dict["peers"]
	if !ok {
		return nil, errors.New("missing peers in tracker response")
	}
	
	return t.parsePeers(peers)
}

// parsePeers 解析peer列表
func (t *Tracker) parsePeers(peers interface{}) ([]PeerInfo, error) {
	var peerInfos []PeerInfo
	
	switch p := peers.(type) {
	case string:
		// compact格式: 每个peer 6字节 (4字节IP + 2字节端口)
		data := []byte(p)
		if len(data)%6 != 0 {
			return nil, fmt.Errorf("invalid compact peers data length: %d", len(data))
		}
		
		for i := 0; i < len(data); i += 6 {
			ip := net.IPv4(data[i], data[i+1], data[i+2], data[i+3])
			port := binary.BigEndian.Uint16(data[i+4 : i+6])
			
			peerInfos = append(peerInfos, PeerInfo{
				IP:   ip,
				Port: port,
			})
		}
		
	case []interface{}:
		// 非compact格式: peer字典列表
		for _, peer := range p {
			if peerDict, ok := peer.(map[string]interface{}); ok {
				ipStr, ok1 := peerDict["ip"].(string)
				port, ok2 := peerDict["port"].(int64)
				peerIDStr, ok3 := peerDict["peer id"].(string)
				
				if !ok1 || !ok2 {
					continue
				}
				
				// 解析IP
				ip := net.ParseIP(ipStr)
				if ip == nil {
					continue
				}
				
				peerInfo := PeerInfo{
					IP:   ip,
					Port: uint16(port),
				}
				
				// 解析peer ID
				if ok3 && len(peerIDStr) == 20 {
					copy(peerInfo.PeerID[:], peerIDStr)
				}
				
				peerInfos = append(peerInfos, peerInfo)
			}
		}
		
	default:
		return nil, fmt.Errorf("unsupported peers format: %T", peers)
	}
	
	return peerInfos, nil
}

// announceUDP 使用UDP协议与tracker通信
func (t *Tracker) announceUDP(ctx context.Context) ([]PeerInfo, error) {
	// 解析UDP地址
	udpURL := strings.TrimPrefix(t.URL, "udp://")
	hostPort := strings.Split(udpURL, "/")[0]
	
	udpAddr, err := net.ResolveUDPAddr("udp", hostPort)
	if err != nil {
		return nil, fmt.Errorf("resolve UDP address failed: %w", err)
	}
	
	// 创建UDP连接
	if t.udpConn == nil {
		localAddr, err := net.ResolveUDPAddr("udp", ":0")
		if err != nil {
			return nil, fmt.Errorf("resolve local address failed: %w", err)
		}
		
		conn, err := net.DialUDP("udp", localAddr, udpAddr)
		if err != nil {
			return nil, fmt.Errorf("dial UDP failed: %w", err)
		}
		
		t.udpConn = conn
	}
	
	// 发送连接请求
	connReq := make([]byte, 16)
	binary.BigEndian.PutUint64(connReq[0:8], 0x41727101980) // 协议ID
	binary.BigEndian.PutUint32(connReq[8:12], 0)            // 动作: connect
	binary.BigEndian.PutUint32(connReq[12:16], randUint32()) // 事务ID
	
	_, err = t.udpConn.Write(connReq)
	if err != nil {
		return nil, fmt.Errorf("send connect request failed: %w", err)
	}
	
	// 设置读取超时
	t.udpConn.SetReadDeadline(time.Now().Add(15 * time.Second))
	
	// 读取连接响应
	connResp := make([]byte, 16)
	n, err := t.udpConn.Read(connResp)
	if err != nil {
		return nil, fmt.Errorf("read connect response failed: %w", err)
	}
	
	if n < 16 {
		return nil, errors.New("short connect response")
	}
	
	// 验证连接响应
	respAction := binary.BigEndian.Uint32(connResp[0:4])
	respTransactionID := binary.BigEndian.Uint32(connResp[4:8])
	connectionID := binary.BigEndian.Uint64(connResp[8:16])
	
	if respAction != 0 || respTransactionID != binary.BigEndian.Uint32(connReq[12:16]) {
		return nil, errors.New("invalid connect response")
	}
	
	// 发送宣告请求
	announceReq := make([]byte, 98)
	binary.BigEndian.PutUint64(announceReq[0:8], connectionID)
	binary.BigEndian.PutUint32(announceReq[8:12], 1) // 动作: announce
	binary.BigEndian.PutUint32(announceReq[12:16], randUint32()) // 事务ID
	copy(announceReq[16:36], t.InfoHash[:])          // info哈希
	copy(announceReq[36:56], t.PeerID[:])            // peer ID
	binary.BigEndian.PutUint64(announceReq[56:64], uint64(t.Downloaded))
	binary.BigEndian.PutUint64(announceReq[64:72], uint64(t.Left))
	binary.BigEndian.PutUint64(announceReq[72:80], uint64(t.Uploaded))
	binary.BigEndian.PutUint32(announceReq[80:84], 2) // 事件: started
	binary.BigEndian.PutUint32(announceReq[84:88], 0) // IP地址 (0表示自动)
	binary.BigEndian.PutUint32(announceReq[88:92], randUint32()) // 密钥
	binary.BigEndian.PutUint32(announceReq[92:96], ^uint32(0)) // num_want (-1表示默认)
	binary.BigEndian.PutUint16(announceReq[96:98], t.Port) // 端口
	
	_, err = t.udpConn.Write(announceReq)
	if err != nil {
		return nil, fmt.Errorf("send announce request failed: %w", err)
	}
	
	// 读取宣告响应
	t.udpConn.SetReadDeadline(time.Now().Add(15 * time.Second))
	
	resp := make([]byte, 4096)
	n, err = t.udpConn.Read(resp)
	if err != nil {
		return nil, fmt.Errorf("read announce response failed: %w", err)
	}
	
	if n < 20 {
		return nil, errors.New("short announce response")
	}
	
	// 解析宣告响应
	return t.parseUDPResponse(resp[:n])
}

// parseUDPResponse 解析UDP tracker响应
func (t *Tracker) parseUDPResponse(data []byte) ([]PeerInfo, error) {
	if len(data) < 20 {
		return nil, errors.New("response too short")
	}
	
	action := binary.BigEndian.Uint32(data[0:4])
	transactionID := binary.BigEndian.Uint32(data[4:8])
	interval := binary.BigEndian.Uint32(data[8:12])
	leechers := binary.BigEndian.Uint32(data[12:16])
	seeders := binary.BigEndian.Uint32(data[16:20])
	
	// 标记未使用变量，避免编译错误
	_ = transactionID
	_ = leechers
	_ = seeders
	
	if action != 1 {
		return nil, fmt.Errorf("unexpected action: %d", action)
	}
	
	t.mu.Lock()
	t.Interval = int64(interval)
	t.mu.Unlock()
	
	// 解析peer列表
	var peerInfos []PeerInfo
	
	if len(data) > 20 {
		peerData := data[20:]
		
		// 每个peer 6字节 (4字节IP + 2字节端口)
		if len(peerData)%6 != 0 {
			return nil, fmt.Errorf("invalid peer data length: %d", len(peerData))
		}
		
		for i := 0; i < len(peerData); i += 6 {
			ip := net.IPv4(peerData[i], peerData[i+1], peerData[i+2], peerData[i+3])
			port := binary.BigEndian.Uint16(peerData[i+4 : i+6])
			
			peerInfos = append(peerInfos, PeerInfo{
				IP:   ip,
				Port: port,
			})
		}
	}
	
	return peerInfos, nil
}

// Scrape 获取tracker统计信息
func (t *Tracker) Scrape(ctx context.Context) (*ScrapeResponse, error) {
	if !strings.Contains(t.URL, "/announce") {
		return nil, errors.New("scrape only supported on announce URLs")
	}
	
	// 将announce URL转换为scrape URL
	scrapeURL := strings.Replace(t.URL, "/announce", "/scrape", 1)
	if scrapeURL == t.URL {
		// 转换失败，尝试其他方式
		scrapeURL = strings.TrimSuffix(t.URL, "announce") + "scrape"
	}
	
	// 构建查询参数
	params := url.Values{}
	params.Set("info_hash", string(t.InfoHash[:]))
	
	fullURL := scrapeURL
	if strings.Contains(scrapeURL, "?") {
		fullURL += "&" + params.Encode()
	} else {
		fullURL += "?" + params.Encode()
	}
	
	// 发送请求
	req, err := http.NewRequestWithContext(ctx, "GET", fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create scrape request failed: %w", err)
	}
	
	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send scrape request failed: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("scrape returned status %d", resp.StatusCode)
	}
	
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read scrape response failed: %w", err)
	}
	
	return t.parseScrapeResponse(body)
}

// ScrapeResponse Scrape响应
type ScrapeResponse struct {
	Files map[string]ScrapeFileInfo `bencode:"files"`
}

// ScrapeFileInfo 文件统计信息
type ScrapeFileInfo struct {
	Complete   int64 `bencode:"complete"`
	Downloaded int64 `bencode:"downloaded"`
	Incomplete int64 `bencode:"incomplete"`
	Name       string `bencode:"name,omitempty"`
}

// parseScrapeResponse 解析scrape响应
func (t *Tracker) parseScrapeResponse(data []byte) (*ScrapeResponse, error) {
	decoder := NewBDecoder(bytes.NewReader(data))
	decoded, err := decoder.Decode()
	if err != nil {
		return nil, fmt.Errorf("bdecode failed: %w", err)
	}
	
	dict, ok := decoded.(map[string]interface{})
	if !ok {
		return nil, errors.New("invalid scrape response format")
	}
	
	// 检查错误
	if failure, ok := dict["failure reason"].(string); ok {
		return nil, fmt.Errorf("scrape failure: %s", failure)
	}
	
	filesDict, ok := dict["files"].(map[string]interface{})
	if !ok {
		return nil, errors.New("missing files in scrape response")
	}
	
	response := &ScrapeResponse{
		Files: make(map[string]ScrapeFileInfo),
	}
	
	for infoHashStr, fileInfo := range filesDict {
		if fileDict, ok := fileInfo.(map[string]interface{}); ok {
			var scrapeFile ScrapeFileInfo
			
			if complete, ok := fileDict["complete"].(int64); ok {
				scrapeFile.Complete = complete
			}
			
			if downloaded, ok := fileDict["downloaded"].(int64); ok {
				scrapeFile.Downloaded = downloaded
			}
			
			if incomplete, ok := fileDict["incomplete"].(int64); ok {
				scrapeFile.Incomplete = incomplete
			}
			
			if name, ok := fileDict["name"].(string); ok {
				scrapeFile.Name = name
			}
			
			response.Files[infoHashStr] = scrapeFile
		}
	}
	
	return response, nil
}

// Close 关闭tracker连接
func (t *Tracker) Close() error {
	if t.udpConn != nil {
		return t.udpConn.Close()
	}
	return nil
}

// SetEvent 设置事件类型
func (t *Tracker) SetEvent(event string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	
	switch event {
	case "started", "stopped", "completed":
		t.Event = event
	default:
		t.Event = ""
	}
}

// GetInterval 获取请求间隔
func (t *Tracker) GetInterval() int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.Interval
}

// GetMinInterval 获取最小间隔
func (t *Tracker) GetMinInterval() int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.MinInterval
}

// GetTrackerID 获取tracker ID
func (t *Tracker) GetTrackerID() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.TrackerID
}

// TrackerManager 管理多个tracker
type TrackerManager struct {
	trackers   []*Tracker
	infoHash   [20]byte
	peerID     [20]byte
	port       uint16
	uploaded   int64
	downloaded int64
	left       int64
	
	mu         sync.RWMutex
}

// NewTrackerManager 创建tracker管理器
func NewTrackerManager(infoHash [20]byte, peerID [20]byte, port uint16) *TrackerManager {
	return &TrackerManager{
		infoHash: infoHash,
		peerID:   peerID,
		port:     port,
		trackers: make([]*Tracker, 0),
	}
}

// AddTracker 添加tracker
func (tm *TrackerManager) AddTracker(trackerURL string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	
	tracker := NewTracker(trackerURL, tm.infoHash, tm.peerID, tm.port)
	tm.trackers = append(tm.trackers, tracker)
}

// AddTrackers 批量添加tracker
func (tm *TrackerManager) AddTrackers(trackerURLs []string) {
	for _, url := range trackerURLs {
		tm.AddTracker(url)
	}
}

// AnnounceAll 向所有trackers宣告
func (tm *TrackerManager) AnnounceAll(ctx context.Context, uploaded, downloaded, left int64) ([]PeerInfo, error) {
	tm.mu.Lock()
	tm.uploaded = uploaded
	tm.downloaded = downloaded
	tm.left = left
	tm.mu.Unlock()
	
	var allPeers []PeerInfo
	var errors []string
	var mu sync.Mutex
	var wg sync.WaitGroup
	
	// 限制并发请求数
	semaphore := make(chan struct{}, 30) // 最多同时30个tracker请求
	
	for _, tracker := range tm.trackers {
		wg.Add(1)
		go func(t *Tracker) {
			defer wg.Done()
			
			// 获取信号量
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			
			// 为每个tracker设置单独的超时
			trackerCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
			defer cancel()
			
			peers, err := t.Announce(trackerCtx, uploaded, downloaded, left)
			if err != nil {
				mu.Lock()
				errors = append(errors, fmt.Sprintf("%s: %v", t.URL, err))
				mu.Unlock()
				return
			}
			
			mu.Lock()
			allPeers = append(allPeers, peers...)
			mu.Unlock()
		}(tracker)
	}
	
	wg.Wait()
	
	if len(allPeers) == 0 && len(errors) > 0 {
		return nil, fmt.Errorf("all trackers failed: %s", strings.Join(errors, "; "))
	}
	
	return allPeers, nil
}

// GetPeers 获取peers（自动选择tracker）
func (tm *TrackerManager) GetPeers(ctx context.Context) ([]PeerInfo, error) {
	// 使用第一个可用的tracker
	for _, tracker := range tm.trackers {
		peers, err := tracker.Announce(ctx, tm.uploaded, tm.downloaded, tm.left)
		if err == nil && len(peers) > 0 {
			return peers, nil
		}
	}
	
	return nil, errors.New("no trackers available")
}

// UpdateStats 更新统计信息
func (tm *TrackerManager) UpdateStats(uploaded, downloaded, left int64) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	
	tm.uploaded = uploaded
	tm.downloaded = downloaded
	tm.left = left
}

// CloseAll 关闭所有tracker连接
func (tm *TrackerManager) CloseAll() {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	
	for _, tracker := range tm.trackers {
		tracker.Close()
	}
}

// GetTrackerCount 获取tracker数量
func (tm *TrackerManager) GetTrackerCount() int {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	
	return len(tm.trackers)
}

// GetActiveTrackers 获取活动的tracker
func (tm *TrackerManager) GetActiveTrackers() []*Tracker {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	
	var active []*Tracker
	for _, tracker := range tm.trackers {
		// 检查tracker是否有效
		if tracker.URL != "" {
			active = append(active, tracker)
		}
	}
	
	return active
}

// Helper functions

// randUint32 生成随机32位整数
func randUint32() uint32 {
	var buf [4]byte
	rand.Read(buf[:])
	return binary.BigEndian.Uint32(buf[:])
}

// EncodeInfoHash 编码info哈希为URL安全字符串
func EncodeInfoHash(infoHash [20]byte) string {
	var encoded strings.Builder
	for _, b := range infoHash[:] {
		encoded.WriteString(fmt.Sprintf("%%%02X", b))
	}
	return encoded.String()
}

// DecodeInfoHash 解码URL安全字符串为info哈希
func DecodeInfoHash(encoded string) ([20]byte, error) {
	var infoHash [20]byte
	
	decoded, err := url.QueryUnescape(encoded)
	if err != nil {
		return infoHash, err
	}
	
	if len(decoded) != 20 {
		return infoHash, fmt.Errorf("invalid info hash length: %d", len(decoded))
	}
	
	copy(infoHash[:], decoded)
	return infoHash, nil
}

// InfoHashFromMagnet 从magnet链接提取info哈希
func InfoHashFromMagnet(magnetURL string) ([20]byte, error) {
	magnet, err := ParseMagnetLink(magnetURL)
	if err != nil {
		return [20]byte{}, err
	}
	
	return magnet.InfoHash, nil
}

// GenerateTrackerKey 生成tracker密钥
func GenerateTrackerKey() string {
	hash := sha1.New()
	hash.Write([]byte(strconv.FormatInt(time.Now().UnixNano(), 10)))
	hash.Write([]byte(strconv.Itoa(mrand.Int())))
	
	return hex.EncodeToString(hash.Sum(nil))[:8]
}

// IsValidTrackerURL 检查tracker URL是否有效
func IsValidTrackerURL(url string) bool {
	if url == "" {
		return false
	}
	
	// 检查协议
	if !strings.HasPrefix(url, "http://") &&
	   !strings.HasPrefix(url, "https://") &&
	   !strings.HasPrefix(url, "udp://") {
		return false
	}
	
	// 尝试解析URL
	_, err := net.ResolveTCPAddr("tcp", strings.TrimPrefix(url, "http://"))
	if err != nil {
		_, err = net.ResolveUDPAddr("udp", strings.TrimPrefix(url, "udp://"))
		if err != nil {
			return false
		}
	}
	
	return true
}

// FilterInvalidTrackers 过滤无效的tracker URL
func FilterInvalidTrackers(urls []string) []string {
	var valid []string
	for _, url := range urls {
		if IsValidTrackerURL(url) {
			valid = append(valid, url)
		}
	}
	return valid
}