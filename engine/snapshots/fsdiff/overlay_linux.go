//go:build linux
// +build linux

package fsdiff

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/pkg/archive"
	"github.com/containerd/continuity/devices"
	continuityfs "github.com/containerd/continuity/fs"
	"github.com/containerd/continuity/sysx"
	"github.com/pkg/errors"
	"golang.org/x/sys/unix"
)

func GetUpperdir(lower, upper []mount.Mount) (string, error) {
	var upperdir string
	if len(lower) == 0 && len(upper) == 1 {
		upperM := upper[0]
		if upperM.Type != "bind" {
			return "", errors.Errorf("bottommost upper must be bind mount but %q", upperM.Type)
		}
		upperdir = upperM.Source
	} else if len(lower) == 1 && len(upper) == 1 {
		var lowerlayers []string
		lowerM := lower[0]
		if lowerM.Type == "bind" {
			lowerlayers = []string{lowerM.Source}
		} else if IsOverlayMountType(lowerM) {
			var err error
			lowerlayers, err = GetOverlayLayers(lowerM)
			if err != nil {
				return "", err
			}
		} else {
			return "", errors.Errorf("cannot get layer information from mount option (type = %q)", lowerM.Type)
		}

		upperM := upper[0]
		if !IsOverlayMountType(upperM) {
			return "", errors.Errorf("upper snapshot isn't overlay mounted (type = %q)", upperM.Type)
		}
		upperlayers, err := GetOverlayLayers(upperM)
		if err != nil {
			return "", err
		}

		if len(upperlayers) != len(lowerlayers)+1 {
			return "", errors.Errorf("cannot determine diff of more than one upper directories")
		}
		for i := 0; i < len(lowerlayers); i++ {
			if upperlayers[i] != lowerlayers[i] {
				return "", errors.Errorf("layer %d must be common between upper and lower snapshots", i)
			}
		}
		upperdir = upperlayers[len(upperlayers)-1]
	} else {
		return "", errors.Errorf("multiple mount configurations are not supported")
	}
	if upperdir == "" {
		return "", errors.Errorf("cannot determine upperdir from mount option")
	}
	return upperdir, nil
}

func GetOverlayLayers(m mount.Mount) ([]string, error) {
	var u string
	var uFound bool
	var l []string
	for _, o := range m.Options {
		if strings.HasPrefix(o, "upperdir=") {
			u, uFound = strings.TrimPrefix(o, "upperdir="), true
		} else if strings.HasPrefix(o, "lowerdir=") {
			l = strings.Split(strings.TrimPrefix(o, "lowerdir="), ":")
			for i, j := 0, len(l)-1; i < j; i, j = i+1, j-1 {
				l[i], l[j] = l[j], l[i]
			}
		} else if strings.HasPrefix(o, "workdir=") || o == "index=off" || o == "userxattr" || strings.HasPrefix(o, "redirect_dir=") || o == "volatile" {
			continue
		} else {
			return nil, errors.Errorf("unknown option %q specified by snapshotter", o)
		}
	}
	if uFound {
		return append(l, u), nil
	}
	return l, nil
}

func WriteUpperdir(ctx context.Context, w io.Writer, upperdir string, lower []mount.Mount) error {
	emptyLower, err := os.MkdirTemp("", "buildkit")
	if err != nil {
		return errors.Wrapf(err, "failed to create temp dir")
	}
	defer os.Remove(emptyLower)
	upperView := []mount.Mount{{
		Type:    "overlay",
		Source:  "overlay",
		Options: []string{fmt.Sprintf("lowerdir=%s", strings.Join([]string{upperdir, emptyLower}, ":"))},
	}}
	return mount.WithTempMount(ctx, lower, func(lowerRoot string) error {
		return mount.WithTempMount(ctx, upperView, func(upperViewRoot string) error {
			cw := archive.NewChangeWriter(&cancellableWriter{ctx, w}, upperViewRoot)
			if err := WalkUpperdirChanges(ctx, cw.HandleChange, upperdir, upperViewRoot, lowerRoot, CompareCompat); err != nil {
				if err2 := cw.Close(); err2 != nil {
					return errors.Wrapf(err, "failed to record upperdir changes (close error: %v)", err2)
				}
				return errors.Wrapf(err, "failed to record upperdir changes")
			}
			return cw.Close()
		})
	})
}

type cancellableWriter struct {
	ctx context.Context
	w   io.Writer
}

func (w *cancellableWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	return w.w.Write(p)
}

func WalkUpperdirChanges(
	ctx context.Context,
	changeFn continuityfs.ChangeFunc,
	upperdir, upperdirView, base string,
	comparison Comparison,
) error {
	return filepath.Walk(upperdir, func(path string, f os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		default:
		}

		path, err = filepath.Rel(upperdir, path)
		if err != nil {
			return err
		}
		path = filepath.Join(string(os.PathSeparator), path)
		if path == string(os.PathSeparator) {
			return nil
		}

		if redirect, err := checkRedirect(upperdir, path, f); err != nil {
			return err
		} else if redirect {
			return errors.New("redirect_dir is used but it's not supported in overlayfs differ")
		}

		isDelete, skip, err := checkDelete(path, base, f)
		if err != nil {
			return err
		} else if skip {
			return nil
		}

		var kind continuityfs.ChangeKind
		var skipRecord bool
		if isDelete {
			kind = continuityfs.ChangeKindDelete
		} else if baseF, err := os.Lstat(filepath.Join(base, path)); err == nil {
			kind = continuityfs.ChangeKindModify
			if same, err := sameDirent(baseF, f, filepath.Join(base, path), filepath.Join(upperdirView, path), comparison); same {
				skipRecord = true
			} else if err != nil {
				return err
			}
		} else if os.IsNotExist(err) || errors.Is(err, unix.ENOTDIR) {
			kind = continuityfs.ChangeKindAdd
		} else {
			return errors.Wrap(err, "failed to stat base file during overlay diff")
		}

		if !skipRecord {
			if err := changeFn(kind, path, f, nil); err != nil {
				return err
			}
		}

		if f != nil {
			if isOpaque, err := checkOpaque(upperdir, path, base, f); err != nil {
				return err
			} else if isOpaque {
				if err := WalkChanges(ctx, filepath.Join(base, path), filepath.Join(upperdirView, path), comparison, func(k continuityfs.ChangeKind, p string, f os.FileInfo, err error) error {
					return changeFn(k, filepath.Join(path, p), f, err)
				}); err != nil {
					return err
				}
				return filepath.SkipDir
			}
		}
		return nil
	})
}

func checkDelete(path string, base string, f os.FileInfo) (delete, skip bool, _ error) {
	if f.Mode()&os.ModeCharDevice != 0 {
		if _, ok := f.Sys().(*syscall.Stat_t); ok {
			maj, min, err := devices.DeviceInfo(f)
			if err != nil {
				return false, false, errors.Wrapf(err, "failed to get device info")
			}
			if maj == 0 && min == 0 {
				if _, err := os.Lstat(filepath.Join(base, path)); err != nil {
					if !os.IsNotExist(err) {
						return false, false, errors.Wrapf(err, "failed to lstat")
					}
					return false, true, nil
				}
				return true, false, nil
			}
		}
	}
	return false, false, nil
}

func checkOpaque(upperdir string, path string, base string, f os.FileInfo) (isOpaque bool, _ error) {
	if f.IsDir() {
		for _, oKey := range []string{"trusted.overlay.opaque", "user.overlay.opaque"} {
			opaque, err := sysx.LGetxattr(filepath.Join(upperdir, path), oKey)
			if err != nil && err != unix.ENODATA {
				return false, errors.Wrapf(err, "failed to retrieve %s attr", oKey)
			} else if len(opaque) == 1 && opaque[0] == 'y' {
				if _, err := os.Lstat(filepath.Join(base, path)); err != nil {
					if !os.IsNotExist(err) {
						return false, errors.Wrapf(err, "failed to lstat")
					}
					return false, nil
				}
				return true, nil
			}
		}
	}
	return false, nil
}

func checkRedirect(upperdir string, path string, f os.FileInfo) (bool, error) {
	if f.IsDir() {
		rKey := "trusted.overlay.redirect"
		redirect, err := sysx.LGetxattr(filepath.Join(upperdir, path), rKey)
		if err != nil && err != unix.ENODATA {
			return false, errors.Wrapf(err, "failed to retrieve %s attr", rKey)
		}
		return len(redirect) > 0, nil
	}
	return false, nil
}

func sameDirent(f1, f2 os.FileInfo, f1fullPath, f2fullPath string, comparison Comparison) (bool, error) {
	return samePathInfo(f1, f2, f1fullPath, f2fullPath, comparison)
}
