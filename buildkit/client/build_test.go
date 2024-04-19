package client

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/gateway/client"
	gatewayapi "github.com/moby/buildkit/frontend/gateway/pb"
	"github.com/moby/buildkit/identity"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/secrets/secretsprovider"
	"github.com/moby/buildkit/session/sshforward/sshprovider"
	"github.com/moby/buildkit/solver/errdefs"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/util/entitlements"
	"github.com/moby/buildkit/util/grpcerrors"
	utilsystem "github.com/moby/buildkit/util/system"
	"github.com/moby/buildkit/util/testutil/echoserver"
	"github.com/moby/buildkit/util/testutil/integration"
	"github.com/moby/buildkit/util/testutil/workers"
	digest "github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
	"github.com/tonistiigi/fsutil"
	"golang.org/x/crypto/ssh/agent"
	"google.golang.org/grpc/codes"
)

func TestClientGatewayIntegration(t *testing.T) {
	integration.Run(t, integration.TestFuncs(
		testClientGatewaySolve,
		testClientGatewayFailedSolve,
		testClientGatewayEmptySolve,
		testNoBuildID,
		testUnknownBuildID,
		testClientGatewayContainerExecPipe,
		testClientGatewayContainerCancelOnRelease,
		testClientGatewayContainerPID1Fail,
		testClientGatewayContainerPID1Exit,
		testClientGatewayContainerMounts,
		testClientGatewayContainerSecretEnv,
		testClientGatewayContainerPID1Tty,
		testClientGatewayContainerCancelPID1Tty,
		testClientGatewayContainerExecTty,
		testClientGatewayContainerCancelExecTty,
		testClientSlowCacheRootfsRef,
		testClientGatewayContainerPlatformPATH,
		testClientGatewayExecError,
		testClientGatewaySlowCacheExecError,
		testClientGatewayExecFileActionError,
		testClientGatewayContainerExtraHosts,
		testClientGatewayContainerSignal,
		testWarnings,
		testClientGatewayNilResult,
		testClientGatewayEmptyImageExec,
	), integration.WithMirroredImages(integration.OfficialImages("busybox:latest")))

	integration.Run(t, integration.TestFuncs(
		testClientGatewayContainerSecurityModeCaps,
		testClientGatewayContainerSecurityModeValidation,
	), integration.WithMirroredImages(integration.OfficialImages("busybox:latest")),
		integration.WithMatrix("secmode", map[string]interface{}{
			"sandbox":  securitySandbox,
			"insecure": securityInsecure,
		}),
	)

	integration.Run(t, integration.TestFuncs(
		testClientGatewayContainerHostNetworkingAccess,
		testClientGatewayContainerHostNetworkingValidation,
	),
		integration.WithMirroredImages(integration.OfficialImages("busybox:latest")),
		integration.WithMatrix("netmode", map[string]interface{}{
			"default": defaultNetwork,
			"host":    hostNetwork,
		}),
	)
}

func testClientGatewaySolve(t *testing.T, sb integration.Sandbox) {
	requiresLinux(t)

	ctx := sb.Context()

	c, err := New(ctx, sb.Address())
	require.NoError(t, err)
	defer c.Close()

	product := "buildkit_test"
	optKey := "test-string"

	b := func(ctx context.Context, c client.Client) (*client.Result, error) {
		if c.BuildOpts().Product != product {
			return nil, errors.Errorf("expected product %q, got %q", product, c.BuildOpts().Product)
		}
		opts := c.BuildOpts().Opts
		testStr, ok := opts[optKey]
		if !ok {
			return nil, errors.Errorf(`build option %q missing`, optKey)
		}

		run := llb.Image("busybox:latest").Run(
			llb.ReadonlyRootFS(),
			llb.Args([]string{"/bin/sh", "-ec", `echo -n '` + testStr + `' > /out/foo`}),
		)
		st := run.AddMount("/out", llb.Scratch())

		def, err := st.Marshal(ctx)
		if err != nil {
			return nil, errors.Wrap(err, "failed to marshal state")
		}

		r, err := c.Solve(ctx, client.SolveRequest{
			Definition: def.ToPB(),
		})
		if err != nil {
			return nil, errors.Wrap(err, "failed to solve")
		}

		read, err := r.Ref.ReadFile(ctx, client.ReadRequest{
			Filename: "/foo",
		})
		if err != nil {
			return nil, errors.Wrap(err, "failed to read result")
		}
		if testStr != string(read) {
			return nil, errors.Errorf("read back %q, expected %q", string(read), testStr)
		}
		return r, nil
	}

	tmpdir := t.TempDir()

	testStr := "This is a test"

	_, err = c.Build(ctx, SolveOpt{
		Exports: []ExportEntry{
			{
				Type:      ExporterLocal,
				OutputDir: tmpdir,
			},
		},
		FrontendAttrs: map[string]string{
			optKey: testStr,
		},
	}, product, b, nil)
	require.NoError(t, err)

	read, err := os.ReadFile(filepath.Join(tmpdir, "foo"))
	require.NoError(t, err)
	require.Equal(t, testStr, string(read))

	checkAllReleasable(t, c, sb, true)
}

func testWarnings(t *testing.T, sb integration.Sandbox) {
	requiresLinux(t)

	ctx := sb.Context()

	c, err := New(ctx, sb.Address())
	require.NoError(t, err)
	defer c.Close()

	product := "buildkit_test"

	b := func(ctx context.Context, c client.Client) (*client.Result, error) {
		st := llb.Scratch().File(llb.Mkfile("/dummy", 0600, []byte("foo")))

		def, err := st.Marshal(ctx)
		if err != nil {
			return nil, errors.Wrap(err, "failed to marshal state")
		}

		dgst, _, _, _, err := st.Output().Vertex(ctx, def.Constraints).Marshal(ctx, def.Constraints)
		if err != nil {
			return nil, err
		}

		r, err := c.Solve(ctx, client.SolveRequest{
			Definition: def.ToPB(),
		})
		if err != nil {
			return nil, errors.Wrap(err, "failed to solve")
		}

		require.NoError(t, c.Warn(ctx, dgst, "this is warning", client.WarnOpts{
			Level: 3,
			SourceInfo: &pb.SourceInfo{
				Filename: "mydockerfile",
				Data:     []byte("filedata"),
			},
			Range: []*pb.Range{
				{Start: pb.Position{Line: 2}, End: pb.Position{Line: 4}},
			},
			Detail: [][]byte{[]byte("this is detail"), []byte("and more detail")},
			URL:    "https://example.com",
		}))

		return r, nil
	}

	status := make(chan *SolveStatus)
	statusDone := make(chan struct{})
	done := make(chan struct{})

	var warnings []*VertexWarning
	vertexes := map[digest.Digest]struct{}{}

	go func() {
		defer close(statusDone)
		for {
			select {
			case st, ok := <-status:
				if !ok {
					return
				}
				for _, s := range st.Vertexes {
					vertexes[s.Digest] = struct{}{}
				}
				warnings = append(warnings, st.Warnings...)
			case <-done:
				return
			}
		}
	}()

	_, err = c.Build(ctx, SolveOpt{}, product, b, status)
	require.NoError(t, err)

	select {
	case <-statusDone:
	case <-time.After(10 * time.Second):
		close(done)
	}

	<-statusDone

	require.Equal(t, 1, len(vertexes))
	require.Equal(t, 1, len(warnings))

	w := warnings[0]

	require.Equal(t, "this is warning", string(w.Short))
	require.Equal(t, 2, len(w.Detail))
	require.Equal(t, "this is detail", string(w.Detail[0]))
	require.Equal(t, "and more detail", string(w.Detail[1]))
	require.Equal(t, "https://example.com", w.URL)
	require.Equal(t, 3, w.Level)
	_, ok := vertexes[w.Vertex]
	require.True(t, ok)

	require.Equal(t, "mydockerfile", w.SourceInfo.Filename)
	require.Equal(t, "filedata", string(w.SourceInfo.Data))
	require.Equal(t, 1, len(w.Range))
	require.Equal(t, int32(2), w.Range[0].Start.Line)
	require.Equal(t, int32(4), w.Range[0].End.Line)

	checkAllReleasable(t, c, sb, true)
}

func testClientGatewayFailedSolve(t *testing.T, sb integration.Sandbox) {
	requiresLinux(t)

	ctx := sb.Context()

	c, err := New(ctx, sb.Address())
	require.NoError(t, err)
	defer c.Close()

	b := func(ctx context.Context, c client.Client) (*client.Result, error) {
		return nil, errors.New("expected to fail")
	}

	_, err = c.Build(ctx, SolveOpt{}, "", b, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "expected to fail")
}

func testClientGatewayEmptySolve(t *testing.T, sb integration.Sandbox) {
	requiresLinux(t)

	ctx := sb.Context()

	c, err := New(ctx, sb.Address())
	require.NoError(t, err)
	defer c.Close()

	b := func(ctx context.Context, c client.Client) (*client.Result, error) {
		r, err := c.Solve(ctx, client.SolveRequest{})
		if err != nil {
			return nil, errors.Wrap(err, "failed to solve")
		}
		if r.Ref != nil || r.Refs != nil || r.Metadata != nil {
			return nil, errors.Errorf("got unexpected non-empty result %+v", r)
		}
		return r, nil
	}

	_, err = c.Build(ctx, SolveOpt{}, "", b, nil)
	require.NoError(t, err)
}

func testNoBuildID(t *testing.T, sb integration.Sandbox) {
	requiresLinux(t)

	ctx := sb.Context()

	c, err := New(ctx, sb.Address())
	require.NoError(t, err)
	defer c.Close()

	g := gatewayapi.NewLLBBridgeClient(c.conn)
	_, err = g.Ping(ctx, &gatewayapi.PingRequest{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no buildid found in context")
}

func testUnknownBuildID(t *testing.T, sb integration.Sandbox) {
	requiresLinux(t)

	ctx := sb.Context()

	c, err := New(ctx, sb.Address())
	require.NoError(t, err)
	defer c.Close()

	g := c.gatewayClientForBuild(t.Name() + identity.NewID())
	_, err = g.Ping(ctx, &gatewayapi.PingRequest{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no such job")
	require.Equal(t, grpcerrors.Code(err), codes.NotFound)
}

// testClientGatewayContainerCancelOnRelease is testing that all running
// processes are terminated when the container is released.
func testClientGatewayContainerCancelOnRelease(t *testing.T, sb integration.Sandbox) {
	requiresLinux(t)

	ctx := sb.Context()

	c, err := New(ctx, sb.Address())
	require.NoError(t, err)
	defer c.Close()

	product := "buildkit_test"

	b := func(ctx context.Context, c client.Client) (*client.Result, error) {
		st := llb.Image("busybox:latest")

		def, err := st.Marshal(ctx)
		if err != nil {
			return nil, errors.Wrap(err, "failed to marshal state")
		}

		r, err := c.Solve(ctx, client.SolveRequest{
			Definition: def.ToPB(),
		})
		if err != nil {
			return nil, errors.Wrap(err, "failed to solve")
		}

		ctr, err := c.NewContainer(ctx, client.NewContainerRequest{
			Mounts: []client.Mount{{
				Dest:      "/",
				MountType: pb.MountType_BIND,
				Ref:       r.Ref,
			}},
		})
		if err != nil {
			return nil, err
		}

		start := time.Now()
		defer func() {
			// ensure pid1 and pid2 exit from cancel before the 10s sleep
			// exits naturally
			require.WithinDuration(t, start, time.Now(), 10*time.Second)
		}()

		// background pid1 process that starts container
		pid1, err := ctr.Start(ctx, client.StartRequest{
			Args: []string{"sleep", "10"},
		})
		require.NoError(t, err)

		pid2, err := ctr.Start(ctx, client.StartRequest{
			Args: []string{"sleep", "10"},
		})
		require.NoError(t, err)

		ctr.Release(ctx)
		err = pid1.Wait()
		require.Contains(t, err.Error(), context.Canceled.Error())

		err = pid2.Wait()
		require.Contains(t, err.Error(), context.Canceled.Error())

		return &client.Result{}, nil
	}

	_, err = c.Build(ctx, SolveOpt{}, product, b, nil)
	require.NoError(t, err)
	checkAllReleasable(t, c, sb, true)
}

// testClientGatewayContainerExecPipe is testing the ability to pipe multiple
// process together all started via `Exec` into the same container.
// We are mimicing: `echo testing | cat | cat > /tmp/foo && cat /tmp/foo`
func testClientGatewayContainerExecPipe(t *testing.T, sb integration.Sandbox) {
	requiresLinux(t)

	ctx := sb.Context()

	c, err := New(ctx, sb.Address())
	require.NoError(t, err)
	defer c.Close()

	product := "buildkit_test"

	output := bytes.NewBuffer(nil)

	b := func(ctx context.Context, c client.Client) (*client.Result, error) {
		st := llb.Image("busybox:latest")

		def, err := st.Marshal(ctx)
		if err != nil {
			return nil, errors.Wrap(err, "failed to marshal state")
		}

		r, err := c.Solve(ctx, client.SolveRequest{
			Definition: def.ToPB(),
		})
		if err != nil {
			return nil, errors.Wrap(err, "failed to solve")
		}

		ctr, err := c.NewContainer(ctx, client.NewContainerRequest{
			Mounts: []client.Mount{{
				Dest:      "/",
				MountType: pb.MountType_BIND,
				Ref:       r.Ref,
			}},
		})

		if err != nil {
			return nil, err
		}

		// background pid1 process that starts container
		pid1, err := ctr.Start(ctx, client.StartRequest{
			Args: []string{"sleep", "10"},
		})
		if err != nil {
			ctr.Release(ctx)
			return nil, err
		}

		defer func() {
			// cancel pid1
			ctr.Release(ctx)
			pid1.Wait()
		}()

		// first part is `echo testing | cat`
		stdin2 := bytes.NewBuffer([]byte("testing"))
		stdin3, stdout2 := io.Pipe()

		pid2, err := ctr.Start(ctx, client.StartRequest{
			Args:   []string{"cat"},
			Cwd:    "/",
			Tty:    false,
			Stdin:  io.NopCloser(stdin2),
			Stdout: stdout2,
		})

		if err != nil {
			return nil, err
		}

		// next part is: `| cat > /tmp/test`
		pid3, err := ctr.Start(ctx, client.StartRequest{
			Args:  []string{"sh", "-c", "cat > /tmp/test"},
			Stdin: stdin3,
		})
		if err != nil {
			return nil, err
		}

		err = pid2.Wait()
		if err != nil {
			stdout2.Close()
			return nil, err
		}

		err = stdout2.Close()
		if err != nil {
			return nil, err
		}

		err = pid3.Wait()
		if err != nil {
			return nil, err
		}

		err = stdin3.Close()
		if err != nil {
			return nil, err
		}

		pid4, err := ctr.Start(ctx, client.StartRequest{
			Args:   []string{"cat", "/tmp/test"},
			Stdout: &nopCloser{output},
		})
		if err != nil {
			return nil, err
		}

		err = pid4.Wait()
		if err != nil {
			return nil, err
		}

		return &client.Result{}, nil
	}

	_, err = c.Build(ctx, SolveOpt{}, product, b, nil)
	require.NoError(t, err)
	require.Equal(t, "testing", output.String())

	checkAllReleasable(t, c, sb, true)
}

// testClientGatewayContainerPID1Fail is testing clean shutdown and release
// of resources when the primary pid1 exits with non-zero exit status
func testClientGatewayContainerPID1Fail(t *testing.T, sb integration.Sandbox) {
	requiresLinux(t)

	ctx := sb.Context()

	c, err := New(ctx, sb.Address())
	require.NoError(t, err)
	defer c.Close()

	product := "buildkit_test"

	b := func(ctx context.Context, c client.Client) (*client.Result, error) {
		st := llb.Image("busybox:latest")

		def, err := st.Marshal(ctx)
		if err != nil {
			return nil, errors.Wrap(err, "failed to marshal state")
		}

		r, err := c.Solve(ctx, client.SolveRequest{
			Definition: def.ToPB(),
		})
		if err != nil {
			return nil, errors.Wrap(err, "failed to solve")
		}

		ctr, err := c.NewContainer(ctx, client.NewContainerRequest{
			Mounts: []client.Mount{{
				Dest:      "/",
				MountType: pb.MountType_BIND,
				Ref:       r.Ref,
			}},
		})

		if err != nil {
			return nil, err
		}

		pid1, err := ctr.Start(ctx, client.StartRequest{
			Args: []string{"sh", "-c", "exit 99"},
		})
		if err != nil {
			ctr.Release(ctx)
			return nil, err
		}

		defer ctr.Release(ctx)
		err = pid1.Wait()

		var exitError *gatewayapi.ExitError
		require.ErrorAs(t, err, &exitError)
		require.Equal(t, uint32(99), exitError.ExitCode)

		return nil, err
	}

	_, err = c.Build(ctx, SolveOpt{}, product, b, nil)
	require.Error(t, err)

	checkAllReleasable(t, c, sb, true)
}

// testClientGatewayContainerPID1Exit is testing that all process started
// via `Exec` are shutdown when the primary pid1 process exits
func testClientGatewayContainerPID1Exit(t *testing.T, sb integration.Sandbox) {
	requiresLinux(t)

	ctx := sb.Context()

	c, err := New(ctx, sb.Address())
	require.NoError(t, err)
	defer c.Close()

	product := "buildkit_test"

	b := func(ctx context.Context, c client.Client) (*client.Result, error) {
		st := llb.Image("busybox:latest")

		def, err := st.Marshal(ctx)
		if err != nil {
			return nil, errors.Wrap(err, "failed to marshal state")
		}

		r, err := c.Solve(ctx, client.SolveRequest{
			Definition: def.ToPB(),
		})
		if err != nil {
			return nil, errors.Wrap(err, "failed to solve")
		}

		ctr, err := c.NewContainer(ctx, client.NewContainerRequest{
			Mounts: []client.Mount{{
				Dest:      "/",
				MountType: pb.MountType_BIND,
				Ref:       r.Ref,
			}},
		})

		if err != nil {
			return nil, err
		}
		defer ctr.Release(ctx)

		start := time.Now()
		defer func() {
			// ensure pid1 and pid2 exits from cancel before the 10s sleep
			// exits naturally
			require.WithinDuration(t, start, time.Now(), 10*time.Second)
			// assert this test ran for at least one second for pid1
			lapse := time.Since(start)
			require.Greater(t, lapse.Seconds(), float64(1))
		}()

		pid1, err := ctr.Start(ctx, client.StartRequest{
			Args: []string{"sleep", "1"},
		})
		require.NoError(t, err)
		defer pid1.Wait()

		pid2, err := ctr.Start(ctx, client.StartRequest{
			Args: []string{"sleep", "10"},
		})
		require.NoError(t, err)

		return &client.Result{}, pid2.Wait()
	}

	_, err = c.Build(ctx, SolveOpt{}, product, b, nil)
	require.Error(t, err)
	var exitError *gatewayapi.ExitError
	require.ErrorAs(t, err, &exitError)
	require.Equal(t, uint32(137), exitError.ExitCode)
	// `exit code: 137` (ie sigkill)
	require.Regexp(t, "exit code: 137", err.Error())

	checkAllReleasable(t, c, sb, true)
}

// testClientGatewayContainerMounts is testing mounts derived from various
// llb.States
func testClientGatewayContainerMounts(t *testing.T, sb integration.Sandbox) {
	requiresLinux(t)

	ctx := sb.Context()

	c, err := New(ctx, sb.Address())
	require.NoError(t, err)
	defer c.Close()

	tmpdir := integration.Tmpdir(t)

	err = os.WriteFile(filepath.Join(tmpdir.Name, "local-file"), []byte("local"), 0644)
	require.NoError(t, err)

	a := agent.NewKeyring()
	sockPath, err := makeSSHAgentSock(t, a)
	require.NoError(t, err)

	ssh, err := sshprovider.NewSSHAgentProvider([]sshprovider.AgentConfig{{
		ID:    t.Name(),
		Paths: []string{sockPath},
	}})
	require.NoError(t, err)

	product := "buildkit_test"

	b := func(ctx context.Context, c client.Client) (*client.Result, error) {
		mounts := map[string]llb.State{
			"/": llb.Image("busybox:latest").Run(
				llb.Shlex("touch /root-file /cached/cache-file"),
				llb.AddMount("/cached", llb.Scratch(), llb.AsPersistentCacheDir(t.Name(), llb.CacheMountShared)),
			).Root(),
			"/foo": llb.Image("busybox:latest").Run(
				llb.Shlex("touch foo-file"),
				llb.Dir("/tmp"),
				llb.AddMount("/tmp", llb.Scratch()),
			).GetMount("/tmp"),
			"/local": llb.Local("mylocal"),
			// TODO How do we get a results.Ref for a cache mount, tmpfs mount
		}

		containerMounts := []client.Mount{{
			Dest:      "/cached",
			MountType: pb.MountType_CACHE,
			CacheOpt: &pb.CacheOpt{
				ID:      t.Name(),
				Sharing: pb.CacheSharingOpt_SHARED,
			},
		}, {
			Dest:      "/tmpfs",
			MountType: pb.MountType_TMPFS,
		}, {
			Dest:      "/run/secrets/mysecret",
			MountType: pb.MountType_SECRET,
			SecretOpt: &pb.SecretOpt{
				ID: "/run/secrets/mysecret",
			},
		}, {
			Dest:      sockPath,
			MountType: pb.MountType_SSH,
			SSHOpt: &pb.SSHOpt{
				ID: t.Name(),
			},
		}}

		for mountpoint, st := range mounts {
			def, err := st.Marshal(ctx)
			if err != nil {
				return nil, errors.Wrap(err, "failed to marshal state")
			}

			r, err := c.Solve(ctx, client.SolveRequest{
				Definition: def.ToPB(),
			})
			if err != nil {
				return nil, errors.Wrap(err, "failed to solve")
			}
			containerMounts = append(containerMounts, client.Mount{
				Dest:      mountpoint,
				MountType: pb.MountType_BIND,
				Ref:       r.Ref,
			})
		}

		ctr, err := c.NewContainer(ctx, client.NewContainerRequest{Mounts: containerMounts})
		if err != nil {
			return nil, err
		}

		pid1, err := ctr.Start(ctx, client.StartRequest{
			Args:   []string{"sleep", "10"},
			Stderr: os.Stderr,
		})
		require.NoError(t, err)
		defer pid1.Wait()

		pid, err := ctr.Start(ctx, client.StartRequest{
			Args: []string{"test", "-f", "/root-file"},
		})
		require.NoError(t, err)
		err = pid.Wait()
		require.NoError(t, err)

		pid, err = ctr.Start(ctx, client.StartRequest{
			Args: []string{"test", "-f", "/foo/foo-file"},
		})
		require.NoError(t, err)
		err = pid.Wait()
		require.NoError(t, err)

		pid, err = ctr.Start(ctx, client.StartRequest{
			Args: []string{"test", "-f", "/local/local-file"},
		})
		require.NoError(t, err)
		err = pid.Wait()
		require.NoError(t, err)

		pid, err = ctr.Start(ctx, client.StartRequest{
			Args: []string{"test", "-f", "/cached/cache-file"},
		})
		require.NoError(t, err)
		err = pid.Wait()
		require.NoError(t, err)

		pid, err = ctr.Start(ctx, client.StartRequest{
			Args: []string{"test", "-w", "/tmpfs"},
		})
		require.NoError(t, err)
		err = pid.Wait()
		require.NoError(t, err)

		secretOutput := bytes.NewBuffer(nil)
		pid, err = ctr.Start(ctx, client.StartRequest{
			Args:   []string{"cat", "/run/secrets/mysecret"},
			Stdout: &nopCloser{secretOutput},
		})
		require.NoError(t, err)
		err = pid.Wait()
		require.NoError(t, err)
		require.Equal(t, "foo-secret", secretOutput.String())

		pid, err = ctr.Start(ctx, client.StartRequest{
			Args: []string{"test", "-S", sockPath},
		})
		require.NoError(t, err)
		err = pid.Wait()
		require.NoError(t, err)

		return &client.Result{}, ctr.Release(ctx)
	}

	_, err = c.Build(ctx, SolveOpt{
		LocalMounts: map[string]fsutil.FS{
			"mylocal": tmpdir,
		},
		Session: []session.Attachable{
			ssh,
			secretsprovider.FromMap(map[string][]byte{
				"/run/secrets/mysecret": []byte("foo-secret"),
			}),
		},
	}, product, b, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), context.Canceled.Error())

	checkAllReleasable(t, c, sb, true)
}

func testClientGatewayContainerSecretEnv(t *testing.T, sb integration.Sandbox) {
	requiresLinux(t)

	ctx := sb.Context()

	c, err := New(ctx, sb.Address())
	require.NoError(t, err)
	defer c.Close()

	product := "buildkit_test"

	b := func(ctx context.Context, c client.Client) (*client.Result, error) {
		mounts := map[string]llb.State{
			"/": llb.Image("busybox:latest"),
		}

		var containerMounts []client.Mount
		for mountpoint, st := range mounts {
			def, err := st.Marshal(ctx)
			if err != nil {
				return nil, errors.Wrap(err, "failed to marshal state")
			}

			r, err := c.Solve(ctx, client.SolveRequest{
				Definition: def.ToPB(),
			})
			if err != nil {
				return nil, errors.Wrap(err, "failed to solve")
			}
			containerMounts = append(containerMounts, client.Mount{
				Dest:      mountpoint,
				MountType: pb.MountType_BIND,
				Ref:       r.Ref,
			})
		}

		ctr, err := c.NewContainer(ctx, client.NewContainerRequest{Mounts: containerMounts})
		if err != nil {
			return nil, err
		}

		pid, err := ctr.Start(ctx, client.StartRequest{
			Args: []string{"sh", "-c", "test $SOME_SECRET = foo-secret"},
			SecretEnv: []*pb.SecretEnv{
				{
					ID:   "sekrit",
					Name: "SOME_SECRET",
				},
			},
		})
		require.NoError(t, err)
		err = pid.Wait()
		require.NoError(t, err)

		return &client.Result{}, ctr.Release(ctx)
	}

	_, err = c.Build(ctx, SolveOpt{
		Session: []session.Attachable{
			secretsprovider.FromMap(map[string][]byte{
				"sekrit": []byte("foo-secret"),
			}),
		},
	}, product, b, nil)
	require.NoError(t, err)

	checkAllReleasable(t, c, sb, true)
}

// testClientGatewayContainerPID1Tty is testing that we can get a tty via
// a container pid1, executor.Run
func testClientGatewayContainerPID1Tty(t *testing.T, sb integration.Sandbox) {
	requiresLinux(t)
	ctx := sb.Context()

	c, err := New(ctx, sb.Address())
	require.NoError(t, err)
	defer c.Close()

	product := "buildkit_test"

	inputR, inputW := io.Pipe()
	output := bytes.NewBuffer(nil)

	b := func(ctx context.Context, c client.Client) (*client.Result, error) {
		ctx, timeout := context.WithTimeoutCause(ctx, 10*time.Second, nil)
		defer timeout()

		st := llb.Image("busybox:latest")

		def, err := st.Marshal(ctx)
		if err != nil {
			return nil, errors.Wrap(err, "failed to marshal state")
		}

		r, err := c.Solve(ctx, client.SolveRequest{
			Definition: def.ToPB(),
		})
		if err != nil {
			return nil, errors.Wrap(err, "failed to solve")
		}

		ctr, err := c.NewContainer(ctx, client.NewContainerRequest{
			Mounts: []client.Mount{{
				Dest:      "/",
				MountType: pb.MountType_BIND,
				Ref:       r.Ref,
			}},
		})
		require.NoError(t, err)
		defer ctr.Release(ctx)

		prompt := newTestPrompt(ctx, t, inputW, output)
		pid1, err := ctr.Start(ctx, client.StartRequest{
			Args:   []string{"sh"},
			Tty:    true,
			Stdin:  inputR,
			Stdout: &nopCloser{output},
			Stderr: &nopCloser{output},
			Env:    []string{fmt.Sprintf("PS1=%s", prompt.String())},
		})
		require.NoError(t, err)
		err = pid1.Resize(ctx, client.WinSize{Rows: 40, Cols: 80})
		require.NoError(t, err)
		prompt.SendExpect("ttysize", "80 40")
		prompt.Send("cd /tmp")
		prompt.SendExpect("pwd", "/tmp")
		prompt.Send("echo foobar > newfile")
		prompt.SendExpect("cat /tmp/newfile", "foobar")
		err = pid1.Resize(ctx, client.WinSize{Rows: 60, Cols: 100})
		require.NoError(t, err)
		prompt.SendExpect("ttysize", "100 60")
		prompt.SendExit(99)

		err = pid1.Wait()
		var exitError *gatewayapi.ExitError
		require.ErrorAs(t, err, &exitError)
		require.Equal(t, uint32(99), exitError.ExitCode)

		return &client.Result{}, err
	}

	_, err = c.Build(ctx, SolveOpt{}, product, b, nil)
	require.Error(t, err)

	inputW.Close()
	inputR.Close()

	checkAllReleasable(t, c, sb, true)
}

// testClientGatewayContainerCancelPID1Tty is testing that the tty will cleanly
// shutdown on context cancel
func testClientGatewayContainerCancelPID1Tty(t *testing.T, sb integration.Sandbox) {
	requiresLinux(t)
	ctx := sb.Context()

	c, err := New(ctx, sb.Address())
	require.NoError(t, err)
	defer c.Close()

	product := "buildkit_test"

	inputR, inputW := io.Pipe()
	output := bytes.NewBuffer(nil)

	b := func(ctx context.Context, c client.Client) (*client.Result, error) {
		ctx, cancel := context.WithTimeoutCause(ctx, 10*time.Second, nil)
		defer cancel()

		st := llb.Image("busybox:latest")

		def, err := st.Marshal(ctx)
		if err != nil {
			return nil, errors.Wrap(err, "failed to marshal state")
		}

		r, err := c.Solve(ctx, client.SolveRequest{
			Definition: def.ToPB(),
		})
		if err != nil {
			return nil, errors.Wrap(err, "failed to solve")
		}

		ctr, err := c.NewContainer(ctx, client.NewContainerRequest{
			Mounts: []client.Mount{{
				Dest:      "/",
				MountType: pb.MountType_BIND,
				Ref:       r.Ref,
			}},
		})
		require.NoError(t, err)
		defer ctr.Release(ctx)

		prompt := newTestPrompt(ctx, t, inputW, output)
		pid1, err := ctr.Start(ctx, client.StartRequest{
			Args:   []string{"sh"},
			Tty:    true,
			Stdin:  inputR,
			Stdout: &nopCloser{output},
			Stderr: &nopCloser{output},
			Env:    []string{fmt.Sprintf("PS1=%s", prompt.String())},
		})
		require.NoError(t, err)
		prompt.SendExpect("echo hi", "hi")
		cancel()

		err = pid1.Wait()
		require.ErrorIs(t, err, context.Canceled)

		return &client.Result{}, err
	}

	_, err = c.Build(ctx, SolveOpt{}, product, b, nil)
	require.Error(t, err)

	inputW.Close()
	inputR.Close()

	checkAllReleasable(t, c, sb, true)
}

type testPrompt struct {
	ctx    context.Context
	t      *testing.T
	output *bytes.Buffer
	input  io.Writer
	prompt string
	pos    int
}

func newTestPrompt(ctx context.Context, t *testing.T, input io.Writer, output *bytes.Buffer) *testPrompt {
	return &testPrompt{
		ctx:    ctx,
		t:      t,
		input:  input,
		output: output,
		prompt: "% ",
	}
}

func (p *testPrompt) String() string { return p.prompt }

func (p *testPrompt) SendExit(status int) {
	p.input.Write([]byte(fmt.Sprintf("exit %d\n", status)))
}

func (p *testPrompt) Send(cmd string) {
	p.input.Write([]byte(cmd + "\n"))
	p.wait(p.prompt)
}

func (p *testPrompt) SendExpect(cmd, expected string) {
	for {
		p.input.Write([]byte(cmd + "\n"))
		response := p.wait(p.prompt)
		if strings.Contains(response, expected) {
			return
		}
	}
}

func (p *testPrompt) wait(msg string) string {
	for {
		newOutput := p.output.String()[p.pos:]
		if strings.Contains(newOutput, msg) {
			p.pos += len(newOutput)
			return newOutput
		}
		select {
		case <-p.ctx.Done():
			p.t.Logf("Output at timeout: %s", p.output.String())
			p.t.Fatalf("Timeout waiting for %q", msg)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// testClientGatewayContainerExecTty is testing that we can get a tty via
// executor.Exec (secondary process)
func testClientGatewayContainerExecTty(t *testing.T, sb integration.Sandbox) {
	requiresLinux(t)
	ctx := sb.Context()

	c, err := New(ctx, sb.Address())
	require.NoError(t, err)
	defer c.Close()

	product := "buildkit_test"

	inputR, inputW := io.Pipe()
	output := bytes.NewBuffer(nil)
	b := func(ctx context.Context, c client.Client) (*client.Result, error) {
		ctx, timeout := context.WithTimeoutCause(ctx, 10*time.Second, nil)
		defer timeout()
		st := llb.Image("busybox:latest")

		def, err := st.Marshal(ctx)
		if err != nil {
			return nil, errors.Wrap(err, "failed to marshal state")
		}

		r, err := c.Solve(ctx, client.SolveRequest{
			Definition: def.ToPB(),
		})
		if err != nil {
			return nil, errors.Wrap(err, "failed to solve")
		}

		ctr, err := c.NewContainer(ctx, client.NewContainerRequest{
			Mounts: []client.Mount{{
				Dest:      "/",
				MountType: pb.MountType_BIND,
				Ref:       r.Ref,
			}},
		})
		require.NoError(t, err)

		pid1, err := ctr.Start(ctx, client.StartRequest{
			Args: []string{"sleep", "10"},
		})
		require.NoError(t, err)

		defer pid1.Wait()
		defer ctr.Release(ctx)

		prompt := newTestPrompt(ctx, t, inputW, output)
		pid2, err := ctr.Start(ctx, client.StartRequest{
			Args:   []string{"sh"},
			Tty:    true,
			Stdin:  inputR,
			Stdout: &nopCloser{output},
			Stderr: &nopCloser{output},
			Env:    []string{fmt.Sprintf("PS1=%s", prompt.String())},
		})
		require.NoError(t, err)

		err = pid2.Resize(ctx, client.WinSize{Rows: 40, Cols: 80})
		require.NoError(t, err)
		prompt.SendExpect("ttysize", "80 40")
		prompt.Send("cd /tmp")
		prompt.SendExpect("pwd", "/tmp")
		prompt.Send("echo foobar > newfile")
		prompt.SendExpect("cat /tmp/newfile", "foobar")
		err = pid2.Resize(ctx, client.WinSize{Rows: 60, Cols: 100})
		require.NoError(t, err)
		prompt.SendExpect("ttysize", "100 60")
		prompt.SendExit(99)

		err = pid2.Wait()
		var exitError *gatewayapi.ExitError
		require.ErrorAs(t, err, &exitError)
		require.Equal(t, uint32(99), exitError.ExitCode)

		return &client.Result{}, err
	}

	_, err = c.Build(ctx, SolveOpt{}, product, b, nil)
	require.Error(t, err)
	var exitError *gatewayapi.ExitError
	require.ErrorAs(t, err, &exitError)
	require.Equal(t, uint32(99), exitError.ExitCode)
	require.Regexp(t, "exit code: 99", err.Error())

	inputW.Close()
	inputR.Close()

	checkAllReleasable(t, c, sb, true)
}

// testClientGatewayContainerExecTty is testing the tty shuts down cleanly
// on context.Cancel
func testClientGatewayContainerCancelExecTty(t *testing.T, sb integration.Sandbox) {
	requiresLinux(t)
	ctx := sb.Context()

	c, err := New(ctx, sb.Address())
	require.NoError(t, err)
	defer c.Close()

	product := "buildkit_test"

	inputR, inputW := io.Pipe()
	output := bytes.NewBuffer(nil)
	b := func(ctx context.Context, c client.Client) (*client.Result, error) {
		ctx, timeout := context.WithTimeoutCause(ctx, 10*time.Second, nil)
		defer timeout()
		st := llb.Image("busybox:latest")

		def, err := st.Marshal(ctx)
		if err != nil {
			return nil, errors.Wrap(err, "failed to marshal state")
		}

		r, err := c.Solve(ctx, client.SolveRequest{
			Definition: def.ToPB(),
		})
		if err != nil {
			return nil, errors.Wrap(err, "failed to solve")
		}

		ctr, err := c.NewContainer(ctx, client.NewContainerRequest{
			Mounts: []client.Mount{{
				Dest:      "/",
				MountType: pb.MountType_BIND,
				Ref:       r.Ref,
			}},
		})
		require.NoError(t, err)

		pid1, err := ctr.Start(ctx, client.StartRequest{
			Args: []string{"sleep", "10"},
		})
		require.NoError(t, err)

		defer pid1.Wait()
		defer ctr.Release(ctx)

		execCtx, cancel := context.WithCancelCause(ctx)
		defer cancel(errors.WithStack(context.Canceled))

		prompt := newTestPrompt(execCtx, t, inputW, output)
		pid2, err := ctr.Start(execCtx, client.StartRequest{
			Args:   []string{"sh"},
			Tty:    true,
			Stdin:  inputR,
			Stdout: &nopCloser{output},
			Stderr: &nopCloser{output},
			Env:    []string{fmt.Sprintf("PS1=%s", prompt.String())},
		})
		require.NoError(t, err)

		prompt.SendExpect("echo hi", "hi")
		cancel(errors.WithStack(context.Canceled))

		err = pid2.Wait()
		require.ErrorIs(t, err, context.Canceled)

		return &client.Result{}, err
	}

	_, err = c.Build(ctx, SolveOpt{}, product, b, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), context.Canceled.Error())

	inputW.Close()
	inputR.Close()

	checkAllReleasable(t, c, sb, true)
}

func testClientSlowCacheRootfsRef(t *testing.T, sb integration.Sandbox) {
	requiresLinux(t)

	ctx := sb.Context()

	c, err := New(ctx, sb.Address())
	require.NoError(t, err)
	defer c.Close()

	b := func(ctx context.Context, c client.Client) (*client.Result, error) {
		id := identity.NewID()
		input := llb.Scratch().File(
			llb.Mkdir("/found", 0700).
				Mkfile("/found/data", 0600, []byte(id)),
		)

		st := llb.Image("busybox:latest").Run(
			llb.Shlexf("echo hello"),
			// Only readonly mounts trigger slow cache errors.
			llb.AddMount("/src", input, llb.SourcePath("/notfound"), llb.Readonly),
		).Root()

		def1, err := st.Marshal(ctx)
		require.NoError(t, err)

		res1, err := c.Solve(ctx, client.SolveRequest{
			Definition: def1.ToPB(),
		})
		require.NoError(t, err)

		ref1, err := res1.SingleRef()
		require.NoError(t, err)

		// First stat should error because unlazy-ing the reference causes an error
		// in CalcSlowCache.
		_, err = ref1.StatFile(ctx, client.StatRequest{
			Path: ".",
		})
		require.Error(t, err)

		def2, err := llb.Image("busybox:latest").Marshal(ctx)
		require.NoError(t, err)

		res2, err := c.Solve(ctx, client.SolveRequest{
			Definition: def2.ToPB(),
		})
		require.NoError(t, err)

		ref2, err := res2.SingleRef()
		require.NoError(t, err)

		// Second stat should not error because the rootfs for `busybox` should not
		// have been released.
		_, err = ref2.StatFile(ctx, client.StatRequest{
			Path: ".",
		})
		require.NoError(t, err)

		return res2, nil
	}

	_, err = c.Build(ctx, SolveOpt{}, "buildkit_test", b, nil)
	require.NoError(t, err)
	checkAllReleasable(t, c, sb, true)
}

// testClientGatewayContainerPlatformPATH is testing the correct default PATH
// gets set for the requested platform
func testClientGatewayContainerPlatformPATH(t *testing.T, sb integration.Sandbox) {
	requiresLinux(t)
	ctx := sb.Context()

	c, err := New(ctx, sb.Address())
	require.NoError(t, err)
	defer c.Close()

	product := "buildkit_test"
	b := func(ctx context.Context, c client.Client) (*client.Result, error) {
		st := llb.Image("busybox:latest")
		def, err := st.Marshal(ctx)
		require.NoError(t, err)
		r, err := c.Solve(ctx, client.SolveRequest{
			Definition: def.ToPB(),
		})
		require.NoError(t, err)

		tests := []struct {
			Name     string
			Platform *pb.Platform
			Expected string
		}{{
			"default path",
			nil,
			utilsystem.DefaultPathEnvUnix,
		}, {
			"linux path",
			&pb.Platform{OS: "linux"},
			utilsystem.DefaultPathEnvUnix,
		}, {
			"windows path",
			&pb.Platform{OS: "windows"},
			utilsystem.DefaultPathEnvWindows,
		}}

		for _, tt := range tests {
			t.Run(tt.Name, func(t *testing.T) {
				ctr, err := c.NewContainer(ctx, client.NewContainerRequest{
					Mounts: []client.Mount{{
						Dest:      "/",
						MountType: pb.MountType_BIND,
						Ref:       r.Ref,
					}},
					Platform: tt.Platform,
				})
				require.NoError(t, err)
				output := bytes.NewBuffer(nil)
				pid1, err := ctr.Start(ctx, client.StartRequest{
					Args:   []string{"/bin/sh", "-c", "echo -n $PATH"},
					Stdout: &nopCloser{output},
				})
				require.NoError(t, err)

				err = pid1.Wait()
				require.NoError(t, err)
				require.Equal(t, tt.Expected, output.String())
				err = ctr.Release(ctx)
				require.NoError(t, err)
			})
		}
		return &client.Result{}, err
	}

	_, err = c.Build(ctx, SolveOpt{}, product, b, nil)
	require.NoError(t, err)
	checkAllReleasable(t, c, sb, true)
}

// testClientGatewayExecError is testing gateway exec to recreate the container
// process for a failed execop.
func testClientGatewayExecError(t *testing.T, sb integration.Sandbox) {
	requiresLinux(t)

	ctx := sb.Context()

	c, err := New(ctx, sb.Address())
	require.NoError(t, err)
	defer c.Close()

	b := func(ctx context.Context, c client.Client) (*client.Result, error) {
		id := identity.NewID()
		tests := []struct {
			Name      string
			State     llb.State
			NumMounts int
			Paths     []string
		}{{
			"only rootfs",
			llb.Image("busybox:latest").Run(
				llb.Shlexf(`sh -c "echo %s > /data && fail"`, id),
			).Root(),
			1, []string{"/data"},
		}, {
			"rootfs and readwrite scratch mount",
			llb.Image("busybox:latest").Run(
				llb.Shlexf(`sh -c "echo %s > /data && echo %s > /rw/data && fail"`, id, id),
				llb.AddMount("/rw", llb.Scratch()),
			).Root(),
			2, []string{"/data", "/rw/data"},
		}, {
			"rootfs and readwrite mount",
			llb.Image("busybox:latest").Run(
				llb.Shlexf(`sh -c "echo %s > /data && echo %s > /rw/data && fail"`, id, id),
				llb.AddMount("/rw", llb.Scratch().File(llb.Mkfile("foo", 0700, []byte(id)))),
			).Root(),
			2, []string{"/data", "/rw/data", "/rw/foo"},
		}, {
			"rootfs and readonly scratch mount",
			llb.Image("busybox:latest").Run(
				llb.Shlexf(`sh -c "echo %s > /data && echo %s > /readonly/foo"`, id, id),
				llb.AddMount("/readonly", llb.Scratch(), llb.Readonly),
			).Root(),
			2, []string{"/data"},
		}, {
			"rootfs and readwrite force no output mount",
			llb.Image("busybox:latest").Run(
				llb.Shlexf(`sh -c "echo %s > /data && echo %s > /rw/data && fail"`, id, id),
				llb.AddMount(
					"/rw",
					llb.Scratch().File(llb.Mkfile("foo", 0700, []byte(id))),
					llb.ForceNoOutput,
				),
			).Root(),
			2, []string{"/data", "/rw/data", "/rw/foo"},
		}}

		for _, tt := range tests {
			tt := tt
			t.Run(tt.Name, func(t *testing.T) {
				def, err := tt.State.Marshal(ctx)
				require.NoError(t, err)

				_, solveErr := c.Solve(ctx, client.SolveRequest{
					Evaluate:   true,
					Definition: def.ToPB(),
				})
				require.Error(t, solveErr)

				var se *errdefs.SolveError
				require.ErrorAs(t, solveErr, &se)
				require.Len(t, se.InputIDs, tt.NumMounts)
				require.Len(t, se.MountIDs, tt.NumMounts)

				op := se.Solve.Op
				require.NotNil(t, op)
				require.NotNil(t, op.Op)

				opExec, ok := se.Solve.Op.Op.(*pb.Op_Exec)
				require.True(t, ok)

				exec := opExec.Exec

				var mounts []client.Mount
				for i, mnt := range exec.Mounts {
					mounts = append(mounts, client.Mount{
						Selector:  mnt.Selector,
						Dest:      mnt.Dest,
						ResultID:  se.Solve.MountIDs[i],
						Readonly:  mnt.Readonly,
						MountType: mnt.MountType,
						CacheOpt:  mnt.CacheOpt,
						SecretOpt: mnt.SecretOpt,
						SSHOpt:    mnt.SSHOpt,
					})
				}

				ctr, err := c.NewContainer(ctx, client.NewContainerRequest{
					Mounts:      mounts,
					NetMode:     exec.Network,
					Platform:    op.Platform,
					Constraints: op.Constraints,
				})
				require.NoError(t, err)
				defer ctr.Release(ctx)

				inputR, inputW := io.Pipe()
				defer inputW.Close()
				defer inputR.Close()

				pid1Output := bytes.NewBuffer(nil)

				prompt := newTestPrompt(ctx, t, inputW, pid1Output)
				pid1, err := ctr.Start(ctx, client.StartRequest{
					Args:   []string{"sh"},
					Tty:    true,
					Stdin:  inputR,
					Stdout: &nopCloser{pid1Output},
					Stderr: &nopCloser{pid1Output},
					Env:    []string{fmt.Sprintf("PS1=%s", prompt.String())},
				})
				require.NoError(t, err)

				meta := exec.Meta
				for _, p := range tt.Paths {
					output := bytes.NewBuffer(nil)
					proc, err := ctr.Start(ctx, client.StartRequest{
						Args:         []string{"cat", p},
						Env:          meta.Env,
						User:         meta.User,
						Cwd:          meta.Cwd,
						Stdout:       &nopCloser{output},
						SecurityMode: exec.Security,
					})
					require.NoError(t, err)

					err = proc.Wait()
					require.NoError(t, err)
					require.Equal(t, id, strings.TrimSpace(output.String()))
				}

				prompt.SendExit(0)
				err = pid1.Wait()
				require.NoError(t, err)
			})
		}

		return client.NewResult(), nil
	}

	_, err = c.Build(ctx, SolveOpt{}, "buildkit_test", b, nil)
	require.NoError(t, err)

	checkAllReleasable(t, c, sb, true)
}

// testClientGatewaySlowCacheExecError is testing gateway exec into the ref
// that failed to mount during an execop.
func testClientGatewaySlowCacheExecError(t *testing.T, sb integration.Sandbox) {
	requiresLinux(t)

	ctx := sb.Context()

	c, err := New(ctx, sb.Address())
	require.NoError(t, err)
	defer c.Close()

	id := identity.NewID()
	input := llb.Scratch().File(
		llb.Mkdir("/found", 0700).
			Mkfile("/found/data", 0600, []byte(id)),
	)

	st := llb.Image("busybox:latest").Run(
		llb.Shlexf("echo hello"),
		// Only readonly mounts trigger slow cache errors.
		llb.AddMount("/src", input, llb.SourcePath("/notfound"), llb.Readonly),
	).Root()

	def, err := st.Marshal(ctx)
	require.NoError(t, err)

	b := func(ctx context.Context, c client.Client) (*client.Result, error) {
		_, solveErr := c.Solve(ctx, client.SolveRequest{
			Evaluate:   true,
			Definition: def.ToPB(),
		})
		require.Error(t, solveErr)

		var se *errdefs.SolveError
		require.ErrorAs(t, solveErr, &se)

		_, ok := se.Solve.Op.Op.(*pb.Op_Exec)
		require.True(t, ok)

		_, ok = se.Solve.Subject.(*errdefs.Solve_Cache)
		require.True(t, ok)
		// Slow cache errors should only have exactly one input and no outputs.
		require.Len(t, se.Solve.InputIDs, 1)
		require.Len(t, se.Solve.MountIDs, 0)

		st := llb.Image("busybox:latest")
		def, err := st.Marshal(ctx)
		require.NoError(t, err)

		res, err := c.Solve(ctx, client.SolveRequest{
			Definition: def.ToPB(),
		})
		require.NoError(t, err)

		ref, err := res.SingleRef()
		require.NoError(t, err)

		ctr, err := c.NewContainer(ctx, client.NewContainerRequest{
			Mounts: []client.Mount{{
				Dest:      "/",
				MountType: pb.MountType_BIND,
				Ref:       ref,
			}, {
				Dest:      "/problem",
				MountType: pb.MountType_BIND,
				ResultID:  se.Solve.InputIDs[0],
			}},
		})
		require.NoError(t, err)
		defer ctr.Release(ctx)

		output := bytes.NewBuffer(nil)
		proc, err := ctr.Start(ctx, client.StartRequest{
			Args:   []string{"cat", "/problem/found/data"},
			Stdout: &nopCloser{output},
		})
		require.NoError(t, err)

		err = proc.Wait()
		require.NoError(t, err)
		require.Equal(t, id, strings.TrimSpace(output.String()))

		return client.NewResult(), nil
	}

	_, err = c.Build(ctx, SolveOpt{}, "buildkit_test", b, nil)
	require.NoError(t, err)

	checkAllReleasable(t, c, sb, true)
}

// testClientGatewayExecFileActionError is testing gateway exec into the modified
// mount of a failed fileop during a solve.
func testClientGatewayExecFileActionError(t *testing.T, sb integration.Sandbox) {
	requiresLinux(t)

	ctx := sb.Context()

	c, err := New(ctx, sb.Address())
	require.NoError(t, err)
	defer c.Close()

	b := func(ctx context.Context, c client.Client) (*client.Result, error) {
		st := llb.Image("busybox:latest")
		def, err := st.Marshal(ctx)
		require.NoError(t, err)

		res, err := c.Solve(ctx, client.SolveRequest{
			Definition: def.ToPB(),
		})
		require.NoError(t, err)

		debugfs, err := res.SingleRef()
		require.NoError(t, err)

		id := identity.NewID()
		tests := []struct {
			Name       string
			State      llb.State
			NumInputs  int
			NumOutputs int
			Path       string
		}{{
			"mkfile",
			llb.Scratch().File(
				llb.Mkdir("/found", 0700).
					Mkfile("/found/foo", 0600, []byte(id)).
					Mkfile("/notfound/foo", 0600, []byte(id)),
			),
			0, 3, "/input/found/foo",
		}, {
			"copy from input",
			llb.Image("busybox").File(
				llb.Copy(
					llb.Scratch().File(
						llb.Mkdir("/foo", 0600).Mkfile("/foo/bar", 0700, []byte(id)),
					),
					"/foo/bar",
					"/notfound/baz",
				),
			),
			2, 1, "/secondary/foo/bar",
		}, {
			"copy from action",
			llb.Image("busybox").File(
				llb.Copy(
					llb.Mkdir("/foo", 0600).Mkfile("/foo/bar", 0700, []byte(id)).WithState(llb.Scratch()),
					"/foo/bar",
					"/notfound/baz",
				),
			),
			1, 3, "/secondary/foo/bar",
		}}

		for _, tt := range tests {
			tt := tt
			t.Run(tt.Name, func(t *testing.T) {
				def, err := tt.State.Marshal(ctx)
				require.NoError(t, err)

				_, err = c.Solve(ctx, client.SolveRequest{
					Evaluate:   true,
					Definition: def.ToPB(),
				})
				require.Error(t, err)

				var se *errdefs.SolveError
				require.ErrorAs(t, err, &se)
				require.Len(t, se.Solve.InputIDs, tt.NumInputs)

				// There is one output for every action in the fileop that failed.
				require.Len(t, se.Solve.MountIDs, tt.NumOutputs)

				op, ok := se.Solve.Op.Op.(*pb.Op_File)
				require.True(t, ok)

				subject, ok := se.Solve.Subject.(*errdefs.Solve_File)
				require.True(t, ok)

				// Retrieve the action that failed from the sbuject.
				idx := subject.File.Index
				require.Less(t, int(idx), len(op.File.Actions))
				action := op.File.Actions[idx]

				// The output for a file action is mapped by its index.
				inputID := se.MountIDs[idx]

				var secondaryID string
				if action.SecondaryInput != -1 {
					// If the secondary input is a result from another exec, it will be one
					// of the input IDs, otherwise it's a intermediary mutable from another
					// action in the same fileop.
					if int(action.SecondaryInput) < len(se.InputIDs) {
						secondaryID = se.InputIDs[action.SecondaryInput]
					} else {
						secondaryID = se.MountIDs[int(action.SecondaryInput)-len(se.InputIDs)]
					}
				}

				mounts := []client.Mount{{
					Dest:      "/",
					MountType: pb.MountType_BIND,
					Ref:       debugfs,
				}, {
					Dest:      "/input",
					MountType: pb.MountType_BIND,
					ResultID:  inputID,
				}}

				if secondaryID != "" {
					mounts = append(mounts, client.Mount{
						Dest:      "/secondary",
						MountType: pb.MountType_BIND,
						ResultID:  secondaryID,
					})
				}

				ctr, err := c.NewContainer(ctx, client.NewContainerRequest{Mounts: mounts})
				require.NoError(t, err)

				// Verify that the randomly generated data can be found in a mutable ref
				// created by the actions that have succeeded.
				output := bytes.NewBuffer(nil)
				proc, err := ctr.Start(ctx, client.StartRequest{
					Args:   []string{"cat", tt.Path},
					Stdout: &nopCloser{output},
				})
				require.NoError(t, err)

				err = proc.Wait()
				require.NoError(t, err)
				require.Equal(t, id, strings.TrimSpace(output.String()))

				err = ctr.Release(ctx)
				require.NoError(t, err)
			})
		}

		return client.NewResult(), nil
	}

	_, err = c.Build(ctx, SolveOpt{}, "buildkit_test", b, nil)
	require.NoError(t, err)

	checkAllReleasable(t, c, sb, true)
}

// testClientGatewayContainerSecurityModeCaps ensures that the correct security mode
// is propagated to the gateway container
func testClientGatewayContainerSecurityModeCaps(t *testing.T, sb integration.Sandbox) {
	testClientGatewayContainerSecurityMode(t, sb, false)
}

func testClientGatewayContainerSecurityModeValidation(t *testing.T, sb integration.Sandbox) {
	testClientGatewayContainerSecurityMode(t, sb, true)
}

func testClientGatewayContainerSecurityMode(t *testing.T, sb integration.Sandbox, expectFail bool) {
	workers.CheckFeatureCompat(t, sb, workers.FeatureSecurityMode)
	requiresLinux(t)

	ctx := sb.Context()

	c, err := New(ctx, sb.Address())
	require.NoError(t, err)
	defer c.Close()

	product := "buildkit_test"

	command := []string{"sh", "-c", `cat /proc/self/status | grep CapEff | cut -f 2`}
	mode := llb.SecurityModeSandbox
	var allowedEntitlements []entitlements.Entitlement
	var assertCaps func(caps uint64)
	secMode := sb.Value("secmode")
	if secMode == securitySandbox {
		assertCaps = func(caps uint64) {
			/*
				$ capsh --decode=00000000a80425fb
				0x00000000a80425fb=cap_chown,cap_dac_override,cap_fowner,cap_fsetid,cap_kill,cap_setgid,cap_setuid,cap_setpcap,
				cap_net_bind_service,cap_net_raw,cap_sys_chroot,cap_mknod,cap_audit_write,cap_setfcap
			*/
			require.EqualValues(t, 0xa80425fb, caps)
		}
		allowedEntitlements = []entitlements.Entitlement{}
		if expectFail {
			return
		}
	} else {
		assertCaps = func(caps uint64) {
			/*
				$ capsh --decode=0000003fffffffff
				0x0000003fffffffff=cap_chown,cap_dac_override,cap_dac_read_search,cap_fowner,cap_fsetid,cap_kill,cap_setgid,
				cap_setuid,cap_setpcap,cap_linux_immutable,cap_net_bind_service,cap_net_broadcast,cap_net_admin,cap_net_raw,
				cap_ipc_lock,cap_ipc_owner,cap_sys_module,cap_sys_rawio,cap_sys_chroot,cap_sys_ptrace,cap_sys_pacct,cap_sys_admin,
				cap_sys_boot,cap_sys_nice,cap_sys_resource,cap_sys_time,cap_sys_tty_config,cap_mknod,cap_lease,cap_audit_write,
				cap_audit_control,cap_setfcap,cap_mac_override,cap_mac_admin,cap_syslog,cap_wake_alarm,cap_block_suspend,cap_audit_read
			*/

			// require that _at least_ minimum capabilities are granted
			require.EqualValues(t, 0x3fffffffff, caps&0x3fffffffff)
		}
		mode = llb.SecurityModeInsecure
		allowedEntitlements = []entitlements.Entitlement{entitlements.EntitlementSecurityInsecure}
		if expectFail {
			allowedEntitlements = []entitlements.Entitlement{}
		}
	}

	b := func(ctx context.Context, c client.Client) (*client.Result, error) {
		st := llb.Image("busybox:latest")

		def, err := st.Marshal(ctx)
		if err != nil {
			return nil, errors.Wrap(err, "failed to marshal state")
		}

		r, err := c.Solve(ctx, client.SolveRequest{
			Definition: def.ToPB(),
		})
		if err != nil {
			return nil, errors.Wrap(err, "failed to solve")
		}

		ctr, err := c.NewContainer(ctx, client.NewContainerRequest{
			Mounts: []client.Mount{{
				Dest:      "/",
				MountType: pb.MountType_BIND,
				Ref:       r.Ref,
			}},
		})

		if err != nil {
			return nil, err
		}

		stdout := bytes.NewBuffer(nil)
		stderr := bytes.NewBuffer(nil)

		pid, err := ctr.Start(ctx, client.StartRequest{
			Args:         command,
			Stdout:       &nopCloser{stdout},
			Stderr:       &nopCloser{stderr},
			SecurityMode: mode,
		})
		if err != nil {
			ctr.Release(ctx)
			return nil, err
		}
		defer ctr.Release(ctx)

		err = pid.Wait()

		t.Logf("Stdout: %q", stdout.String())
		t.Logf("Stderr: %q", stderr.String())

		if expectFail {
			require.Error(t, err)
			require.Contains(t, err.Error(), "security.insecure is not allowed")
			return nil, err
		}

		require.NoError(t, err)

		capsValue, err := strconv.ParseUint(strings.TrimSpace(stdout.String()), 16, 64)
		require.NoError(t, err)

		assertCaps(capsValue)

		return &client.Result{}, nil
	}

	solveOpts := SolveOpt{
		AllowedEntitlements: allowedEntitlements,
	}
	_, err = c.Build(ctx, solveOpts, product, b, nil)

	if expectFail {
		require.Error(t, err)
		require.Contains(t, err.Error(), "security.insecure is not allowed")
	} else {
		require.NoError(t, err)
	}

	checkAllReleasable(t, c, sb, true)
}

func testClientGatewayContainerExtraHosts(t *testing.T, sb integration.Sandbox) {
	requiresLinux(t)

	ctx := sb.Context()
	product := "buildkit_test"

	c, err := New(ctx, sb.Address())
	require.NoError(t, err)
	defer c.Close()

	b := func(ctx context.Context, c client.Client) (*client.Result, error) {
		st := llb.Image("busybox")

		def, err := st.Marshal(ctx)
		if err != nil {
			return nil, errors.Wrap(err, "failed to marshal state")
		}

		r, err := c.Solve(ctx, client.SolveRequest{
			Definition: def.ToPB(),
		})
		if err != nil {
			return nil, errors.Wrap(err, "failed to solve")
		}

		ctr, err := c.NewContainer(ctx, client.NewContainerRequest{
			Mounts: []client.Mount{{
				Dest:      "/",
				MountType: pb.MountType_BIND,
				Ref:       r.Ref,
			}},
			ExtraHosts: []*pb.HostIP{{
				Host: "some.host",
				IP:   "169.254.11.22",
			}},
		})

		if err != nil {
			return nil, err
		}

		stdout := bytes.NewBuffer(nil)
		stderr := bytes.NewBuffer(nil)

		pid, err := ctr.Start(ctx, client.StartRequest{
			Args:   []string{"grep", "169.254.11.22\tsome.host", "/etc/hosts"},
			Stdout: &nopCloser{stdout},
			Stderr: &nopCloser{stderr},
		})
		if err != nil {
			ctr.Release(ctx)
			return nil, err
		}
		defer ctr.Release(ctx)

		err = pid.Wait()

		t.Logf("Stdout: %q", stdout.String())
		t.Logf("Stderr: %q", stderr.String())

		require.NoError(t, err)

		return &client.Result{}, nil
	}

	_, err = c.Build(ctx, SolveOpt{}, product, b, nil)
	require.NoError(t, err)

	checkAllReleasable(t, c, sb, true)
}

func testClientGatewayContainerHostNetworkingAccess(t *testing.T, sb integration.Sandbox) {
	testClientGatewayContainerHostNetworking(t, sb, false)
}

func testClientGatewayContainerHostNetworkingValidation(t *testing.T, sb integration.Sandbox) {
	testClientGatewayContainerHostNetworking(t, sb, true)
}

func testClientGatewayContainerHostNetworking(t *testing.T, sb integration.Sandbox, expectFail bool) {
	if os.Getenv("BUILDKIT_RUN_NETWORK_INTEGRATION_TESTS") == "" {
		t.SkipNow()
	}

	if sb.Rootless() && sb.Value("netmode") == defaultNetwork {
		// skip "default" network test for rootless, it always runs with "host" network
		// https://github.com/moby/buildkit/blob/v0.9.0/docs/rootless.md#known-limitations
		t.SkipNow()
	}

	requiresLinux(t)

	ctx := sb.Context()
	product := "buildkit_test"

	var allowedEntitlements []entitlements.Entitlement
	netMode := pb.NetMode_UNSET
	if sb.Value("netmode") == hostNetwork {
		netMode = pb.NetMode_HOST
		allowedEntitlements = []entitlements.Entitlement{entitlements.EntitlementNetworkHost}
		if expectFail {
			allowedEntitlements = []entitlements.Entitlement{}
		}
	}
	c, err := New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	s, err := echoserver.NewTestServer("foo")
	require.NoError(t, err)
	addrParts := strings.Split(s.Addr().String(), ":")
	port := addrParts[len(addrParts)-1]

	b := func(ctx context.Context, c client.Client) (*client.Result, error) {
		st := llb.Image("busybox")

		def, err := st.Marshal(ctx)
		if err != nil {
			return nil, errors.Wrap(err, "failed to marshal state")
		}

		r, err := c.Solve(ctx, client.SolveRequest{
			Definition: def.ToPB(),
		})
		if err != nil {
			return nil, errors.Wrap(err, "failed to solve")
		}

		ctr, err := c.NewContainer(ctx, client.NewContainerRequest{
			Mounts: []client.Mount{{
				Dest:      "/",
				MountType: pb.MountType_BIND,
				Ref:       r.Ref,
			}},
			NetMode: netMode,
		})

		if err != nil {
			return nil, err
		}

		stdout := bytes.NewBuffer(nil)
		stderr := bytes.NewBuffer(nil)

		pid, err := ctr.Start(ctx, client.StartRequest{
			Args:   []string{"/bin/sh", "-c", fmt.Sprintf("nc 127.0.0.1 %s | grep foo", port)},
			Stdout: &nopCloser{stdout},
			Stderr: &nopCloser{stderr},
		})
		if err != nil {
			ctr.Release(ctx)
			return nil, err
		}
		defer ctr.Release(ctx)

		err = pid.Wait()

		t.Logf("Stdout: %q", stdout.String())
		t.Logf("Stderr: %q", stderr.String())

		if netMode == pb.NetMode_HOST {
			if expectFail {
				require.Error(t, err)
				require.Contains(t, err.Error(), "network.host is not allowed")
			} else {
				require.NoError(t, err)
			}
		} else {
			require.Error(t, err)
		}

		return &client.Result{}, nil
	}

	solveOpts := SolveOpt{
		AllowedEntitlements: allowedEntitlements,
	}
	_, err = c.Build(ctx, solveOpts, product, b, nil)
	require.NoError(t, err)

	checkAllReleasable(t, c, sb, true)
}

// testClientGatewayContainerSignal is testing that we can send a signal
func testClientGatewayContainerSignal(t *testing.T, sb integration.Sandbox) {
	requiresLinux(t)
	ctx := sb.Context()

	c, err := New(ctx, sb.Address())
	require.NoError(t, err)
	defer c.Close()

	product := "buildkit_test"

	b := func(ctx context.Context, c client.Client) (*client.Result, error) {
		ctx, timeout := context.WithTimeoutCause(ctx, 10*time.Second, nil)
		defer timeout()

		st := llb.Image("busybox:latest")

		def, err := st.Marshal(ctx)
		if err != nil {
			return nil, errors.Wrap(err, "failed to marshal state")
		}

		r, err := c.Solve(ctx, client.SolveRequest{
			Definition: def.ToPB(),
		})
		if err != nil {
			return nil, errors.Wrap(err, "failed to solve")
		}

		ctr1, err := c.NewContainer(ctx, client.NewContainerRequest{
			Mounts: []client.Mount{{
				Dest:      "/",
				MountType: pb.MountType_BIND,
				Ref:       r.Ref,
			}},
		})
		require.NoError(t, err)
		defer ctr1.Release(ctx)

		pid1, err := ctr1.Start(ctx, client.StartRequest{
			Args: []string{"sh", "-c", `trap 'kill $(jobs -p); exit 99' HUP; sleep 10 & wait`},
		})
		require.NoError(t, err)

		// allow for the shell script to setup the trap before we signal it
		time.Sleep(time.Second)

		err = pid1.Signal(ctx, syscall.SIGHUP)
		require.NoError(t, err)

		err = pid1.Wait()
		var exitError *gatewayapi.ExitError
		require.ErrorAs(t, err, &exitError)
		require.Equal(t, uint32(99), exitError.ExitCode)

		// Now try again to signal an exec process

		ctr2, err := c.NewContainer(ctx, client.NewContainerRequest{
			Mounts: []client.Mount{{
				Dest:      "/",
				MountType: pb.MountType_BIND,
				Ref:       r.Ref,
			}},
		})
		require.NoError(t, err)
		defer ctr2.Release(ctx)

		pid1, err = ctr2.Start(ctx, client.StartRequest{
			Args: []string{"sleep", "10"},
		})
		require.NoError(t, err)

		pid2, err := ctr2.Start(ctx, client.StartRequest{
			Args: []string{"sh", "-c", `trap 'kill $(jobs -p); exit 111' INT; sleep 10 & wait`},
		})
		require.NoError(t, err)

		// allow for the shell script to setup the trap before we signal it
		time.Sleep(time.Second)

		err = pid2.Signal(ctx, syscall.SIGINT)
		require.NoError(t, err)

		err = pid2.Wait()
		require.ErrorAs(t, err, &exitError)
		require.Equal(t, uint32(111), exitError.ExitCode)

		pid1.Signal(ctx, syscall.SIGKILL)
		pid1.Wait()
		return &client.Result{}, err
	}

	_, err = c.Build(ctx, SolveOpt{}, product, b, nil)
	require.Error(t, err)

	checkAllReleasable(t, c, sb, true)
}

func testClientGatewayNilResult(t *testing.T, sb integration.Sandbox) {
	workers.CheckFeatureCompat(t, sb, workers.FeatureMergeDiff)
	requiresLinux(t)
	c, err := New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	b := func(ctx context.Context, c client.Client) (*client.Result, error) {
		st := llb.Image("busybox:latest")
		diff := llb.Diff(st, st)
		def, err := diff.Marshal(sb.Context())
		if err != nil {
			return nil, err
		}
		res, err := c.Solve(ctx, client.SolveRequest{
			Definition: def.ToPB(),
			Evaluate:   true,
		})
		require.NoError(t, err)

		ref, err := res.SingleRef()
		require.NoError(t, err)

		dirEnts, err := ref.ReadDir(ctx, client.ReadDirRequest{
			Path: "/",
		})
		require.NoError(t, err)
		require.Len(t, dirEnts, 0)
		return nil, nil
	}

	_, err = c.Build(sb.Context(), SolveOpt{}, "", b, nil)
	require.NoError(t, err)
}

func testClientGatewayEmptyImageExec(t *testing.T, sb integration.Sandbox) {
	integration.SkipOnPlatform(t, "windows")
	workers.CheckFeatureCompat(t, sb, workers.FeatureDirectPush)
	c, err := New(sb.Context(), sb.Address())
	require.NoError(t, err)
	defer c.Close()

	registry, err := sb.NewRegistry()
	if errors.Is(err, integration.ErrRequirements) {
		t.Skip(err.Error())
	}
	require.NoError(t, err)
	target := registry + "/buildkit/testemptyimage:latest"

	// push an empty image
	_, err = c.Build(sb.Context(), SolveOpt{
		Exports: []ExportEntry{
			{
				Type: ExporterImage,
				Attrs: map[string]string{
					"name": target,
					"push": "true",
				},
			},
		},
	}, "", func(ctx context.Context, c client.Client) (*client.Result, error) {
		return client.NewResult(), nil
	}, nil)
	require.NoError(t, err)

	_, err = c.Build(sb.Context(), SolveOpt{}, "", func(ctx context.Context, gw client.Client) (*client.Result, error) {
		// create an exec on that empty image (expected to fail, but not to panic)
		st := llb.Image(target).Run(
			llb.Args([]string{"echo", "hello"}),
		).Root()
		def, err := st.Marshal(sb.Context())
		if err != nil {
			return nil, err
		}
		_, err = gw.Solve(ctx, client.SolveRequest{
			Definition: def.ToPB(),
			Evaluate:   true,
		})
		require.ErrorContains(t, err, `process "echo hello" did not complete successfully`)
		return nil, nil
	}, nil)
	require.NoError(t, err)
}

type nopCloser struct {
	io.Writer
}

func (n *nopCloser) Close() error {
	return nil
}
