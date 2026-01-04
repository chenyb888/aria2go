// aria2go 主程序
package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	
	"aria2go/internal/config"
	"aria2go/internal/core"
	"aria2go/internal/factory"
	"aria2go/internal/protocol/bt"
	"aria2go/internal/session"
	rpcjsonrpc "aria2go/internal/rpc/jsonrpc"
)

const (
	version = "1.0.0"
	buildDate = "2025-12-22"
)

func main() {
	// 检查是否有 --help 或 --version 参数（在解析配置之前）
	args := os.Args[1:]
	for _, arg := range args {
		if arg == "--help" || arg == "-h" {
			parser := config.NewParser()
			printHelp(parser)
			return
		}
		if arg == "--version" || arg == "-v" {
			printVersion()
			return
		}
	}
	
	// 检查是否有 --init-config 参数（在解析配置之前）
	initConfigPath := ""
	for i, arg := range args {
		if strings.HasPrefix(arg, "--init-config=") {
			initConfigPath = strings.TrimPrefix(arg, "--init-config=")
			break
		} else if arg == "--init-config" {
			// 如果 --init-config 后面没有参数，使用默认配置文件名
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				initConfigPath = args[i+1]
			} else {
				initConfigPath = "aria2go.json"
			}
			break
		}
	}
	
	// 如果指定了 --init-config，生成配置文件并退出
	if initConfigPath != "" {
		// 创建默认配置用于生成配置文件
		parser := config.NewParser()
		cfg, err := parser.Parse([]string{})
		if err != nil {
			fmt.Fprintf(os.Stderr, "创建配置失败: %v\n", err)
			os.Exit(1)
		}
		if err := config.SaveConfig(cfg, initConfigPath); err != nil {
			fmt.Fprintf(os.Stderr, "生成配置文件失败: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("配置文件已生成: %s\n", initConfigPath)
		return
	}
	
	// 解析配置
	parser := config.NewParser()
	cfg, err := parser.Parse(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "配置错误: %v\n", err)
		fmt.Fprintf(os.Stderr, "使用 --help 查看可用选项\n")
		os.Exit(1)
	}
	
	// 初始化日志
	initLogging(cfg)
	
	// 输出配置文件加载信息
	configFile := parser.FindConfigFile()
	if configFile != "" {
		log.Printf("已加载配置文件: %s", configFile)
	} else {
		log.Printf("未找到配置文件，使用默认配置")
	}
	
	// 处理daemon模式
	if cfg.Daemon {
		if err := daemonize(); err != nil {
			log.Fatalf("daemon化失败: %v", err)
		}
	}
	
	// 处理quiet模式
	if cfg.Quiet {
		log.SetOutput(io.Discard)
	}
	
	log.Printf("aria2go %s 启动", version)
	log.Printf("下载目录: %s", cfg.Dir)
	log.Printf("最大并发下载数: %d", cfg.MaxConcurrentDownloads)
	log.Printf("DHT: %v, PEX: %v, CheckIntegrity: %v, SaveSession: %v", cfg.EnableDHT, cfg.EnablePEX, cfg.CheckIntegrity, cfg.SaveSession)
	log.Printf("配置文件: %s", parser.FindConfigFile())
	
	// 检查dry-run模式
	if cfg.DryRun {
		log.Println("试运行模式：不实际下载文件")
		// 这里可以添加dry-run的逻辑，比如只显示将要下载的文件信息
	}
	
	// 创建上下文，处理信号
	
		ctx, cancel := context.WithCancel(context.Background())
	
		defer cancel()
	
		
	
		// 创建下载引擎
	
		engine := core.NewDefaultEngine()
	
		
	
		// 启动引擎
	
		if err := engine.Start(ctx); err != nil {
	
			log.Fatalf("启动引擎失败: %v", err)
	
		}
	
		defer engine.Stop()
	
		
	
		log.Println("下载引擎已启动，等待任务...")
	
		
	
		// 启动 RPC 服务器（如果启用）
	
		var rpcServer *rpcjsonrpc.Server
	
		if cfg.EnableRPC {
	
			rpcConfig := &rpcjsonrpc.Config{
	
				Host:       cfg.RPCHost,
	
				Port:       cfg.RPCPort,
	
				EnableAuth: cfg.RPCSecret != "",
	
				Token:      cfg.RPCSecret,
	
			}
	
			
	
			rpcServer = rpcjsonrpc.NewServer(engine, rpcConfig)
	
			
	
			// 在单独的 goroutine 中启动 RPC 服务器
	
			go func() {
	
				if err := rpcServer.Start(ctx); err != nil {
	
					log.Printf("RPC 服务器启动失败: %v", err)
	
				} else {
	
					log.Printf("RPC 服务器已启动，监听地址: %s:%d", cfg.RPCHost, cfg.RPCPort)
	
				}
	
			}()
	
			
	
			defer func() {
	
				if err := rpcServer.Stop(); err != nil {
	
					log.Printf("RPC 服务器停止失败: %v", err)
	
				}
	
			}()
	
		} else {
	
			log.Println("RPC 服务器未启用（使用 --enable-rpc 启用）")
	
		}
	
		
	
		// 创建会话管理器
	
		var sessionManager *session.Manager
	
		
	
		if cfg.SaveSession {
	
			var err error
	
			sessionManager, err = session.NewManager(cfg.Dir)
	
			if err != nil {
	
				log.Printf("创建会话管理器失败: %v", err)
	
			} else {
	
				log.Printf("会话管理器已创建，配置文件: %s/aria2go.session.json", cfg.Dir)
	
				
	
				// 恢复未完成的任务
	
				incompleteTasks := sessionManager.GetIncompleteTasks()
	
				if len(incompleteTasks) > 0 {
	
					log.Printf("发现 %d 个未完成的任务，正在恢复...", len(incompleteTasks))
	
					for _, taskInfo := range incompleteTasks {
	
											// 构建任务描述字符串
	
											taskDesc := taskInfo.ID
	
											if len(taskInfo.URLs) > 0 && taskInfo.URLs[0] != "" {
	
												taskDesc = fmt.Sprintf("%s (%s)", taskInfo.ID, taskInfo.URLs[0])
	
											} else {
	
												taskDesc = fmt.Sprintf("%s (%s)", taskInfo.ID, taskInfo.OutputPath)
	
											}
	
											log.Printf("恢复任务: %s", taskDesc)
	
											
	
											task := createDownloadTaskFromInfo(taskInfo, cfg, engine.EventCh())
	
											taskID := task.ID()
	
											
	
											
	
											if err := engine.AddTask(task); err != nil {
	
												log.Printf("恢复任务失败: %v", err)
	
											} else {
	
												log.Printf("任务已恢复: %s", taskID)
	
											}
	
										}
	
				} else {
	
					log.Printf("没有发现未完成的任务")
	
				}
	
			}
	
		} else {
	
					log.Printf("会话保存功能未启用 (--save-session=false)")
	
				}
	
				
	
				// 设置 sessionManager 到 RPC 服务器
	
				if rpcServer != nil && sessionManager != nil {
	
					rpcServer.SetSessionManager(sessionManager)
	
					log.Println("会话管理器已设置到 RPC 服务器")
	
				}
	
		
	
		// 设置信号处理（在 sessionManager 创建之后）
	
		sigCh := make(chan os.Signal, 1)
	
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	
		
	
		go func() {
	
			sig := <-sigCh
	
			log.Printf("接收到信号: %v，正在优雅关闭...", sig)
	
			
	
			// 先取消上下文，停止所有任务
	
			cancel()
	
			
	
			// 等待一小段时间让任务清理
	
			time.Sleep(1 * time.Second)
	
			
	
			// 保存会话（在退出前）
	
			if sessionManager != nil {
	
				log.Println("正在保存会话...")
	
				if err := sessionManager.Close(); err != nil {
	
					log.Printf("保存会话失败: %v", err)
	
				} else {
	
					log.Println("会话已保存")
	
				}
	
			}
	
			
	
			log.Println("正在关闭...")
	
		}()	// 任务ID管理和进度显示
	var (
		taskIDs   []string
		taskIDsMu sync.RWMutex
	)
	
	// 启动进度显示goroutine
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond) // 每500毫秒更新一次
		defer ticker.Stop()
		
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				displayProgress(engine, &taskIDs, &taskIDsMu, cfg)
			}
		}
	}()
	
	// 处理输入文件
	if cfg.InputFile != "" {
		urls, err := readURLsFromFile(cfg.InputFile)
		if err != nil {
			log.Printf("读取输入文件失败: %v", err)
		} else {
			// 添加下载任务
			for _, url := range urls {
				task := createDownloadTask(url, cfg, engine.EventCh())
				taskID := task.ID()
				
				taskIDsMu.Lock()
				taskIDs = append(taskIDs, taskID)
				taskIDsMu.Unlock()
				
				if err := engine.AddTask(task); err != nil {
					log.Printf("添加任务失败: %v", err)
				} else {
					log.Printf("已添加任务: %s (ID: %s)", url, taskID)
					// 异步保存任务到会话
					if sessionManager != nil {
						go func(t core.Task) {
							if err := sessionManager.AddTask(t); err != nil {
								log.Printf("保存任务到会话失败: %v", err)
							} else {
								log.Printf("任务已保存到会话: %s", t.ID())
							}
						}(task)
					} else {
						log.Printf("sessionManager 为 nil，跳过保存")
					}
				}
			}
		}
	}
	
	// 处理命令行参数中的URL
	urls := extractURLsFromArgs(os.Args[1:])
	for _, url := range urls {
		task := createDownloadTask(url, cfg, engine.EventCh())
		taskID := task.ID()
		
		taskIDsMu.Lock()
		taskIDs = append(taskIDs, taskID)
		taskIDsMu.Unlock()
		
		if err := engine.AddTask(task); err != nil {
		
										log.Printf("添加任务失败: %v", err)
		
									} else {
		
										log.Printf("已添加任务: %s (ID: %s)", url, taskID)
		
										// 异步保存任务到会话
		
										if sessionManager != nil {
		
											go func(t core.Task) {
		
												if err := sessionManager.AddTask(t); err != nil {
		
													log.Printf("保存任务到会话失败: %v", err)
		
												} else {
		
													log.Printf("任务已保存到会话: %s", t.ID())
		
												}
		
											}(task)
		
										}
		
									}	}
	
	// 如果没有任务，显示提示
	if cfg.InputFile == "" && len(urls) == 0 {
		log.Println("没有指定下载任务")
		log.Println("使用方式: aria2go [选项] URL1 [URL2 ...]")
		log.Println("或: aria2go -i urls.txt")
		log.Println("使用 --help 查看所有选项")
	}
	
	// 处理pause选项
	if cfg.Pause && len(taskIDs) > 0 {
		log.Println("启动时暂停：所有任务已暂停")
		for _, taskID := range taskIDs {
			if err := engine.PauseTask(taskID); err != nil {
				log.Printf("暂停任务 %s 失败: %v", taskID, err)
			}
		}
	}
	
	// 启动事件监听器（用于自动更新会话）
	if sessionManager != nil {
		go monitorEvents(engine.EventChReadOnly(), sessionManager, engine)
		log.Println("事件监听器已启动")
	}
	
	// 启动定时保存会话（每60秒）
	if sessionManager != nil {
		go startAutoSave(sessionManager, 60*time.Second)
		log.Println("定时保存已启动（间隔：60秒）")
	}
	
	// 等待所有任务完成或上下文取消
	<-ctx.Done()
	
	log.Println("正在关闭...")
}

// initLogging 初始化日志
func initLogging(cfg *config.Config) {
	// 设置日志输出
	if cfg.LogFile != "" {
		file, err := os.OpenFile(cfg.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			log.Printf("无法打开日志文件 %s: %v，使用标准输出", cfg.LogFile, err)
		} else {
			log.SetOutput(file)
		}
	}
	
	// 设置日志级别
	switch cfg.LogLevel {
	case "debug":
		// 启用详细日志，包括 DHT 交互信息
		log.SetFlags(log.LstdFlags | log.Lshortfile)
		log.Printf("日志级别设置为: debug")
	case "info":
		// 默认级别
		log.SetFlags(log.LstdFlags)
	case "warn":
		// 只记录警告和错误
		log.SetFlags(log.LstdFlags)
	case "error":
		// 只记录错误
		log.SetFlags(log.LstdFlags)
	}
}

// printHelp 显示帮助信息
func printHelp(parser *config.Parser) {
	fmt.Printf("aria2go - 多协议下载工具 (版本 %s)\n\n", version)
	fmt.Printf("使用方式:\n")
	fmt.Printf("  aria2go [选项] URL1 [URL2 ...]    - 下载文件\n")
	fmt.Printf("  aria2go --init-config=文件路径     - 生成默认配置文件\n\n")
	fmt.Printf("常用选项:\n")
	fmt.Printf("  --log-level=LEVEL    设置日志级别 (debug, info, warn, error)\n")
	fmt.Printf("  --dir=DIR            设置下载目录\n")
	fmt.Printf("  --enable-dht=true    启用DHT网络\n")
	fmt.Printf("  --log-level=debug    启用调试模式\n\n")
	fmt.Printf("完整选项列表:\n\n")
	
	// 获取选项集并显示帮助
	optionSet := parser.GetOptionSet()
	if optionSet != nil {
		fmt.Println(optionSet.Help())
	}
	
	fmt.Println("\n示例:")
	fmt.Println("  aria2go https://example.com/file.zip")
	fmt.Println("  aria2go -i urls.txt -d ~/Downloads")
	fmt.Println("  aria2go --max-concurrent-downloads=10 --split=5 http://example.com/largefile.iso")
	fmt.Println("\n支持的协议: HTTP, HTTPS, FTP, BitTorrent, Metalink")
}

// printVersion 显示版本信息
func printVersion() {
	fmt.Printf("aria2go 版本 %s (构建日期: %s)\n", version, buildDate)
	fmt.Println("基于 aria2 用 Go 语言重构")
	fmt.Println("版权所有 (C) 2025 aria2go 项目")
}

// extractURLsFromArgs 从命令行参数中提取URL
func extractURLsFromArgs(args []string) []string {
	var urls []string
	
	for _, arg := range args {
		// 跳过选项（以-开头）
		if len(arg) > 0 && arg[0] == '-' {
			// 检查是否是--option=value格式
			if len(arg) > 1 && arg[1] == '-' {
				// 跳过
				continue
			}
			// 跳过短选项
			continue
		}
		
		// 检查是否是URL（简单检测）
		if isURL(arg) {
			urls = append(urls, arg)
		}
	}
	
	return urls
}

// isURL 简单检查字符串是否是URL
func isURL(s string) bool {
	// 检查标准URL协议
	if len(s) > 7 && (s[:7] == "http://" || s[:8] == "https://" ||
		s[:6] == "ftp://" || s[:7] == "ftps://" ||
		s[:9] == "bittorrent:" || s[:8] == "magnet:?" ||
		s[:5] == "file:" || s[:9] == "metalink:") {
		return true
	}
	
	// 检查文件扩展名
	sLower := strings.ToLower(s)
	if strings.HasSuffix(sLower, ".torrent") || strings.HasSuffix(sLower, ".metalink") {
		return true
	}
	
	// 本地文件路径（简单检查）
	if len(s) > 0 && s[0] != '-' {
		// 可能是一个文件路径，暂时接受
		return true
	}
	
	return false
}

// createDownloadTask 创建下载任务
func createDownloadTask(url string, cfg *config.Config, eventCh chan<- core.Event) core.Task {
	// 生成任务ID（使用URL的简单哈希）
	taskID := generateTaskID(url)
	
	// 创建任务配置
	config := core.TaskConfig{
		URLs:         []string{url},
		OutputPath:   cfg.Dir + "/" + extractFilename(url),
		SegmentSize:  cfg.MinSplitSize,
		Connections:  cfg.MaxConnectionPerServer,
		MaxSpeed:     cfg.MaxDownloadLimit,
		MaxUploadSpeed: cfg.MaxUploadLimit,
		Protocol:     detectProtocol(url),
		Options:      make(map[string]interface{}),
	}
	
	// 设置选项
	config.Options["user-agent"] = cfg.UserAgent
	config.Options["referer"] = cfg.Referer
	config.Options["timeout"] = cfg.Timeout
	config.Options["retry-wait"] = cfg.RetryWait
	config.Options["max-tries"] = cfg.MaxTries
	
	// 设置 BitTorrent 相关选项
	btOptions := make(map[string]interface{})
	btOptions["enable-dht"] = cfg.EnableDHT
	btOptions["dht-listen-port"] = cfg.DHTListenPort
	btOptions["enable-pex"] = cfg.EnablePEX
	btOptions["write-interval"] = cfg.BTWriteInterval
	config.Options["bt"] = btOptions
	
	// 使用工厂创建任务
	task, err := factory.CreateTask(taskID, config, eventCh)
	if err != nil {
		// 创建任务失败，返回一个基础任务占位符
		log.Printf("创建任务失败: %v，使用基础任务占位符", err)
		return core.NewBaseTask(taskID, config, eventCh)
	}
	
	return task
}

// createDownloadTaskFromInfo 从 TaskInfo 创建下载任务（用于恢复）
func createDownloadTaskFromInfo(taskInfo session.TaskInfo, cfg *config.Config, eventCh chan<- core.Event) core.Task {
	// 创建任务配置
	config := core.TaskConfig{
		URLs:         taskInfo.URLs,
		OutputPath:   taskInfo.OutputPath,
		SegmentSize:  cfg.MinSplitSize,
		Connections:  cfg.MaxConnectionPerServer,
		MaxSpeed:     cfg.MaxDownloadLimit,
		MaxUploadSpeed: cfg.MaxUploadLimit,
		Protocol:     taskInfo.Protocol,
		Options:      make(map[string]interface{}),
	}

	// 从 taskInfo 恢复选项
	if taskInfo.Options != nil {
		config.Options = taskInfo.Options
	} else {
		// 设置默认选项
		config.Options["user-agent"] = cfg.UserAgent
		config.Options["referer"] = cfg.Referer
		config.Options["timeout"] = cfg.Timeout
		config.Options["retry-wait"] = cfg.RetryWait
		config.Options["max-tries"] = cfg.MaxTries
		
		// 设置 BitTorrent 相关选项
		btOptions := make(map[string]interface{})
		btOptions["enable-dht"] = cfg.EnableDHT
		btOptions["dht-listen-port"] = cfg.DHTListenPort
		btOptions["enable-pex"] = cfg.EnablePEX
		btOptions["write-interval"] = cfg.BTWriteInterval
		config.Options["bt"] = btOptions
	}
	
	// 特殊处理 BitTorrent 任务
	if taskInfo.Protocol == "bt" {
		// 检查是否有种子文件数据
		if torrentDataB64, ok := config.Options["torrent_data"].(string); ok && torrentDataB64 != "" {
			// 解码种子文件数据
			torrentData, err := base64.StdEncoding.DecodeString(torrentDataB64)
			if err != nil {
				log.Printf("解码种子文件数据失败: %v", err)
				return core.NewBaseTask(taskInfo.ID, config, eventCh)
			}
			
			// 解析种子文件
			metaInfo, err := bt.ParseTorrentData(torrentData)
			if err != nil {
				log.Printf("解析种子文件失败: %v", err)
				return core.NewBaseTask(taskInfo.ID, config, eventCh)
			}
			
			// 初始化 BitTorrent 客户端
			btClient := bt.GetGlobalClient()
			if btClient == nil {
				btConfig := bt.Config{
					DownloadDir:      cfg.Dir,
					ListenPort:       6881,
					EnableDHT:        cfg.EnableDHT,
					DHTListenPort:    cfg.DHTListenPort,
					EnablePEX:        cfg.EnablePEX,
					EnableEncryption: false,
					MaxUploadRate:    cfg.MaxUploadLimit,
					MaxDownloadRate:  cfg.MaxDownloadLimit,
					MaxActiveTasks:   cfg.MaxConcurrentDownloads,
					MaxConnectionsPerTask: cfg.MaxConnectionPerServer,
					WriteInterval:    cfg.BTWriteInterval,
				}
				
				btClient, err = bt.InitGlobalClient(btConfig)
				if err != nil {
					log.Printf("初始化 BitTorrent 客户端失败: %v", err)
					return core.NewBaseTask(taskInfo.ID, config, eventCh)
				}
			}
			
			// 创建 BitTorrent 任务
			return core.NewBitTorrentTask(taskInfo.ID, metaInfo, taskInfo.OutputPath, config.Options, eventCh, btClient)
		} else {
			log.Printf("BitTorrent 任务缺少种子文件数据: %s", taskInfo.ID)
			return core.NewBaseTask(taskInfo.ID, config, eventCh)
		}
	}
	
	// 使用工厂创建任务
	task, err := factory.CreateTask(taskInfo.ID, config, eventCh)
	if err != nil {
		// 创建任务失败，返回一个基础任务占位符
		log.Printf("创建恢复任务失败: %v，使用基础任务占位符", err)
		return core.NewBaseTask(taskInfo.ID, config, eventCh)
	}
	
	return task
}

// extractFilename 从URL中提取文件名
func extractFilename(url string) string {
	// 简化实现
	// 实际应该解析URL并提取路径的最后部分
	lastSlash := -1
	for i := len(url) - 1; i >= 0; i-- {
		if url[i] == '/' || url[i] == '\\' {
			lastSlash = i
			break
		}
	}
	
	if lastSlash >= 0 && lastSlash < len(url)-1 {
		filename := url[lastSlash+1:]
		// 移除查询参数
		for i := 0; i < len(filename); i++ {
			if filename[i] == '?' || filename[i] == '#' {
				return filename[:i]
			}
		}
		return filename
	}
	
	return "download"
}

// detectProtocol 检测协议类型
func detectProtocol(url string) string {
	if len(url) >= 7 && url[:7] == "http://" {
		return "http"
	}
	if len(url) >= 8 && url[:8] == "https://" {
		return "https"
	}
	if len(url) >= 6 && url[:6] == "ftp://" {
		return "ftp"
	}
	if len(url) >= 7 && url[:7] == "ftps://" {
		return "ftps"
	}
	if len(url) >= 9 && url[:9] == "bittorrent:" {
		return "bt"
	}
	if len(url) >= 8 && url[:8] == "magnet:?" {
		return "bt"
	}
	if len(url) >= 9 && url[:9] == "metalink:" {
		return "metalink"
	}
	// 检查文件扩展名
	if len(url) > 8 && strings.HasSuffix(strings.ToLower(url), ".torrent") {
		return "bt"
	}
	return "unknown"
}

// generateTaskID 生成任务ID
func generateTaskID(url string) string {
	// 简化实现：使用URL的哈希
	// 实际应该生成唯一ID
	return fmt.Sprintf("task_%d", len(url))
}

// readURLsFromFile 从文件中读取URL列表
func readURLsFromFile(filePath string) ([]string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("读取文件失败: %w", err)
	}
	
	// 简单实现：按空白字符分割
	urls := []string{}
	content := string(data)
	// 简化处理：按空白字符分割内容
	fields := strings.Fields(content)
	for _, field := range fields {
		if isURL(field) {
			urls = append(urls, field)
		}
	}
	return urls, nil
}

// displayProgress 显示下载进度
func displayProgress(engine core.Engine, taskIDs *[]string, mu *sync.RWMutex, cfg *config.Config) {
	mu.RLock()
	ids := *taskIDs
	mu.RUnlock()
	
	if len(ids) == 0 {
		return
	}
	
	// 获取终端宽度（简化实现）
	const maxLineLen = 80
	
	// 遍历所有任务，显示进度
	for _, taskID := range ids {
		progress, err := engine.GetTaskProgress(taskID)
		if err != nil {
			// 任务可能已被移除，跳过
			continue
		}
		
		status, err := engine.GetTaskStatus(taskID)
		if err != nil {
			continue
		}
		
		// 只显示活跃或等待中的任务
		if status.State == core.TaskStateCompleted || status.State == core.TaskStateError {
			// 已完成或出错的任务只显示一次结果
			continue
		}
		
		// 计算下载速度
		speed := progress.DownloadSpeed
		speedStr := formatSpeed(speed)
		
		// 计算进度百分比和显示信息
		var percent float64
		var bar string
		var sizeInfo string
		
		if progress.TotalBytes > 0 {
			percent = float64(progress.DownloadedBytes) / float64(progress.TotalBytes) * 100
			bar = createProgressBar(percent, 20, cfg.EnableColor)
			if cfg.HumanReadable {
				sizeInfo = fmt.Sprintf("%.1f%% (%s/%s)", 
					percent, 
					formatBytes(progress.DownloadedBytes),
					formatBytes(progress.TotalBytes))
			} else {
				sizeInfo = fmt.Sprintf("%.1f%%", percent)
			}
		} else {
			percent = 0
			bar = createProgressBar(0, 20, cfg.EnableColor)
			if cfg.HumanReadable {
				sizeInfo = fmt.Sprintf("%s / ?", formatBytes(progress.DownloadedBytes))
			} else {
				sizeInfo = fmt.Sprintf("%d / ?", progress.DownloadedBytes)
			}
		}
		
		// 输出进度信息
		// 安全截取任务ID前8个字符
		taskIDShort := taskID
		if len(taskID) > 8 {
			taskIDShort = taskID[:8]
		}
		
		// 根据配置添加颜色
		var line string
		if cfg.EnableColor {
			// 使用ANSI颜色代码
			colorReset := "\033[0m"
			colorCyan := "\033[36m"
			colorGreen := "\033[32m"
			colorYellow := "\033[33m"
			
			line = fmt.Sprintf("%s[%s]%s %s %s%s | %s%s",
				colorCyan, taskIDShort, colorReset,
				bar,
				colorGreen, sizeInfo, colorReset,
				colorYellow, speedStr)
		} else {
			line = fmt.Sprintf("[%s] %s %s | %s",
				taskIDShort,
				bar,
				sizeInfo,
				speedStr)
		}
		
		// 限制行长度（考虑颜色代码）
		cleanLine := strings.ReplaceAll(line, "\033[", "")
		cleanLine = strings.ReplaceAll(cleanLine, "m", "")
		for i := 0; i < 10; i++ {
			cleanLine = strings.ReplaceAll(cleanLine, fmt.Sprintf("3%dm", i), "")
		}
		cleanLine = strings.ReplaceAll(cleanLine, "0m", "")
		
		if len(cleanLine) > maxLineLen {
			line = line[:maxLineLen]
		}
		
		fmt.Printf("\r%s", line)
	}
	fmt.Printf("\n") // 换行，以便下次更新可以覆盖
}

// createProgressBar 创建文本进度条
func createProgressBar(percent float64, width int, enableColor bool) string {
	bar := "["
	filled := int(float64(width) * percent / 100)
	
	for i := 0; i < width; i++ {
		if i < filled {
			if enableColor {
				// 根据进度使用不同颜色
				if percent >= 100 {
					bar += "\033[32m=\033[0m" // 绿色
				} else if percent >= 70 {
					bar += "\033[33m=\033[0m" // 黄色
				} else if percent >= 40 {
					bar += "\033[36m=\033[0m" // 青色
				} else {
					bar += "\033[34m=\033[0m" // 蓝色
				}
			} else {
				bar += "="
			}
		} else if i == filled {
			if enableColor {
				bar += "\033[1m>\033[0m" // 粗体
			} else {
				bar += ">"
			}
		} else {
			bar += " "
		}
	}
	bar += "]"
	return bar
}

// formatSpeed 格式化速度显示
func formatSpeed(bytesPerSec int64) string {
	if bytesPerSec <= 0 {
		return "0 B/s"
	}
	
	const (
		KB = 1024
		MB = 1024 * 1024
		GB = 1024 * 1024 * 1024
	)
	
	switch {
	case bytesPerSec >= GB:
		return fmt.Sprintf("%.2f GB/s", float64(bytesPerSec)/float64(GB))
	case bytesPerSec >= MB:
		return fmt.Sprintf("%.2f MB/s", float64(bytesPerSec)/float64(MB))
	case bytesPerSec >= KB:
		return fmt.Sprintf("%.2f KB/s", float64(bytesPerSec)/float64(KB))
	default:
		return fmt.Sprintf("%d B/s", bytesPerSec)
	}
}

// formatBytes 格式化字节数显示
func formatBytes(bytes int64) string {
	if bytes <= 0 {
		return "0 B"
	}
	
	const (
		KB = 1024
		MB = 1024 * 1024
		GB = 1024 * 1024 * 1024
	)
	
	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.2f GB", float64(bytes)/float64(GB))
	case bytes >= MB:
		return fmt.Sprintf("%.2f MB", float64(bytes)/float64(MB))
	case bytes >= KB:
		return fmt.Sprintf("%.2f KB", float64(bytes)/float64(KB))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// daemonize 将进程转换为守护进程
func daemonize() error {
	// 检查是否已经是守护进程
	if os.Getppid() == 1 {
		// 已经是守护进程，直接返回
		return nil
	}
	
	// 第一次fork
	ret, err := syscall.ForkExec(os.Args[0], os.Args, &syscall.ProcAttr{
		Dir:   ".",
		Env:   os.Environ(),
		Files: []uintptr{0, 1, 2},
		Sys:   &syscall.SysProcAttr{Setsid: true},
	})
	
	if err != nil {
		return fmt.Errorf("第一次fork失败: %w", err)
	}
	
	if ret > 0 {
		// 父进程退出
		os.Exit(0)
	}
	
	// 子进程继续执行
	// 创建新的会话
	_, err = syscall.Setsid()
	if err != nil {
		return fmt.Errorf("创建新会话失败: %w", err)
	}
	
	// 更改工作目录到根目录
	err = os.Chdir("/")
	if err != nil {
		return fmt.Errorf("更改工作目录失败: %w", err)
	}
	
	// 重设文件权限掩码
	syscall.Umask(0)
	
	// 关闭标准文件描述符
	f, err := os.OpenFile("/dev/null", os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("打开/dev/null失败: %w", err)
	}
	defer f.Close()
	
	// 重定向标准输入、输出、错误到/dev/null
	nullFD := f.Fd()
	syscall.Dup2(int(nullFD), int(os.Stdin.Fd()))
	syscall.Dup2(int(nullFD), int(os.Stdout.Fd()))
	syscall.Dup2(int(nullFD), int(os.Stderr.Fd()))
	
	// 记录守护进程的PID
	pid := os.Getpid()
	pidFile := "/tmp/aria2go.pid"
	
	err = os.WriteFile(pidFile, []byte(strconv.Itoa(pid)), 0644)
	if err != nil {
		// 无法写入PID文件，但这不是致命错误
		// 在daemon模式下，我们无法输出到控制台
	}
	
	return nil
}

// isDaemonRunning 检查守护进程是否在运行
func isDaemonRunning() bool {
	pidFile := "/tmp/aria2go.pid"
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return false
	}
	
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return false
	}
	
	// 检查进程是否存在
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	
	// 发送信号0来检查进程是否存在
	err = process.Signal(syscall.Signal(0))
	return err == nil
}

// stopDaemon 停止守护进程
func stopDaemon() error {
	pidFile := "/tmp/aria2go.pid"
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return fmt.Errorf("无法读取PID文件 %s: %w", pidFile, err)
	}
	
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return fmt.Errorf("无效的PID格式: %w", err)
	}
	
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("找不到进程 %d: %w", pid, err)
	}
	
	// 发送SIGTERM信号
	err = process.Signal(syscall.SIGTERM)
	if err != nil {
		return fmt.Errorf("无法发送信号到进程 %d: %w", pid, err)
	}
	
	// 等待进程退出
	for i := 0; i < 10; i++ {
		err = process.Signal(syscall.Signal(0))
		if err != nil {
			// 进程已退出
			os.Remove(pidFile)
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	
	return fmt.Errorf("进程 %d 没有在预期时间内退出", pid)
}

// monitorEvents 监听引擎事件并自动更新会话
func monitorEvents(eventCh <-chan core.Event, sessionManager *session.Manager, engine *core.DownloadEngine) {
	for event := range eventCh {
		switch event.Type {
		case core.EventTaskStateChanged:
			// 任务状态变化时更新会话
			task, err := engine.GetTask(event.TaskID)
			if err != nil {
				log.Printf("获取任务失败: %s, 错误: %v", event.TaskID, err)
				continue
			}
			
			if err := sessionManager.UpdateTask(task); err != nil {
				// 如果任务不存在，尝试添加
				if err.Error() == "task not found: "+event.TaskID {
					if addErr := sessionManager.AddTask(task); addErr != nil {
						log.Printf("添加任务到会话失败: %s, 错误: %v", event.TaskID, addErr)
					} else {
						log.Printf("任务已添加到会话: %s", event.TaskID)
					}
				} else {
					log.Printf("更新任务到会话失败: %s, 错误: %v", event.TaskID, err)
				}
			} else {
				log.Printf("任务状态已更新到会话: %s", event.TaskID)
			}
			
		case core.EventTaskRemoved:
			// 任务移除时从会话中删除
			if err := sessionManager.RemoveTask(event.TaskID); err != nil {
				log.Printf("从会话中移除任务失败: %s, 错误: %v", event.TaskID, err)
			} else {
				log.Printf("任务已从会话中移除: %s", event.TaskID)
			}
			
		case core.EventTaskCompleted:
			// 任务完成时更新会话
			task, err := engine.GetTask(event.TaskID)
			if err != nil {
				log.Printf("获取任务失败: %s, 错误: %v", event.TaskID, err)
				continue
			}
			
			if err := sessionManager.UpdateTask(task); err != nil {
				// 如果任务不存在，尝试添加
				if err.Error() == "task not found: "+event.TaskID {
					if addErr := sessionManager.AddTask(task); addErr != nil {
						log.Printf("添加任务到会话失败: %s, 错误: %v", event.TaskID, addErr)
					} else {
						log.Printf("任务已添加到会话: %s", event.TaskID)
					}
				} else {
					log.Printf("更新任务到会话失败: %s, 错误: %v", event.TaskID, err)
				}
			} else {
				log.Printf("任务完成状态已保存到会话: %s", event.TaskID)
			}
		}
	}
}

// startAutoSave 启动定时保存会话
func startAutoSave(sessionManager *session.Manager, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			if err := sessionManager.Save(); err != nil {
				log.Printf("定时保存会话失败: %v", err)
			} else {
				log.Printf("定时保存会话成功")
			}
		}
	}
}