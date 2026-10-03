//go:build unix

package daggercmd

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// startBackground starts this executable with args in the background, in a
// new process session, with its output going to a log file. It returns the
// read end of the copy's status pipe and the log file's path.
func startBackground(args []string) (*os.File, string, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, "", err
	}
	if err := os.MkdirAll(backgroundLogDir(), 0o700); err != nil {
		return nil, "", err
	}
	logFile, err := os.CreateTemp(backgroundLogDir(), "starting-*.log")
	if err != nil {
		return nil, "", err
	}
	defer logFile.Close()
	status, statusW, err := os.Pipe()
	if err != nil {
		return nil, "", err
	}
	defer statusW.Close()

	cmd := exec.Command(exe, args...)
	cmd.Dir = invocationDir
	cmd.Env = append(os.Environ(), backgroundEnv+"=1")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.ExtraFiles = []*os.File{statusW}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		status.Close()
		os.Remove(logFile.Name())
		return nil, "", fmt.Errorf("start background command: %w", err)
	}
	// The log is named after the background process, which removes it when
	// it succeeds.
	logPath := backgroundLogPath(cmd.Process.Pid)
	if err := os.Rename(logFile.Name(), logPath); err != nil {
		logPath = logFile.Name()
	}
	_ = cmd.Process.Release()
	return status, logPath, nil
}
