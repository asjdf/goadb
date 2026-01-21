package adb

import (
	"fmt"
	"strconv"
	"strings"
)

type ForwardProtocolKind string

func (v ForwardProtocolKind) PortStrOf(port int) string {
	var resultStr = ""
	var rawProtocolStr = string(v)
	if rawProtocolStr != "" {
		resultStr = rawProtocolStr + ":"
	}
	resultStr += fmt.Sprintf("%d", port)
	return resultStr
}

const (
	ForwardProtocolKindNotSpecified = ForwardProtocolKind("")
	ForwardProtocolKindInvalid      = ForwardProtocolKind("invalid")
	ForwardProtocolKindTCP          = ForwardProtocolKind("tcp")
	ForwardProtocolKindUDP          = ForwardProtocolKind("udp")
)

// Forward 表示一个端口转发规则
type Forward struct {
	Serial             string // 设备serial
	LocalProtocolKind  ForwardProtocolKind
	LocalPort          int //本机端口
	RemoteProtocolKind ForwardProtocolKind
	RemotePort         int //设备上的端口
}

func (i Forward) String() string {
	return fmt.Sprintf("%s %s %s", i.Serial, i.LocalProtocolKind.PortStrOf(i.LocalPort), i.RemoteProtocolKind.PortStrOf(i.RemotePort))
}

// parseForwardList 解析 forward:list 命令返回的列表
// 格式示例: "172.16.7.212:5555 tcp:29700 tcp:29700"
func parseForwardList(data string) []Forward {
	data = strings.TrimSpace(data)
	if data == "" {
		return []Forward{}
	}

	lines := strings.Split(data, "\n")
	forwards := make([]Forward, 0, len(lines))

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.Fields(line)
		if len(parts) >= 3 {
			var (
				serial    = parts[0]
				localStr  = parts[1]
				remoteStr = parts[2]
			)
			localProtocolKind, localPort, _ := splitPortStr(localStr)
			remoteProtocolKind, remotePort, _ := splitPortStr(remoteStr)
			forwards = append(forwards, Forward{
				Serial:             serial,
				LocalProtocolKind:  localProtocolKind,
				LocalPort:          localPort,
				RemoteProtocolKind: remoteProtocolKind,
				RemotePort:         remotePort,
			})
		}
	}

	return forwards
}

func splitPortStr(portStr string) (ForwardProtocolKind, int, error) {
	var partList = strings.Split(portStr, ":")
	if len(partList) == 1 {
		portNumber, err := strconv.ParseInt(partList[0], 10, 32)
		if err == nil {
			return ForwardProtocolKindNotSpecified, int(portNumber), nil
		}
		return ForwardProtocolKindInvalid, 0, err
	}
	if len(partList) == 2 {
		var protocolStr = partList[0]
		var protocolKind = ForwardProtocolKindInvalid
		switch protocolStr {
		case "tcp":
			protocolKind = ForwardProtocolKindTCP
		case "udp":
			protocolKind = ForwardProtocolKindUDP
		}
		portNumber, err := strconv.ParseInt(partList[1], 10, 32)
		if err == nil {
			return protocolKind, int(portNumber), nil
		}
		return protocolKind, 0, err
	}
	return ForwardProtocolKindInvalid, 0, fmt.Errorf("unknown port str: %s", portStr)
}
