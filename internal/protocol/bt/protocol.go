// Package bt 提供BitTorrent协议支持
package bt

import (
	"bytes"
	"compress/zlib"
	"crypto/rand"
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// Protocol Constants
const (
	// 协议标识
	ProtocolIdentifier = "BitTorrent protocol"
	
	// 消息类型
	MsgChoke         = 0
	MsgUnchoke       = 1
	MsgInterested    = 2
	MsgNotInterested = 3
	MsgHave          = 4
	MsgBitfield      = 5
	MsgRequest       = 6
	MsgPiece         = 7
	MsgCancel        = 8
	MsgPort          = 9
	MsgExtended      = 20
	
	// 扩展消息ID
	ExtHandshake     = 0
	ExtMetadata      = 1
	ExtPEX           = 2
	ExtMetadataData  = 3
	ExtMetadataReq   = 4
	
	// 连接超时
	DefaultTimeout = 30 * time.Second
	
	// 最大消息大小
	MaxMessageSize = 256 * 1024 // 256KB
	
	// Piece块大小
	DefaultBlockSize = 16 * 1024 // 16KB
	MaxBlockSize     = 128 * 1024 // 128KB
	
	// 协议扩展
	ExtensionBits = 0x100000
)

// Peer 表示一个peer连接
type Peer struct {
	// 基本信息
	ID       [20]byte  // Peer ID
	IP       net.IP    // IP地址
	Port     uint16    // 端口
	Bitfield []byte    // bitfield（拥有的piece）
	
	// 连接状态
	conn      net.Conn    // TCP连接
	connected bool        // 是否已连接
	choked    bool        // 是否被阻塞
	interested bool       // 是否感兴趣
	amChoking bool        // 我是否阻塞对方
	amInterested bool     // 我是否感兴趣
	
	// 统计信息
	Downloaded int64 // 已下载字节数
	Uploaded   int64 // 已上传字节数
	Left       int64 // 剩余字节数
	
	// 扩展协议支持
	SupportsExtensions bool   // 是否支持扩展协议
	ExtensionBits      uint64 // 扩展位掩码
	
	// 锁保护并发访问
	mu sync.RWMutex
	
	// 消息通道
	messageCh chan Message
	errorCh   chan error
	
	// 关闭信号
	closeCh chan struct{}
	
	// 上次活动时间
	lastActivity time.Time
}

// Message BitTorrent协议消息
type Message struct {
	Type    int    // 消息类型
	Payload []byte // 消息负载
	Index   int    // piece索引（用于piece相关消息）
	Begin   int    // piece内偏移（用于piece相关消息）
	Length  int    // 长度（用于piece相关消息）
	Data    []byte // 数据（用于piece消息）
}

// Handshake 握手消息
type Handshake struct {
	Protocol string   // 协议标识
	InfoHash [20]byte // info哈希
	PeerID   [20]byte // peer ID
	Reserved [8]byte  // 保留字段（扩展位）
}

// PieceBlock piece数据块
type PieceBlock struct {
	Index  int    // piece索引
	Begin  int    // piece内偏移
	Length int    // 块长度
	Data   []byte // 数据
}

// NewPeer 创建新的peer
func NewPeer(ip net.IP, port uint16) *Peer {
	return &Peer{
		IP:               ip,
		Port:             port,
		choked:           true,
		amChoking:        true,
		messageCh:        make(chan Message, 100),
		errorCh:          make(chan error, 10),
		closeCh:          make(chan struct{}),
		lastActivity:     time.Now(),
	}
}

// Connect 连接到peer
func (p *Peer) Connect(infoHash [20]byte, peerID [20]byte, timeout time.Duration) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	
	if p.connected {
		return errors.New("already connected")
	}
	
	// 建立TCP连接
	addr := fmt.Sprintf("%s:%d", p.IP.String(), p.Port)
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return fmt.Errorf("dial failed: %w", err)
	}
	
	p.conn = conn
	p.connected = true
	
	// 发送握手消息
	handshake := Handshake{
		Protocol: ProtocolIdentifier,
		InfoHash: infoHash,
		PeerID:   peerID,
	}
	
	// 设置扩展位
	copy(handshake.Reserved[:], []byte{0, 0, 0, 0, 0, 0x10, 0, 0}) // 启用扩展协议
	
	err = p.sendHandshake(handshake)
	if err != nil {
		p.conn.Close()
		p.connected = false
		return fmt.Errorf("send handshake failed: %w", err)
	}
	
	// 接收握手响应
	respHandshake, err := p.receiveHandshake()
	if err != nil {
		p.conn.Close()
		p.connected = false
		return fmt.Errorf("receive handshake failed: %w", err)
	}
	
	// 验证info哈希
	if respHandshake.InfoHash != infoHash {
		p.conn.Close()
		p.connected = false
		return errors.New("info hash mismatch")
	}
	
	// 保存peer ID
	p.ID = respHandshake.PeerID
	
	// 检查扩展协议支持
	if respHandshake.Reserved[5]&0x10 != 0 {
		p.SupportsExtensions = true
	}
	
	// 启动消息处理goroutine
	go p.messageHandler()
	
	return nil
}

// sendHandshake 发送握手消息
func (p *Peer) sendHandshake(h Handshake) error {
	buf := make([]byte, 68) // 1 + 19 + 8 + 20 + 20
	
	// 协议长度
	buf[0] = byte(len(h.Protocol))
	
	// 协议标识
	copy(buf[1:20], h.Protocol)
	
	// 保留字段
	copy(buf[20:28], h.Reserved[:])
	
	// info哈希
	copy(buf[28:48], h.InfoHash[:])
	
	// peer ID
	copy(buf[48:68], h.PeerID[:])
	
	_, err := p.conn.Write(buf)
	return err
}

// receiveHandshake 接收握手消息
func (p *Peer) receiveHandshake() (Handshake, error) {
	var h Handshake
	
	// 读取协议长度
	buf := make([]byte, 1)
	_, err := io.ReadFull(p.conn, buf)
	if err != nil {
		return h, err
	}
	
	protocolLen := int(buf[0])
	if protocolLen != 19 {
		return h, fmt.Errorf("invalid protocol length: %d", protocolLen)
	}
	
	// 读取完整握手消息
	buf = make([]byte, 48+protocolLen) // 协议 + 保留字段 + info哈希 + peer ID
	_, err = io.ReadFull(p.conn, buf)
	if err != nil {
		return h, err
	}
	
	// 解析协议
	h.Protocol = string(buf[:protocolLen])
	
	// 解析保留字段
	copy(h.Reserved[:], buf[protocolLen:protocolLen+8])
	
	// 解析info哈希
	copy(h.InfoHash[:], buf[protocolLen+8:protocolLen+28])
	
	// 解析peer ID
	copy(h.PeerID[:], buf[protocolLen+28:protocolLen+48])
	
	return h, nil
}

// messageHandler 消息处理goroutine
func (p *Peer) messageHandler() {
	defer func() {
		p.mu.Lock()
		if p.connected {
			p.conn.Close()
			p.connected = false
		}
		p.mu.Unlock()
	}()
	
	for {
		select {
		case <-p.closeCh:
			return
		default:
			// 继续处理消息
		}
		
		// 设置读取超时
		p.conn.SetReadDeadline(time.Now().Add(DefaultTimeout))
		
		// 读取消息长度
		var lengthBuf [4]byte
		_, err := io.ReadFull(p.conn, lengthBuf[:])
		if err != nil {
			p.errorCh <- fmt.Errorf("read message length failed: %w", err)
			return
		}
		
		length := binary.BigEndian.Uint32(lengthBuf[:])
		if length == 0 {
			// keep-alive消息
			p.lastActivity = time.Now()
			continue
		}
		
		if length > MaxMessageSize {
			p.errorCh <- fmt.Errorf("message too large: %d bytes", length)
			return
		}
		
		// 读取消息ID和负载
		msgBuf := make([]byte, length)
		_, err = io.ReadFull(p.conn, msgBuf)
		if err != nil {
			p.errorCh <- fmt.Errorf("read message payload failed: %w", err)
			return
		}
		
		// 更新活动时间
		p.lastActivity = time.Now()
		
		// 解析消息
		msgType := int(msgBuf[0])
		payload := msgBuf[1:]
		
		msg := Message{
			Type:    msgType,
			Payload: payload,
		}
		
		// 根据消息类型进一步解析
		switch msgType {
		case MsgChoke:
			p.mu.Lock()
			p.choked = true
			p.mu.Unlock()
			
		case MsgUnchoke:
			p.mu.Lock()
			p.choked = false
			p.mu.Unlock()
			
		case MsgInterested:
			p.mu.Lock()
			p.interested = true
			p.mu.Unlock()
			
		case MsgNotInterested:
			p.mu.Lock()
			p.interested = false
			p.mu.Unlock()
			
		case MsgHave:
			if len(payload) >= 4 {
				index := int(binary.BigEndian.Uint32(payload))
				msg.Index = index
				// 更新bitfield
				p.updateBitfield(index)
			}
			
		case MsgBitfield:
			p.mu.Lock()
			p.Bitfield = make([]byte, len(payload))
			copy(p.Bitfield, payload)
			p.mu.Unlock()
			
		case MsgRequest:
			if len(payload) >= 12 {
				msg.Index = int(binary.BigEndian.Uint32(payload[0:4]))
				msg.Begin = int(binary.BigEndian.Uint32(payload[4:8]))
				msg.Length = int(binary.BigEndian.Uint32(payload[8:12]))
			}
			
		case MsgPiece:
			if len(payload) >= 8 {
				msg.Index = int(binary.BigEndian.Uint32(payload[0:4]))
				msg.Begin = int(binary.BigEndian.Uint32(payload[4:8]))
				msg.Data = payload[8:]
			}
			
		case MsgCancel:
			if len(payload) >= 12 {
				msg.Index = int(binary.BigEndian.Uint32(payload[0:4]))
				msg.Begin = int(binary.BigEndian.Uint32(payload[4:8]))
				msg.Length = int(binary.BigEndian.Uint32(payload[8:12]))
			}
			
		case MsgPort:
			// DHT端口消息
			
		case MsgExtended:
			// 扩展消息
			if len(payload) > 0 {
				extID := int(payload[0])
				extPayload := payload[1:]
				msg.Type = extID
				msg.Payload = extPayload
			}
		}
		
		// 发送消息到通道
		select {
		case p.messageCh <- msg:
		default:
			// 通道满，丢弃消息
		}
	}
}

// updateBitfield 更新bitfield
func (p *Peer) updateBitfield(pieceIndex int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	
	if p.Bitfield == nil {
		// 需要知道总piece数才能创廻bitfield
		return
	}
	
	byteIndex := pieceIndex / 8
	bitIndex := pieceIndex % 8
	
	if byteIndex < len(p.Bitfield) {
		p.Bitfield[byteIndex] |= 1 << (7 - bitIndex)
	}
}

// SendMessage 发送消息
func (p *Peer) SendMessage(msgType int, payload []byte) error {
	p.mu.RLock()
	if !p.connected {
		p.mu.RUnlock()
		return errors.New("not connected")
	}
	p.mu.RUnlock()
	
	length := uint32(1 + len(payload))
	buf := make([]byte, 4+1+len(payload))
	
	// 写入长度
	binary.BigEndian.PutUint32(buf[0:4], length)
	
	// 写入消息类型
	buf[4] = byte(msgType)
	
	// 写入负载
	copy(buf[5:], payload)
	
	// 发送消息
	_, err := p.conn.Write(buf)
	if err == nil {
		p.lastActivity = time.Now()
	}
	
	return err
}

// SendRequest 发送请求消息
func (p *Peer) SendRequest(index, begin, length int) error {
	buf := make([]byte, 12)
	binary.BigEndian.PutUint32(buf[0:4], uint32(index))
	binary.BigEndian.PutUint32(buf[4:8], uint32(begin))
	binary.BigEndian.PutUint32(buf[8:12], uint32(length))
	
	return p.SendMessage(MsgRequest, buf)
}

// SendHave 发送have消息
func (p *Peer) SendHave(index int) error {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, uint32(index))
	
	return p.SendMessage(MsgHave, buf)
}

// SendBitfield 发送bitfield消息
func (p *Peer) SendBitfield(bitfield []byte) error {
	return p.SendMessage(MsgBitfield, bitfield)
}

// SendPiece 发送piece数据
func (p *Peer) SendPiece(index, begin int, data []byte) error {
	buf := make([]byte, 8+len(data))
	binary.BigEndian.PutUint32(buf[0:4], uint32(index))
	binary.BigEndian.PutUint32(buf[4:8], uint32(begin))
	copy(buf[8:], data)
	
	return p.SendMessage(MsgPiece, buf)
}

// SendCancel 发送取消消息
func (p *Peer) SendCancel(index, begin, length int) error {
	buf := make([]byte, 12)
	binary.BigEndian.PutUint32(buf[0:4], uint32(index))
	binary.BigEndian.PutUint32(buf[4:8], uint32(begin))
	binary.BigEndian.PutUint32(buf[8:12], uint32(length))
	
	return p.SendMessage(MsgCancel, buf)
}

// SendChoke 发送阻塞消息
func (p *Peer) SendChoke() error {
	p.mu.Lock()
	p.amChoking = true
	p.mu.Unlock()
	
	return p.SendMessage(MsgChoke, nil)
}

// SendUnchoke 发送解除阻塞消息
func (p *Peer) SendUnchoke() error {
	p.mu.Lock()
	p.amChoking = false
	p.mu.Unlock()
	
	return p.SendMessage(MsgUnchoke, nil)
}

// SendInterested 发送感兴趣消息
func (p *Peer) SendInterested() error {
	p.mu.Lock()
	p.amInterested = true
	p.mu.Unlock()
	
	return p.SendMessage(MsgInterested, nil)
}

// SendNotInterested 发送不感兴趣消息
func (p *Peer) SendNotInterested() error {
	p.mu.Lock()
	p.amInterested = false
	p.mu.Unlock()
	
	return p.SendMessage(MsgNotInterested, nil)
}

// SendKeepAlive 发送keep-alive消息
func (p *Peer) SendKeepAlive() error {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, 0)
	
	_, err := p.conn.Write(buf)
	if err == nil {
		p.lastActivity = time.Now()
	}
	
	return err
}

// Close 关闭peer连接
func (p *Peer) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	
	close(p.closeCh)
	
	if p.connected {
		err := p.conn.Close()
		p.connected = false
		return err
	}
	
	return nil
}

// IsChoked 是否被阻塞
func (p *Peer) IsChoked() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.choked
}

// IsInterested 是否感兴趣
func (p *Peer) IsInterested() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.interested
}

// HasPiece 检查是否拥有指定的piece
func (p *Peer) HasPiece(index int) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	
	if p.Bitfield == nil || index < 0 {
		return false
	}
	
	byteIndex := index / 8
	bitIndex := index % 8
	
	if byteIndex >= len(p.Bitfield) {
		return false
	}
	
	return (p.Bitfield[byteIndex] & (1 << (7 - bitIndex))) != 0
}

// GetMessages 获取消息通道
func (p *Peer) GetMessages() <-chan Message {
	return p.messageCh
}

// GetErrors 获取错误通道
func (p *Peer) GetErrors() <-chan error {
	return p.errorCh
}

// IsConnected 是否已连接
func (p *Peer) IsConnected() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.connected
}

// GetBitfield 获取bitfield
func (p *Peer) GetBitfield() []byte {
	p.mu.RLock()
	defer p.mu.RUnlock()
	
	if p.Bitfield == nil {
		return nil
	}
	
	bitfield := make([]byte, len(p.Bitfield))
	copy(bitfield, p.Bitfield)
	return bitfield
}

// GeneratePeerID 生成peer ID
func GeneratePeerID() [20]byte {
	var peerID [20]byte
	
	// 使用Azureus风格: -AZ2060- + 12位随机字符
	copy(peerID[:8], []byte("-AZ2060-"))
	
	// 生成随机部分
	rand.Read(peerID[8:])
	
	return peerID
}

// CreateBitfield 创建bitfield
func CreateBitfield(numPieces int, havePieces []bool) []byte {
	if numPieces <= 0 {
		return nil
	}
	
	numBytes := (numPieces + 7) / 8
	bitfield := make([]byte, numBytes)
	
	for i := 0; i < numPieces; i++ {
		if i < len(havePieces) && havePieces[i] {
			byteIndex := i / 8
			bitIndex := i % 8
			bitfield[byteIndex] |= 1 << (7 - bitIndex)
		}
	}
	
	return bitfield
}

// ParseBitfield 解析bitfield
func ParseBitfield(bitfield []byte, numPieces int) []bool {
	if bitfield == nil || numPieces <= 0 {
		return nil
	}
	
	havePieces := make([]bool, numPieces)
	
	for i := 0; i < numPieces; i++ {
		byteIndex := i / 8
		bitIndex := i % 8
		
		if byteIndex < len(bitfield) {
			havePieces[i] = (bitfield[byteIndex] & (1 << (7 - bitIndex))) != 0
		}
	}
	
	return havePieces
}

// CompressData 压缩数据（用于扩展协议）
func CompressData(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	
	_, err := w.Write(data)
	if err != nil {
		w.Close()
		return nil, err
	}
	
	w.Close()
	return buf.Bytes(), nil
}

// DecompressData 解压数据
func DecompressData(compressed []byte) ([]byte, error) {
	r, err := zlib.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	
	var buf bytes.Buffer
	_, err = io.Copy(&buf, r)
	if err != nil {
		return nil, err
	}
	
	return buf.Bytes(), nil
}

// CalculatePieceSize 计算piece大小
func CalculatePieceSize(pieceIndex int, totalSize int64, pieceLength int64) int64 {
	pieceStart := int64(pieceIndex) * pieceLength
	pieceEnd := pieceStart + pieceLength
	
	if pieceEnd > totalSize {
		return totalSize - pieceStart
	}
	
	return pieceLength
}

// ValidatePieceData 验证piece数据
func ValidatePieceData(pieceIndex int, data []byte, expectedHash [20]byte) bool {
	actualHash := sha1.Sum(data)
	return bytes.Equal(actualHash[:], expectedHash[:])
}