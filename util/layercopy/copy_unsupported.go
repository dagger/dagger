//go:build !linux

package layercopy

import (
	"context"
	"errors"

	"github.com/containerd/containerd/v2/core/snapshots"
)

type destination struct{}

func NewCopier(Mount) (*Copier, error) {
	return nil, errors.New("layercopy is only implemented on linux")
}

func (c *Copier) Copy(context.Context, Mount, string, string, CopyOptions) error {
	return errors.New("layercopy is only implemented on linux")
}

func (c *Copier) CopyFile(context.Context, Mount, string, string, CopyOptions) error {
	return errors.New("layercopy is only implemented on linux")
}

func (c *Copier) Mkdir(context.Context, string, CopyOptions) error {
	return errors.New("layercopy is only implemented on linux")
}

func (c *Copier) MaterializeDestDir(context.Context, string) (string, error) {
	return "", errors.New("layercopy is only implemented on linux")
}

func (c *Copier) Close() error {
	return nil
}

func (c *Copier) Usage() (snapshots.Usage, error) {
	return snapshots.Usage{}, errors.New("layercopy is only implemented on linux")
}
