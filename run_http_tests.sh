#!/bin/bash

# aria2go HTTP协议测试脚本
# 运行HTTP相关的单元测试和集成测试

set -e

echo "=== 开始运行aria2go HTTP协议测试 ==="
echo "当前时间: $(date)"
echo ""

# 进入项目目录
cd /opt/aria2go

# 1. 运行HTTP客户端单元测试
echo "=== 运行HTTP客户端单元测试 ==="
go test -v ./internal/protocol/http/test -run "TestNewClient|TestNewImprovedClient|TestDownloadSmallFile|TestHeadRequest|TestRangeSupport|TestRetryOnFailure|TestCancelDownload|TestInvalidURL|TestFileCreation|TestClientStats|TestProxyConfiguration|TestTLSConfiguration" 2>&1 | tee http_client_tests.log

echo ""
echo "客户端单元测试完成"
echo ""

# 2. 运行HTTP任务单元测试
echo "=== 运行HTTP任务单元测试 ==="
go test -v ./internal/protocol/http/test -run "TestNewHTTPTask|TestHTTPTaskLifecycle|TestHTTPTaskPauseResume|TestHTTPTaskStop|TestHTTPTaskErrorHandling|TestHTTPTaskSegmentedDownload|TestHTTPTaskConfigValidation|TestHTTPTaskProgressUpdates" 2>&1 | tee http_task_tests.log

echo ""
echo "任务单元测试完成"
echo ""

# 3. 运行HTTP集成测试（跳过性能测试）
echo "=== 运行HTTP集成测试 ==="
go test -v ./internal/protocol/http/test -run "TestHTTPIntegration|TestHTTPConcurrentTasks|TestHTTPResumeDownload|TestHTTPErrorRecovery" -timeout 5m 2>&1 | tee http_integration_tests.log

echo ""
echo "集成测试完成"
echo ""

# 4. 运行性能测试（可选）
if [[ "$1" == "--include-performance" ]]; then
    echo "=== 运行HTTP性能测试 ==="
    go test -v ./internal/protocol/http/test -run "TestHTTPPerformance" -timeout 10m 2>&1 | tee http_performance_tests.log
    echo ""
    echo "性能测试完成"
    echo ""
fi

# 5. 生成测试报告
echo "=== 生成测试报告 ==="
echo "测试完成时间: $(date)"
echo ""
echo "测试日志文件:"
echo "  - http_client_tests.log: HTTP客户端单元测试"
echo "  - http_task_tests.log: HTTP任务单元测试"
echo "  - http_integration_tests.log: HTTP集成测试"
if [[ "$1" == "--include-performance" ]]; then
    echo "  - http_performance_tests.log: HTTP性能测试"
fi
echo ""
echo "=== aria2go HTTP协议测试完成 ==="