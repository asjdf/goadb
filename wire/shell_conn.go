package wire

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/asjdf/goadb/wire/os_specific"
)

type ShellConn struct {
	rawConn *Conn
}

func NewShellConn(rawConn *Conn) (*ShellConn, error) {
	var shellConn = ShellConn{
		rawConn: rawConn,
	}
	return &shellConn, nil
}

func (s *ShellConn) Read(p []byte) (n int, err error) {
	return s.rawConn.Read(p)
}

func (s *ShellConn) Write(p []byte) (n int, err error) {
	//n=len(p)
	//err= s.rawConn.SendMessage(p)
	//return n, err
	return s.rawConn.Write(p)
}

func (s *ShellConn) Close() error {
	return s.rawConn.Close()
}

func (s *ShellConn) ReadUntil(untilData []byte) ([]byte, error) {
	if len(untilData) == 0 {
		return nil, nil
	}

	var buf []byte
	// 临时缓冲，用于读取单个字节。
	// 虽然逐字节读取性能较低，但对于纯 io.Reader 来说，这是防止多读（Over-reading）的唯一安全方法。
	tmp := make([]byte, 1)

	for {
		n, err := s.rawConn.Read(tmp)
		if n > 0 {
			b := tmp[0]
			buf = append(buf, b)

			// 优化：只有当已读取长度大于等于分隔符长度，且最后一个字节匹配时，才进行完整后缀检查
			if len(buf) >= len(untilData) && b == untilData[len(untilData)-1] {
				// 检查 buf 的末尾是否就是 untilData
				if bytes.HasSuffix(buf, untilData) {
					return buf, nil
				}
			}
		}

		if err != nil {
			// 如果遇到 EOF，返回已读取的数据和错误
			if err == io.EOF {
				return buf, err
			}
			return buf, err
		}
	}
}

func (s *ShellConn) ReadLine() (string, error) {
	lineData, err := s.ReadUntil([]byte{'\n'})
	return string(lineData), err
}

func (s *ShellConn) WriteLine(line string) (int, error) {
	var lineWithEOL = fmt.Sprintf("%s\n", line)
	var lineData = []byte(lineWithEOL)
	return s.Write(lineData)
}

type ShellConnRunCommandArgs struct {
	CommandStr string
}

func (s *ShellConn) RunCommand(ctx context.Context, args ShellConnRunCommandArgs) (exitCode int, outputData []byte, err error) {
	return os_specific.RunCommandInShell(ctx, s.rawConn, args.CommandStr)
}
