package wire

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/asjdf/goadb/wire/os_specific"
)

type ShellConn struct {
	rawConn *Conn
	framed  bool
	readBuf bytes.Buffer
}

const (
	shellV2PacketStdin            byte = 0
	shellV2PacketStdout           byte = 1
	shellV2PacketStderr           byte = 2
	shellV2PacketExit             byte = 3
	shellV2PacketCloseStdin       byte = 4
	shellV2PacketWindowSizeChange byte = 5
)

func NewShellConn(rawConn *Conn) (*ShellConn, error) {
	var shellConn = ShellConn{
		rawConn: rawConn,
	}
	return &shellConn, nil
}

func NewShellV2Conn(rawConn *Conn) (*ShellConn, error) {
	var shellConn = ShellConn{
		rawConn: rawConn,
		framed:  true,
	}
	return &shellConn, nil
}

func (s *ShellConn) Read(p []byte) (n int, err error) {
	if s.framed {
		return s.readShellV2(p)
	}
	return s.rawConn.Read(p)
}

func (s *ShellConn) Write(p []byte) (n int, err error) {
	if s.framed {
		if err := s.writeShellV2(shellV2PacketStdin, p); err != nil {
			return 0, err
		}
		return len(p), nil
	}
	//n=len(p)
	//err= s.rawConn.SendMessage(p)
	//return n, err
	return s.rawConn.Write(p)
}

func (s *ShellConn) Close() error {
	return s.rawConn.Close()
}

func (s *ShellConn) CloseStdin() error {
	if !s.framed {
		return nil
	}
	return s.writeShellV2(shellV2PacketCloseStdin, nil)
}

func (s *ShellConn) SetWindowSize(rows, cols, xPixels, yPixels int) error {
	if !s.framed {
		return nil
	}
	payload := []byte(fmt.Sprintf("%dx%d,%dx%d\x00", rows, cols, xPixels, yPixels))
	return s.writeShellV2(shellV2PacketWindowSizeChange, payload)
}

func (s *ShellConn) readShellV2(p []byte) (int, error) {
	for s.readBuf.Len() == 0 {
		packetID, payload, err := s.readShellV2Packet()
		if err != nil {
			return 0, err
		}
		switch packetID {
		case shellV2PacketStdout, shellV2PacketStderr:
			if len(payload) > 0 {
				_, _ = s.readBuf.Write(payload)
			}
		case shellV2PacketExit:
			return 0, io.EOF
		default:
			// Ignore control packets that are not command output.
		}
	}
	return s.readBuf.Read(p)
}

func (s *ShellConn) readShellV2Packet() (byte, []byte, error) {
	header := make([]byte, 5)
	if _, err := io.ReadFull(s.rawConn, header); err != nil {
		return 0, nil, err
	}
	payloadLen := binary.LittleEndian.Uint32(header[1:])
	payload := make([]byte, payloadLen)
	if payloadLen > 0 {
		if _, err := io.ReadFull(s.rawConn, payload); err != nil {
			return 0, nil, err
		}
	}
	return header[0], payload, nil
}

func (s *ShellConn) writeShellV2(packetID byte, payload []byte) error {
	header := make([]byte, 5)
	header[0] = packetID
	binary.LittleEndian.PutUint32(header[1:], uint32(len(payload)))
	if _, err := s.rawConn.Write(header); err != nil {
		return err
	}
	if len(payload) == 0 {
		return nil
	}
	_, err := s.rawConn.Write(payload)
	return err
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
	return os_specific.RunCommandInShell(ctx, s, args.CommandStr)
}
