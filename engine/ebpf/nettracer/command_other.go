//go:build !linux || (!386 && !amd64 && !arm64)

package nettracer

import (
	"context"
	"os/exec"
)

type Command struct{}

func (*Command) CgroupPath() string { return "" }

func PrepareCommand(*exec.Cmd) (*Command, error)  { return nil, errUnsupported }
func InitCommandPlacement() (func() error, error) { return nil, errUnsupported }
func (*Command) Sample() (Sample, error)          { return Sample{}, errUnsupported }
func (*Command) Close() error                     { return nil }
func (*Command) WaitEmpty(context.Context) error  { return errUnsupported }
