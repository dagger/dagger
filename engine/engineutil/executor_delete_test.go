package engineutil

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	runc "github.com/containerd/go-runc"
	"github.com/stretchr/testify/require"
)

var errBusyCgroup = errors.New("unable to remove container's cgroup: rmdir /sys/fs/cgroup/buildkit/test/init: device or resource busy")

func TestDeleteContainerUnifiedDrain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		root := t.TempDir()
		write := func(dir, file, value string) error {
			return os.WriteFile(filepath.Join(dir, file), []byte(value), 0600)
		}
		read := func(dir, file string) (string, error) {
			b, err := os.ReadFile(filepath.Join(dir, file))
			return string(b), err
		}
		require.NoError(t, write(root, "cgroup.procs", ""))
		require.NoError(t, write(root, "cgroup.events", "populated 1\nfrozen 0\n"))
		calls := 0
		result := make(chan error, 1)
		go func() {
			result <- deleteContainer(t.Context(), "test", true, func(_ context.Context, _ string, opts *runc.DeleteOpts) error {
				calls++
				if calls == 1 {
					return errBusyCgroup
				}
				if !opts.Force {
					return errors.New("retry must be forced")
				}
				events, err := read(root, "cgroup.events")
				if err != nil {
					return err
				}
				if events != "populated 0\n" {
					return fmt.Errorf("delete before descendant drain: %q", events)
				}
				return nil
			}, func(ctx context.Context) error {
				return killAndDrainCgroup(ctx, root, func(dir, file, value string) error {
					if err := write(dir, file, value); err != nil {
						return err
					}
					// The fake kernel still has a late descendant after the recursive
					// kill request. Empty root procs must not permit an early delete.
					child := filepath.Join(root, "init")
					if err := os.Mkdir(child, 0700); err != nil {
						return err
					}
					return write(child, "cgroup.procs", "123\n")
				}, read)
			})
		}()
		synctest.Wait()
		require.Equal(t, 1, calls, "delete must wait for the late child to drain")
		kill, err := read(root, "cgroup.kill")
		require.NoError(t, err)
		require.Equal(t, "1", kill)
		require.NoError(t, write(filepath.Join(root, "init"), "cgroup.procs", ""))
		require.NoError(t, write(root, "cgroup.events", "populated 0\n"))
		require.NoError(t, <-result)
		require.Equal(t, 2, calls)
	})
}

func TestDeleteContainerRecoveryLimits(t *testing.T) {
	for _, tc := range []struct {
		name    string
		unified bool
		first   error
	}{
		{"normal delete", true, nil},
		{"unrelated failure", true, errors.New("runtime unavailable")},
		{"legacy or hybrid", false, errBusyCgroup},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			err := deleteContainer(t.Context(), "test", tc.unified, func(ctx context.Context, _ string, opts *runc.DeleteOpts) error {
				calls++
				require.False(t, opts.Force)
				require.NoError(t, ctx.Err())
				return tc.first
			}, func(context.Context) error { t.Fatal("unexpected recovery"); return nil })
			require.True(t, err == tc.first, "ordinary or unsupported deletion must preserve the original error")
			require.Equal(t, 1, calls)
		})
	}
	for _, mode := range []string{"kill error", "events read error", "invalid events", "never drains", "retry still busy"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cause := errors.New(mode)
				calls := 0
				started := time.Now()
				err := deleteContainer(t.Context(), "test", true, func(ctx context.Context, _ string, opts *runc.DeleteOpts) error {
					calls++
					if calls == 1 {
						return errBusyCgroup
					}
					require.True(t, opts.Force)
					require.NoError(t, ctx.Err())
					return cause
				}, func(ctx context.Context) error {
					return killAndDrainCgroup(ctx, "unused", func(string, string, string) error {
						if mode == "kill error" {
							return cause
						}
						return nil
					}, func(string, string) (string, error) {
						switch mode {
						case "events read error":
							return "", cause
						case "invalid events":
							return "frozen 0\n", nil
						case "never drains":
							return "populated 1\n", nil
						default:
							return "populated 0\n", nil
						}
					})
				})
				require.ErrorIs(t, err, errBusyCgroup)
				if mode == "never drains" {
					require.ErrorIs(t, err, context.DeadlineExceeded)
					require.Equal(t, containerDeleteRecoveryTimeout, time.Since(started))
				}
				if mode == "kill error" || mode == "events read error" || mode == "retry still busy" {
					require.ErrorIs(t, err, cause)
				}
				if mode == "retry still busy" {
					require.Equal(t, 2, calls)
				} else {
					require.Equal(t, 1, calls)
				}
			})
		})
	}
}
