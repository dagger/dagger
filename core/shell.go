package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/network"
	"github.com/gorilla/websocket"
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/identity"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/util/bklog"
	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"
)

func (container *Container) ShellEndpoint(bk *buildkit.Client, progSock string) (string, http.Handler, error) {
	shellID := identity.NewID()
	endpoint := "shells/" + shellID
	return endpoint, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// TODO:
		bklog.G(r.Context()).Debugf("SHELL HANDLER FOR %s", endpoint)

		clientMetadata, err := engine.ClientMetadataFromContext(r.Context())
		if err != nil {
			panic(err)
		}

		var upgrader = websocket.Upgrader{} // TODO: timeout?
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			// FIXME: send error
			panic(err)
		}
		defer ws.Close()

		// TODO:
		bklog.G(context.TODO()).Debugf("SHELL HANDLER FOR %s HAS BEEN UPGRADED", endpoint)

		if err := container.runShell(r.Context(), ws, bk, progSock, clientMetadata); err != nil {
			bklog.G(r.Context()).WithError(err).Error("shell handler failed")
		}
	}), nil
}

var (
	// TODO:dedupe w/ same thing in cmd/dagger
	stdinPrefix  = []byte{0, byte(',')}
	stdoutPrefix = []byte{1, byte(',')}
	stderrPrefix = []byte{2, byte(',')}
	resizePrefix = []byte("resize,")
	exitPrefix   = []byte("exit,")
)

func (container *Container) runShell(
	ctx context.Context,
	conn *websocket.Conn,
	bk *buildkit.Client,
	progSock string,
	clientMetadata *engine.ClientMetadata,
) error {
	host, err := container.HostnameOrErr()
	if err != nil {
		return err
	}

	// TODO:
	opts := ContainerExecOpts{
		// TODO: use default args if set
		Args:                          []string{"/bin/sh"},
		SkipEntrypoint:                true,
		ExperimentalPrivilegedNesting: true,
	}

	cfg := container.Config

	_, err = WithServices(ctx, bk, container.Services, func() (_ any, rerr error) {
		// TODO:
		defer func() {
			if rerr != nil {
				bklog.G(ctx).WithError(rerr).Error("runShell failed")
			}
		}()

		mounts, err := container.mounts(ctx, bk)
		if err != nil {
			return nil, fmt.Errorf("mounts: %w", err)
		}

		args, err := container.command(opts)
		if err != nil {
			return nil, fmt.Errorf("command: %w", err)
		}

		env := container.env(opts)

		metaEnv, err := ContainerExecUncachedMetadata{
			ParentClientIDs: clientMetadata.ClientIDs(),
			ServerID:        clientMetadata.ServerID,
			ProgSockPath:    progSock,
		}.ToEnv()
		if err != nil {
			return nil, fmt.Errorf("uncached metadata: %w", err)
		}
		env = append(env, metaEnv...)

		secretEnv, mounts, env, err := container.secrets(mounts, env)
		if err != nil {
			return nil, fmt.Errorf("secrets: %w", err)
		}

		var securityMode pb.SecurityMode
		if opts.InsecureRootCapabilities {
			securityMode = pb.SecurityMode_INSECURE
		}

		fullHost := host + "." + network.ClientDomain(clientMetadata.ClientID)

		pbPlatform := pb.PlatformFromSpec(container.Platform)

		// TODO: gc, err := bk.NewContainer(ctx, bkgw.NewContainerRequest{
		gc, err := bk.NewContainer(context.TODO(), bkgw.NewContainerRequest{
			Mounts:   mounts,
			Hostname: fullHost,
			Platform: &pbPlatform,
		})
		if err != nil {
			return nil, fmt.Errorf("new container: %w", err)
		}

		defer func() {
			if err != nil {
				gc.Release(context.Background())
			}
		}()

		stdinCtr, stdinClient := io.Pipe()
		stdoutClient, stdoutCtr := io.Pipe()
		stderrClient, stderrCtr := io.Pipe()

		eg, egctx := errgroup.WithContext(ctx)

		// forward a io.Reader to websocket
		forwardFD := func(r io.Reader, prefix []byte) error {
			for {
				b := make([]byte, 512)
				n, err := r.Read(b)
				if err != nil {
					if err == io.EOF {
						return nil
					}

					return err
				}
				message := append([]byte{}, prefix...)
				message = append(message, b[:n]...)
				err = conn.WriteMessage(websocket.BinaryMessage, message)
				if err != nil {
					return err
				}
			}
		}

		// TODO:
		env = append(env, "HACK_TO_PASS_TTY_THROUGH=1")
		svcProc, err := gc.Start(ctx, bkgw.StartRequest{
			Args:         args,
			Env:          env,
			SecretEnv:    secretEnv,
			User:         cfg.User,
			Cwd:          cfg.WorkingDir,
			Tty:          true,
			Stdin:        stdinCtr,
			Stdout:       stdoutCtr,
			Stderr:       stderrCtr,
			SecurityMode: securityMode,
		})
		if err != nil {
			return nil, fmt.Errorf("start container: %w", err)
		}

		// stream stdout
		eg.Go(func() error {
			return forwardFD(stdoutClient, stdoutPrefix)
		})

		// stream stderr
		eg.Go(func() error {
			return forwardFD(stderrClient, stderrPrefix)
		})

		// handle stdin
		eg.Go(func() error {
			for {
				_, buff, err := conn.ReadMessage()
				if err != nil {
					return err
				}
				switch {
				case bytes.HasPrefix(buff, stdinPrefix):
					_, err = stdinClient.Write(bytes.TrimPrefix(buff, stdinPrefix))
					if err != nil {
						return err
					}
				case bytes.HasPrefix(buff, resizePrefix):
					sizeMessage := string(bytes.TrimPrefix(buff, resizePrefix))
					size := strings.SplitN(sizeMessage, ";", 2)
					cols, err := strconv.Atoi(size[0])
					if err != nil {
						return err
					}
					rows, err := strconv.Atoi(size[1])
					if err != nil {
						return err
					}

					svcProc.Resize(egctx, bkgw.WinSize{Rows: uint32(rows), Cols: uint32(cols)})
				default:
					// FIXME: send error message
					panic("invalid message")
				}
			}
		})

		stopSvc := func(ctx context.Context) error {
			// TODO(vito): graceful shutdown?
			if err := svcProc.Signal(ctx, syscall.SIGKILL); err != nil {
				return fmt.Errorf("signal: %w", err)
			}

			if err := gc.Release(ctx); err != nil {
				// TODO(vito): returns context.Canceled, which is a bit strange, because
				// that's the goal
				if !errors.Is(err, context.Canceled) {
					return fmt.Errorf("release: %w", err)
				}
			}

			return nil
		}
		defer stopSvc(context.TODO())

		// handle shutdown
		eg.Go(func() error {
			waitErr := svcProc.Wait()
			var exitCode int
			if waitErr != nil {
				// TODO:
				exitCode = 1
			}

			message := append([]byte{}, exitPrefix...)
			message = append(message, []byte(fmt.Sprintf("%d", exitCode))...)
			err := conn.WriteMessage(websocket.BinaryMessage, message)
			if err != nil {
				// FIXME: send error message
				panic(err)
			}
			err = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			if err != nil {
				// FIXME: send error message
				panic(err)
			}
			time.Sleep(3 * time.Second) // TODO: I forget if I copy-pasted this or added it, check if load-bearing
			conn.Close()
			return err
		})

		return nil, eg.Wait()
	})
	if err != nil {
		return err
	}
	return nil
}

// TODO: all the below is 99% copy-pasted from @vito's service v2 code, de-dupe
func (container *Container) command(opts ContainerExecOpts) ([]string, error) {
	cfg := container.Config
	args := opts.Args

	if len(args) == 0 {
		// we use the default args if no new default args are passed
		args = cfg.Cmd
	}

	if len(cfg.Entrypoint) > 0 && !opts.SkipEntrypoint {
		args = append(cfg.Entrypoint, args...)
	}

	if len(args) == 0 {
		return nil, errors.New("no command has been set")
	}

	return args, nil
}

func (container *Container) mounts(ctx context.Context, bk *buildkit.Client) ([]bkgw.Mount, error) {
	fsRef, err := solveRef(ctx, bk, container.FS)
	if err != nil {
		return nil, err
	}

	mounts := []bkgw.Mount{
		{
			Dest:      "/",
			MountType: pb.MountType_BIND,
			Ref:       fsRef,
		},
	}

	metaSt, metaSourcePath := metaMount("")

	metaDef, err := metaSt.Marshal(ctx, llb.Platform(container.Platform))
	if err != nil {
		return nil, err
	}

	metaRef, err := solveRef(ctx, bk, metaDef.ToPB())
	if err != nil {
		return nil, err
	}

	mounts = append(mounts, bkgw.Mount{
		Dest:     buildkit.MetaMountDestPath,
		Ref:      metaRef,
		Selector: metaSourcePath,
	})

	for _, mnt := range container.Mounts {
		mount := bkgw.Mount{
			Dest:      mnt.Target,
			MountType: pb.MountType_BIND,
		}

		if mnt.Source != nil {
			srcRef, err := solveRef(ctx, bk, mnt.Source)
			if err != nil {
				return nil, fmt.Errorf("mount %s: %w", mnt.Target, err)
			}

			mount.Ref = srcRef
		}

		if mnt.SourcePath != "" {
			mount.Selector = mnt.SourcePath
		}

		if mnt.CacheID != "" {
			mount.MountType = pb.MountType_CACHE
			mount.CacheOpt = &pb.CacheOpt{
				ID: mnt.CacheID,
			}

			switch CacheSharingMode(mnt.CacheSharingMode) {
			case CacheSharingModeShared:
				mount.CacheOpt.Sharing = pb.CacheSharingOpt_SHARED
			case CacheSharingModePrivate:
				mount.CacheOpt.Sharing = pb.CacheSharingOpt_PRIVATE
			case CacheSharingModeLocked:
				mount.CacheOpt.Sharing = pb.CacheSharingOpt_LOCKED
			default:
				return nil, errors.Errorf("invalid cache mount sharing mode %q", mnt.CacheSharingMode)
			}
		}

		if mnt.Tmpfs {
			mount.MountType = pb.MountType_TMPFS
		}

		mounts = append(mounts, mount)
	}

	for _, ctrSocket := range container.Sockets {
		if ctrSocket.UnixPath == "" {
			return nil, fmt.Errorf("unsupported socket: only unix paths are implemented")
		}

		opt := &pb.SSHOpt{
			ID: ctrSocket.SocketID.String(),
		}

		if ctrSocket.Owner != nil {
			opt.Uid = uint32(ctrSocket.Owner.UID)
			opt.Gid = uint32(ctrSocket.Owner.UID)
			opt.Mode = 0o600 // preserve default
		}

		mounts = append(mounts, bkgw.Mount{
			Dest:      ctrSocket.UnixPath,
			MountType: pb.MountType_SSH,
			SSHOpt:    opt,
		})
	}

	return mounts, nil
}

func (container *Container) env(opts ContainerExecOpts) []string {
	cfg := container.Config

	env := []string{}

	for _, e := range cfg.Env {
		// strip out any env that are meant for internal use only, to prevent
		// manually setting them
		switch {
		case strings.HasPrefix(e, "_DAGGER_ENABLE_NESTING="):
		default:
			env = append(env, e)
		}
	}

	if opts.ExperimentalPrivilegedNesting {
		env = append(env, "_DAGGER_ENABLE_NESTING=")
	}

	for svcID, aliases := range container.Services {
		svc, err := svcID.ToContainer()
		if err != nil {
			panic(err)
		}
		svcHostname, err := svc.HostnameOrErr()
		if err != nil {
			panic(err)
		}
		for _, alias := range aliases {
			env = append(env, "_DAGGER_HOSTNAME_ALIAS_"+alias+"="+svcHostname)
		}
	}

	return env
}

func (container *Container) secrets(mounts []bkgw.Mount, env []string) ([]*pb.SecretEnv, []bkgw.Mount, []string, error) {
	secretEnv := []*pb.SecretEnv{}
	secretsToScrub := SecretToScrubInfo{}
	for i, ctrSecret := range container.Secrets {
		switch {
		case ctrSecret.EnvName != "":
			secretsToScrub.Envs = append(secretsToScrub.Envs, ctrSecret.EnvName)
			secret, err := ctrSecret.Secret.ToSecret()
			if err != nil {
				return nil, nil, nil, err
			}
			secretEnv = append(secretEnv, &pb.SecretEnv{
				ID:   secret.Name,
				Name: ctrSecret.EnvName,
			})
		case ctrSecret.MountPath != "":
			secretsToScrub.Files = append(secretsToScrub.Files, ctrSecret.MountPath)
			opt := &pb.SecretOpt{}
			if ctrSecret.Owner != nil {
				opt.Uid = uint32(ctrSecret.Owner.UID)
				opt.Gid = uint32(ctrSecret.Owner.UID)
				opt.Mode = 0o400 // preserve default
			}
			mounts = append(mounts, bkgw.Mount{
				Dest:      ctrSecret.MountPath,
				MountType: pb.MountType_SECRET,
				SecretOpt: opt,
			})
		default:
			return nil, nil, nil, fmt.Errorf("malformed secret config at index %d", i)
		}
	}

	if len(secretsToScrub.Envs) != 0 || len(secretsToScrub.Files) != 0 {
		// we sort to avoid non-deterministic order that would break caching
		sort.Strings(secretsToScrub.Envs)
		sort.Strings(secretsToScrub.Files)

		secretsToScrubJSON, err := json.Marshal(secretsToScrub)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("scrub secrets json: %w", err)
		}
		env = append(env, "_DAGGER_SCRUB_SECRETS="+string(secretsToScrubJSON))
	}

	return secretEnv, mounts, env, nil
}

func metaMount(stdin string) (llb.State, string) {
	// because the shim might run as non-root, we need to make a world-writable
	// directory first and then make it the base of the /dagger mount point.
	//
	// TODO(vito): have the shim exec as the other user instead?
	meta := llb.Mkdir(buildkit.MetaSourcePath, 0o777)
	if stdin != "" {
		meta = meta.Mkfile(path.Join(buildkit.MetaSourcePath, "stdin"), 0o600, []byte(stdin))
	}

	return llb.Scratch().File(
			meta,
			llb.WithCustomName(buildkit.InternalPrefix+"creating dagger metadata"),
		),
		buildkit.MetaSourcePath
}

func solveRef(ctx context.Context, bk *buildkit.Client, def *pb.Definition) (bkgw.Reference, error) {
	res, err := bk.Solve(ctx, bkgw.SolveRequest{
		Definition: def,
	})
	if err != nil {
		return nil, err
	}

	// TODO(vito): is this needed anymore? had to deal with unwrapping at one point
	return res.SingleRef()
}
