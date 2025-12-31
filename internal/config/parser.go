// Package config 提供配置解析功能
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Parser 配置解析器
type Parser struct {
	optionSet *OptionSet
}

// NewParser 创建配置解析器
func NewParser() *Parser {
	return &Parser{
		optionSet: DefaultOptionSet(),
	}
}

// Parse 解析配置
func (p *Parser) Parse(args []string) (*Config, error) {
	// 创建默认配置
	config := DefaultConfig()
	// 首先解析配置文件
	configFile := p.findConfigFile()
	if configFile != "" {
		fileConfig, err := p.parseConfigFile(configFile)
		if err != nil {
			return nil, fmt.Errorf("parse config file failed: %w", err)
		}
		config.Merge(fileConfig)
	}
	// 解析命令行参数
	cmdConfig, err := p.optionSet.Parse(args)
	if err != nil {
		return nil, fmt.Errorf("parse command line failed: %w", err)
	}
	config.Merge(cmdConfig)
	// 验证配置
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}
	return config, nil
}

// findConfigFile 查找配置文件
func (p *Parser) findConfigFile() string {
	// 只查找 aria2go.json 配置文件
	configFile := "aria2go.json"
	// 检查当前目录
	if _, err := os.Stat(configFile); err == nil {
		return configFile
	}
	// 检查用户主目录
	homeDir, err := os.UserHomeDir()
	if err == nil {
		fullPath := filepath.Join(homeDir, configFile)
		if _, err := os.Stat(fullPath); err == nil {
			return fullPath
		}
	}
	return ""
}

// parseConfigFile 解析配置文件
func (p *Parser) parseConfigFile(filePath string) (*Config, error) {
	// 只支持 JSON 格式
	return p.parseJSONConfig(filePath)
}

// parseJSONConfig 解析JSON配置文件
func (p *Parser) parseJSONConfig(filePath string) (*Config, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("read config file failed: %w", err)
	}
	
	// 解析JSON到map
	var configMap map[string]interface{}
	if err := json.Unmarshal(data, &configMap); err != nil {
		return nil, fmt.Errorf("parse JSON failed: %w", err)
	}
	
	// 转换为Config结构
	config := DefaultConfig()
	
			// 映射字段
		for key, value := range configMap {
			switch key {
			case "log_level":
				if str, ok := value.(string); ok {
					config.LogLevel = str
				}
			case "log_file":
				if str, ok := value.(string); ok {
					config.LogFile = str
				}
			case "dir":
				if str, ok := value.(string); ok {
					config.Dir = str
				}
			case "max_concurrent_downloads":
				if num, ok := value.(float64); ok {
					config.MaxConcurrentDownloads = int(num)
				}
			case "daemon":
				if b, ok := value.(bool); ok {
					config.Daemon = b
				}
			case "quiet":
				if b, ok := value.(bool); ok {
					config.Quiet = b
				}
			case "pause":
				if b, ok := value.(bool); ok {
					config.Pause = b
				}
			case "human_readable":
				if b, ok := value.(bool); ok {
					config.HumanReadable = b
				}
			case "enable_color":
				if b, ok := value.(bool); ok {
					config.EnableColor = b
				}
			case "dry_run":
				if b, ok := value.(bool); ok {
					config.DryRun = b
				}
		case "max_connection_per_server":
			if num, ok := value.(float64); ok {
				config.MaxConnectionPerServer = int(num)
			}
		case "min_split_size":
			if num, ok := value.(float64); ok {
				config.MinSplitSize = int64(num)
			}
		case "split":
			if num, ok := value.(float64); ok {
				config.Split = int(num)
			}
		case "lowest_speed_limit":
			if num, ok := value.(float64); ok {
				config.LowestSpeedLimit = int64(num)
			}
		case "max_overall_download_limit":
			if num, ok := value.(float64); ok {
				config.MaxOverallDownloadLimit = int64(num)
			}
		case "max_download_limit":
			if num, ok := value.(float64); ok {
				config.MaxDownloadLimit = int64(num)
			}
		case "max_overall_upload_limit":
			if num, ok := value.(float64); ok {
				config.MaxOverallUploadLimit = int64(num)
			}
		case "max_upload_limit":
			if num, ok := value.(float64); ok {
				config.MaxUploadLimit = int64(num)
			}
		case "timeout":
			if str, ok := value.(string); ok {
				if dur, err := parseDuration(str); err == nil {
					config.Timeout = dur
				}
			}
		case "retry_wait":
			if str, ok := value.(string); ok {
				if dur, err := parseDuration(str); err == nil {
					config.RetryWait = dur
				}
			}
		case "max_tries":
			if num, ok := value.(float64); ok {
				config.MaxTries = int(num)
			}
		case "user_agent":
			if str, ok := value.(string); ok {
				config.UserAgent = str
			}
		case "referer":
			if str, ok := value.(string); ok {
				config.Referer = str
			}
		case "all_proxy":
			if str, ok := value.(string); ok {
				config.AllProxy = str
			}
		case "no_proxy":
			if str, ok := value.(string); ok {
				config.NoProxy = str
			}
		case "http_proxy":
			if str, ok := value.(string); ok {
				config.HTTPProxy = str
			}
		case "https_proxy":
			if str, ok := value.(string); ok {
				config.HTTPSProxy = str
			}
		case "ftp_proxy":
			if str, ok := value.(string); ok {
				config.FTPProxy = str
			}
		case "ftp_user":
			if str, ok := value.(string); ok {
				config.FTPUser = str
			}
		case "ftp_passwd":
			if str, ok := value.(string); ok {
				config.FTPPasswd = str
			}
		case "enable_dht":
			if b, ok := value.(bool); ok {
				config.EnableDHT = b
			}
		case "dht_listen_port":
			if num, ok := value.(float64); ok {
				config.DHTListenPort = int(num)
			}
		case "enable_pex":
			if b, ok := value.(bool); ok {
				config.EnablePEX = b
			}
		case "seed_ratio":
			if num, ok := value.(float64); ok {
				config.SeedRatio = num
			}
		case "seed_time":
			if str, ok := value.(string); ok {
				if dur, err := parseDuration(str); err == nil {
					config.SeedTime = dur
				}
			}
		case "metalink_preferred_protocol":
			if arr, ok := value.([]interface{}); ok {
				protocols := make([]string, len(arr))
				for i, v := range arr {
					if str, ok := v.(string); ok {
						protocols[i] = str
					}
				}
				config.MetalinkPreferredProtocol = protocols
			}
		case "metalink_file":
			if str, ok := value.(string); ok {
				config.MetalinkFile = str
			}
		case "enable_rpc":
			if b, ok := value.(bool); ok {
				config.EnableRPC = b
			}
		case "rpc_port":
			if num, ok := value.(float64); ok {
				config.RPCPort = int(num)
			}
		case "rpc_secret":
			if str, ok := value.(string); ok {
				config.RPCSecret = str
			}
		case "check_integrity":
			if b, ok := value.(bool); ok {
				config.CheckIntegrity = b
			}
		case "continue":
			if b, ok := value.(bool); ok {
				config.ContinueDownload = b
			}
		case "allow_overwrite":
			if b, ok := value.(bool); ok {
				config.AllowOverwrite = b
			}
		case "auto_save_interval":
			if str, ok := value.(string); ok {
				if dur, err := parseDuration(str); err == nil {
					config.AutoSaveInterval = dur
				}
			}
		case "save_session":
			if b, ok := value.(bool); ok {
				config.SaveSession = b
			}
		case "input_file":
			if str, ok := value.(string); ok {
				config.InputFile = str
			}
		}
	}
	
	return config, nil
}

// parseConfConfig 解析.conf配置文件（简化实现）
func (p *Parser) parseConfConfig(filePath string) (*Config, error) {
	// 简化实现：直接调用parseJSONConfig
	return p.parseJSONConfig(filePath)
}

// parseDuration 解析持续时间字符串
func parseDuration(str string) (time.Duration, error) {
	// 尝试直接解析
	dur, err := time.ParseDuration(str)
	if err == nil {
		return dur, nil
	}
	
	// 尝试解析数字（秒）
	if seconds, err := strconv.ParseFloat(str, 64); err == nil {
		return time.Duration(seconds * float64(time.Second)), nil
	}
	
	return 0, fmt.Errorf("invalid duration format: %s", str)
}

// SaveConfig 保存配置到文件
func SaveConfig(config *Config, filePath string) error {
	// 转换为map
	configMap := map[string]interface{}{
		"log_level":                   config.LogLevel,
		"log_file":                    config.LogFile,
		"dir":                         config.Dir,
		"max_concurrent_downloads":    config.MaxConcurrentDownloads,
		"daemon":                      config.Daemon,
		"quiet":                       config.Quiet,
		"pause":                       config.Pause,
		"human_readable":              config.HumanReadable,
		"enable_color":                config.EnableColor,
		"dry_run":                     config.DryRun,
		"max_connection_per_server":   config.MaxConnectionPerServer,
		"min_split_size":              config.MinSplitSize,
		"split":                       config.Split,
		"lowest_speed_limit":          config.LowestSpeedLimit,
		"max_overall_download_limit":  config.MaxOverallDownloadLimit,
		"max_download_limit":          config.MaxDownloadLimit,
		"max_overall_upload_limit":    config.MaxOverallUploadLimit,
		"max_upload_limit":            config.MaxUploadLimit,
		"timeout":                     config.Timeout.String(),
		"retry_wait":                  config.RetryWait.String(),
		"max_tries":                   config.MaxTries,
		"user_agent":                  config.UserAgent,
		"referer":                     config.Referer,
		"all_proxy":                   config.AllProxy,
		"no_proxy":                    config.NoProxy,
		"http_proxy":                  config.HTTPProxy,
		"https_proxy":                 config.HTTPSProxy,
		"ftp_proxy":                   config.FTPProxy,
		"ftp_user":                    config.FTPUser,
		"ftp_passwd":                  config.FTPPasswd,
		"enable_dht":                  config.EnableDHT,
		"dht_listen_port":             config.DHTListenPort,
		"enable_pex":                  config.EnablePEX,
		"seed_ratio":                  config.SeedRatio,
		"seed_time":                   config.SeedTime.String(),
		"metalink_preferred_protocol": config.MetalinkPreferredProtocol,
		"metalink_file":               config.MetalinkFile,
		"enable_rpc":                  config.EnableRPC,
		"rpc_port":                    config.RPCPort,
		"rpc_secret":                  config.RPCSecret,
		"check_integrity":             config.CheckIntegrity,
		"continue":                    config.ContinueDownload,
		"allow_overwrite":             config.AllowOverwrite,
		"auto_save_interval":          config.AutoSaveInterval.String(),
		"save_session":                config.SaveSession,
		"input_file":                  config.InputFile,
	}
	// 转换为JSON
	data, err := json.MarshalIndent(configMap, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config failed: %w", err)
	}
	// 写入文件
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("write config file failed: %w", err)
	}
	return nil
}

// GetOptionSet 获取选项集
func (p *Parser) GetOptionSet() *OptionSet {
	return p.optionSet
}
