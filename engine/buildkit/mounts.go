package buildkit

import (
	"context"
	"errors"

	"github.com/containerd/containerd/mount"
	"github.com/docker/docker/pkg/idtools"
	"github.com/moby/buildkit/executor"
)

type cacheVolumeMount struct {
	hostSrcPath string
	ctrDestPath string
	readonly    bool
}

var _ executor.Mountable = (*cacheVolumeMount)(nil)

func (m cacheVolumeMount) ctdMnts() []mount.Mount {
	options := []string{"bind"}
	if m.readonly {
		options = append(options, "ro")
	}
	return []mount.Mount{{
		Type:    "bind",
		Source:  m.hostSrcPath,
		Target:  m.ctrDestPath,
		Options: options,
	}}
}

func (m cacheVolumeMount) Mount(_ context.Context, readonly bool) (executor.MountableRef, error) {
	return cacheVolumeMountRef(m), nil
}

type cacheVolumeMountRef cacheVolumeMount

var _ executor.MountableRef = (*cacheVolumeMountRef)(nil)

func (m cacheVolumeMountRef) Mount() ([]mount.Mount, func() error, error) {
	// TODO: double check we don't need a cleanup callback here
	return cacheVolumeMount(m).ctdMnts(), func() error { return nil }, nil
}

func (m cacheVolumeMountRef) IdentityMapping() *idtools.IdentityMapping {
	return nil
}

// *technically* read-only is an arg to Mount, so this is a bit redundant, but we want
// to make use of mounts that must be read-only extremely explicit since accidentally
// mounting them read-write can be a fairly devastating security issue.
type readOnlyHostBindMount struct {
	srcPath string
}

var _ executor.Mountable = (*readOnlyHostBindMount)(nil)

func (m readOnlyHostBindMount) Mount(_ context.Context, readonly bool) (executor.MountableRef, error) {
	if !readonly {
		return nil, errors.New("readonly host bind mounts must be readonly")
	}
	return readOnlyHostBindMountRef(m), nil
}

type readOnlyHostBindMountRef readOnlyHostBindMount

var _ executor.MountableRef = (*readOnlyHostBindMountRef)(nil)

func (m readOnlyHostBindMountRef) Mount() ([]mount.Mount, func() error, error) {
	return []mount.Mount{{
		Type:    "bind",
		Source:  m.srcPath,
		Options: []string{"ro", "rbind"},
	}}, func() error { return nil }, nil
}

func (m readOnlyHostBindMountRef) IdentityMapping() *idtools.IdentityMapping {
	return nil
}
