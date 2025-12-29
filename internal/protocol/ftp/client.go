// Package ftp 提供FTP协议支持
package ftp

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"aria2go/internal/core"
	"aria2go/internal/protocol"
)

// Client FTP客户端
type Client struct {
	config     Config
	conn       net.Conn
	textConn   *textproto.Conn
	serverAddr string
	isTLS      bool
}

// Config FTP客户端配置
type Config struct {
	Host     string
	Port     int
	Username string
	Password string
	Timeout  int
	Passive  bool
	TLS      bool
}

// NewClient 创建FTP客户端
func NewClient(config Config) (*Client, error) {
	return &Client{
		config: config,
	}, nil
}

// Connect 连接到FTP服务器
func (c *Client) Connect(ctx context.Context) error {
	addr := fmt.Sprintf("%s:%d", c.config.Host, c.config.Port)

	dialer := &net.Dialer{
		Timeout: time.Duration(c.config.Timeout) * time.Second,
	}

	var conn net.Conn
	var err error

	if c.config.TLS {
		// FTPS连接
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{
			InsecureSkipVerify: true,
		})
		c.isTLS = true
	} else {
		// 普通FTP连接
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}

	if err != nil {
		return fmt.Errorf("connect to FTP server failed: %w", err)
	}

	c.conn = conn
	c.textConn = textproto.NewConn(conn)
	c.serverAddr = addr

	// 读取欢迎消息
	_, _, err = c.textConn.ReadResponse(220)
	if err != nil {
		c.Close()
		return fmt.Errorf("read greeting failed: %w", err)
	}

	return nil
}

// Login 登录FTP服务器
func (c *Client) Login(ctx context.Context) error {
	// 发送USER命令
	err := c.textConn.PrintfLine("USER %s", c.config.Username)
	if err != nil {
		return fmt.Errorf("send USER command failed: %w", err)
	}

	_, _, err = c.textConn.ReadResponse(331)
	if err != nil {
		return fmt.Errorf("USER command failed: %w", err)
	}

	// 发送PASS命令
	err = c.textConn.PrintfLine("PASS %s", c.config.Password)
	if err != nil {
		return fmt.Errorf("send PASS command failed: %w", err)
	}

	_, _, err = c.textConn.ReadResponse(230)
	if err != nil {
		return fmt.Errorf("PASS command failed: %w", err)
	}

	return nil
}

// Type 设置传输模式
func (c *Client) Type(mode string) error {
	err := c.textConn.PrintfLine("TYPE %s", mode)
	if err != nil {
		return fmt.Errorf("send TYPE command failed: %w", err)
	}

	_, _, err = c.textConn.ReadResponse(200)
	if err != nil {
		return fmt.Errorf("TYPE command failed: %w", err)
	}

	return nil
}

// Size 获取文件大小
func (c *Client) Size(path string) (int64, error) {
	err := c.textConn.PrintfLine("SIZE %s", path)
	if err != nil {
		return 0, fmt.Errorf("send SIZE command failed: %w", err)
	}

	_, line, err := c.textConn.ReadResponse(213)
	if err != nil {
		return 0, fmt.Errorf("SIZE command failed: %w", err)
	}

	size, err := strconv.ParseInt(strings.TrimSpace(line), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse size failed: %w", err)
	}

	return size, nil
}

// Pasv 进入被动模式
func (c *Client) Pasv() (string, error) {
	err := c.textConn.PrintfLine("PASV")
	if err != nil {
		return "", fmt.Errorf("send PASV command failed: %w", err)
	}

	_, line, err := c.textConn.ReadResponse(227)
	if err != nil {
		return "", fmt.Errorf("PASV command failed: %w", err)
	}

	// 解析PASV响应: 227 Entering Passive Mode (h1,h2,h3,h4,p1,p2)
	start := strings.Index(line, "(")
	end := strings.Index(line, ")")
	if start == -1 || end == -1 {
		return "", fmt.Errorf("invalid PASV response format")
	}

	parts := strings.Split(line[start+1:end], ",")
	if len(parts) != 6 {
		return "", fmt.Errorf("invalid PASV response format")
	}

	p1, err := strconv.Atoi(parts[4])
	if err != nil {
		return "", fmt.Errorf("parse port failed: %w", err)
	}

	p2, err := strconv.Atoi(parts[5])
	if err != nil {
		return "", fmt.Errorf("parse port failed: %w", err)
	}

	port := p1*256 + p2
	host := fmt.Sprintf("%s.%s.%s.%s", parts[0], parts[1], parts[2], parts[3])

	return fmt.Sprintf("%s:%d", host, port), nil
}

// Retr 下载文件
func (c *Client) Retr(path string, offset int64) (net.Conn, error) {
	if offset > 0 {
		err := c.textConn.PrintfLine("REST %d", offset)
		if err != nil {
			return nil, fmt.Errorf("send REST command failed: %w", err)
		}

		_, _, err = c.textConn.ReadResponse(350)
		if err != nil {
			return nil, fmt.Errorf("REST command failed: %w", err)
		}
	}

	err := c.textConn.PrintfLine("RETR %s", path)
	if err != nil {
		return nil, fmt.Errorf("send RETR command failed: %w", err)
	}

	_, _, err = c.textConn.ReadResponse(150)
	if err != nil {
		return nil, fmt.Errorf("RETR command failed: %w", err)
	}

	return c.conn, nil
}

// Close 关闭连接
func (c *Client) Close() error {
	if c.textConn != nil {
		c.textConn.PrintfLine("QUIT")
		c.textConn.ReadResponse(221)
		c.textConn.Close()
	}
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// FTPDownloader FTP下载器实现
type FTPDownloader struct {
	client *Client
	config Config
}

// NewFTPDownloader 创建FTP下载器
func NewFTPDownloader(config Config) (*FTPDownloader, error) {
	client, err := NewClient(config)
	if err != nil {
		return nil, err
	}

	return &FTPDownloader{
		client: client,
		config: config,
	}, nil
}

// Download 实现protocol.Downloader接口
func (fd *FTPDownloader) Download(ctx context.Context, task core.Task) error {
	// 解析URL获取主机、端口、路径
	url := task.GetURL()

	// 从URL中提取FTP连接信息
	host, port, username, password, path, err := parseFTPURL(url)
	if err != nil {
		return fmt.Errorf("parse FTP URL failed: %w", err)
	}

	// 创建FTP客户端配置
	config := Config{
		Host:     host,
		Port:     port,
		Username: username,
		Password: password,
		Timeout:  30,
		Passive:  true,
		TLS:      strings.HasPrefix(url, "ftps://"),
	}

	// 创建FTP客户端
	client, err := NewClient(config)
	if err != nil {
		return fmt.Errorf("create FTP client failed: %w", err)
	}
	defer client.Close()

	// 连接FTP服务器
	if err := client.Connect(ctx); err != nil {
		return fmt.Errorf("connect FTP server failed: %w", err)
	}

	// 登录
	if err := client.Login(ctx); err != nil {
		return fmt.Errorf("login FTP server failed: %w", err)
	}

	// 设置二进制传输模式
	if err := client.Type("I"); err != nil {
		return fmt.Errorf("set binary mode failed: %w", err)
	}

	// 获取文件大小
	fileSize, err := client.Size(path)
	if err != nil {
		return fmt.Errorf("get file size failed: %w", err)
	}

	// 进入被动模式
	dataAddr, err := client.Pasv()
	if err != nil {
		return fmt.Errorf("enter passive mode failed: %w", err)
	}

	// 建立数据连接
	dataConn, err := net.DialTimeout("tcp", dataAddr, 30*time.Second)
	if err != nil {
		return fmt.Errorf("connect data port failed: %w", err)
	}
	defer dataConn.Close()

	// 发送RETR命令
	_, err = client.Retr(path, 0)
	if err != nil {
		return fmt.Errorf("send RETR command failed: %w", err)
	}

	// 创建输出文件
	outputPath := task.GetOutputPath()
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return fmt.Errorf("create output directory failed: %w", err)
	}

	file, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("create output file failed: %w", err)
	}
	defer file.Close()

	// 从数据连接读取数据并写入文件
	buf := make([]byte, 32*1024)
	var downloaded int64

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			n, err := dataConn.Read(buf)
			if err != nil && err != io.EOF {
				return fmt.Errorf("read data failed: %w", err)
			}

			if n > 0 {
				if _, err := file.Write(buf[:n]); err != nil {
					return fmt.Errorf("write file failed: %w", err)
				}
				downloaded += int64(n)

				// 更新进度
				if progressCallback := task.GetProgressCallback(); progressCallback != nil {
					progressCallback(core.TaskProgress{
						TotalBytes:      fileSize,
						DownloadedBytes: downloaded,
						Progress:        float64(downloaded) / float64(fileSize) * 100,
					})
				}
			}

			if err == io.EOF {
				break
			}
		}
	}

	return nil
}

// CanHandle 检查是否可以处理FTP URL
func (fd *FTPDownloader) CanHandle(url string) bool {
	// 检查是否是FTP或FTPS URL
	return strings.HasPrefix(url, "ftp://") || strings.HasPrefix(url, "ftps://")
}

// parseFTPURL 解析FTP URL
func parseFTPURL(url string) (host string, port int, username, password, path string, err error) {
	// 移除协议前缀
	var prefix string
	if strings.HasPrefix(url, "ftps://") {
		prefix = "ftps://"
	} else {
		prefix = "ftp://"
	}

	urlWithoutPrefix := strings.TrimPrefix(url, prefix)

	// 解析认证信息
	atIndex := strings.Index(urlWithoutPrefix, "@")
	if atIndex != -1 {
		authPart := urlWithoutPrefix[:atIndex]
		urlWithoutPrefix = urlWithoutPrefix[atIndex+1:]

		// 解析用户名和密码
		colonIndex := strings.Index(authPart, ":")
		if colonIndex != -1 {
			username = authPart[:colonIndex]
			password = authPart[colonIndex+1:]
		} else {
			username = authPart
			password = "anonymous"
		}
	} else {
		username = "anonymous"
		password = "anonymous@example.com"
	}

	// 解析主机和端口
	slashIndex := strings.Index(urlWithoutPrefix, "/")
	var hostPort string
	if slashIndex != -1 {
		hostPort = urlWithoutPrefix[:slashIndex]
		path = urlWithoutPrefix[slashIndex:]
	} else {
		hostPort = urlWithoutPrefix
		path = "/"
	}

	// 解析端口
	colonIndex := strings.Index(hostPort, ":")
	if colonIndex != -1 {
		host = hostPort[:colonIndex]
		portStr := hostPort[colonIndex+1:]
		port, err = strconv.Atoi(portStr)
		if err != nil {
			return "", 0, "", "", "", fmt.Errorf("invalid port: %w", err)
		}
	} else {
		host = hostPort
		port = 21
	}

	if host == "" {
		return "", 0, "", "", "", fmt.Errorf("invalid FTP URL: missing host")
	}

	return host, port, username, password, path, nil
}

// Name 返回下载器名称
func (fd *FTPDownloader) Name() string {
	return "ftp"
}

// SupportsResume 是否支持断点续传
func (fd *FTPDownloader) SupportsResume() bool {
	return true
}

// SupportsConcurrent 是否支持并发下载
func (fd *FTPDownloader) SupportsConcurrent() bool {
	return false
}

// SupportsSegments 是否支持分段下载
func (fd *FTPDownloader) SupportsSegments() bool {
	return false
}

// Factory FTP下载器工厂
type Factory struct{}

// CreateDownloader 创建FTP下载器
func (f *Factory) CreateDownloader(config interface{}) (protocol.Downloader, error) {
	var ftpConfig Config
	if config != nil {
		if c, ok := config.(Config); ok {
			ftpConfig = c
		} else {
			// 使用默认配置
			ftpConfig = DefaultConfig()
		}
	} else {
		ftpConfig = DefaultConfig()
	}

	return NewFTPDownloader(ftpConfig)
}

// SupportedProtocols 返回支持的协议列表
func (f *Factory) SupportedProtocols() []string {
	return []string{"ftp", "ftps"}
}

// DefaultConfig 返回默认配置
func DefaultConfig() Config {
	return Config{
		Host:     "localhost",
		Port:     21,
		Username: "anonymous",
		Password: "anonymous@example.com",
		Timeout:  30,
		Passive:  true,
		TLS:      false,
	}
}

// IsFTPURL 检查URL是否是FTP协议
func IsFTPURL(url string) bool {
	return strings.HasPrefix(strings.ToLower(url), "ftp://") ||
		strings.HasPrefix(strings.ToLower(url), "ftps://")
}

// NewFTPTask 创建新的FTP下载任务
func NewFTPTask(id string, config core.TaskConfig, eventCh chan<- core.Event) (core.Task, error) {
	// 简化实现：实际应该创建真正的 FTP 任务
	// 参考 aria2 的 FtpDownloadCommand
	return core.NewBaseTask(id, config, eventCh), nil
}
