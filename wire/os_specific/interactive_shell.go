package os_specific

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/pkg/errors"
)

// RunCommandInShell 在 Windows 上执行命令，不使用 expect.Console
// 它通过拼接 echo 命令来捕获退出码，并自动剥离回显。
func RunCommandInShell(ctx context.Context, conn io.ReadWriter, cmdStr string) (int, []byte, error) {
	// 1. 生成一个随机的唯一标记 (Sentinel)
	// 格式例如: __CMD_END_a1b2c3d4__
	randBytes := make([]byte, 8)
	if _, err := rand.Read(randBytes); err != nil {
		return -1, nil, fmt.Errorf("failed to generate random marker: %w", err)
	}
	marker := "__CMD_END_" + hex.EncodeToString(randBytes) + "__"

	// 2. 构造组合命令
	// 语法: <cmd>; echo " <marker> $?"
	// 这样无论 cmd 成功与否，后面的 echo 都会执行，打印出标记和 cmd 的退出码
	fullCmd := fmt.Sprintf("%s; echo \" %s $?\"", cmdStr, marker)

	// 3. 设置期望的正则表达式
	// 我们期待看到:  __CMD_END_xxxx__ <exit_code>
	// 注意：ADB shell 返回的换行通常是 \r\n
	markerRegex := regexp.MustCompile(marker + `\s+(\d+)`)

	// 4. 发送命令
	// 需要手动追加 \n
	cmdWithEOL := []byte(fullCmd + "\n")
	if _, err := conn.Write(cmdWithEOL); err != nil {
		return -1, nil, fmt.Errorf("failed to send command: %w", err)
	}

	// 5. 等待标记出现 (带超时控制)
	// 使用 ReadUntil 读取数据直到找到标记
	// 由于需要超时控制，我们需要实现一个带超时的读取机制
	done := make(chan bool, 1)
	var rawOutput []byte
	var readErr error

	go func() {
		// 读取数据直到找到标记
		var buf []byte
		bufStr := ""
		for {
			// 尝试读取一个字节
			tmp := make([]byte, 1)
			n, err := conn.Read(tmp)
			if err != nil {
				readErr = err
				rawOutput = buf
				done <- true
				return
			}
			if n > 0 {
				buf = append(buf, tmp[0])
				bufStr = string(buf)

				// 检查是否包含标记
				if markerRegex.MatchString(bufStr) {
					rawOutput = buf
					done <- true
					return
				}
			}
		}
	}()

	// 等待完成或超时
	// 从 ctx 中读取超时设置
	_, hasDeadline := ctx.Deadline()
	if !hasDeadline {
		// 没有设置超时，无限等待
		<-done
		if readErr != nil {
			return -1, nil, fmt.Errorf("command execution error: %w", readErr)
		}
	} else {
		// 带超时等待，使用 ctx.Done() 来检测超时
		select {
		case <-done:
			if readErr != nil {
				return -1, nil, fmt.Errorf("command execution error: %w", readErr)
			}
		case <-ctx.Done():
			if closer, ok := conn.(io.Closer); ok {
				_ = closer.Close()
			}
			return -1, nil, fmt.Errorf("command execution timeout: %w", ctx.Err())
		}
	}

	// 6. 解析退出码
	// 从 rawOutput 中提取匹配组
	rawOutputStr := string(rawOutput)
	match := markerRegex.FindStringSubmatch(rawOutputStr)
	if len(match) < 2 {
		return -1, rawOutput, errors.New("failed to parse exit code from output")
	}
	exitCodeStr := match[1]
	var exitCode int
	_, err := fmt.Sscanf(exitCodeStr, "%d", &exitCode)
	if err != nil {
		return -1, rawOutput, fmt.Errorf("invalid exit code format: %s", exitCodeStr)
	}

	// 7. 清洗输出数据 (Output Cleaning)
	// rawOutput 包含了非常杂乱的数据，通常结构如下：
	// Line 1: ls -l; echo "__CMD_END_..." (Shell 的回显，Input Echo)
	// Line 2: ... (命令真实输出)
	// Line N: __CMD_END_... 0 (我们构造的结束行)

	lines := strings.Split(strings.ReplaceAll(rawOutputStr, "\r\n", "\n"), "\n")
	var cleanLines []string

	for i, line := range lines {
		// 移除行首尾空白字符处理
		trimLine := strings.TrimSpace(line)

		// 过滤掉第一行：通常是命令的回显 (Input Echo)
		// 简单的判断方法：如果这一行包含了我们的 marker 且包含了 cmdStr，或者是 cmdStr 本身，就认为是回显
		if i == 0 && (strings.Contains(trimLine, marker) || trimLine == cmdStr) {
			continue
		}

		// 过滤掉最后一行：这是我们构造的 echo 输出
		if strings.Contains(trimLine, marker) {
			continue
		}

		// 过滤掉空行（可选，视需求而定，这里保留空行但移除行尾\r）
		cleanLines = append(cleanLines, strings.TrimRight(line, "\r"))
	}

	finalOutput := strings.Join(cleanLines, "\n")

	// 再次修剪首尾的空行，让输出更干净
	finalOutput = strings.TrimSpace(finalOutput)

	return exitCode, []byte(finalOutput), nil
}
