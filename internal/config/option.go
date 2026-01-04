// Package config 提供配置选项定义
package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Option 配置选项
type Option struct {
	Name        string
	Description string
	Default     interface{}
	Value       interface{}
	Type        string // "string", "int", "bool", "duration", "float", "int64"
}

// OptionSet 选项集合
type OptionSet struct {
	options map[string]*Option
}

// NewOptionSet 创建选项集合
func NewOptionSet() *OptionSet {
	return &OptionSet{
		options: make(map[string]*Option),
	}
}

// AddOption 添加选项
func (os *OptionSet) AddOption(opt *Option) {
	os.options[opt.Name] = opt
}

// GetOption 获取选项
func (os *OptionSet) GetOption(name string) (*Option, bool) {
	opt, exists := os.options[name]
	return opt, exists
}

// Parse 解析命令行参数，只返回命令行明确指定的选项
func (os *OptionSet) Parse(args []string) (*Config, error) {
 	// 创建空配置，不使用默认值
 	config := &Config{}
 	
 	// 自定义解析，支持 -- 前缀
 	for i := 0; i < len(args); i++ {
 		arg := args[i]
 		
 		// 跳过非选项参数
 		if !strings.HasPrefix(arg, "-") {
 			continue
 		}
 		
 		// 移除 - 或 -- 前缀
 		name := strings.TrimLeft(arg, "-")
 		
 		// 检查是否是 = 格式
 		if strings.Contains(name, "=") {
 			parts := strings.SplitN(name, "=", 2)
 			name = parts[0]
 			value := parts[1]
 			
 			// 设置值
 			if err := os.setConfigValueFromString(config, name, value); err != nil {
 				return nil, fmt.Errorf("invalid value for --%s: %v", name, err)
 			}
 		} else {
 			// 检查是否有下一个参数作为值
 			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
 				value := args[i+1]
 				
 				// 设置值
 				if err := os.setConfigValueFromString(config, name, value); err != nil {
 					return nil, fmt.Errorf("invalid value for --%s: %v", name, err)
 				}
 				
 				i++ // 跳过值参数
 			} else {
 				// 布尔值，设置为 true
 				if err := os.setConfigValueFromString(config, name, "true"); err != nil {
 					return nil, fmt.Errorf("invalid value for --%s: %v", name, err)
 				}
 			}
 		}
 	}
 	
 	return config, nil
 }
// setConfigValueFromString 从字符串设置配置值
func (os *OptionSet) setConfigValueFromString(config *Config, name string, value string) error {
	opt, exists := os.options[name]
	if !exists {
		return fmt.Errorf("unknown option: --%s", name)
	}
	
	switch opt.Type {
	case "string":
		os.setConfigValue(config, name, value)
	case "int":
		intValue, err := strconv.Atoi(value)
		if err != nil {
			return err
		}
		os.setConfigValue(config, name, intValue)
	case "bool":
		boolValue, err := strconv.ParseBool(value)
		if err != nil {
			return err
		}
		os.setConfigValue(config, name, boolValue)
	case "duration":
		durationValue, err := time.ParseDuration(value)
		if err != nil {
			return err
		}
		os.setConfigValue(config, name, durationValue)
	case "float":
		floatValue, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return err
		}
		os.setConfigValue(config, name, floatValue)
	case "int64":
		int64Value, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return err
		}
		os.setConfigValue(config, name, int64Value)
	default:
		return fmt.Errorf("unsupported type: %s", opt.Type)
	}
	
	return nil
}

// setConfigValue 设置配置值
func (os *OptionSet) setConfigValue(config *Config, name string, value interface{}) {
	// 根据选项名称设置配置值
	// 这里简化实现，实际需要更完整的映射
	switch name {
	case "log-level":
		config.LogLevel = value.(string)
	case "log-file":
		config.LogFile = value.(string)
	case "dir":
		config.Dir = value.(string)
	case "max-concurrent-downloads":
		config.MaxConcurrentDownloads = value.(int)
	case "daemon":
		config.Daemon = value.(bool)
	case "quiet":
		config.Quiet = value.(bool)
	case "pause":
		config.Pause = value.(bool)
	case "human-readable":
		config.HumanReadable = value.(bool)
	case "enable-color":
		config.EnableColor = value.(bool)
	case "dry-run":
		config.DryRun = value.(bool)
	case "init-config":
		config.InputFile = value.(string)
	case "max-connection-per-server":
		config.MaxConnectionPerServer = value.(int)
	case "min-split-size":
		config.MinSplitSize = value.(int64)
	case "split":
		config.Split = value.(int)
	case "lowest-speed-limit":
		config.LowestSpeedLimit = value.(int64)
	case "max-overall-download-limit":
		config.MaxOverallDownloadLimit = value.(int64)
	case "max-download-limit":
		config.MaxDownloadLimit = value.(int64)
	case "max-overall-upload-limit":
		config.MaxOverallUploadLimit = value.(int64)
	case "max-upload-limit":
		config.MaxUploadLimit = value.(int64)
	case "timeout":
		config.Timeout = value.(time.Duration)
	case "retry-wait":
		config.RetryWait = value.(time.Duration)
	case "max-tries":
		config.MaxTries = value.(int)
	case "user-agent":
		config.UserAgent = value.(string)
	case "referer":
		config.Referer = value.(string)
	case "all-proxy":
		config.AllProxy = value.(string)
	case "no-proxy":
		config.NoProxy = value.(string)
	case "http-proxy":
		config.HTTPProxy = value.(string)
	case "https-proxy":
		config.HTTPSProxy = value.(string)
	case "ftp-proxy":
		config.FTPProxy = value.(string)
	case "ftp-user":
		config.FTPUser = value.(string)
	case "ftp-passwd":
		config.FTPPasswd = value.(string)
	case "enable-dht":
		config.EnableDHT = value.(bool)
	case "dht-listen-port":
		config.DHTListenPort = value.(int)
	case "enable-pex":
		config.EnablePEX = value.(bool)
	case "seed-ratio":
		config.SeedRatio = value.(float64)
	case "seed-time":
		config.SeedTime = value.(time.Duration)
	case "metalink-preferred-protocol":
		config.MetalinkPreferredProtocol = strings.Split(value.(string), ",")
	case "metalink-file":
		config.MetalinkFile = value.(string)
	case "enable-rpc":
		config.EnableRPC = value.(bool)
	case "rpc-host":
		config.RPCHost = value.(string)
	case "rpc-port":
		config.RPCPort = value.(int)
	case "rpc-secret":
		config.RPCSecret = value.(string)
	case "check-integrity":
		config.CheckIntegrity = value.(bool)
	case "continue":
		config.ContinueDownload = value.(bool)
	case "allow-overwrite":
		config.AllowOverwrite = value.(bool)
	case "auto-save-interval":
		config.AutoSaveInterval = value.(time.Duration)
	case "save-session":
		config.SaveSession = value.(bool)
	case "input-file":
		config.InputFile = value.(string)
	}
}

// DefaultOptionSet 返回默认选项集合
func DefaultOptionSet() *OptionSet {
	optionSet := NewOptionSet()
	
	// 添加选项
	options := []*Option{
		// 通用选项
		{"log-level", "设置日志级别 (debug, info, warn, error)", "info", nil, "string"},
		{"log-file", "日志文件路径", "", nil, "string"},
		{"dir", "下载目录", "", nil, "string"},
		{"max-concurrent-downloads", "最大并发下载数", 5, nil, "int"},
		{"daemon", "后台运行模式", false, nil, "bool"},
		{"quiet", "安静模式（减少控制台输出）", false, nil, "bool"},
		{"pause", "启动时暂停所有下载", false, nil, "bool"},
		{"human-readable", "人类可读的输出格式", true, nil, "bool"},
		{"enable-color", "启用彩色输出", true, nil, "bool"},
		{"dry-run", "试运行模式（不实际下载）", false, nil, "bool"},
		{"init-config", "生成默认配置文件", "", nil, "string"},
		
		// 连接选项
		{"max-connection-per-server", "每个服务器的最大连接数", 1, nil, "int"},
		{"min-split-size", "最小分段大小 (字节)", int64(20 * 1024 * 1024), nil, "int64"},
		{"split", "每个文件的分段数", 5, nil, "int"},
		{"lowest-speed-limit", "最低速度限制 (字节/秒)", int64(0), nil, "int64"},
		{"max-overall-download-limit", "全局最大下载速度限制 (字节/秒)", int64(0), nil, "int64"},
		{"max-download-limit", "单个任务最大下载速度限制 (字节/秒)", int64(0), nil, "int64"},
		{"max-overall-upload-limit", "全局最大上传速度限制 (字节/秒)", int64(0), nil, "int64"},
		{"max-upload-limit", "单个任务最大上传速度限制 (字节/秒)", int64(0), nil, "int64"},
		
		// 网络选项
		{"timeout", "超时时间", 60 * time.Second, nil, "duration"},
		{"retry-wait", "重试等待时间", 0 * time.Second, nil, "duration"},
		{"max-tries", "最大重试次数", 5, nil, "int"},
		{"user-agent", "用户代理字符串", "aria2go/1.0", nil, "string"},
		{"referer", "Referer头", "", nil, "string"},
		{"all-proxy", "所有协议的代理服务器", "", nil, "string"},
		{"no-proxy", "不使用代理的主机列表", "", nil, "string"},
		
		// HTTP/FTP选项
		{"http-proxy", "HTTP代理服务器", "", nil, "string"},
		{"https-proxy", "HTTPS代理服务器", "", nil, "string"},
		{"ftp-proxy", "FTP代理服务器", "", nil, "string"},
		{"ftp-user", "FTP用户名", "anonymous", nil, "string"},
		{"ftp-passwd", "FTP密码", "anonymous@example.com", nil, "string"},
		
		// BitTorrent选项
		{"enable-dht", "启用DHT", true, nil, "bool"},
		{"dht-listen-port", "DHT监听端口", 6881, nil, "int"},
		{"enable-pex", "启用PEX", true, nil, "bool"},
		{"seed-ratio", "做种分享率", 1.0, nil, "float"},
		{"seed-time", "做种时间", 30 * time.Minute, nil, "duration"},
		
		// Metalink选项
		{"metalink-preferred-protocol", "首选协议 (逗号分隔)", "https,http,ftp", nil, "string"},
		{"metalink-file", "Metalink文件路径", "", nil, "string"},
		
		// RPC选项
		{"enable-rpc", "启用RPC", false, nil, "bool"},
		{"rpc-host", "RPC监听主机", "0.0.0.0", nil, "string"},
		{"rpc-port", "RPC端口", 6800, nil, "int"},
		{"rpc-secret", "RPC密钥", "", nil, "string"},
		
		// 高级选项
		{"check-integrity", "下载完成后检查文件完整性", false, nil, "bool"},
		{"continue", "继续部分下载的文件", true, nil, "bool"},
		{"allow-overwrite", "允许覆盖文件", false, nil, "bool"},
		{"auto-save-interval", "自动保存间隔", 30 * time.Second, nil, "duration"},
		{"save-session", "保存会话", false, nil, "bool"},
		{"input-file", "输入文件路径", "", nil, "string"},
	}
	
	for _, opt := range options {
		optionSet.AddOption(opt)
	}
	
	return optionSet
}

// Help 显示帮助信息
func (os *OptionSet) Help() string {
	var builder strings.Builder
	builder.WriteString("可用选项:\n\n")
	
	for _, opt := range os.options {
		defaultValue := fmt.Sprintf("%v", opt.Default)
		if defaultValue == "" {
			defaultValue = "(空)"
		}
		builder.WriteString(fmt.Sprintf("  --%-30s %s (默认: %s)\n", 
			opt.Name, opt.Description, defaultValue))
	}
	
	return builder.String()
}