// Package bt 提供BitTorrent协议支持
package bt

import (
	"bytes"
	"crypto/sha1"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// TorrentFile 表示解析后的.torrent文件
type TorrentFile struct {
	// 基本信息
	InfoHash     [20]byte   // 信息哈希
	Announce     string     // Tracker URL
	AnnounceList [][]string // Tracker列表
	CreatedBy    string     // 创建者
	CreationDate int64      // 创建时间戳
	Comment      string     // 注释
	Encoding     string     // 编码
	Info         FileInfo   // 文件信息
	
	// 私有种子标志
	Private bool
	
	// 文件列表（多文件模式）
	Files []FileEntry
	
	// Piece信息
	PieceLength int64   // 每个piece的大小（字节）
	Pieces      [][20]byte // 所有piece的SHA1哈希列表
}

// FileInfo 表示.torrent文件中的info字典
type FileInfo struct {
	// 单文件模式
	Name        string // 文件名
	Length      int64  // 文件大小
	MD5Sum      string // MD5校验和
	
	// 多文件模式
	Files       []FileEntry // 文件列表
	PieceLength int64       // 每个piece的大小
	Pieces      string      // piece哈希的串联字符串
	Private     int64       // 私有标志（0=公开，1=私有）
}

// FileEntry 表示多文件模式中的单个文件
type FileEntry struct {
	Length int64    // 文件大小
	Path   []string // 文件路径（目录层级）
	MD5Sum string   // MD5校验和
}

// PieceHash 表示一个piece的哈希
type PieceHash [20]byte

// ParseTorrentFile 解析.torrent文件
func ParseTorrentFile(filePath string) (*TorrentFile, error) {
	// 读取文件
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("read torrent file failed: %w", err)
	}
	
	return ParseTorrentData(data)
}

// ParseTorrentData 解析.torrent数据
func ParseTorrentData(data []byte) (*TorrentFile, error) {
	if len(data) == 0 {
		return nil, errors.New("empty torrent data")
	}
	
	// 解码B编码数据
	decoder := NewBDecoder(bytes.NewReader(data))
	decoded, err := decoder.Decode()
	if err != nil {
		return nil, fmt.Errorf("bdecode failed: %w", err)
	}
	
	// 转换为字典
	dict, ok := decoded.(map[string]interface{})
	if !ok {
		return nil, errors.New("torrent file must be a dictionary")
	}
	
	// 解析.torrent文件
	torrent := &TorrentFile{}
	
	// 解析announce
	if announce, ok := dict["announce"].(string); ok {
		torrent.Announce = announce
	}
	
	// 解析announce-list
	if announceList, ok := dict["announce-list"].([]interface{}); ok {
		for _, tier := range announceList {
			if urls, ok := tier.([]interface{}); ok {
				var tierURLs []string
				for _, url := range urls {
					if urlStr, ok := url.(string); ok {
						tierURLs = append(tierURLs, urlStr)
					}
				}
				if len(tierURLs) > 0 {
					torrent.AnnounceList = append(torrent.AnnounceList, tierURLs)
				}
			}
		}
	}
	
	// 解析创建信息
	if createdBy, ok := dict["created by"].(string); ok {
		torrent.CreatedBy = createdBy
	}
	
	if creationDate, ok := dict["creation date"].(int64); ok {
		torrent.CreationDate = creationDate
	}
	
	if comment, ok := dict["comment"].(string); ok {
		torrent.Comment = comment
	}
	
	if encoding, ok := dict["encoding"].(string); ok {
		torrent.Encoding = encoding
	}
	
	// 解析info字典
	infoDict, ok := dict["info"].(map[string]interface{})
	if !ok {
		return nil, errors.New("missing info dictionary")
	}
	
	// 计算info哈希（对整个info字典进行B编码后的SHA1）
	infoData := bencodeDict(infoDict)
	torrent.InfoHash = sha1.Sum(infoData)
	
	// 解析文件信息
	fileInfo, err := parseFileInfo(infoDict)
	if err != nil {
		return nil, fmt.Errorf("parse file info failed: %w", err)
	}
	torrent.Info = fileInfo
	
	// 设置piece信息
	torrent.PieceLength = fileInfo.PieceLength
	torrent.Private = fileInfo.Private != 0
	
	// 解析pieces哈希
	if len(fileInfo.Pieces)%20 != 0 {
		return nil, errors.New("invalid pieces data length")
	}
	
	numPieces := len(fileInfo.Pieces) / 20
	torrent.Pieces = make([][20]byte, numPieces)
	for i := 0; i < numPieces; i++ {
		pieceHash := [20]byte{}
		copy(pieceHash[:], fileInfo.Pieces[i*20:(i+1)*20])
		torrent.Pieces[i] = pieceHash
	}
	
	// 构建文件列表
	if fileInfo.Name != "" && fileInfo.Length > 0 {
		// 单文件模式
		torrent.Files = []FileEntry{{
			Length: fileInfo.Length,
			Path:   []string{fileInfo.Name},
			MD5Sum: fileInfo.MD5Sum,
		}}
	} else if len(fileInfo.Files) > 0 {
		// 多文件模式
		torrent.Files = fileInfo.Files
	} else {
		return nil, errors.New("no file information found")
	}
	
	return torrent, nil
}

// parseFileInfo 解析info字典
func parseFileInfo(infoDict map[string]interface{}) (FileInfo, error) {
	var info FileInfo
	
	// 解析piece长度
	if pieceLength, ok := infoDict["piece length"].(int64); ok {
		info.PieceLength = pieceLength
	} else {
		return info, errors.New("missing piece length")
	}
	
	// 解析pieces哈希
	if pieces, ok := infoDict["pieces"].(string); ok {
		info.Pieces = pieces
	} else {
		return info, errors.New("missing pieces")
	}
	
	// 解析私有标志
	if private, ok := infoDict["private"].(int64); ok {
		info.Private = private
	}
	
	// 检查是单文件还是多文件模式
	if name, ok := infoDict["name"].(string); ok {
		info.Name = name
		
		// 检查是否有length字段（单文件模式）
		if length, ok := infoDict["length"].(int64); ok {
			info.Length = length
			
			// 解析MD5校验和
			if md5sum, ok := infoDict["md5sum"].(string); ok {
				info.MD5Sum = md5sum
			}
		} else if files, ok := infoDict["files"].([]interface{}); ok {
			// 多文件模式
			for i, file := range files {
				if fileDict, ok := file.(map[string]interface{}); ok {
					entry := FileEntry{}
					
					// 解析文件长度
					if length, ok := fileDict["length"].(int64); ok {
						entry.Length = length
					} else {
						return info, fmt.Errorf("file %d missing length", i)
					}
					
					// 解析文件路径
					if pathList, ok := fileDict["path"].([]interface{}); ok {
						var path []string
						for _, pathPart := range pathList {
							if part, ok := pathPart.(string); ok {
								path = append(path, part)
							}
						}
						if len(path) == 0 {
							return info, fmt.Errorf("file %d has empty path", i)
						}
						entry.Path = path
					} else {
						return info, fmt.Errorf("file %d missing path", i)
					}
					
					// 解析MD5校验和
					if md5sum, ok := fileDict["md5sum"].(string); ok {
						entry.MD5Sum = md5sum
					}
					
					info.Files = append(info.Files, entry)
				}
			}
		} else {
			return info, errors.New("missing length or files in info dictionary")
		}
	} else {
		return info, errors.New("missing name in info dictionary")
	}
	
	return info, nil
}

// TotalSize 计算torrent的总大小
func (tf *TorrentFile) TotalSize() int64 {
	var total int64
	for _, file := range tf.Files {
		total += file.Length
	}
	return total
}

// NumPieces 计算piece数量
func (tf *TorrentFile) NumPieces() int {
	return len(tf.Pieces)
}

// GetPieceHash 获取指定piece的哈希
func (tf *TorrentFile) GetPieceHash(index int) ([20]byte, error) {
	if index < 0 || index >= len(tf.Pieces) {
		return [20]byte{}, fmt.Errorf("piece index %d out of range [0, %d)", index, len(tf.Pieces))
	}
	return tf.Pieces[index], nil
}

// GetFilePaths 获取所有文件的路径
func (tf *TorrentFile) GetFilePaths() []string {
	var paths []string
	for _, file := range tf.Files {
		path := strings.Join(file.Path, string(os.PathSeparator))
		paths = append(paths, path)
	}
	return paths
}

// InfoHashString 返回info哈希的十六进制字符串
func (tf *TorrentFile) InfoHashString() string {
	return fmt.Sprintf("%x", tf.InfoHash)
}

// MagnetLink 生成magnet链接
func (tf *TorrentFile) MagnetLink() string {
	var buf strings.Builder
	buf.WriteString("magnet:?xt=urn:btih:")
	buf.WriteString(tf.InfoHashString())
	
	if tf.Info.Name != "" {
		buf.WriteString("&dn=")
		buf.WriteString(urlEncode(tf.Info.Name))
	}
	
	if len(tf.AnnounceList) > 0 {
		for _, tier := range tf.AnnounceList {
			for _, tracker := range tier {
				buf.WriteString("&tr=")
				buf.WriteString(urlEncode(tracker))
			}
		}
	} else if tf.Announce != "" {
		buf.WriteString("&tr=")
		buf.WriteString(urlEncode(tf.Announce))
	}
	
	return buf.String()
}

// BDecoder B编码解码器
type BDecoder struct {
	reader *bytes.Reader
}

// NewBDecoder 创建新的B解码器
func NewBDecoder(r io.Reader) *BDecoder {
	var buf bytes.Buffer
	io.Copy(&buf, r)
	return &BDecoder{
		reader: bytes.NewReader(buf.Bytes()),
	}
}

// Decode 解码B编码数据
func (bd *BDecoder) Decode() (interface{}, error) {
	byte, err := bd.reader.ReadByte()
	if err != nil {
		return nil, err
	}
	
	// 回溯一个字节
	bd.reader.Seek(-1, io.SeekCurrent)
	
	switch {
	case byte >= '0' && byte <= '9':
		// 字符串
		return bd.decodeString()
	case byte == 'i':
		// 整数
		return bd.decodeInteger()
	case byte == 'l':
		// 列表
		return bd.decodeList()
	case byte == 'd':
		// 字典
		return bd.decodeDict()
	default:
		return nil, fmt.Errorf("invalid bencode type: %c", byte)
	}
}

// decodeString 解码字符串
func (bd *BDecoder) decodeString() (string, error) {
	// 读取长度，直到遇到冒号
	var lengthBytes []byte
	for {
		b, err := bd.reader.ReadByte()
		if err != nil {
			return "", err
		}
		if b == ':' {
			break
		}
		lengthBytes = append(lengthBytes, b)
	}
	
	// 解析长度
	var length int64
	for _, ch := range lengthBytes {
		if ch < '0' || ch > '9' {
			return "", errors.New("invalid string length")
		}
		length = length*10 + int64(ch-'0')
	}
	
	if length < 0 {
		return "", errors.New("negative string length")
	}
	
	// 读取字符串数据
	data := make([]byte, length)
	n, err := bd.reader.Read(data)
	if err != nil {
		return "", err
	}
	if int64(n) != length {
		return "", errors.New("short read")
	}
	
	return string(data), nil
}

// decodeInteger 解码整数
func (bd *BDecoder) decodeInteger() (int64, error) {
	// 读取'i'
	b, err := bd.reader.ReadByte()
	if err != nil {
		return 0, err
	}
	if b != 'i' {
		return 0, errors.New("expected 'i'")
	}
	
	// 读取数字，直到遇到'e'
	var numBytes []byte
	for {
		b, err := bd.reader.ReadByte()
		if err != nil {
			return 0, err
		}
		if b == 'e' {
			break
		}
		numBytes = append(numBytes, b)
	}
	
	// 解析数字
	var num int64
	var negative bool
	
	for i, ch := range numBytes {
		if i == 0 && ch == '-' {
			negative = true
			continue
		}
		if ch < '0' || ch > '9' {
			return 0, errors.New("invalid integer")
		}
		num = num*10 + int64(ch-'0')
	}
	
	if negative {
		num = -num
	}
	
	return num, nil
}

// decodeList 解码列表
func (bd *BDecoder) decodeList() ([]interface{}, error) {
	// 读取'l'
	byte, err := bd.reader.ReadByte()
	if err != nil {
		return nil, err
	}
	if byte != 'l' {
		return nil, errors.New("expected 'l'")
	}
	
	var list []interface{}
	
	for {
		// 检查是否到达列表结尾
		nextByte, err := bd.reader.ReadByte()
		if err != nil {
			return nil, err
		}
		
		if nextByte == 'e' {
			// 列表结束
			break
		}
		
		// 回溯一个字节
		bd.reader.Seek(-1, io.SeekCurrent)
		
		// 解码元素
		element, err := bd.Decode()
		if err != nil {
			return nil, err
		}
		
		list = append(list, element)
	}
	
	return list, nil
}

// decodeDict 解码字典
func (bd *BDecoder) decodeDict() (map[string]interface{}, error) {
	// 读取'd'
	byte, err := bd.reader.ReadByte()
	if err != nil {
		return nil, err
	}
	if byte != 'd' {
		return nil, errors.New("expected 'd'")
	}
	
	dict := make(map[string]interface{})
	
	for {
		// 检查是否到达字典结尾
		nextByte, err := bd.reader.ReadByte()
		if err != nil {
			return nil, err
		}
		
		if nextByte == 'e' {
			// 字典结束
			break
		}
		
		// 回溯一个字节
		bd.reader.Seek(-1, io.SeekCurrent)
		
		// 解码键（必须是字符串）
		key, err := bd.decodeString()
		if err != nil {
			return nil, err
		}
		
		// 解码值
		value, err := bd.Decode()
		if err != nil {
			return nil, err
		}
		
		dict[key] = value
	}
	
	return dict, nil
}

// bencodeDict 将字典编码为B编码
func bencodeDict(dict map[string]interface{}) []byte {
	var buf bytes.Buffer
	
	// 按键排序（B编码要求字典键排序）
	keys := make([]string, 0, len(dict))
	for k := range dict {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	
	buf.WriteByte('d')
	
	for _, key := range keys {
		value := dict[key]
		
		// 编码键
		bencodeString(&buf, key)
		
		// 编码值
		bencodeValue(&buf, value)
	}
	
	buf.WriteByte('e')
	return buf.Bytes()
}

// bencodeValue 编码值
func bencodeValue(buf *bytes.Buffer, value interface{}) {
	switch v := value.(type) {
	case string:
		bencodeString(buf, v)
	case int, int8, int16, int32, int64:
		var num int64
		switch n := v.(type) {
		case int:
			num = int64(n)
		case int8:
			num = int64(n)
		case int16:
			num = int64(n)
		case int32:
			num = int64(n)
		case int64:
			num = n
		}
		bencodeInteger(buf, num)
	case []interface{}:
		bencodeList(buf, v)
	case map[string]interface{}:
		bencodeDictToWriter(buf, v)
	default:
		// 忽略不支持的类型
	}
}

// bencodeString 编码字符串
func bencodeString(buf *bytes.Buffer, s string) {
	buf.WriteString(fmt.Sprintf("%d:", len(s)))
	buf.WriteString(s)
}

// bencodeInteger 编码整数
func bencodeInteger(buf *bytes.Buffer, n int64) {
	buf.WriteString(fmt.Sprintf("i%de", n))
}

// bencodeList 编码列表
func bencodeList(buf *bytes.Buffer, list []interface{}) {
	buf.WriteByte('l')
	for _, item := range list {
		bencodeValue(buf, item)
	}
	buf.WriteByte('e')
}

// bencodeDictToWriter 编码字典到writer
func bencodeDictToWriter(buf *bytes.Buffer, dict map[string]interface{}) {
	// 按键排序
	keys := make([]string, 0, len(dict))
	for k := range dict {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	
	buf.WriteByte('d')
	
	for _, key := range keys {
		value := dict[key]
		
		// 编码键
		bencodeString(buf, key)
		
		// 编码值
		bencodeValue(buf, value)
	}
	
	buf.WriteByte('e')
}

// urlEncode URL编码（简化版本）
func urlEncode(s string) string {
	return strings.ReplaceAll(s, " ", "%20")
}

// PieceRange 计算piece在文件中的范围
type PieceRange struct {
	Index     int   // piece索引
	FileIndex int   // 文件索引
	FileOffset int64 // 在文件中的偏移量
	PieceSize  int64 // piece大小
	Data       []byte // piece数据
}

// CalculatePieceRanges 计算每个piece在文件中的范围
func (tf *TorrentFile) CalculatePieceRanges() ([]PieceRange, error) {
	if len(tf.Files) == 0 {
		return nil, errors.New("no files in torrent")
	}
	
	numPieces := tf.NumPieces()
	ranges := make([]PieceRange, numPieces)
	
	// 单文件模式
	if len(tf.Files) == 1 {
		fileSize := tf.Files[0].Length
		pieceLength := tf.PieceLength
		
		for i := 0; i < numPieces; i++ {
			start := int64(i) * pieceLength
			end := start + pieceLength
			if end > fileSize {
				end = fileSize
			}
			
			ranges[i] = PieceRange{
				Index:       i,
				FileIndex:   0,
				FileOffset:  start,
				PieceSize:   end - start,
			}
		}
		
		return ranges, nil
	}
	
	// 多文件模式
	// 计算每个文件的累计偏移量
	fileOffsets := make([]int64, len(tf.Files))
	var totalOffset int64
	for i, file := range tf.Files {
		fileOffsets[i] = totalOffset
		totalOffset += file.Length
	}
	
	// 计算piece范围
	var currentPiece int
	var pieceOffset int64
	
	for fileIdx, file := range tf.Files {
		fileSize := file.Length
		fileStart := fileOffsets[fileIdx]
		fileEnd := fileStart + fileSize
		
		for pieceOffset < fileEnd && currentPiece < numPieces {
			pieceStart := int64(currentPiece) * tf.PieceLength
			pieceEnd := pieceStart + tf.PieceLength
			
			// 计算piece与文件的交集
			intersectStart := max(pieceStart, fileStart)
			intersectEnd := min(pieceEnd, fileEnd)
			
			if intersectStart < intersectEnd {
				// piece跨越这个文件
				ranges[currentPiece] = PieceRange{
					Index:      currentPiece,
					FileIndex:  fileIdx,
					FileOffset: intersectStart - fileStart,
					PieceSize:  intersectEnd - intersectStart,
				}
			}
			
			if pieceEnd <= fileEnd {
				// piece完全在这个文件中
				pieceOffset = pieceEnd
				currentPiece++
			} else {
				// piece跨越到下一个文件
				break
			}
		}
	}
	
	return ranges, nil
}

func max(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func min(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// VerifyPiece 验证piece的哈希
func (tf *TorrentFile) VerifyPiece(index int, data []byte) bool {
	if index < 0 || index >= len(tf.Pieces) {
		return false
	}
	
	expectedHash := tf.Pieces[index]
	actualHash := sha1.Sum(data)
	
	return bytes.Equal(expectedHash[:], actualHash[:])
}

// VerifyFile 验证整个文件的完整性
func (tf *TorrentFile) VerifyFile(filePath string) (bool, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return false, err
	}
	defer file.Close()
	
	fileInfo, err := file.Stat()
	if err != nil {
		return false, err
	}
	
	// 检查文件大小
	if fileInfo.Size() != tf.TotalSize() {
		return false, nil
	}
	
	// 验证每个piece
	buffer := make([]byte, tf.PieceLength)
	
	for i := 0; i < tf.NumPieces(); i++ {
		// 读取piece
		pieceSize := tf.PieceLength
		if i == tf.NumPieces()-1 {
			// 最后一个piece可能较小
			pieceSize = tf.TotalSize() - int64(i)*tf.PieceLength
		}
		
		n, err := file.ReadAt(buffer[:pieceSize], int64(i)*tf.PieceLength)
		if err != nil && err != io.EOF {
			return false, err
		}
		if int64(n) != pieceSize {
			return false, fmt.Errorf("piece %d: read %d bytes, expected %d", i, n, pieceSize)
		}
		
		// 验证哈希
		if !tf.VerifyPiece(i, buffer[:pieceSize]) {
			return false, nil
		}
	}
	
	return true, nil
}