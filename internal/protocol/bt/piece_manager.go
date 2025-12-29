// Package bt 提供BitTorrent协议支持
package bt

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// PieceManagerConfig Piece管理器配置
type PieceManagerConfig struct {
	// 下载设置
	MaxConcurrentPieces  int           // 最大并发piece数
	MaxBlocksPerPiece    int           // 每个piece的最大块数
	BlockSize            int           // 块大小（字节）
	MaxRetries           int           // 最大重试次数
	RetryDelay           time.Duration // 重试延迟
	
	// 存储设置
	DataDir              string        // 数据目录
	UseMemoryCache       bool          // 是否使用内存缓存
	MemoryCacheSize      int64         // 内存缓存大小（字节）
	WriteBufferSize      int           // 写入缓冲区大小
	
	// 校验设置
	VerifyOnCompletion   bool          // 完成时验证
	PreAllocateSpace     bool          // 预分配空间
}

// DefaultPieceManagerConfig 默认Piece管理器配置
func DefaultPieceManagerConfig() PieceManagerConfig {
	return PieceManagerConfig{
		MaxConcurrentPieces:  10,  // 增加同时处理的piece数
		MaxBlocksPerPiece:    16,
		BlockSize:            16 * 1024, // 16KB
		MaxRetries:           5,  // 增加重试次数
		RetryDelay:           1 * time.Second,  // 减少重试延迟
		DataDir:              ".",
		UseMemoryCache:       true,
		MemoryCacheSize:      200 * 1024 * 1024, // 增加内存缓存到200MB
		WriteBufferSize:      128 * 1024,        // 增加写入缓冲区到128KB
		VerifyOnCompletion:   true,
		PreAllocateSpace:     true,
	}
}

// BlockRequester 块数据请求器接口
type BlockRequester interface {
	// RequestBlock 请求下载一个数据块
	RequestBlock(pieceIndex int, offset int, size int) ([]byte, error)
	
	// CancelBlock 取消块请求
	CancelBlock(pieceIndex int, offset int) error
}

// PieceState Piece状态
type PieceState int

const (
	PieceStateMissing PieceState = iota  // 缺失
	PieceStatePending                    // 等待下载
	PieceStateDownloading                // 下载中
	PieceStateDownloaded                 // 已下载
	PieceStateWriting                    // 写入中
	PieceStateWritten                    // 已写入
	PieceStateVerified                   // 已验证
	PieceStateCorrupted                  // 损坏
	PieceStateFailed                     // 失败
)

// PieceInfo Piece信息
type PieceInfo struct {
	Index       int         // piece索引
	Hash        [20]byte    // piece哈希
	Size        int64       // piece大小
	State       PieceState  // 状态
	Progress    float64     // 进度 (0.0 - 1.0)
	Blocks      []BlockInfo // 块信息
	Retries     int         // 重试次数
	LastAttempt time.Time   // 最后尝试时间
	Priority    int         // 优先级
}

// BlockInfo 块信息
type BlockInfo struct {
	PieceIndex int    // piece索引
	Offset     int    // piece内偏移
	Size       int    // 块大小
	State      BlockState // 块状态
	Data       []byte // 块数据（内存缓存）
}

// BlockState 块状态
type BlockState int

const (
	BlockStateMissing BlockState = iota  // 缺失
	BlockStateRequested                  // 已请求
	BlockStateDownloading                // 下载中
	BlockStateDownloaded                 // 已下载
	BlockStateWritten                    // 已写入
)

// ResumeData 恢复数据结构，参考 aria2 的进度文件格式
type ResumeData struct {
	Version        int           `json:"version"`        // 版本号
	InfoHash       [20]byte      `json:"infoHash"`       // InfoHash
	PieceLength    int64         `json:"pieceLength"`    // Piece 长度
	TotalLength    int64         `json:"totalLength"`    // 总长度
	Bitfield       []byte        `json:"bitfield"`       // Bitfield
	PieceStates    []PieceState  `json:"pieceStates"`    // Piece 状态
	CompletedBytes int64         `json:"completedBytes"` // 已完成字节数
	UploadedBytes  int64         `json:"uploadedBytes"`  // 已上传字节数
	SavedAt        int64         `json:"savedAt"`        // 保存时间
}

// PieceManager Piece管理器
type PieceManager struct {
	torrent        *TorrentFile
	config         PieceManagerConfig
	blockRequester BlockRequester // 块数据请求器
	
	// Piece存储
	pieces     []*PieceInfo
	pieceMap   map[int]*PieceInfo
	bitfield   []byte // 已完成的piece位图
	
	// 块存储
	blocks     map[[2]int]*BlockInfo // 键: [pieceIndex, offset]
	
	// 下载队列
	pieceQueue chan *PieceInfo       // 等待下载的piece队列
	blockQueue chan *BlockInfo       // 等待下载的块队列
	
	// 状态跟踪
	downloadedPieces int32           // 已下载的piece数
	downloadedBytes  int64           // 已下载字节数
	writtenBytes     int64           // 已写入字节数
	
	// 文件句柄
	files      []*os.File            // 文件句柄
	fileLocks  []sync.Mutex          // 文件锁
	
	// 内存缓存
	memoryCache map[[2]int][]byte    // 内存缓存: [pieceIndex, offset] -> data
	cacheSize   int64                // 当前缓存大小
	cacheHits   int64                // 缓存命中数
	cacheMisses int64                // 缓存未命中数

	// 上传统计
	uploadedBytes  int64              // 已上传字节数
	totalLength    int64              // 总长度
	downloadSpeed  int64              // 下载速度
	uploadSpeed    int64              // 上传速度

	// Torrent 信息
	infoHash       [20]byte          // InfoHash
	pieceLength    int64              // Piece 长度
	completedBytes int64              // 已完成字节数

	// 锁和同步
	mu          sync.RWMutex
	wg          sync.WaitGroup
	ctx         context.Context
	cancel      context.CancelFunc

	// 事件通道
	eventCh     chan PieceEvent
}

// PieceEvent Piece事件
type PieceEvent struct {
	Type      PieceEventType
	Piece     *PieceInfo
	Block     *BlockInfo
	Error     error
	Timestamp time.Time
}

// PieceEventType Piece事件类型
type PieceEventType int

const (
	PieceStarted PieceEventType = iota
	PieceCompleted
	PieceFailed
	BlockStarted
	BlockCompleted
	BlockFailed
	PieceVerified
	PieceCorrupted
	DiskWriteStarted
	DiskWriteCompleted
	DiskWriteFailed
)

// NewPieceManager 创建新的Piece管理器
func NewPieceManager(torrent *TorrentFile, config PieceManagerConfig, requester BlockRequester) (*PieceManager, error) {
	if torrent == nil {
		return nil, errors.New("torrent is nil")
	}
	
	ctx, cancel := context.WithCancel(context.Background())
	
	pm := &PieceManager{
		torrent:        torrent,
		config:         config,
		blockRequester: requester,
		pieces:         make([]*PieceInfo, torrent.NumPieces()),
		pieceMap:       make(map[int]*PieceInfo),
		blocks:         make(map[[2]int]*BlockInfo),
		pieceQueue:     make(chan *PieceInfo, 1000),  // 增加队列容量以避免队列满的问题
		blockQueue:     make(chan *BlockInfo, 1000),
		memoryCache:    make(map[[2]int][]byte),
		ctx:            ctx,
		cancel:         cancel,
		eventCh:        make(chan PieceEvent, 100),
		totalLength:    torrent.TotalSize(),
		uploadedBytes:  0,
		downloadSpeed:  0,
		uploadSpeed:    0,
		infoHash:       torrent.InfoHash,
		pieceLength:    torrent.PieceLength,
		completedBytes: 0,
	}
	
	// 初始化bitfield
	bitfieldSize := (torrent.NumPieces() + 7) / 8
	pm.bitfield = make([]byte, bitfieldSize)
	
	// 初始化piece信息
	if err := pm.initPieces(); err != nil {
		return nil, err
	}
	
	// 初始化文件系统
	if err := pm.initFiles(); err != nil {
		return nil, err
	}
	
	// 启动worker
	pm.startWorkers()
	
	return pm, nil
}

// initPieces 初始化piece信息
func (pm *PieceManager) initPieces() error {
	numPieces := pm.torrent.NumPieces()
	pieceLength := pm.torrent.PieceLength
	totalSize := pm.torrent.TotalSize()
	
	for i := 0; i < numPieces; i++ {
		// 计算piece大小
		pieceSize := pieceLength
		if i == numPieces-1 {
			pieceSize = totalSize - int64(i)*pieceLength
		}
		
		// 获取piece哈希
		pieceHash, err := pm.torrent.GetPieceHash(i)
		if err != nil {
			return fmt.Errorf("get piece %d hash failed: %w", i, err)
		}
		
		// 创建piece信息
		piece := &PieceInfo{
			Index:    i,
			Hash:     pieceHash,
			Size:     pieceSize,
			State:    PieceStateMissing,
			Progress: 0.0,
			Blocks:   make([]BlockInfo, 0),
			Priority: 1, // 默认优先级
		}
		
		// 计算块信息
		pm.initPieceBlocks(piece)
		
		pm.pieces[i] = piece
		pm.pieceMap[i] = piece
	}
	
	return nil
}

// initPieceBlocks 初始化piece的块信息
func (pm *PieceManager) initPieceBlocks(piece *PieceInfo) {
	pieceSize := piece.Size
	blockSize := int64(pm.config.BlockSize)
	
	var offset int64
	for offset < pieceSize {
		blockSizeActual := blockSize
		if offset+blockSize > pieceSize {
			blockSizeActual = pieceSize - offset
		}
		
		block := BlockInfo{
			PieceIndex: piece.Index,
			Offset:     int(offset),
			Size:       int(blockSizeActual),
			State:      BlockStateMissing,
		}
		
		piece.Blocks = append(piece.Blocks, block)
		
		// 添加到块映射
		key := [2]int{piece.Index, int(offset)}
		pm.blocks[key] = &piece.Blocks[len(piece.Blocks)-1]
		
		offset += blockSizeActual
	}
}

// initFiles 初始化文件系统
func (pm *PieceManager) initFiles() error {
	// 创建数据目录
	if err := os.MkdirAll(pm.config.DataDir, 0755); err != nil {
		return fmt.Errorf("create data directory failed: %w", err)
	}
	
	// 单文件模式
	if len(pm.torrent.Files) == 1 {
		file := pm.torrent.Files[0]
		filePath := filepath.Join(pm.config.DataDir, file.Path[0])
		
		// 创建目录
		dir := filepath.Dir(filePath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("create file directory failed: %w", err)
		}
		
		// 打开或创建文件
		f, err := pm.openOrCreateFile(filePath, file.Length)
		if err != nil {
			return fmt.Errorf("open file %s failed: %w", filePath, err)
		}
		
		pm.files = append(pm.files, f)
		pm.fileLocks = append(pm.fileLocks, sync.Mutex{})
		
	} else {
		// 多文件模式
		for _, file := range pm.torrent.Files {
			filePath := filepath.Join(pm.config.DataDir, filepath.Join(file.Path...))
			
			// 创建目录
			dir := filepath.Dir(filePath)
			if err := os.MkdirAll(dir, 0755); err != nil {
				return fmt.Errorf("create file directory %s failed: %w", dir, err)
			}
			
			// 打开或创建文件
			f, err := pm.openOrCreateFile(filePath, file.Length)
			if err != nil {
				return fmt.Errorf("open file %s failed: %w", filePath, err)
			}
			
			pm.files = append(pm.files, f)
			pm.fileLocks = append(pm.fileLocks, sync.Mutex{})
		}
	}
	
	return nil
}

// openOrCreateFile 打开或创建文件
func (pm *PieceManager) openOrCreateFile(filePath string, size int64) (*os.File, error) {
	// 检查文件是否存在
	fileInfo, err := os.Stat(filePath)
	if err == nil {
		// 文件存在，检查大小
		if fileInfo.Size() != size {
			// 大小不匹配，删除并重新创建
			if err := os.Remove(filePath); err != nil {
				return nil, fmt.Errorf("remove existing file failed: %w", err)
			}
			return pm.createFile(filePath, size)
		}
		
		// 打开现有文件
		return os.OpenFile(filePath, os.O_RDWR, 0644)
	}
	
	// 文件不存在，创建新文件
	return pm.createFile(filePath, size)
}

// createFile 创建新文件
func (pm *PieceManager) createFile(filePath string, size int64) (*os.File, error) {
	// 创建文件
	file, err := os.Create(filePath)
	if err != nil {
		return nil, err
	}
	
	// 预分配空间
	if pm.config.PreAllocateSpace && size > 0 {
		if err := file.Truncate(size); err != nil {
			file.Close()
			os.Remove(filePath)
			return nil, fmt.Errorf("pre-allocate space failed: %w", err)
		}
	}
	
	return file, nil
}

// startWorkers 启动worker
func (pm *PieceManager) startWorkers() {
	// 启动piece下载worker
	for i := 0; i < pm.config.MaxConcurrentPieces; i++ {
		pm.wg.Add(1)
		go pm.pieceWorker(i)
	}
	
	// 启动块写入worker
	pm.wg.Add(1)
	go pm.writeWorker()
	
	// 启动状态监控worker
	pm.wg.Add(1)
	go pm.monitorWorker()
}

// pieceWorker Piece下载worker
func (pm *PieceManager) pieceWorker(id int) {
	defer pm.wg.Done()
	
	for {
		select {
		case <-pm.ctx.Done():
			return
		case piece := <-pm.pieceQueue:
			pm.downloadPiece(piece)
		}
	}
}

// writeWorker 块写入worker
func (pm *PieceManager) writeWorker() {
	defer pm.wg.Done()
	
	buffer := make([]byte, pm.config.WriteBufferSize)
	
	for {
		select {
		case <-pm.ctx.Done():
			return
		case block := <-pm.blockQueue:
			pm.writeBlock(block, buffer)
		}
	}
}

// monitorWorker 状态监控worker
func (pm *PieceManager) monitorWorker() {
	defer pm.wg.Done()
	
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	
	for {
		select {
		case <-pm.ctx.Done():
			return
		case <-ticker.C:
			pm.checkProgress()
		}
	}
}

// downloadPiece 下载piece
func (pm *PieceManager) downloadPiece(piece *PieceInfo) {
	// 更新piece状态
	pm.mu.Lock()
	piece.State = PieceStateDownloading
	piece.LastAttempt = time.Now()
	pm.mu.Unlock()
	
	// 发送piece开始事件
	pm.eventCh <- PieceEvent{
		Type:      PieceStarted,
		Piece:     piece,
		Timestamp: time.Now(),
	}
	
	// 下载所有块
	var successBlocks int
	for i := range piece.Blocks {
		block := &piece.Blocks[i]
		
		// 跳过已下载的块
		if block.State == BlockStateDownloaded || block.State == BlockStateWritten {
			successBlocks++
			continue
		}
		
		// 下载块
		if err := pm.downloadBlock(block); err != nil {
			// 块下载失败
			pm.handleBlockFailure(block, err)
			
			// 检查是否达到最大重试次数
			if piece.Retries >= pm.config.MaxRetries {
				pm.handlePieceFailure(piece, err)
				return
			}
			
			// 重试piece
			piece.Retries++
			time.Sleep(pm.config.RetryDelay)
			pm.pieceQueue <- piece
			return
		}
		
		successBlocks++
		
		// 更新piece进度
		pm.mu.Lock()
		piece.Progress = float64(successBlocks) / float64(len(piece.Blocks))
		pm.mu.Unlock()
	}
	
	// 所有块下载完成
	pm.mu.Lock()
	piece.State = PieceStateDownloaded
	piece.Progress = 1.0
	pm.mu.Unlock()
	
	// 验证piece
	if pm.config.VerifyOnCompletion {
		if err := pm.verifyPiece(piece); err != nil {
			pm.handlePieceFailure(piece, err)
			return
		}
	}
	
	// 标记piece为完成
	atomic.AddInt32(&pm.downloadedPieces, 1)
	pm.updateBitfield(piece.Index)
	
	// 发送piece完成事件
	pm.eventCh <- PieceEvent{
		Type:      PieceCompleted,
		Piece:     piece,
		Timestamp: time.Now(),
	}
}

// downloadBlock 下载块
func (pm *PieceManager) downloadBlock(block *BlockInfo) error {
	// 发送块开始事件
	pm.eventCh <- PieceEvent{
		Type:      BlockStarted,
		Block:     block,
		Timestamp: time.Now(),
	}
	
	// 检查blockRequester是否可用
	if pm.blockRequester == nil {
		pm.eventCh <- PieceEvent{
			Type:      BlockFailed,
			Block:     block,
			Error:     errors.New("block requester not available"),
			Timestamp: time.Now(),
		}
		return errors.New("block requester not available")
	}
	
	// 通过blockRequester请求数据
	data, err := pm.blockRequester.RequestBlock(block.PieceIndex, block.Offset, block.Size)
	if err != nil {
		pm.eventCh <- PieceEvent{
			Type:      BlockFailed,
			Block:     block,
			Error:     err,
			Timestamp: time.Now(),
		}
		return fmt.Errorf("request block failed: %w", err)
	}
	
	// 检查数据长度
	if len(data) != block.Size {
		pm.eventCh <- PieceEvent{
			Type:      BlockFailed,
			Block:     block,
			Error:     fmt.Errorf("invalid data length: expected %d, got %d", block.Size, len(data)),
			Timestamp: time.Now(),
		}
		return fmt.Errorf("invalid data length: expected %d, got %d", block.Size, len(data))
	}
	
	// 设置块数据
	block.Data = data
	
	// 更新块状态
	block.State = BlockStateDownloaded
	
	// 更新下载字节数
	atomic.AddInt64(&pm.downloadedBytes, int64(block.Size))
	
	// 发送块完成事件
	pm.eventCh <- PieceEvent{
		Type:      BlockCompleted,
		Block:     block,
		Timestamp: time.Now(),
	}
	
	// 添加到写入队列
	pm.blockQueue <- block
	
	return nil
}

// writeBlock 写入块到磁盘
func (pm *PieceManager) writeBlock(block *BlockInfo, buffer []byte) {
	// 发送磁盘写入开始事件
	pm.eventCh <- PieceEvent{
		Type:      DiskWriteStarted,
		Block:     block,
		Timestamp: time.Now(),
	}
	
	// 计算文件位置
	fileIndex, fileOffset, err := pm.calculateFilePosition(block.PieceIndex, block.Offset)
	if err != nil {
		pm.eventCh <- PieceEvent{
			Type:      DiskWriteFailed,
			Block:     block,
			Error:     err,
			Timestamp: time.Now(),
		}
		return
	}
	
	if fileIndex < 0 || fileIndex >= len(pm.files) {
		err := fmt.Errorf("invalid file index: %d", fileIndex)
		pm.eventCh <- PieceEvent{
			Type:      DiskWriteFailed,
			Block:     block,
			Error:     err,
			Timestamp: time.Now(),
		}
		return
	}
	
	// 获取文件锁
	pm.fileLocks[fileIndex].Lock()
	defer pm.fileLocks[fileIndex].Unlock()
	
	// 写入数据
	file := pm.files[fileIndex]
	n, err := file.WriteAt(block.Data, fileOffset)
	if err != nil {
		pm.eventCh <- PieceEvent{
			Type:      DiskWriteFailed,
			Block:     block,
			Error:     err,
			Timestamp: time.Now(),
		}
		return
	}
	
	if n != len(block.Data) {
		err := fmt.Errorf("short write: %d != %d", n, len(block.Data))
		pm.eventCh <- PieceEvent{
			Type:      DiskWriteFailed,
			Block:     block,
			Error:     err,
			Timestamp: time.Now(),
		}
		return
	}
	
	// 更新块状态
	block.State = BlockStateWritten
	
	// 清理内存数据
	block.Data = nil
	
	// 更新写入字节数
	atomic.AddInt64(&pm.writtenBytes, int64(n))
	
	// 发送磁盘写入完成事件
	pm.eventCh <- PieceEvent{
		Type:      DiskWriteCompleted,
		Block:     block,
		Timestamp: time.Now(),
	}
}

// verifyPiece 验证piece
func (pm *PieceManager) verifyPiece(piece *PieceInfo) error {
	// 读取piece数据
	data, err := pm.readPieceData(piece.Index)
	if err != nil {
		return fmt.Errorf("read piece data failed: %w", err)
	}
	
	// 计算哈希
	hash := sha1.Sum(data)
	
	// 验证哈希
	if hash != piece.Hash {
		return errors.New("piece hash mismatch")
	}
	
	// 更新piece状态
	piece.State = PieceStateVerified
	
	// 发送验证事件
	pm.eventCh <- PieceEvent{
		Type:      PieceVerified,
		Piece:     piece,
		Timestamp: time.Now(),
	}
	
	return nil
}

// readPieceData 读取piece数据
func (pm *PieceManager) readPieceData(pieceIndex int) ([]byte, error) {
	piece := pm.pieces[pieceIndex]
	if piece == nil {
		return nil, fmt.Errorf("piece %d not found", pieceIndex)
	}
	
	// 分配缓冲区
	data := make([]byte, piece.Size)
	var offset int64
	
	// 读取每个块
	for _, block := range piece.Blocks {
		if block.State != BlockStateWritten {
			return nil, fmt.Errorf("block %d:%d not written", pieceIndex, block.Offset)
		}
		
		// 计算文件位置
		fileIndex, fileOffset, err := pm.calculateFilePosition(pieceIndex, block.Offset)
		if err != nil {
			return nil, err
		}
		
		// 读取块数据
		file := pm.files[fileIndex]
		n, err := file.ReadAt(data[offset:offset+int64(block.Size)], fileOffset)
		if err != nil {
			return nil, fmt.Errorf("read block failed: %w", err)
		}
		
		if n != block.Size {
			return nil, fmt.Errorf("short read: %d != %d", n, block.Size)
		}
		
		offset += int64(block.Size)
	}
	
	return data, nil
}

// calculateFilePosition 计算文件位置
func (pm *PieceManager) calculateFilePosition(pieceIndex, pieceOffset int) (int, int64, error) {
	// 单文件模式
	if len(pm.torrent.Files) == 1 {
		globalOffset := int64(pieceIndex)*pm.torrent.PieceLength + int64(pieceOffset)
		return 0, globalOffset, nil
	}
	
	// 多文件模式
	// 计算全局偏移量
	globalOffset := int64(pieceIndex)*pm.torrent.PieceLength + int64(pieceOffset)
	
	// 查找对应的文件
	var fileStart int64
	for i, file := range pm.torrent.Files {
		fileEnd := fileStart + file.Length
		
		if globalOffset >= fileStart && globalOffset < fileEnd {
			fileOffset := globalOffset - fileStart
			return i, fileOffset, nil
		}
		
		fileStart = fileEnd
	}
	
	return -1, 0, fmt.Errorf("position %d out of range", globalOffset)
}

// handleBlockFailure 处理块失败
func (pm *PieceManager) handleBlockFailure(block *BlockInfo, err error) {
	pm.eventCh <- PieceEvent{
		Type:      BlockFailed,
		Block:     block,
		Error:     err,
		Timestamp: time.Now(),
	}
}

// handlePieceFailure 处理piece失败
func (pm *PieceManager) handlePieceFailure(piece *PieceInfo, err error) {
	pm.mu.Lock()
	piece.State = PieceStateFailed
	pm.mu.Unlock()
	
	pm.eventCh <- PieceEvent{
		Type:      PieceFailed,
		Piece:     piece,
		Error:     err,
		Timestamp: time.Now(),
	}
}

// checkProgress 检查进度
func (pm *PieceManager) checkProgress() {
	// 检查是否有卡住的piece
	now := time.Now()
	
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	
	for _, piece := range pm.pieces {
		if piece.State == PieceStateDownloading {
			// 检查是否超时（30秒）
			if now.Sub(piece.LastAttempt) > 30*time.Second {
				// 重置piece状态
				piece.State = PieceStateMissing
				piece.Progress = 0.0
				piece.Retries++
				
				// 重新排队
				if piece.Retries <= pm.config.MaxRetries {
					pm.pieceQueue <- piece
				}
			}
		}
	}
}

// updateBitfield 更新bitfield
func (pm *PieceManager) updateBitfield(pieceIndex int) {
	byteIndex := pieceIndex / 8
	bitIndex := pieceIndex % 8
	
	if byteIndex < len(pm.bitfield) {
		pm.bitfield[byteIndex] |= 1 << (7 - bitIndex)
	}
}

// GetNextPiece 获取下一个要下载的piece
func (pm *PieceManager) GetNextPiece() *PieceInfo {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	
	// 优先选择缺失的piece
	for _, piece := range pm.pieces {
		if piece.State == PieceStateMissing || piece.State == PieceStateFailed {
			piece.State = PieceStatePending
			return piece
		}
	}
	
	return nil
}

// QueuePiece 将piece加入下载队列
func (pm *PieceManager) QueuePiece(piece *PieceInfo) error {
	if piece == nil {
		return errors.New("piece is nil")
	}
	
	select {
	case pm.pieceQueue <- piece:
		return nil
	default:
		return errors.New("piece queue is full")
	}
}

// GetPiece 获取piece信息
func (pm *PieceManager) GetPiece(index int) (*PieceInfo, error) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	
	if index < 0 || index >= len(pm.pieces) {
		return nil, fmt.Errorf("piece index %d out of range", index)
	}
	
	return pm.pieces[index], nil
}

// GetBitfield 获取bitfield
func (pm *PieceManager) GetBitfield() []byte {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	
	bitfield := make([]byte, len(pm.bitfield))
	copy(bitfield, pm.bitfield)
	return bitfield
}

// GetProgress 获取下载进度
func (pm *PieceManager) GetProgress() float64 {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	
	if len(pm.pieces) == 0 {
		return 0.0
	}
	
	downloaded := atomic.LoadInt32(&pm.downloadedPieces)
	return float64(downloaded) / float64(len(pm.pieces))
}

// GetDownloadedBytes 获取已下载字节数
func (pm *PieceManager) GetDownloadedBytes() int64 {
	return atomic.LoadInt64(&pm.downloadedBytes)
}

// GetWrittenBytes 获取已写入字节数
func (pm *PieceManager) GetWrittenBytes() int64 {
	return atomic.LoadInt64(&pm.writtenBytes)
}

// GetStats 获取统计信息
func (pm *PieceManager) GetStats() map[string]interface{} {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	stats := make(map[string]interface{})

	stats["total_pieces"] = len(pm.pieces)
	stats["downloaded_pieces"] = atomic.LoadInt32(&pm.downloadedPieces)
	stats["downloaded_bytes"] = atomic.LoadInt64(&pm.downloadedBytes)
	stats["written_bytes"] = atomic.LoadInt64(&pm.writtenBytes)

	// 添加 bttask.go 需要的字段
	stats["UploadedBytes"] = atomic.LoadInt64(&pm.uploadedBytes)
	stats["TotalLength"] = pm.totalLength
	stats["CompletedBytes"] = atomic.LoadInt64(&pm.downloadedBytes)
	stats["DownloadSpeed"] = atomic.LoadInt64(&pm.downloadSpeed)
	stats["UploadSpeed"] = atomic.LoadInt64(&pm.uploadSpeed)

	// 按状态统计piece数
	stateCounts := make(map[PieceState]int)
	for _, piece := range pm.pieces {
		stateCounts[piece.State]++
	}

	stats["piece_state_counts"] = stateCounts
	stats["cache_hits"] = atomic.LoadInt64(&pm.cacheHits)
	stats["cache_misses"] = atomic.LoadInt64(&pm.cacheMisses)

	return stats
}

// Close 关闭Piece管理器
func (pm *PieceManager) Close() error {
	// 发送取消信号
	pm.cancel()
	
	// 等待所有worker完成
	pm.wg.Wait()
	
	// 关闭事件通道
	close(pm.eventCh)
	
	// 关闭文件句柄
	var firstErr error
	for _, file := range pm.files {
		if err := file.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	
	return firstErr
}

// GetEventChannel 获取事件通道
func (pm *PieceManager) GetEventChannel() <-chan PieceEvent {
	return pm.eventCh
}

// VerifyAllPieces 验证所有piece
func (pm *PieceManager) VerifyAllPieces() (int, int, error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	
	var valid, invalid int
	
	for _, piece := range pm.pieces {
		if piece.State == PieceStateVerified || piece.State == PieceStateWritten {
			// 读取piece数据
			data, err := pm.readPieceData(piece.Index)
			if err != nil {
				return valid, invalid, fmt.Errorf("read piece %d failed: %w", piece.Index, err)
			}
			
			// 验证哈希
			hash := sha1.Sum(data)
			if hash == piece.Hash {
				valid++
				piece.State = PieceStateVerified
			} else {
				invalid++
				piece.State = PieceStateCorrupted
				
				// 发送损坏事件
				pm.eventCh <- PieceEvent{
					Type:      PieceCorrupted,
					Piece:     piece,
					Timestamp: time.Now(),
				}
			}
		}
	}
	
	return valid, invalid, nil
}

// SaveResumeData 保存恢复数据，参考 aria2 的 DefaultBtProgressInfoFile::save()
func (pm *PieceManager) SaveResumeData(filePath string) error {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	// 创建恢复数据结构
	resumeData := ResumeData{
		Version:        1, // 版本号
		InfoHash:       pm.infoHash,
		PieceLength:    pm.pieceLength,
		TotalLength:    pm.totalLength,
		Bitfield:       make([]byte, len(pm.bitfield)),
		PieceStates:    make([]PieceState, len(pm.pieces)),
		CompletedBytes: pm.completedBytes,
		UploadedBytes:  pm.uploadedBytes,
		SavedAt:        time.Now().Unix(),
	}

	// 复制 bitfield
	copy(resumeData.Bitfield, pm.bitfield)

	// 保存 piece 状态
	for i, piece := range pm.pieces {
		resumeData.PieceStates[i] = piece.State
	}

	// 序列化为 JSON
	data, err := json.Marshal(resumeData)
	if err != nil {
		return fmt.Errorf("serialize resume data failed: %w", err)
	}

	// 先写入临时文件，然后原子性重命名（参考 aria2 的实现）
	tempPath := filePath + ".tmp"
	if err := os.WriteFile(tempPath, data, 0644); err != nil {
		return fmt.Errorf("write temp file failed: %w", err)
	}

	// 原子性重命名
	if err := os.Rename(tempPath, filePath); err != nil {
		os.Remove(tempPath) // 清理临时文件
		return fmt.Errorf("rename file failed: %w", err)
	}

	return nil
}

// LoadResumeData 加载恢复数据，参考 aria2 的 DefaultBtProgressInfoFile::load()
func (pm *PieceManager) LoadResumeData(filePath string) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	// 读取文件
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("read resume file failed: %w", err)
	}

	// 反序列化
	var resumeData ResumeData
	if err := json.Unmarshal(data, &resumeData); err != nil {
		return fmt.Errorf("unmarshal resume data failed: %w", err)
	}

	// 验证版本
	if resumeData.Version != 1 {
		return fmt.Errorf("unsupported resume data version: %d", resumeData.Version)
	}

	// 验证 InfoHash（如果已设置）
	if pm.infoHash != [20]byte{} && resumeData.InfoHash != pm.infoHash {
		return fmt.Errorf("info hash mismatch")
	}

	// 验证 piece 长度和总长度
	if resumeData.PieceLength != pm.pieceLength || resumeData.TotalLength != pm.totalLength {
		return fmt.Errorf("torrent parameters mismatch")
	}

	// 恢复 bitfield
	if len(resumeData.Bitfield) != len(pm.bitfield) {
		return fmt.Errorf("bitfield length mismatch")
	}
	copy(pm.bitfield, resumeData.Bitfield)

	// 恢复 piece 状态
	if len(resumeData.PieceStates) != len(pm.pieces) {
		return fmt.Errorf("piece states count mismatch")
	}
	for i, state := range resumeData.PieceStates {
		pm.pieces[i].State = state
	}

	// 恢复统计信息
	pm.completedBytes = resumeData.CompletedBytes
	pm.uploadedBytes = resumeData.UploadedBytes

	return nil
}

// HasPiece 检查是否拥有piece
func (pm *PieceManager) HasPiece(index int) bool {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	
	if index < 0 || index >= len(pm.pieces) {
		return false
	}
	
	piece := pm.pieces[index]
	return piece.State == PieceStateVerified || piece.State == PieceStateWritten
}

// SetPiecePriority 设置piece优先级
func (pm *PieceManager) SetPiecePriority(index int, priority int) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	
	if index < 0 || index >= len(pm.pieces) {
		return fmt.Errorf("piece index %d out of range", index)
	}
	
	pm.pieces[index].Priority = priority
	return nil
}