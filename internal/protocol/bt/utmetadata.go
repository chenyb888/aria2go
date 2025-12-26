// Package bt 提供 BitTorrent 扩展协议支持
package bt

import (
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"sync"
)

// UTMetadata 扩展协议常量
const (
	MetadataPieceSize = 16 * 1024 // 每个 metadata piece 16KB
	InfoHashLength   = 20         // InfoHash 长度 20 字节
	
	// UTMetadata 消息类型
	UTMetadataRequest  = 0 // 请求 metadata piece
	UTMetadataData     = 1 // 返回 metadata piece 数据
	UTMetadataReject   = 2 // 拒绝请求
)

// UTMetadataRequestTracker 跟踪已请求的 metadata piece
// 参考 aria2 的 UTMetadataRequestTracker
type UTMetadataRequestTracker struct {
	mu          sync.RWMutex
	tracked     map[int]time.Time // piece index -> request time
	timeout     time.Duration
}

// NewUTMetadataRequestTracker 创建新的请求跟踪器
func NewUTMetadataRequestTracker() *UTMetadataRequestTracker {
	return &UTMetadataRequestTracker{
		tracked: make(map[int]time.Time),
		timeout: 60 * time.Second, // 60 秒超时
	}
}

// Add 添加要跟踪的 piece
func (t *UTMetadataRequestTracker) Add(index int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.tracked[index] = time.Now()
}

// Remove 移除已完成的 piece
func (t *UTMetadataRequestTracker) Remove(index int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.tracked, index)
}

// Tracks 检查是否正在跟踪该 piece
func (t *UTMetadataRequestTracker) Tracks(index int) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	_, exists := t.tracked[index]
	return exists
}

// RemoveTimeoutEntry 移除超时的请求
func (t *UTMetadataRequestTracker) RemoveTimeoutEntry() []int {
	t.mu.Lock()
	defer t.mu.Unlock()
	
	var expired []int
	now := time.Now()
	for index, reqTime := range t.tracked {
		if now.Sub(reqTime) > t.timeout {
			delete(t.tracked, index)
			expired = append(expired, index)
		}
	}
	return expired
}

// GetAllTrackedIndex 获取所有正在跟踪的 piece 索引
func (t *UTMetadataRequestTracker) GetAllTrackedIndex() []int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	
	indexes := make([]int, 0, len(t.tracked))
	for index := range t.tracked {
		indexes = append(indexes, index)
	}
	return indexes
}

// Count 获取跟踪的 piece 数量
func (t *UTMetadataRequestTracker) Count() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.tracked)
}

// UTMetadataMessage UTMetadata 扩展消息
// 参考 aria2 的 UTMetadataExtensionMessage
type UTMetadataMessage struct {
	MsgType  int    // 0=request, 1=data, 2=reject
	Piece    int    // piece 索引
	TotalSize int64 // metadata 总大小（仅 data 消息）
	Data     []byte // metadata piece 数据（仅 data 消息）
}

// Encode 编码 UTMetadata 消息为 bencode 格式
// 参考 aria2 的 UTMetadataRequestExtensionMessage::getPayload()
func (m *UTMetadataMessage) Encode() ([]byte, error) {
	dict := map[string]interface{}{
		"msg_type": m.MsgType,
		"piece":    m.Piece,
	}
	
	if m.MsgType == UTMetadataData {
		dict["total_size"] = m.TotalSize
	}
	
	// 简化实现：实际应该使用 bencode 库
	// 这里返回一个简化的编码格式
	var result []byte
	
	// 编码字典
	result = append(result, 'd')
	
	// msg_type
	result = append(result, []byte("msg_type")...)
	result = append(result, 'i')
	result = append(result, byte(m.MsgType))
	result = append(result, 'e')
	
	// piece
	result = append(result, []byte("piece")...)
	result = append(result, 'i')
	pieceBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(pieceBytes, uint32(m.Piece))
	result = append(result, pieceBytes...)
	result = append(result, 'e')
	
	// total_size (仅 data 消息)
	if m.MsgType == UTMetadataData {
		result = append(result, []byte("total_size")...)
		result = append(result, 'i')
		sizeBytes := make([]byte, 8)
		binary.BigEndian.PutUint64(sizeBytes, uint64(m.TotalSize))
		result = append(result, sizeBytes...)
		result = append(result, 'e')
	}
	
	result = append(result, 'e')
	
	// 附加数据（仅 data 消息）
	if m.MsgType == UTMetadataData && len(m.Data) > 0 {
		result = append(result, m.Data...)
	}
	
	return result, nil
}

// Decode 解码 UTMetadata 消息
func DecodeUTMetadataMessage(data []byte) (*UTMetadataMessage, error) {
	// 简化实现：解析 bencode 格式
	// 实际应该使用完整的 bencode 解析器
	
	msg := &UTMetadataMessage{}
	
	// 查找 msg_type
	if len(data) < 20 {
		return nil, errors.New("invalid UTMetadata message: too short")
	}
	
	// 简化解析：假设 msg_type 在固定位置
	// 实际应该正确解析 bencode
	
	return msg, nil
}

// UTMetadataExchange UTMetadata 交换器
// 参考 aria2 的 UTMetadataRequestFactory
type UTMetadataExchange struct {
	mu            sync.RWMutex
	metadata      []byte              // 完整的 metadata
	metadataSize  int64               // metadata 总大小
	tracker       *UTMetadataRequestTracker
	pieceStorage  *PieceManager
	infoHash      [20]byte
}

// NewUTMetadataExchange 创建新的 UTMetadata 交换器
func NewUTMetadataExchange(infoHash [20]byte, metadataSize int64) *UTMetadataExchange {
	return &UTMetadataExchange{
		metadataSize: metadataSize,
		tracker:      NewUTMetadataRequestTracker(),
		infoHash:     infoHash,
	}
}

// CreateRequest 创建 UTMetadata 请求消息
// 参考 aria2 的 UTMetadataRequestFactory::create()
func (e *UTMetadataExchange) CreateRequest(pieceIndex int) (*UTMetadataMessage, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	
	msg := &UTMetadataMessage{
		MsgType: UTMetadataRequest,
		Piece:   pieceIndex,
	}
	
	// 添加到跟踪器
	e.tracker.Add(pieceIndex)
	
	return msg, nil
}

// HandleDataMessage 处理收到的 data 消息
// 参考 aria2 的 UTMetadataDataExtensionMessage::doReceivedAction()
func (e *UTMetadataExchange) HandleDataMessage(msg *UTMetadataMessage) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	
	// 检查是否在跟踪列表中
	if !e.tracker.Tracks(msg.Piece) {
		log.Printf("UTMetadataExchange[HandleDataMessage] piece %d not tracked", msg.Piece)
		return nil
	}
	
	// 从跟踪器移除
	e.tracker.Remove(msg.Piece)
	
	// 写入数据
	offset := msg.Piece * MetadataPieceSize
	if offset >= len(e.metadata) {
		e.metadata = make([]byte, msg.TotalSize)
	}
	
	end := offset + len(msg.Data)
	if end > len(e.metadata) {
		end = len(e.metadata)
	}
	
	copy(e.metadata[offset:end], msg.Data)
	
	// 检查是否完成
	if e.isComplete() {
		log.Printf("UTMetadataExchange[HandleDataMessage] metadata download complete")
		// 验证 InfoHash
		if err := e.verifyInfoHash(); err != nil {
			log.Printf("UTMetadataExchange[HandleDataMessage] InfoHash verification failed: %v", err)
			// 清空 metadata
			e.metadata = nil
			return fmt.Errorf("metadata InfoHash mismatch: %w", err)
		}
		log.Printf("UTMetadataExchange[HandleDataMessage] InfoHash verification successful")
	}
	
	return nil
}

// HandleRejectMessage 处理收到的 reject 消息
func (e *UTMetadataExchange) HandleRejectMessage(msg *UTMetadataMessage) {
	e.mu.Lock()
	defer e.mu.Unlock()
	
	// 从跟踪器移除
	e.tracker.Remove(msg.Piece)
	log.Printf("UTMetadataExchange[HandleRejectMessage] piece %d rejected", msg.Piece)
}

// isComplete 检查 metadata 是否下载完成
func (e *UTMetadataExchange) isComplete() bool {
	if e.metadataSize == 0 {
		return false
	}
	return int64(len(e.metadata)) >= e.metadataSize
}

// verifyInfoHash 验证 metadata 的 InfoHash
func (e *UTMetadataExchange) verifyInfoHash() error {
	// 计算 metadata 的 SHA1
	// 这里简化实现，实际应该使用 crypto/sha1
	hash := sha1.Sum(e.metadata)
	
	if hash != e.infoHash {
		return errors.New("InfoHash mismatch")
	}
	
	return nil
}

// GetMetadata 获取完整的 metadata
func (e *UTMetadataExchange) GetMetadata() []byte {
	e.mu.RLock()
	defer e.mu.RUnlock()
	
	if e.isComplete() {
		return e.metadata
	}
	return nil
}

// GetMissingPieces 获取缺失的 piece 索引
func (e *UTMetadataExchange) GetMissingPieces() []int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	
	if e.metadataSize == 0 {
		return nil
	}
	
	totalPieces := int((e.metadataSize + MetadataPieceSize - 1) / MetadataPieceSize)
	tracked := e.tracker.GetAllTrackedIndex()
	trackedMap := make(map[int]bool)
	for _, idx := range tracked {
		trackedMap[idx] = true
	}
	
	var missing []int
	for i := 0; i < totalPieces; i++ {
		if !trackedMap[i] {
			missing = append(missing, i)
		}
	}
	
	return missing
}

// GetTracker 获取请求跟踪器
func (e *UTMetadataExchange) GetTracker() *UTMetadataRequestTracker {
	return e.tracker
}