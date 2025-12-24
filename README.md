# aria2go - Aria2 的 Go 语言重构版本

## 项目简介

aria2go 是 aria2 下载工具的 Go 语言重构版本，参考了原始 aria2 项目的架构和功能设计，但充分利用 Go 语言的并发特性重新实现。

## 功能特性

- 支持多协议下载：HTTP/HTTPS、FTP、SFTP、BitTorrent、Metalink
- 分段下载和断点续传
- 多源下载和速度限制
- 磁盘缓存和内存优化
- 配置文件和命令行参数支持
- JSON-RPC/XML-RPC 接口（计划中）

## 架构设计

### 包结构

```
aria2go/
├── cmd/
│   └── aria2go/          # 命令行入口
├── internal/
│   ├── core/             # 核心引擎
│   │   ├── engine.go     # 下载引擎
│   │   ├── task.go       # 下载任务管理
│   │   └── scheduler.go  # 任务调度器
│   ├── protocol/         # 协议实现
│   │   ├── http/         # HTTP/HTTPS 协议
│   │   ├── ftp/          # FTP 协议
│   │   ├── bt/           # BitTorrent 协议
│   │   └── metalink/     # Metalink 协议
│   ├── network/          # 网络层
│   │   ├── connector.go  # 连接器
│   │   ├── socket.go     # Socket 封装
│   │   └── resolver.go   # DNS 解析
│   ├── storage/          # 存储层
│   │   ├── disk_writer.go # 磁盘写入
│   │   ├── piece_storage.go # 分片存储
│   │   └── cache.go      # 磁盘缓存
│   └── config/           # 配置管理
│       ├── option.go     # 配置选项
│       └── parser.go     # 配置解析
└── pkg/
    └── api/              # 公共 API 接口
```

### 设计原则

1. **并发模型**：使用 goroutine 和 channel 替代原始的事件循环
2. **模块化**：每个协议独立实现，易于扩展
3. **错误处理**：完善的错误处理和恢复机制
4. **内存安全**：利用 Go 的内存安全特性

## 开发状态

项目正在积极开发中，当前已完成架构设计，开始核心模块实现。

## 参考

- 原始 aria2 项目：https://github.com/aria2/aria2
- aria2 文档：https://aria2.github.io/