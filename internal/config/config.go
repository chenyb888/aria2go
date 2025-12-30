// Package config 提供配置管理功能
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Config 全局配置
type Config struct {
	// 通用配置
	LogLevel    string
	LogFile     string
	Dir         string // 下载目录
	MaxConcurrentDownloads int
	Daemon      bool   // 后台运行模式
	Quiet       bool   // 安静模式
	Pause       bool   // 启动时暂停
	HumanReadable bool // 人类可读的输出
	EnableColor bool   // 启用彩色输出
	DryRun      bool   // 试运行模式
	
	// 连接配置
	MaxConnectionPerServer int
	MinSplitSize          int64
	Split                 int
	LowestSpeedLimit      int64
	MaxOverallDownloadLimit int64
	MaxDownloadLimit      int64
	MaxOverallUploadLimit int64
	MaxUploadLimit        int64
	
	// 网络配置
	Timeout       time.Duration
	RetryWait     time.Duration
	MaxTries      int
	UserAgent     string
	Referer       string
	AllProxy      string
	NoProxy       string
	
	// HTTP/FTP配置
	HTTPProxy     string
	HTTPSProxy    string
	FTPProxy      string
	FTPUser       string
	FTPPasswd     string
	
	// BitTorrent配置
	EnableDHT     bool
	DHTListenPort int
	EnablePEX     bool
	SeedRatio     float64
	SeedTime      time.Duration
	BTWriteInterval time.Duration // BitTorrent 写入磁盘间隔
	
	// Metalink配置
	MetalinkPreferredProtocol []string
	MetalinkFile              string
	
	// RPC配置
	EnableRPC     bool
	RPCPort       int
	RPCSecret     string
	
	// 高级配置
	CheckIntegrity bool
	ContinueDownload bool
	AllowOverwrite bool
	AutoSaveInterval time.Duration
	SaveSession     bool
	InputFile       string
}

// DefaultConfig 返回默认配置
func DefaultConfig() *Config {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = "."
	}
	
	return &Config{
		LogLevel:                 "info",
		LogFile:                  "",
		Dir:                      filepath.Join(homeDir, "Downloads"),
		MaxConcurrentDownloads:   5,
		Daemon:                   false,
		Quiet:                    false,
		Pause:                    false,
		HumanReadable:            true,
		EnableColor:              true,
		DryRun:                   false,
		MaxConnectionPerServer:   1,
		MinSplitSize:             20 * 1024 * 1024, // 20MB
		Split:                    5,
		LowestSpeedLimit:         0,
		MaxOverallDownloadLimit:  0,
		MaxDownloadLimit:         0,
		MaxOverallUploadLimit:    0,
		MaxUploadLimit:           0,
		Timeout:                  60 * time.Second,
		RetryWait:                0,
		MaxTries:                 5,
		UserAgent:                "aria2go/1.0",
		Referer:                  "",
		AllProxy:                 "",
		NoProxy:                  "",
		HTTPProxy:                "",
		HTTPSProxy:               "",
		FTPProxy:                 "",
		FTPUser:                  "anonymous",
		FTPPasswd:                "anonymous@example.com",
		EnableDHT:                true,
		DHTListenPort:            6881,
		EnablePEX:                true,
		SeedRatio:                1.0,
		SeedTime:                 30 * time.Minute,
		BTWriteInterval:          1 * time.Second, // 默认每秒写入一次
		MetalinkPreferredProtocol: []string{"https", "http", "ftp"},
		MetalinkFile:             "",
		EnableRPC:                false,
		RPCPort:                  6800,
		RPCSecret:                "",
		CheckIntegrity:           false,
		ContinueDownload:         true,
		AllowOverwrite:           false,
		AutoSaveInterval:         30 * time.Second,
		SaveSession:              false,
		InputFile:                "",
	}
}

// Validate 验证配置
func (c *Config) Validate() error {
	if c.MaxConcurrentDownloads < 1 {
		return fmt.Errorf("max-concurrent-downloads must be greater than 0")
	}
	
	if c.MaxConnectionPerServer < 1 {
		return fmt.Errorf("max-connection-per-server must be greater than 0")
	}
	
	if c.Split < 1 {
		return fmt.Errorf("split must be greater than 0")
	}
	
	if c.MinSplitSize < 1024*1024 { // 1MB
		return fmt.Errorf("min-split-size must be at least 1MB")
	}
	
	if c.DHTListenPort < 1 || c.DHTListenPort > 65535 {
		return fmt.Errorf("dht-listen-port must be between 1 and 65535")
	}
	
	if c.RPCPort < 1 || c.RPCPort > 65535 {
		return fmt.Errorf("rpc-port must be between 1 and 65535")
	}
	
	return nil
}

// Merge 合并配置（后面的配置覆盖前面的）
func (c *Config) Merge(other *Config) {
	if other.LogLevel != "" {
		c.LogLevel = other.LogLevel
	}
	if other.LogFile != "" {
		c.LogFile = other.LogFile
	}
	if other.Dir != "" {
		c.Dir = other.Dir
	}
	if other.MaxConcurrentDownloads > 0 {
		c.MaxConcurrentDownloads = other.MaxConcurrentDownloads
	}
	
	c.Daemon = other.Daemon
	c.Quiet = other.Quiet
	c.Pause = other.Pause
	c.HumanReadable = other.HumanReadable
	c.EnableColor = other.EnableColor
	c.DryRun = other.DryRun
	if other.MaxConnectionPerServer > 0 {
		c.MaxConnectionPerServer = other.MaxConnectionPerServer
	}
	if other.MinSplitSize > 0 {
		c.MinSplitSize = other.MinSplitSize
	}
	if other.Split > 0 {
		c.Split = other.Split
	}
	if other.LowestSpeedLimit > 0 {
		c.LowestSpeedLimit = other.LowestSpeedLimit
	}
	if other.MaxOverallDownloadLimit > 0 {
		c.MaxOverallDownloadLimit = other.MaxOverallDownloadLimit
	}
	if other.MaxDownloadLimit > 0 {
		c.MaxDownloadLimit = other.MaxDownloadLimit
	}
	if other.MaxOverallUploadLimit > 0 {
		c.MaxOverallUploadLimit = other.MaxOverallUploadLimit
	}
	if other.MaxUploadLimit > 0 {
		c.MaxUploadLimit = other.MaxUploadLimit
	}
	if other.Timeout > 0 {
		c.Timeout = other.Timeout
	}
	if other.RetryWait > 0 {
		c.RetryWait = other.RetryWait
	}
	if other.MaxTries > 0 {
		c.MaxTries = other.MaxTries
	}
	if other.UserAgent != "" {
		c.UserAgent = other.UserAgent
	}
	if other.Referer != "" {
		c.Referer = other.Referer
	}
	if other.AllProxy != "" {
		c.AllProxy = other.AllProxy
	}
	if other.NoProxy != "" {
		c.NoProxy = other.NoProxy
	}
	if other.HTTPProxy != "" {
		c.HTTPProxy = other.HTTPProxy
	}
	if other.HTTPSProxy != "" {
		c.HTTPSProxy = other.HTTPSProxy
	}
	if other.FTPProxy != "" {
		c.FTPProxy = other.FTPProxy
	}
	if other.FTPUser != "" {
		c.FTPUser = other.FTPUser
	}
	if other.FTPPasswd != "" {
		c.FTPPasswd = other.FTPPasswd
	}
	
	c.EnableDHT = other.EnableDHT
	if other.DHTListenPort > 0 {
		c.DHTListenPort = other.DHTListenPort
	}
	c.EnablePEX = other.EnablePEX
	if other.SeedRatio > 0 {
		c.SeedRatio = other.SeedRatio
	}
	if other.SeedTime > 0 {
		c.SeedTime = other.SeedTime
	}
	
	if len(other.MetalinkPreferredProtocol) > 0 {
		c.MetalinkPreferredProtocol = other.MetalinkPreferredProtocol
	}
	if other.MetalinkFile != "" {
		c.MetalinkFile = other.MetalinkFile
	}
	
	c.EnableRPC = other.EnableRPC
	if other.RPCPort > 0 {
		c.RPCPort = other.RPCPort
	}
	if other.RPCSecret != "" {
		c.RPCSecret = other.RPCSecret
	}
	
	c.CheckIntegrity = other.CheckIntegrity
	c.ContinueDownload = other.ContinueDownload
	c.AllowOverwrite = other.AllowOverwrite
	if other.AutoSaveInterval > 0 {
		c.AutoSaveInterval = other.AutoSaveInterval
	}
	c.SaveSession = other.SaveSession
	if other.InputFile != "" {
		c.InputFile = other.InputFile
	}
}