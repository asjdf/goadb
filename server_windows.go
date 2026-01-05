package adb

import (
	stderrors "errors"
	"github.com/pkg/errors"
	"io/fs"
	"os"
	"os/exec"
	"syscall"

	"github.com/asjdf/goadb/pkg"
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
	ListFileNonRecursive: func(dir string) ([]fs.FileInfo, error) {
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
	CmdWithStream: func(args pkg.CommandExecuteArgs) (processHolder *pkg.ProcessHolder, err error) {
		var cmd = args.BuildExecCommand()
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
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
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		return cmd.CombinedOutput()
	},
}
