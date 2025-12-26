package rpc

import (
	"context"
	"crypto/sha1"
	"encoding/json"
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
	"aria2go/internal/protocol/ftp"
	"aria2go/internal/protocol/http"
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
	if dir, ok := options["dir"]; ok {
		if dirStr, ok := dir.(string); ok {
			if config.OutputPath != "" {
				// 如果同时有dir和out，组合路径
				config.OutputPath = dirStr + "/" + config.OutputPath
			}
		}
	}

	// 根据协议类型创建对应的任务
	var task core.Task
	var err error

	// 检测协议类型
	url := req.GetUris()[0]
	if http.IsHTTPURL(url) {
		// HTTP/HTTPS 任务
		task, err = http.NewHTTPTask(taskID, config, s.engine.EventCh())
	} else if ftp.IsFTPURL(url) {
		// FTP 任务
		task, err = ftp.NewFTPTask(taskID, config, s.engine.EventCh())
	} else if bt.IsBitTorrentURL(url) {
		// BitTorrent 任务
		task, err = bt.NewBTTask(taskID, config, s.engine.EventCh())
	} else if metalink.IsMetalinkURL(url) {
		// Metalink 任务
		task, err = metalink.NewMetalinkTask(taskID, config, s.engine.EventCh())
	} else {
		// 未知协议，尝试使用 HTTP 任务
		log.Printf("RPC[AddUri] 未知协议，尝试使用HTTP任务: %s", url)
		task, err = http.NewHTTPTask(taskID, config, s.engine.EventCh())
	}

	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "create task failed: %v", err)
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
	
	// 创建任务配置
	optionsInterface := make(map[string]interface{})
	for k, v := range options {
		optionsInterface[k] = v
	}

	taskConfig := core.TaskConfig{
		URLs:       urls,
		Options:    optionsInterface,
		OutputPath: options["dir"], // 使用dir选项作为输出目录
	}

	// 生成任务ID（GID）
	taskID := fmt.Sprintf("torrent-%x-%d", torrent.InfoHash[:8], time.Now().UnixNano())

	// 创建 BitTorrent 任务
	task, err := bt.NewBTTask(taskID, taskConfig, s.engine.EventCh())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "create BT task failed: %v", err)
	}

	// 添加到引擎
	if err := s.engine.AddTask(task); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to add task: %v", err)
	}

	// 记录解析成功的日志
	log.Printf("Torrent parsed successfully: %s, infoHash: %x, files: %d, total size: %d, gid: %s",
		torrent.Info.Name, torrent.InfoHash, len(torrent.Files), torrent.TotalSize(), taskID)

	return &pb.AddTorrentResponse{
		Gid: &pb.Gid{Value: taskID},
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
		
		// 创建任务配置
		fileOptionsInterface := make(map[string]interface{})
		for k, v := range fileOptions {
			fileOptionsInterface[k] = v
		}

		// 确定输出路径
		outputPath := fileOptions["dir"]
		if fileOptions["out"] != "" {
			if outputPath != "" {
				outputPath = outputPath + "/" + fileOptions["out"]
			} else {
				outputPath = fileOptions["out"]
			}
		}

		taskConfig := core.TaskConfig{
			URLs:       []string{resource.URL},
			Options:    fileOptionsInterface,
			OutputPath: outputPath,
		}

		// 根据资源类型创建对应的任务
		var task core.Task
		var err error

		if http.IsHTTPURL(resource.URL) {
			task, err = http.NewHTTPTask(gid, taskConfig, s.engine.EventCh())
		} else if ftp.IsFTPURL(resource.URL) {
			task, err = ftp.NewFTPTask(gid, taskConfig, s.engine.EventCh())
		} else if bt.IsBitTorrentURL(resource.URL) {
			task, err = bt.NewBTTask(gid, taskConfig, s.engine.EventCh())
		} else {
			// 未知协议，尝试使用 HTTP 任务
			log.Printf("RPC[AddMetalink] 未知协议，尝试使用HTTP任务: %s", resource.URL)
			task, err = http.NewHTTPTask(gid, taskConfig, s.engine.EventCh())
		}

		if err != nil {
			log.Printf("RPC[AddMetalink] 创建任务失败: %s, 错误: %v", file.Name, err)
			continue
		}

		// 添加到引擎
		if err := s.engine.AddTask(task); err != nil {
			log.Printf("RPC[AddMetalink] 添加任务失败: %s, 错误: %v", file.Name, err)
			continue
		}

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

	// 先获取任务并强制停止
	task, err := s.engine.GetTask(gid)
	if err != nil {
		return nil, status.Error(codes.NotFound, "task not found")
	}

	// 强制停止任务
	if err := task.Stop(); err != nil {
		log.Printf("RPC[ForceRemove] 停止任务失败: %s, 错误: %v", gid, err)
	}

	// 然后移除任务
	err = s.engine.RemoveTask(gid)
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
	// 调用引擎暂停所有任务
	if err := s.engine.PauseAllTasks(); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to pause all tasks: %v", err)
	}

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

	// 先获取任务
	task, err := s.engine.GetTask(gid)
	if err != nil {
		return nil, status.Error(codes.NotFound, "task not found")
	}

	// 强制停止任务
	if err := task.Stop(); err != nil {
		log.Printf("RPC[ForcePause] 停止任务失败: %s, 错误: %v", gid, err)
	}

	// 然后暂停任务
	err = s.engine.PauseTask(gid)
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
	// 调用引擎强制暂停所有任务
	if err := s.engine.ForcePauseAllTasks(); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to force pause all tasks: %v", err)
	}

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
	// 调用引擎恢复所有任务
	if err := s.engine.ResumeAllTasks(); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to resume all tasks: %v", err)
	}

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

	// 从引擎获取任务
	task, err := s.engine.GetTask(gid)
	if err != nil {
		return nil, status.Error(codes.NotFound, "task not found")
	}

	taskStatus := task.Status()
	taskProgress := task.Progress()

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
		pbStatus = pb.Status_STATUS_REMOVED
	default:
		pbStatus = pb.Status_STATUS_WAITING
	}

	// 构建 DownloadStatus
	downloadStatus := &pb.DownloadStatus{
		Gid:             &pb.Gid{Value: gid},
		Status:          pbStatus,
		TotalLength:     taskProgress.TotalBytes,
		CompletedLength: taskProgress.DownloadedBytes,
		DownloadSpeed:   int32(taskProgress.DownloadSpeed),
		UploadSpeed:     int32(taskProgress.UploadSpeed),
	}

	// 如果有错误信息，添加到响应
	if taskStatus.Error != nil {
		downloadStatus.ErrorMessage = taskStatus.Error.Error()
	}

	return &pb.TellStatusResponse{
		Status: downloadStatus,
	}, nil
}

// TellActive 获取活动任务列表
func (s *Server) TellActive(ctx context.Context, req *pb.TellActiveRequest) (*pb.TellActiveResponse, error) {
	// 从引擎获取活跃任务列表
	tasks := s.engine.GetActiveTasks()

	// 转换为 DownloadStatus
	var statuses []*pb.DownloadStatus
	for _, task := range tasks {
		status := s.taskToDownloadStatus(task)
		statuses = append(statuses, status)
	}

	return &pb.TellActiveResponse{
		Statuses: statuses,
	}, nil
}

// TellWaiting 获取等待任务列表
func (s *Server) TellWaiting(ctx context.Context, req *pb.TellWaitingRequest) (*pb.TellWaitingResponse, error) {
	// 从引擎获取等待任务列表
	tasks := s.engine.GetWaitingTasks()

	// 转换为 DownloadStatus
	var statuses []*pb.DownloadStatus
	for _, task := range tasks {
		status := s.taskToDownloadStatus(task)
		statuses = append(statuses, status)
	}

	return &pb.TellWaitingResponse{
		Statuses: statuses,
	}, nil
}

// TellStopped 获取停止任务列表
func (s *Server) TellStopped(ctx context.Context, req *pb.TellStoppedRequest) (*pb.TellStoppedResponse, error) {
	// 从引擎获取停止任务列表
	tasks := s.engine.GetStoppedTasks()

	// 转换为 DownloadStatus
	var statuses []*pb.DownloadStatus
	for _, task := range tasks {
		status := s.taskToDownloadStatus(task)
		statuses = append(statuses, status)
	}

	return &pb.TellStoppedResponse{
		Statuses: statuses,
	}, nil
}

// GetUris 获取任务URI列表
func (s *Server) GetUris(ctx context.Context, req *pb.GetUrisRequest) (*pb.GetUrisResponse, error) {
	gid := req.GetGid().GetValue()
	if gid == "" {
		return nil, status.Error(codes.InvalidArgument, "gid is required")
	}

	// 从引擎获取任务
	task, err := s.engine.GetTask(gid)
	if err != nil {
		return nil, status.Error(codes.NotFound, "task not found")
	}

	// 获取 URI 列表
	uriInfos := task.GetURIs()

	// 转换为 protobuf 格式
	uris := make([]*pb.Uri, 0, len(uriInfos))
	for _, uriInfo := range uriInfos {
		uris = append(uris, &pb.Uri{
			Uri:    uriInfo.URI,
			Status: uriInfo.Status,
		})
	}

	return &pb.GetUrisResponse{
		Uris: uris,
	}, nil
}

// GetFiles 获取任务文件列表
func (s *Server) GetFiles(ctx context.Context, req *pb.GetFilesRequest) (*pb.GetFilesResponse, error) {
	gid := req.GetGid().GetValue()
	if gid == "" {
		return nil, status.Error(codes.InvalidArgument, "gid is required")
	}

	// 从引擎获取任务
	task, err := s.engine.GetTask(gid)
	if err != nil {
		return nil, status.Error(codes.NotFound, "task not found")
	}

	// 获取文件列表
	fileInfos := task.GetFiles()

	// 转换为 protobuf 格式
	files := make([]*pb.File, 0, len(fileInfos))
	for _, fileInfo := range fileInfos {
		// 转换 URI 列表
		uris := make([]*pb.Uri, 0, len(fileInfo.URIs))
		for _, uriInfo := range fileInfo.URIs {
			uris = append(uris, &pb.Uri{
				Uri:    uriInfo.URI,
				Status: uriInfo.Status,
			})
		}

		files = append(files, &pb.File{
			Index:            int32(fileInfo.Index),
			Path:             fileInfo.Path,
			Length:           fileInfo.Length,
			CompletedLength:  fileInfo.CompletedLength,
			Selected:         fileInfo.Selected,
			Uris:             uris,
		})
	}

	return &pb.GetFilesResponse{
		Files: files,
	}, nil
}

// GetPeers 获取任务Peer列表
func (s *Server) GetPeers(ctx context.Context, req *pb.GetPeersRequest) (*pb.GetPeersResponse, error) {
	gid := req.GetGid().GetValue()
	if gid == "" {
		return nil, status.Error(codes.InvalidArgument, "gid is required")
	}

	// 从引擎获取任务
	task, err := s.engine.GetTask(gid)
	if err != nil {
		return nil, status.Error(codes.NotFound, "task not found")
	}

	// 获取 Peer 列表
	peerInfos := task.GetPeers()

	// 转换为 protobuf 格式
	peers := make([]*pb.Peer, 0, len(peerInfos))
	for _, peerInfo := range peerInfos {
		seeder := "false"
		if peerInfo.Seeder {
			seeder = "true"
		}

		peers = append(peers, &pb.Peer{
			PeerId:         peerInfo.PeerId,
			Ip:             peerInfo.Ip,
			Port:           int32(peerInfo.Port),
			Bitfield:       peerInfo.Bitfield,
			AmChoking:      peerInfo.AmChoking,
			AmInterested:   peerInfo.AmInterested,
			PeerChoking:    peerInfo.PeerChoking,
			PeerInterested: peerInfo.PeerInterested,
			DownloadSpeed:  peerInfo.DownloadSpeed,
			UploadSpeed:    peerInfo.UploadSpeed,
			Seeder:         seeder,
		})
	}

	return &pb.GetPeersResponse{
		Peers: peers,
	}, nil
}

// GetServers 获取任务服务器列表
func (s *Server) GetServers(ctx context.Context, req *pb.GetServersRequest) (*pb.GetServersResponse, error) {
	gid := req.GetGid().GetValue()
	if gid == "" {
		return nil, status.Error(codes.InvalidArgument, "gid is required")
	}

	// 从引擎获取任务
	task, err := s.engine.GetTask(gid)
	if err != nil {
		return nil, status.Error(codes.NotFound, "task not found")
	}

	// 获取服务器列表
	serverInfos := task.GetServers()

	// 转换为 protobuf 格式
	servers := make([]*pb.Server, 0, len(serverInfos))
	for _, serverInfo := range serverInfos {
		servers = append(servers, &pb.Server{
			Uri:           serverInfo.URI,
			CurrentUri:    serverInfo.CurrentUri,
			DownloadSpeed: serverInfo.DownloadSpeed,
		})
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
	gid := req.GetGid().GetValue()
	if gid == "" {
		return nil, status.Error(codes.InvalidArgument, "gid is required")
	}

	// 从引擎获取任务
	task, err := s.engine.GetTask(gid)
	if err != nil {
		return nil, status.Error(codes.NotFound, "task not found")
	}

	// 获取任务配置选项
	options := task.GetOption()

	return &pb.GetOptionResponse{
		Options: options,
	}, nil
}

// ChangeOption 更改任务选项
func (s *Server) ChangeOption(ctx context.Context, req *pb.ChangeOptionRequest) (*pb.ChangeOptionResponse, error) {
	gid := req.GetGid().GetValue()
	if gid == "" {
		return nil, status.Error(codes.InvalidArgument, "gid is required")
	}

	options := req.GetOptions()
	if len(options) == 0 {
		return nil, status.Error(codes.InvalidArgument, "options is required")
	}

	// 从引擎获取任务
	task, err := s.engine.GetTask(gid)
	if err != nil {
		return nil, status.Error(codes.NotFound, "task not found")
	}

	// 修改任务配置选项
	if err := task.ChangeOption(options); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to change option: %v", err)
	}

	return &pb.ChangeOptionResponse{
		Success: true,
	}, nil
}

// GetGlobalOption 获取全局选项
func (s *Server) GetGlobalOption(ctx context.Context, req *pb.GetGlobalOptionRequest) (*pb.GetGlobalOptionResponse, error) {
	// 从引擎获取全局配置选项
	options := s.engine.GetGlobalOption()

	return &pb.GetGlobalOptionResponse{
		Options: options,
	}, nil
}

// ChangeGlobalOption 更改全局选项
func (s *Server) ChangeGlobalOption(ctx context.Context, req *pb.ChangeGlobalOptionRequest) (*pb.ChangeGlobalOptionResponse, error) {
	options := req.GetOptions()
	if len(options) == 0 {
		return nil, status.Error(codes.InvalidArgument, "options is required")
	}

	// 修改全局配置选项
	if err := s.engine.ChangeGlobalOption(options); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to change global option: %v", err)
	}

	return &pb.ChangeGlobalOptionResponse{
		Success: true,
	}, nil
}

// Shutdown 关闭aria2
func (s *Server) Shutdown(ctx context.Context, req *pb.ShutdownRequest) (*pb.ShutdownResponse, error) {
	// 参考aria2实现：3秒后执行优雅关闭
	// 先返回成功响应，给客户端时间接收响应
	go func() {
		time.Sleep(3 * time.Second)
		log.Printf("RPC[Shutdown] 3秒后执行优雅关闭")
		if err := s.engine.Shutdown(false); err != nil {
			log.Printf("RPC[Shutdown] 关闭失败: %v", err)
		}
	}()

	return &pb.ShutdownResponse{
		Success: true,
	}, nil
}

// ForceShutdown 强制关闭aria2
func (s *Server) ForceShutdown(ctx context.Context, req *pb.ForceShutdownRequest) (*pb.ForceShutdownResponse, error) {
	// 参考aria2实现：3秒后执行强制关闭
	// 先返回成功响应，给客户端时间接收响应
	go func() {
		time.Sleep(3 * time.Second)
		log.Printf("RPC[ForceShutdown] 3秒后执行强制关闭")
		if err := s.engine.Shutdown(true); err != nil {
			log.Printf("RPC[ForceShutdown] 关闭失败: %v", err)
		}
	}()

	return &pb.ForceShutdownResponse{
		Success: true,
	}, nil
}

// SaveSession 保存会话
func (s *Server) SaveSession(ctx context.Context, req *pb.SaveSessionRequest) (*pb.SaveSessionResponse, error) {
	// 使用默认文件名或从全局配置获取
	filename := "aria2go-session.json"
	if dir, ok := s.engine.GetGlobalOption()["dir"]; ok {
		filename = dir + "/aria2go-session.json"
	}

	// 保存会话
	if err := s.engine.SaveSession(filename); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to save session: %v", err)
	}

	return &pb.SaveSessionResponse{
		Success: true,
	}, nil
}

// SystemMulticall 系统批量调用
func (s *Server) SystemMulticall(ctx context.Context, req *pb.SystemMulticallRequest) (*pb.SystemMulticallResponse, error) {
	calls := req.GetCalls()
	if len(calls) == 0 {
		return nil, status.Error(codes.InvalidArgument, "calls is required")
	}

	var results []string

	for _, call := range calls {
		methodName := call.GetMethodName()
		_ = call.GetParams() // 暂时忽略参数

		// 递归调用禁止
		if methodName == "system.multicall" {
			errorMsg := fmt.Sprintf(`{"code": -1, "message": "Recursive system.multicall forbidden."}`)
			results = append(results, errorMsg)
			continue
		}

		// 根据方法名执行对应的 RPC 方法
		// 注意：这里简化实现，只支持部分方法
		var result interface{}
		var err error

		switch methodName {
		case "aria2.getGlobalStat":
			result, err = s.handleGetGlobalStat()
		case "aria2.getVersion":
			result, err = s.handleGetVersion()
		default:
			err = fmt.Errorf("method %s not supported in multicall", methodName)
		}

		if err != nil {
			errorMsg := fmt.Sprintf(`{"code": -1, "message": "%s"}`, err.Error())
			results = append(results, errorMsg)
		} else {
			// 成功：包装在数组中
			resultJSON, _ := json.Marshal(result)
			results = append(results, fmt.Sprintf("[%s]", string(resultJSON)))
		}
	}

	return &pb.SystemMulticallResponse{
		Results: results,
	}, nil
}

// handleGetGlobalStat 处理 getGlobalStat 方法
func (s *Server) handleGetGlobalStat() (interface{}, error) {
	stat := s.engine.GetGlobalStat()
	return map[string]interface{}{
		"downloadSpeed": stat.DownloadSpeed,
		"uploadSpeed":   stat.UploadSpeed,
		"numActive":     stat.NumActive,
		"numWaiting":    stat.NumWaiting,
		"numStopped":    stat.NumStopped,
		"numTotal":      stat.NumTotal,
	}, nil
}

// handleGetVersion 处理 getVersion 方法
func (s *Server) handleGetVersion() (interface{}, error) {
	return map[string]interface{}{
		"version":           "1.0.0",
		"enabledFeatures":   []string{},
	}, nil
}
// taskToDownloadStatus 将 Task 转换为 DownloadStatus
func (s *Server) taskToDownloadStatus(task core.Task) *pb.DownloadStatus {
	taskStatus := task.Status()
	taskProgress := task.Progress()

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
		pbStatus = pb.Status_STATUS_REMOVED
	default:
		pbStatus = pb.Status_STATUS_WAITING
	}

	downloadStatus := &pb.DownloadStatus{
		Gid:             &pb.Gid{Value: task.ID()},
		Status:          pbStatus,
		TotalLength:     taskProgress.TotalBytes,
		CompletedLength: taskProgress.DownloadedBytes,
		DownloadSpeed:   int32(taskProgress.DownloadSpeed),
		UploadSpeed:     int32(taskProgress.UploadSpeed),
	}

	// 如果有错误信息，添加到响应
	if taskStatus.Error != nil {
		downloadStatus.ErrorMessage = taskStatus.Error.Error()
	}

	return downloadStatus
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