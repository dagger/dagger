//go:build !linux

package nettracer

import (
	"context"
	"os/exec"
)

type Command struct{}

func PrepareCommand(*exec.Cmd) (*Command, error) { return nil, errUnsupported }
func (*Command) Sample() (Sample, error)         { return Sample{}, errUnsupported }
func (*Command) Close() error                    { return nil }
func (*Command) WaitEmpty(context.Context) error { return errUnsupported }
