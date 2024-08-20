//go:build !windows
// +build !windows

package adb

import (
	stderrors "errors"
	"os"
	"os/exec"
)

var localFilesystem = &filesystem{
	LookPath: exec.LookPath,
	IsExecutableFile: func(path string) error {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return stderrors.New("not a regular file")
		}
		return isExecutable(path)
	},
	CmdCombinedOutput: func(name string, arg ...string) ([]byte, error) {
		return exec.Command(name, arg...).CombinedOutput()
	},
}
