package rpc

import (
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"aria2go/internal/core"
	bt "aria2go/internal/protocol/bt"
	metalink "aria2go/internal/protocol/metalink"
	pb "aria2go/pkg/api/pb/aria2go/pkg/api"
)

// Server gRPC服务器实现
type Server struct {
	pb.UnimplementedAria2ServiceServer
	
	engine core.Engine
	config *Config
	
	mu     sync.RWMutex
	server *grpc.Server
}

// Config gRPC服务器配置
type Config struct {
	Host         string        // 监听主机
	Port         int           // 监听端口
	EnableTLS    bool          // 启用TLS
	CertFile     string        // 证书文件
	KeyFile      string        // 私钥文件
	EnableAuth   bool          // 启用认证
	Token        string        // 认证令牌
	MaxRecvSize  int           // 最大接收大小
	MaxSendSize  int           // 最大发送大小
	IdleTimeout  time.Duration // 空闲超时
}

// DefaultConfig 返回默认配置
func DefaultConfig() *Config {
	return &Config{
		Host:        "127.0.0.1",
		Port:        6800,
		EnableTLS:   false,
		EnableAuth:  false,
		MaxRecvSize: 10 * 1024 * 1024, // 10MB
		MaxSendSize: 10 * 1024 * 1024, // 10MB
		IdleTimeout: 120 * time.Second,
	}
}

// NewServer 创建新的gRPC服务器
func NewServer(engine core.Engine, config *Config) *Server {
	if config == nil {
		config = DefaultConfig()
	}
	
	return &Server{
		engine: engine,
		config: config,
	}
}

// Start 启动gRPC服务器
func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	// 创建gRPC服务器选项
	opts := []grpc.ServerOption{
		grpc.MaxRecvMsgSize(s.config.MaxRecvSize),
		grpc.MaxSendMsgSize(s.config.MaxSendSize),
		grpc.ConnectionTimeout(s.config.IdleTimeout),
	}
	
	// 添加认证中间件（如果启用）
	if s.config.EnableAuth && s.config.Token != "" {
		opts = append(opts, grpc.UnaryInterceptor(s.authUnaryInterceptor))
		opts = append(opts, grpc.StreamInterceptor(s.authStreamInterceptor))
	}
	
	// 创建gRPC服务器
	s.server = grpc.NewServer(opts...)
	pb.RegisterAria2ServiceServer(s.server, s)
	
	// 监听端口
	addr := fmt.Sprintf("%s:%d", s.config.Host, s.config.Port)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}
	
	// 启动服务器
	go func() {
		log.Printf("gRPC server listening on %s", addr)
		if err := s.server.Serve(lis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			log.Printf("gRPC server error: %v", err)
		}
	}()
	
	return nil
}

// Stop 停止gRPC服务器
func (s *Server) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	if s.server == nil {
		return nil
	}
	
	s.server.GracefulStop()
	s.server = nil
	
	log.Println("gRPC server stopped")
	return nil
}

// authUnaryInterceptor 认证一元拦截器
func (s *Server) authUnaryInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	// TODO: 实现认证逻辑
	return handler(ctx, req)
}

// authStreamInterceptor 认证流拦截器
func (s *Server) authStreamInterceptor(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	// TODO: 实现认证逻辑
	return handler(srv, ss)
}

// 以下为RPC方法实现占位符

// AddUri 添加URI下载任务
func (s *Server) AddUri(ctx context.Context, req *pb.AddUriRequest) (*pb.AddUriResponse, error) {
	// 生成任务ID（GID）
	taskID := fmt.Sprintf("gid-%d", time.Now().UnixNano())
	
	// 转换选项
	options := make(map[string]interface{})
	for k, v := range req.GetOptions() {
		options[k] = v
	}
	
	// 创建任务配置
	config := core.TaskConfig{
		URLs:       req.GetUris(),
		Options:    options,
		OutputPath: "", // 根据选项确定
	}
	
	// 确定输出路径（如果选项中有dir或out参数）
	if out, ok := options["out"]; ok {
		if outStr, ok := out.(string); ok {
			config.OutputPath = outStr
		}
	}
	
	// 创建基础任务（TODO: 需要根据协议创建具体的任务实现）
	// 目前仅创建基础任务占位符
	task := &core.BaseTask{
		// 需要eventCh，暂时传nil
		// 实际应该使用事件通道
	}
	
	// 添加到引擎
	if err := s.engine.AddTask(task); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to add task: %v", err)
	}
	
	// 返回GID
	return &pb.AddUriResponse{
		Gid: &pb.Gid{Value: taskID},
	}, nil
}

// AddTorrent 添加Torrent下载任务
func (s *Server) AddTorrent(ctx context.Context, req *pb.AddTorrentRequest) (*pb.AddTorrentResponse, error) {
	// 获取torrent数据
	torrentContent := req.GetTorrent()
	if len(torrentContent) == 0 {
		return nil, status.Error(codes.InvalidArgument, "torrent data is empty")
	}
	
	// 解析torrent文件
	torrent, err := bt.ParseTorrentData(torrentContent)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "parse torrent failed: %v", err)
	}
	
	// 提取可用的URLs（从trackers和可能的web seeds）
	var urls []string
	if torrent.Announce != "" {
		urls = append(urls, torrent.Announce)
	}
	
	for _, tier := range torrent.AnnounceList {
		urls = append(urls, tier...)
	}
	
	// 如果没有URLs，使用magnet链接
	if len(urls) == 0 {
		magnetLink := torrent.MagnetLink()
		urls = append(urls, magnetLink)
	}
	
	// 获取选项
	options := req.GetOptions()
	if options == nil {
		options = make(map[string]string)
	}
	
	// 设置默认输出文件名
	if _, hasOutput := options["out"]; !hasOutput && torrent.Info.Name != "" {
		options["out"] = torrent.Info.Name
	}
	
	// 创建任务配置（目前未使用，保留供未来扩展）
	optionsInterface := make(map[string]interface{})
	for k, v := range options {
		optionsInterface[k] = v
	}
	
	taskConfig := core.TaskConfig{
		URLs:       urls,
		Options:    optionsInterface,
		OutputPath: options["dir"], // 使用dir选项作为输出目录
	}
	_ = taskConfig // 暂时未使用
	
	// 尝试通过引擎创建任务
	// TODO: 需要引擎支持从配置创建任务
	// 目前先创建GID并返回成功响应
	gid := fmt.Sprintf("torrent-%x-%d", torrent.InfoHash[:8], time.Now().UnixNano())
	
	// 记录解析成功的日志
	log.Printf("Torrent parsed successfully: %s, infoHash: %x, files: %d, total size: %d", 
		torrent.Info.Name, torrent.InfoHash, len(torrent.Files), torrent.TotalSize())
	
	return &pb.AddTorrentResponse{
		Gid: &pb.Gid{Value: gid},
	}, nil
}

// AddMetalink 添加Metalink下载任务
func (s *Server) AddMetalink(ctx context.Context, req *pb.AddMetalinkRequest) (*pb.AddMetalinkResponse, error) {
	// 获取metalink数据
	metalinkContent := req.GetMetalink()
	if len(metalinkContent) == 0 {
		return nil, status.Error(codes.InvalidArgument, "metalink data is empty")
	}
	
	// 解析metalink文件
	metalink, err := metalink.ParseMetalinkData(metalinkContent)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "parse metalink failed: %v", err)
	}
	
	if len(metalink.Files) == 0 {
		return nil, status.Error(codes.InvalidArgument, "no files found in metalink")
	}
	
	// 获取选项
	options := req.GetOptions()
	if options == nil {
		options = make(map[string]string)
	}
	
	// 为每个文件创建GID
	var gidValues []*pb.Gid
	timestamp := time.Now().UnixNano()
	
	for i, file := range metalink.Files {
		// 生成唯一的GID
		gid := fmt.Sprintf("metalink-%d-%x-%d", i, sha1Hash(file.Name)[:8], timestamp)
		
		// 获取该文件的最佳资源
		resource := metalink.GetBestResource(i, "")
		if resource == nil {
			log.Printf("Warning: No suitable resource found for file %s", file.Name)
			continue
		}
		
		// 创建文件特定的选项
		fileOptions := make(map[string]string)
		for k, v := range options {
			fileOptions[k] = v
		}
		
		// 设置输出文件名
		if _, hasOut := fileOptions["out"]; !hasOut {
			fileOptions["out"] = file.Name
		}
		
		// 设置资源特定的选项
		if resource.Type != "" {
			fileOptions["metalink-resource-type"] = resource.Type
		}
		if resource.Priority > 0 {
			fileOptions["metalink-priority"] = fmt.Sprintf("%d", resource.Priority)
		}
		
		// 创建任务配置（目前未使用，保留供未来扩展）
		fileOptionsInterface := make(map[string]interface{})
		for k, v := range fileOptions {
			fileOptionsInterface[k] = v
		}
		
		taskConfig := core.TaskConfig{
			URLs:       []string{resource.URL},
			Options:    fileOptionsInterface,
			OutputPath: fileOptions["dir"], // 使用dir选项作为输出目录
		}
		_ = taskConfig // 暂时未使用
		
		// TODO: 通过引擎创建实际任务
		// 目前只记录日志
		log.Printf("Metalink file parsed: %s, size: %d, resource: %s, gid: %s", 
			file.Name, file.Size, resource.URL, gid)
		
		gidValues = append(gidValues, &pb.Gid{Value: gid})
	}
	
	if len(gidValues) == 0 {
		return nil, status.Error(codes.Internal, "failed to create any download tasks from metalink")
	}
	
	return &pb.AddMetalinkResponse{
		Gids: gidValues,
	}, nil
}

// sha1Hash 计算字符串的SHA1哈希
func sha1Hash(s string) []byte {
	h := sha1.New()
	h.Write([]byte(s))
	return h.Sum(nil)
}

// Remove 移除下载任务
func (s *Server) Remove(ctx context.Context, req *pb.RemoveRequest) (*pb.RemoveResponse, error) {
	// 获取任务GID
	gid := req.GetGid().GetValue()
	if gid == "" {
		return nil, status.Error(codes.InvalidArgument, "gid is required")
	}
	
	// 从引擎移除任务
	err := s.engine.RemoveTask(gid)
	if err != nil {
		// 检查是否是任务未找到错误
		if err.Error() == "task not found" {
			return nil, status.Error(codes.NotFound, "task not found")
		}
		return nil, status.Errorf(codes.Internal, "failed to remove task: %v", err)
	}
	
	// 返回成功响应
	return &pb.RemoveResponse{
		Gid: &pb.Gid{Value: gid},
	}, nil
}

// ForceRemove 强制移除下载任务
func (s *Server) ForceRemove(ctx context.Context, req *pb.ForceRemoveRequest) (*pb.ForceRemoveResponse, error) {
	// 获取任务GID
	gid := req.GetGid().GetValue()
	if gid == "" {
		return nil, status.Error(codes.InvalidArgument, "gid is required")
	}
	
	// TODO: 强制移除应该先停止任务，然后再移除
	// 目前先调用普通移除
	err := s.engine.RemoveTask(gid)
	if err != nil {
		// 检查是否是任务未找到错误
		if err.Error() == "task not found" {
			return nil, status.Error(codes.NotFound, "task not found")
		}
		return nil, status.Errorf(codes.Internal, "failed to force remove task: %v", err)
	}
	
	// 返回成功响应
	return &pb.ForceRemoveResponse{
		Gid: &pb.Gid{Value: gid},
	}, nil
}

// Pause 暂停下载任务
func (s *Server) Pause(ctx context.Context, req *pb.PauseRequest) (*pb.PauseResponse, error) {
	// 获取任务GID
	gid := req.GetGid().GetValue()
	if gid == "" {
		return nil, status.Error(codes.InvalidArgument, "gid is required")
	}
	
	// 暂停任务
	err := s.engine.PauseTask(gid)
	if err != nil {
		// 检查是否是任务未找到错误
		if err.Error() == "task not found" {
			return nil, status.Error(codes.NotFound, "task not found")
		}
		return nil, status.Errorf(codes.Internal, "failed to pause task: %v", err)
	}
	
	// 返回成功响应
	return &pb.PauseResponse{
		Gid: &pb.Gid{Value: gid},
	}, nil
}

// PauseAll 暂停所有下载任务
func (s *Server) PauseAll(ctx context.Context, req *pb.PauseAllRequest) (*pb.PauseAllResponse, error) {
	// TODO: 实现暂停所有任务
	// 目前返回成功响应
	return &pb.PauseAllResponse{
		Success: true,
	}, nil
}

// ForcePause 强制暂停下载任务
func (s *Server) ForcePause(ctx context.Context, req *pb.ForcePauseRequest) (*pb.ForcePauseResponse, error) {
	// 获取任务GID
	gid := req.GetGid().GetValue()
	if gid == "" {
		return nil, status.Error(codes.InvalidArgument, "gid is required")
	}
	
	// TODO: 强制暂停应该先停止任务，然后再暂停
	// 目前先调用普通暂停
	err := s.engine.PauseTask(gid)
	if err != nil {
		// 检查是否是任务未找到错误
		if err.Error() == "task not found" {
			return nil, status.Error(codes.NotFound, "task not found")
		}
		return nil, status.Errorf(codes.Internal, "failed to force pause task: %v", err)
	}
	
	// 返回成功响应
	return &pb.ForcePauseResponse{
		Gid: &pb.Gid{Value: gid},
	}, nil
}

// ForcePauseAll 强制暂停所有下载任务
func (s *Server) ForcePauseAll(ctx context.Context, req *pb.ForcePauseAllRequest) (*pb.ForcePauseAllResponse, error) {
	// TODO: 实现强制暂停所有任务
	// 目前返回成功响应
	return &pb.ForcePauseAllResponse{
		Success: true,
	}, nil
}

// Unpause 恢复下载任务
func (s *Server) Unpause(ctx context.Context, req *pb.UnpauseRequest) (*pb.UnpauseResponse, error) {
	// 获取任务GID
	gid := req.GetGid().GetValue()
	if gid == "" {
		return nil, status.Error(codes.InvalidArgument, "gid is required")
	}
	
	// 恢复任务
	err := s.engine.ResumeTask(gid)
	if err != nil {
		// 检查是否是任务未找到错误
		if err.Error() == "task not found" {
			return nil, status.Error(codes.NotFound, "task not found")
		}
		return nil, status.Errorf(codes.Internal, "failed to unpause task: %v", err)
	}
	
	// 返回成功响应
	return &pb.UnpauseResponse{
		Gid: &pb.Gid{Value: gid},
	}, nil
}

// UnpauseAll 恢复所有下载任务
func (s *Server) UnpauseAll(ctx context.Context, req *pb.UnpauseAllRequest) (*pb.UnpauseAllResponse, error) {
	// TODO: 实现恢复所有任务
	// 目前返回成功响应
	return &pb.UnpauseAllResponse{
		Success: true,
	}, nil
}

// TellStatus 获取任务状态

func (s *Server) TellStatus(ctx context.Context, req *pb.TellStatusRequest) (*pb.TellStatusResponse, error) {

	// 获取任务GID

	gid := req.GetGid().GetValue()

	if gid == "" {

		return nil, status.Error(codes.InvalidArgument, "gid is required")

	}

	

	// 从引擎获取任务状态

	taskStatus, err := s.engine.GetTaskStatus(gid)

	if err != nil {

		// 检查是否是任务未找到错误

		if err.Error() == "task not found" {

			return nil, status.Error(codes.NotFound, "task not found")

		}

		return nil, status.Errorf(codes.Internal, "failed to get task status: %v", err)

	}

	

	// 转换状态枚举

	var pbStatus pb.Status

	switch taskStatus.State {

	case core.TaskStateActive:

		pbStatus = pb.Status_STATUS_ACTIVE

	case core.TaskStateWaiting:

		pbStatus = pb.Status_STATUS_WAITING

	case core.TaskStatePaused:

		pbStatus = pb.Status_STATUS_PAUSED

	case core.TaskStateError:

		pbStatus = pb.Status_STATUS_ERROR

	case core.TaskStateCompleted:

		pbStatus = pb.Status_STATUS_COMPLETE

	case core.TaskStateStopped:

		// aria2 中没有直接的stopped状态，映射为REMOVED或保持原样

		// 这里映射为REMOVED，因为stopped可能意味着任务已移除

		pbStatus = pb.Status_STATUS_REMOVED

	default:

		pbStatus = pb.Status_STATUS_WAITING

	}

	

	// 构建DownloadStatus

	downloadStatus := &pb.DownloadStatus{

		Gid:      &pb.Gid{Value: gid},

		Status:   pbStatus,

		// TODO: 填充更多字段

	}

	

	// 如果有错误信息，添加到响应

	if taskStatus.Error != nil {

		downloadStatus.ErrorMessage = taskStatus.Error.Error()

	}

	

	// TODO: 添加更多字段，如下载进度、文件信息等

	// 需要从任务对象获取更多信息

	

	return &pb.TellStatusResponse{

		Status: downloadStatus,

	}, nil

}

// TellActive 获取活动任务列表
func (s *Server) TellActive(ctx context.Context, req *pb.TellActiveRequest) (*pb.TellActiveResponse, error) {
	// TODO: 需要engine提供获取活动任务列表的方法
	// 目前返回空列表
	return &pb.TellActiveResponse{
		Statuses: []*pb.DownloadStatus{},
	}, nil
}

// TellWaiting 获取等待任务列表
func (s *Server) TellWaiting(ctx context.Context, req *pb.TellWaitingRequest) (*pb.TellWaitingResponse, error) {
	// TODO: 需要engine提供获取等待任务列表的方法
	// 目前返回空列表
	return &pb.TellWaitingResponse{
		Statuses: []*pb.DownloadStatus{},
	}, nil
}

// TellStopped 获取停止任务列表
func (s *Server) TellStopped(ctx context.Context, req *pb.TellStoppedRequest) (*pb.TellStoppedResponse, error) {
	// TODO: 需要engine提供获取停止任务列表的方法
	// 目前返回空列表
	return &pb.TellStoppedResponse{
		Statuses: []*pb.DownloadStatus{},
	}, nil
}

// GetUris 获取任务URI列表
func (s *Server) GetUris(ctx context.Context, req *pb.GetUrisRequest) (*pb.GetUrisResponse, error) {
	// TODO: 从任务获取URI列表
	// 目前返回示例数据
	gid := req.GetGid().GetValue()
	_ = gid // 暂时忽略
	
	// 返回示例URI列表
	uris := []*pb.Uri{
		{
			Uri:    "http://example.com/file1.zip",
			Status: "used",
		},
		{
			Uri:    "http://example.com/file2.zip",
			Status: "waiting",
		},
	}
	
	return &pb.GetUrisResponse{
		Uris: uris,
	}, nil
}

// GetFiles 获取任务文件列表
func (s *Server) GetFiles(ctx context.Context, req *pb.GetFilesRequest) (*pb.GetFilesResponse, error) {
	// TODO: 从任务获取文件列表
	// 目前返回示例数据
	gid := req.GetGid().GetValue()
	_ = gid // 暂时忽略
	
	// 返回示例文件列表
	files := []*pb.File{
		{
			Index:            1,
			Path:             "/downloads/file1.zip",
			Length:           1024 * 1024 * 100, // 100MB
			CompletedLength:  1024 * 1024 * 50,  // 50MB
			Selected:         true,
		},
		{
			Index:            2,
			Path:             "/downloads/file2.zip",
			Length:           1024 * 1024 * 200, // 200MB
			CompletedLength:  1024 * 1024 * 0,   // 0MB
			Selected:         false,
		},
	}
	
	return &pb.GetFilesResponse{
		Files: files,
	}, nil
}

// GetPeers 获取任务Peer列表
func (s *Server) GetPeers(ctx context.Context, req *pb.GetPeersRequest) (*pb.GetPeersResponse, error) {
	// TODO: 从BitTorrent任务获取Peer列表
	// 目前返回示例数据
	gid := req.GetGid().GetValue()
	_ = gid // 暂时忽略
	
	// 返回示例Peer列表
	peers := []*pb.Peer{
		{
			PeerId:        "peer-001",
			Ip:            "192.168.1.101",
			Port:          6881,
			Bitfield:      0xFFFFFF, // 示例bitfield
			AmChoking:     false,
			AmInterested:  true,
			PeerChoking:   false,
			PeerInterested: true,
			DownloadSpeed: 1024 * 1024, // 1MB/s
			UploadSpeed:   512 * 1024,  // 512KB/s
			Seeder:        "true",
		},
		{
			PeerId:        "peer-002",
			Ip:            "192.168.1.102",
			Port:          6881,
			Bitfield:      0xFF00, // 示例bitfield
			AmChoking:     true,
			AmInterested:  false,
			PeerChoking:   true,
			PeerInterested: false,
			DownloadSpeed: 512 * 1024,
			UploadSpeed:   256 * 1024,
			Seeder:        "false",
		},
	}
	
	return &pb.GetPeersResponse{
		Peers: peers,
	}, nil
}

// GetServers 获取任务服务器列表
func (s *Server) GetServers(ctx context.Context, req *pb.GetServersRequest) (*pb.GetServersResponse, error) {
	// TODO: 从任务获取服务器列表（如HTTP/FTP服务器）
	// 目前返回示例数据
	gid := req.GetGid().GetValue()
	_ = gid // 暂时忽略
	
	// 返回示例服务器列表
	servers := []*pb.Server{
		{
			Uri:            "http://example.com/file1.zip",
			CurrentUri:     "http://example.com/file1.zip",
			DownloadSpeed:  1024 * 1024, // 1MB/s
		},
		{
			Uri:            "http://example.com/file2.zip",
			CurrentUri:     "http://example.com/file2.zip",
			DownloadSpeed:  512 * 1024, // 512KB/s
		},
	}
	
	return &pb.GetServersResponse{
		Servers: servers,
	}, nil
}

// GetGlobalStat 获取全局统计信息
func (s *Server) GetGlobalStat(ctx context.Context, req *pb.GetGlobalStatRequest) (*pb.GetGlobalStatResponse, error) {
	// 从引擎获取全局统计
	stat := s.engine.GetGlobalStat()
	
	// 创建GlobalStat
	globalStat := &pb.GlobalStat{
		DownloadSpeed: int32(stat.DownloadSpeed),
		UploadSpeed:   int32(stat.UploadSpeed),
		NumActive:     int32(stat.NumActive),
		NumWaiting:    int32(stat.NumWaiting),
		NumStopped:    int32(stat.NumStopped),
	}
	
	return &pb.GetGlobalStatResponse{
		Stat: globalStat,
	}, nil
}

// GetOption 获取任务选项
func (s *Server) GetOption(ctx context.Context, req *pb.GetOptionRequest) (*pb.GetOptionResponse, error) {
	// TODO: 获取任务配置选项
	gid := req.GetGid().GetValue()
	_ = gid // 暂时忽略
	
	// 返回示例配置选项
	options := map[string]string{
		"dir":           "/downloads",
		"max-connection-per-server": "5",
		"split":        "5",
		"continue":     "true",
		"check-integrity": "false",
	}
	
	// 转换为StringMap格式
	return &pb.GetOptionResponse{
		Options: options,
	}, nil
}

// ChangeOption 更改任务选项
func (s *Server) ChangeOption(ctx context.Context, req *pb.ChangeOptionRequest) (*pb.ChangeOptionResponse, error) {
	// TODO: 修改任务配置选项
	gid := req.GetGid().GetValue()
	options := req.GetOptions()
	
	_ = gid    // 暂时忽略
	_ = options // 暂时忽略
	
	// 返回成功响应
	return &pb.ChangeOptionResponse{
		Success: true,
	}, nil
}

// GetGlobalOption 获取全局选项
func (s *Server) GetGlobalOption(ctx context.Context, req *pb.GetGlobalOptionRequest) (*pb.GetGlobalOptionResponse, error) {
	// TODO: 获取全局配置选项
	
	// 返回示例全局配置选项
	options := map[string]string{
		"dir":           "/downloads",
		"max-overall-download-limit": "0",
		"max-overall-upload-limit": "0",
		"max-concurrent-downloads": "5",
		"continue":     "true",
		"auto-file-renaming": "true",
	}
	
	return &pb.GetGlobalOptionResponse{
		Options: options,
	}, nil
}

// ChangeGlobalOption 更改全局选项
func (s *Server) ChangeGlobalOption(ctx context.Context, req *pb.ChangeGlobalOptionRequest) (*pb.ChangeGlobalOptionResponse, error) {
	// TODO: 修改全局配置选项
	options := req.GetOptions()
	
	_ = options // 暂时忽略
	
	// 返回成功响应
	return &pb.ChangeGlobalOptionResponse{
		Success: true,
	}, nil
}

// Shutdown 关闭aria2
func (s *Server) Shutdown(ctx context.Context, req *pb.ShutdownRequest) (*pb.ShutdownResponse, error) {
	// TODO: 实现优雅关闭逻辑
	// 应该停止下载引擎和gRPC服务器
	// 目前只返回成功响应
	return &pb.ShutdownResponse{
		Success: true,
	}, nil
}

// ForceShutdown 强制关闭aria2
func (s *Server) ForceShutdown(ctx context.Context, req *pb.ForceShutdownRequest) (*pb.ForceShutdownResponse, error) {
	// TODO: 实现强制关闭逻辑，立即停止所有任务
	// 目前只返回成功响应
	return &pb.ForceShutdownResponse{
		Success: true,
	}, nil
}

// SaveSession 保存会话
func (s *Server) SaveSession(ctx context.Context, req *pb.SaveSessionRequest) (*pb.SaveSessionResponse, error) {
	// TODO: 保存会话到文件
	
	// 返回成功响应
	return &pb.SaveSessionResponse{
		Success: true,
	}, nil
}

// SystemMulticall 系统批量调用
func (s *Server) SystemMulticall(ctx context.Context, req *pb.SystemMulticallRequest) (*pb.SystemMulticallResponse, error) {
	// TODO: 实现批量调用多个RPC方法
	calls := req.GetCalls()
	
	// 简单返回每个调用的结果
	var results []string
	for range calls {
		results = append(results, "{}") // 空JSON对象
	}
	
	return &pb.SystemMulticallResponse{
		Results: results,
	}, nil
}

// SystemListMethods 系统方法列表
func (s *Server) SystemListMethods(ctx context.Context, req *pb.SystemListMethodsRequest) (*pb.SystemListMethodsResponse, error) {
	// 返回所有可用的RPC方法名
	methods := []string{
		"aria2.addUri",
		"aria2.addTorrent",
		"aria2.addMetalink",
		"aria2.remove",
		"aria2.forceRemove",
		"aria2.pause",
		"aria2.forcePause",
		"aria2.pauseAll",
		"aria2.forcePauseAll",
		"aria2.unpause",
		"aria2.unpauseAll",
		"aria2.tellStatus",
		"aria2.tellActive",
		"aria2.tellWaiting",
		"aria2.tellStopped",
		"aria2.getUris",
		"aria2.getFiles",
		"aria2.getPeers",
		"aria2.getServers",
		"aria2.getOption",
		"aria2.changeOption",
		"aria2.getGlobalOption",
		"aria2.changeGlobalOption",
		"aria2.getGlobalStat",
		"aria2.shutdown",
		"aria2.forceShutdown",
		"aria2.saveSession",
		"system.multicall",
		"system.listMethods",
		"system.listNotifications",
	}
	
	// 直接返回字符串切片
	return &pb.SystemListMethodsResponse{
		Methods: methods,
	}, nil
}

// SystemListNotifications 系统通知列表
func (s *Server) SystemListNotifications(ctx context.Context, req *pb.SystemListNotificationsRequest) (*pb.SystemListNotificationsResponse, error) {
	// 返回所有可用的通知事件名
	notifications := []string{
		"aria2.onDownloadStart",
		"aria2.onDownloadPause",
		"aria2.onDownloadStop",
		"aria2.onDownloadComplete",
		"aria2.onDownloadError",
		"aria2.onBtDownloadComplete",
	}
	
	// 直接返回字符串切片
	return &pb.SystemListNotificationsResponse{
		Notifications: notifications,
	}, nil
}