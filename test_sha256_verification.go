package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
)

func main() {
	// 预期的SHA256值
	expectedSHA256 := "9602c69c52d93f51295c0199af395ca0edbe35e36506e32b8e749ce6c8f5b60a"
	
	// 创建一个测试文件来演示SHA256验证
	testDir := "/tmp/sha256_test"
	if err := os.MkdirAll(testDir, 0755); err != nil {
		log.Fatalf("创建测试目录失败: %v", err)
	}
	
	// 创建测试文件
	testFilePath := filepath.Join(testDir, "test_file.bin")
	testContent := []byte("This is a test file for SHA256 verification. CentOS-8.5.2111-x86_64-boot.iso should have SHA256: " + expectedSHA256)
	
	// 写入测试文件
	if err := os.WriteFile(testFilePath, testContent, 0644); err != nil {
		log.Fatalf("创建测试文件失败: %v", err)
	}
	
	fmt.Printf("测试文件已创建: %s\n", testFilePath)
	fmt.Printf("文件大小: %d 字节\n", len(testContent))
	
	// 计算测试文件的SHA256
	calculatedSHA256, err := calculateSHA256(testFilePath)
	if err != nil {
		log.Fatalf("计算SHA256失败: %v", err)
	}
	
	fmt.Printf("计算出的SHA256: %s\n", calculatedSHA256)
	fmt.Printf("预期的SHA256:   %s\n", expectedSHA256)
	
	// 演示验证功能
	fmt.Println("\n=== SHA256验证演示 ===")
	
	// 场景1：验证匹配
	fmt.Println("\n场景1：验证匹配的SHA256")
	match := verifySHA256(testFilePath, calculatedSHA256)
	if match {
		fmt.Println("✓ SHA256验证通过")
	} else {
		fmt.Println("✗ SHA256验证失败")
	}
	
	// 场景2：验证不匹配
	fmt.Println("\n场景2：验证不匹配的SHA256")
	wrongSHA256 := "0000000000000000000000000000000000000000000000000000000000000000"
	match = verifySHA256(testFilePath, wrongSHA256)
	if match {
		fmt.Println("✓ SHA256验证通过")
	} else {
		fmt.Println("✗ SHA256验证失败")
	}
	
	// 场景3：验证预期的CentOS SHA256
	fmt.Println("\n场景3：验证预期的CentOS SHA256（使用测试文件）")
	match = verifySHA256(testFilePath, expectedSHA256)
	if match {
		fmt.Println("✓ SHA256验证通过")
	} else {
		fmt.Println("✗ SHA256验证失败 - 这是预期的，因为测试文件内容不同")
	}
	
	// 演示如何集成到下载器中
	fmt.Println("\n=== 下载器集成示例 ===")
	fmt.Println("在下载器中集成SHA256验证的步骤：")
	fmt.Println("1. 下载文件前获取预期的SHA256值")
	fmt.Println("2. 下载完成后计算文件的SHA256")
	fmt.Println("3. 比较计算值和预期值")
	fmt.Println("4. 如果匹配，标记下载为成功")
	fmt.Println("5. 如果不匹配，标记下载为失败并重试或报告错误")
	
	// 清理
	fmt.Printf("\n清理测试文件...\n")
	if err := os.Remove(testFilePath); err != nil {
		log.Printf("清理测试文件失败: %v", err)
	}
	if err := os.Remove(testDir); err != nil {
		log.Printf("清理测试目录失败: %v", err)
	}
	
	fmt.Println("测试完成!")
}

// calculateSHA256 计算文件的SHA256哈希值
func calculateSHA256(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()
	
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// verifySHA256 验证文件的SHA256哈希值
func verifySHA256(filePath string, expectedSHA256 string) bool {
	calculatedSHA256, err := calculateSHA256(filePath)
	if err != nil {
		return false
	}
	
	return calculatedSHA256 == expectedSHA256
}
