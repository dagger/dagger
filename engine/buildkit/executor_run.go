//go:build !darwin && !windows

package buildkit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	runc "github.com/containerd/go-runc"
	"github.com/dagger/dagger/engine/buildkit/resources"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/internal/buildkit/util/bklog"
	"github.com/dagger/dagger/util/cleanups"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/sourcegraph/conc/pool"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sys/unix"

	"dagger.io/dagger/telemetry"
)

//nolint:gocyclo
func (w *Worker) runContainer(ctx context.Context, state *execState) (rerr error) {
	bundle := filepath.Join(w.executorRoot, state.id)
	if err := os.Mkdir(bundle, 0o711); err != nil {
		return err
	}
	state.cleanups.Add("remove bundle", func() error {
		return os.RemoveAll(bundle)
	})

	configPath := filepath.Join(bundle, "config.json")
	f, err := os.Create(configPath)
	if err != nil {
		return err
	}
	defer f.Close()

	if err := json.NewEncoder(f).Encode(state.spec); err != nil {
		return fmt.Errorf("failed to encode spec: %w", err)
	}
	f.Close()

	lg := bklog.G(ctx).
		WithField("id", state.id).
		WithField("args", state.spec.Process.Args)
	if w.execMD != nil {
		if w.execMD.CallID != nil {
			lg = lg.WithField("call_id", w.execMD.CallID.Digest())
		}
		if w.execMD.CallerClientID != "" {
			lg = lg.WithField("caller_client_id", w.execMD.CallerClientID)
		}
		if w.execMD.ClientID != "" {
			lg = lg.WithField("nested_client_id", w.execMD.ClientID)
		}
	}
	lg.Info("starting container")
	defer func() {
		lg.WithError(rerr).Info("container done")
	}()

	trace.SpanFromContext(ctx).AddEvent("Container created")

	state.cleanups.Add("runc delete container", func() error {
		return w.runc.Delete(context.WithoutCancel(ctx), state.id, &runc.DeleteOpts{})
	})

	cgroupPath := state.spec.Linux.CgroupsPath
	if cgroupPath != "" && w.execMD != nil && w.execMD.CallID != nil {
		meter := telemetry.Meter(ctx, InstrumentationLibrary)

		commonAttrs := []attribute.KeyValue{
			attribute.String(telemetry.DagDigestAttr, string(w.execMD.CallID.Digest())),
		}
		spanContext := trace.SpanContextFromContext(ctx)
		if spanContext.HasSpanID() {
			commonAttrs = append(commonAttrs,
				attribute.String(telemetry.MetricsSpanIDAttr, spanContext.SpanID().String()),
			)
		}
		if spanContext.HasTraceID() {
			commonAttrs = append(commonAttrs,
				attribute.String(telemetry.MetricsTraceIDAttr, spanContext.TraceID().String()),
			)
		}

		cgroupSampler, err := resources.NewSampler(cgroupPath, state.networkNamespace, meter, attribute.NewSet(commonAttrs...))
		if err != nil {
			return fmt.Errorf("create cgroup sampler: %w", err)
		}

		cgroupSamplerCtx, cgroupSamplerCancel := context.WithCancelCause(context.WithoutCancel(ctx))
		cgroupSamplerPool := pool.New()

		state.cleanups.Add("cancel cgroup sampler", cleanups.Infallible(func() {
			cgroupSamplerCancel(fmt.Errorf("container cleanup: %w", context.Canceled))
			cgroupSamplerPool.Wait()
		}))

		cgroupSamplerPool.Go(func() {
			ticker := time.NewTicker(cgroupSampleInterval)
			defer ticker.Stop()

			for {
				select {
				case <-cgroupSamplerCtx.Done():
					// try a quick final sample before closing
					finalCtx, finalCancel := context.WithTimeout(context.WithoutCancel(cgroupSamplerCtx), finalCgroupSampleTimeout)
					defer finalCancel()
					if err := cgroupSampler.Sample(finalCtx); err != nil {
						bklog.G(ctx).Error("failed to sample cgroup after cancel", "err", err)
					}

					return
				case <-ticker.C:
					if err := cgroupSampler.Sample(cgroupSamplerCtx); err != nil {
						bklog.G(ctx).Error("failed to sample cgroup", "err", err)
					}
				}
			}
		})
	}

	startedCallback := func() {
		state.startedOnce.Do(func() {
			trace.SpanFromContext(ctx).AddEvent("Container started")
			if state.startedCh != nil {
				close(state.startedCh)
			}
		})
	}

	killer := newRunProcKiller(w.runc, state.id)

	runcCall := func(ctx context.Context, started chan<- int, io runc.IO, pidfile string) error {
		/*
			We need to avoid the following type of race condition, which can result in invalid overlapping overlay mounts:
			1. Engine creates random (unrelated to this exec) overlay mount like upperdir=B,lowerdir=A
			2. We hit this code and start runc, which gets to the point where it has unshared its mount namespace but
			   not yet pivot_root'd. In this state, since mount namespaces are forks of their parent, the overlay mount
				 from (1) is visible in the runc processes mount namespace.
			3. Engine unmounts the overlay from (1), but that does NOT unmount it from the runc process's mount namespace
			4. Engine creates a new overlay mount like upperdir=C,lowerdir=B:A (i.e. upperdir from (1) is now lowerdir).
				 This mount is invalid and technically hitting "undefined behavior" since B is still an upperdir in mounts
				 that exist on the system.

			We avoid this by starting the runc process in a clean mount namespace that was created during engine init before
			any mounts existed, guaranteeing none are leaked into it. We setns to that clean mount namespace and then unshare
			again to guarantee the namespace for the runc process is fully isolated. OpenTree+MoveMount are then used to
			bind the mounts actually needed by the container into that isolated namespace so that runc can see them.
		*/
		rootfsFD, err := unix.OpenTree(unix.AT_FDCWD, state.rootfsPath, unix.OPEN_TREE_CLONE|unix.OPEN_TREE_CLOEXEC|unix.AT_RECURSIVE)
		var isOldKernel bool
		switch {
		case errors.Is(err, unix.ENOSYS):
			// truly ancient kernels like 4.14 are still used in places like AWS CodeBuild, so just accept the problem with overlay leaks
			// in those obscure cases rather than erroring out.
			isOldKernel = true
		case err != nil:
			return fmt.Errorf("open rootfs path %s: %w", state.rootfsPath, err)
		}

		var rootfsFile *os.File
		var nsPath string
		var nsPathFile *os.File
		if !isOldKernel {
			rootfsFile = os.NewFile(uintptr(rootfsFD), "rootfs")
			defer rootfsFile.Close()

			// CNI network namespaces are actually bind mounts of the namespace file, so we gotta move this into the mount ns for runc too
			if state.networkNamespace != nil {
				var tmpSpec specs.Spec
				if err := state.networkNamespace.Set(&tmpSpec); err != nil {
					return fmt.Errorf("set network namespace: %w", err)
				}
				if tmpSpec.Linux != nil {
					for _, ns := range tmpSpec.Linux.Namespaces {
						if ns.Type == specs.NetworkNamespace {
							nsPath = ns.Path
							break
						}
					}
				}
			}
			if nsPath != "" {
				nsPathFD, err := unix.OpenTree(unix.AT_FDCWD, nsPath, unix.OPEN_TREE_CLONE|unix.OPEN_TREE_CLOEXEC)
				if err != nil {
					return fmt.Errorf("open network namespace path %s: %w", nsPath, err)
				}
				nsPathFile = os.NewFile(uintptr(nsPathFD), "netns")
				defer nsPathFile.Close()
			}
		}

		var eg errgroup.Group
		eg.Go(func() error {
			if !isOldKernel {
				runtime.LockOSThread()

				// gotta CLONE_FS first to avoid EINVAL when setns'ing to another mount namespace
				if err := unix.Unshare(unix.CLONE_FS); err != nil {
					return fmt.Errorf("unshare fs attrs: %w", err)
				}
				// switch to the clean mount namespace free of leaks from other unrelated engine mounts
				if err := unix.Setns(int(w.cleanMntNS.Fd()), unix.CLONE_NEWNS); err != nil {
					return fmt.Errorf("setns clean mount namespace: %w", err)
				}
				// do a final unshare, forking from the clean mount namespace to get a final fully isolated mount namespace for runc
				if err := unix.Unshare(unix.CLONE_NEWNS); err != nil {
					return fmt.Errorf("unshare new mount namespace: %w", err)
				}

				defer func() {
					// best effort try to setns back to the host mount namespace so the go runtime can re-use this thread rather than
					// burning it off
					err := unix.Setns(int(w.hostMntNS.Fd()), unix.CLONE_NEWNS)
					if err != nil {
						slog.Error("failed to setns host mount namespace after container run", "err", err)
					} else {
						runtime.UnlockOSThread()
					}
				}()

				if err := unix.MoveMount(int(rootfsFile.Fd()), "", unix.AT_FDCWD, state.rootfsPath, unix.MOVE_MOUNT_F_EMPTY_PATH); err != nil {
					return fmt.Errorf("move mount rootfs %s: %w", state.rootfsPath, err)
				}
				rootfsFile.Close()

				if nsPathFile != nil {
					if err := unix.MoveMount(int(nsPathFile.Fd()), "", unix.AT_FDCWD, nsPath, unix.MOVE_MOUNT_F_EMPTY_PATH); err != nil {
						return fmt.Errorf("move mount network namespace %s: %w", nsPath, err)
					}
					nsPathFile.Close()
				}
			}

			_, err = w.runc.Run(ctx, state.id, bundle, &runc.CreateOpts{
				Started:   started,
				IO:        io,
				ExtraArgs: []string{"--keep"},
			})
			return err
		})
		return eg.Wait()
	}

	return exitError(ctx, state.exitCodePath, w.callWithIO(ctx, state.procInfo, startedCallback, killer, runcCall), state.procInfo.Meta.ValidExitCodes)
}
