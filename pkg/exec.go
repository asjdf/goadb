package pkg

import (
	"bufio"
	"fmt"
	"github.com/pkg/errors"
	"io"
	"os"
	"os/exec"
)

type CommandExecuteArgs struct {
	Name    string
	ArgList []string
	EnvMap  map[string]string
}

func (a CommandExecuteArgs) BuildExecCommand() *exec.Cmd {
	cmd := exec.Command(a.Name, a.ArgList...)
	var envStrList = make([]string, 0)
	if a.EnvMap != nil {
		for k, v := range a.EnvMap {
			var envStr = fmt.Sprintf("%s=%s", k, v)
			envStrList = append(envStrList, envStr)
		}
		cmd.Env = envStrList
	}
	return cmd
}

type ProcessHolder struct {
	ProcessCmd *exec.Cmd
	Stdin      io.WriteCloser
	Stdout     io.ReadCloser
	Stderr     io.ReadCloser
}

func (p *ProcessHolder) RedirectOutputAsync(logPrefix string) error {
	// 转发stdout
	go func() {
		reader := bufio.NewReader(p.Stdout)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				break
			}
			fmt.Fprintf(os.Stdout, "%s %s", logPrefix, line)
			// 也可以保存到你的缓冲区
		}
	}()

	// 转发stderr
	go func() {
		reader := bufio.NewReader(p.Stderr)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				break
			}
			fmt.Fprintf(os.Stderr, "%s %s", logPrefix, line)
		}
	}()
	return nil
}

func NewStreamHolderFromExecCommand(cmd *exec.Cmd) (*ProcessHolder, error) {
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, errors.Wrap(err, "failed to create stdin pipe")
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, errors.Wrap(err, "failed to create stdout pipe")
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, errors.Wrap(err, "failed to create stderr pipe")
	}
	var streamHolder = ProcessHolder{
		ProcessCmd: cmd,
		Stdin:      stdin,
		Stdout:     stdout,
		Stderr:     stderr,
	}
	return &streamHolder, nil
}
