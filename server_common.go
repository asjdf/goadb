//go:build !windows
// +build !windows

package adb

import (
	stderrors "errors"
	"github.com/asjdf/goadb/pkg"
	"io/fs"
	"os"
	"os/exec"
)

var localFilesystem = &filesystem{
	LookPath: exec.LookPath,
	ListFileNonRecursive: func(dir string) ([]os.FileInfo, error) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}

		fileInfoList := make([]fs.FileInfo, len(entries))
		for i, entry := range entries {
			fileInfoList[i], _ = entry.Info()
		}
		return fileInfoList, nil
	},
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
	CmdWithStream: func(args pkg.CommandExecuteArgs) (processHolder *pkg.ProcessHolder, err error) {
		var cmd = args.BuildExecCommand()
		processHolder, err = pkg.NewStreamHolderFromExecCommand(cmd)
		if err != nil {
			return processHolder, errors.Wrap(err, "failed to create stream holder")
		}
		if err := processHolder.ProcessCmd.Start(); err != nil {
			return nil, errors.Wrap(err, "failed to start process")
		}
		return processHolder, nil
	},
	CmdCombinedOutput: func(args pkg.CommandExecuteArgs) ([]byte, error) {
		var cmd = args.BuildExecCommand()
		return cmd.CombinedOutput()
	},
}
