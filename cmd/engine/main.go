package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/containerd/containerd/pkg/seed" //nolint:staticcheck // SA1019 deprecated
	"github.com/containerd/containerd/sys"
	sddaemon "github.com/coreos/go-systemd/v22/daemon"
	"github.com/docker/docker/pkg/reexec"
	"github.com/gofrs/flock"
	"github.com/moby/buildkit/cmd/buildkitd/config"
	"github.com/moby/buildkit/util/apicaps"
	"github.com/moby/buildkit/util/appcontext"
	"github.com/moby/buildkit/util/bklog"
	"github.com/moby/buildkit/util/disk"
	"github.com/moby/buildkit/util/profiler"
	"github.com/moby/buildkit/util/stack"
	"github.com/moby/buildkit/version"
	"github.com/moby/sys/userns"
	sloglogrus "github.com/samber/slog-logrus/v2"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/engine/buildkit/cacerts"
	"github.com/dagger/dagger/engine/server"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/network"
	"github.com/dagger/dagger/network/netinst"
)

func init() {
	apicaps.ExportedProduct = "buildkit"
	stack.SetVersionInfo(version.Version, version.Revision)

	//nolint:staticcheck // SA1019 deprecated
	seed.WithTimeAndRand()
	if reexec.Init() {
		os.Exit(0)
	}
}

func addFlags(app *cli.App) {
	defaultConf, err := defaultConf()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%+v\n", err)
		os.Exit(1)
	}

	rootlessUsage := "set all the default options to be compatible with rootless containers"
	if userns.RunningInUserNS() {
		app.Flags = append(app.Flags, cli.BoolTFlag{
			Name:  "rootless",
			Usage: rootlessUsage + " (default: true)",
		})
	} else {
		app.Flags = append(app.Flags, cli.BoolFlag{
			Name:  "rootless",
			Usage: rootlessUsage,
		})
	}

	groupValue := func(gid *int) string {
		if gid == nil {
			return ""
		}
		return strconv.Itoa(*gid)
	}

	app.Flags = append(app.Flags,
		cli.StringFlag{
			Name:  "config",
			Usage: "path to config file",
			Value: defaultConfigPath(),
		},
		cli.BoolFlag{
			Name:  "debug",
			Usage: "enable debug output in logs",
		},
		cli.BoolFlag{
			Name:  "extra-debug",
			Usage: "enable extra debug output in logs",
		},
		cli.BoolFlag{
			Name:  "trace",
			Usage: "enable trace output in logs (highly verbose, could affect performance)",
		},
		cli.StringFlag{
			Name:  "root",
			Usage: "path to state directory",
			Value: defaultConf.Root,
		},
		cli.StringSliceFlag{
			Name:  "addr",
			Usage: "listening address (socket or tcp)",
			Value: &cli.StringSlice{defaultConf.GRPC.Address[0]},
		},
		cli.StringFlag{
			Name:  "group",
			Usage: "group (name or gid) which will own all Unix socket listening addresses",
			Value: groupValue(defaultConf.GRPC.GID),
		},
		cli.StringFlag{
			Name:  "debugaddr",
			Usage: "debugging address (eg. 0.0.0.0:6060)",
			Value: defaultConf.GRPC.DebugAddress,
		},
		cli.StringFlag{
			Name:  "tlscert",
			Usage: "certificate file to use",
			Value: defaultConf.GRPC.TLS.Cert,
		},
		cli.StringFlag{
			Name:  "tlskey",
			Usage: "key file to use",
			Value: defaultConf.GRPC.TLS.Key,
		},
		cli.StringFlag{
			Name:  "tlscacert",
			Usage: "ca certificate to verify clients",
			Value: defaultConf.GRPC.TLS.CA,
		},
		cli.StringSliceFlag{
			Name:  "allow-insecure-entitlement",
			Usage: "allows insecure entitlements e.g. network.host, security.insecure",
		},
		cli.StringFlag{
			Name:  "network-name",
			Usage: "short name for the engine's container network; used for interface name",
			Value: network.DefaultName,
		},
		cli.StringFlag{
			Name:  "network-cidr",
			Usage: "address range to use for networked containers",
			Value: network.DefaultCIDR,
		},
		cli.StringSliceFlag{
			Name:  "oci-worker-labels",
			Usage: "user-specific annotation labels (com.example.foo=bar)",
		},
		cli.StringFlag{
			Name:  "oci-worker-snapshotter",
			Usage: "name of snapshotter (overlayfs, native, etc.)",
			Value: defaultConf.Workers.OCI.Snapshotter,
		},
		cli.StringFlag{
			Name:  "oci-worker-proxy-snapshotter-path",
			Usage: "address of proxy snapshotter socket (do not include 'unix://' prefix)",
		},
		cli.StringSliceFlag{
			Name:  "oci-worker-platform",
			Usage: "override supported platforms for worker",
		},
		cli.StringFlag{
			Name:  "oci-worker-net",
			Usage: "worker network type (auto, cni or host)",
			Value: defaultConf.Workers.OCI.NetworkConfig.Mode,
		},
		cli.StringFlag{
			Name:  "oci-cni-config-path",
			Usage: "path of cni config file",
			Value: defaultConf.Workers.OCI.NetworkConfig.CNIConfigPath,
		},
		cli.StringFlag{
			Name:  "oci-cni-binary-dir",
			Usage: "path of cni binary files",
			Value: defaultConf.Workers.OCI.NetworkConfig.CNIBinaryPath,
		},
		cli.IntFlag{
			Name:  "oci-cni-pool-size",
			Usage: "size of cni network namespace pool",
			Value: defaultConf.Workers.OCI.NetworkConfig.CNIPoolSize,
		},
		cli.StringFlag{
			Name:  "oci-worker-binary",
			Usage: "name of specified oci worker binary",
			Value: defaultConf.Workers.OCI.Binary,
		},
		cli.StringFlag{
			Name:  "oci-worker-apparmor-profile",
			Usage: "set the name of the apparmor profile applied to containers",
		},
		cli.BoolFlag{
			Name:  "oci-worker-selinux",
			Usage: "apply SELinux labels",
		},
		cli.StringFlag{
			Name:  "oci-max-parallelism",
			Usage: "maximum number of parallel build steps that can be run at the same time (or \"num-cpu\" to automatically set to the number of CPUs). 0 means unlimited parallelism.",
		},
		cli.StringFlag{
			Name:  "oci-worker-gc-keepstorage",
			Usage: "Amount of storage GC keep locally, format \"Reserved[,Free[,Maximum]]\" (MB)",
			Value: func() string {
				cfg := defaultConf.Workers.OCI.GCConfig
				dstat, err := disk.GetDiskStat(defaultConf.Root)
				if err != nil {
					panic(err)
				}
				return gcConfigToString(cfg, dstat)
			}(),
			Hidden: len(defaultConf.Workers.OCI.GCPolicy) != 0,
		},
	)

	if defaultConf.Workers.OCI.GC == nil || *defaultConf.Workers.OCI.GC {
		app.Flags = append(app.Flags, cli.BoolTFlag{
			Name:  "oci-worker-gc",
			Usage: "Enable automatic garbage collection on worker",
		})
	} else {
		app.Flags = append(app.Flags, cli.BoolFlag{
			Name:  "oci-worker-gc",
			Usage: "Enable automatic garbage collection on worker",
		})
	}
}

func main() { //nolint:gocyclo
	cli.VersionPrinter = func(c *cli.Context) {
		fmt.Println(c.App.Name, version.Package, c.App.Version, version.Revision)
	}
	app := cli.NewApp()
	app.Name = "buildkitd"
	app.Usage = "build daemon"
	app.Version = version.Version

	addFlags(app)

	ctx, cancel := context.WithCancelCause(appcontext.Context())

	app.Action = func(c *cli.Context) error {
		defer cancel(errors.New("main done"))
		// TODO: On Windows this always returns -1. The actual "are you admin" check is very Windows-specific.
		// See https://github.com/golang/go/issues/28804#issuecomment-505326268 for the "short" version.
		if os.Geteuid() > 0 {
			return errors.New("rootless mode requires to be executed as the mapped root in a user namespace; you may use RootlessKit for setting up the namespace")
		}
		// install CA certs in case the user has a custom engine w/ extra certs installed to
		// /usr/local/share/ca-certificates
		if out, err := exec.CommandContext(ctx, "update-ca-certificates").CombinedOutput(); err != nil {
			bklog.G(ctx).WithError(err).Warnf("failed to update ca-certificates: %s", out)
		} else {
			//nolint:gosec // it thinks we're using untrusted input even though we're only using consts here...?
			if out, err := exec.CommandContext(ctx, "c_rehash", cacerts.EngineCustomCACertsDir).CombinedOutput(); err != nil {
				bklog.G(ctx).WithError(err).Warnf("failed to rehash ca-certificates: %s", out)
			}
		}

		ctx = InitTelemetry(ctx)

		bklog.G(ctx).Debug("loading engine config file")
		cfg, err := config.LoadFile(c.GlobalString("config"))
		if err != nil {
			return err
		}

		bklog.G(ctx).Debug("setting up engine networking")
		networkContext, cancelNetworking := context.WithCancelCause(context.Background())
		defer cancelNetworking(errors.New("main done"))
		netConf, err := setupNetwork(networkContext,
			c.GlobalString("network-name"),
			c.GlobalString("network-cidr"),
		)
		if err != nil {
			return err
		}

		bklog.G(ctx).Debug("setting engine configs from defaults and flags")
		setDefaultConfig(&cfg, netConf)

		if err := applyMainFlags(c, &cfg); err != nil {
			return err
		}

		logrus.SetFormatter(&logrus.TextFormatter{FullTimestamp: true})

		// Wire slog up to send to Logrus so engine logs using slog also get sent
		// to Cloud
		slogOpts := sloglogrus.Option{}

		noiseReduceHook := &noiseReductionHook{
			ignoreLogger: logrus.New(),
		}
		noiseReduceHook.ignoreLogger.SetOutput(io.Discard)

		switch {
		case cfg.Trace:
			slogOpts.Level = slog.LevelTrace
			logrus.SetLevel(logrus.TraceLevel)
			// don't add noise reduction hook for trace level
		case c.IsSet("extra-debug"):
			slogOpts.Level = slog.LevelExtraDebug
			logrus.SetLevel(logrus.DebugLevel)
			// don't add noise reduction hook for extra debug level
		case cfg.Debug:
			slogOpts.Level = slog.LevelDebug
			logrus.SetLevel(logrus.DebugLevel)
			logrus.AddHook(noiseReduceHook)
		default:
			logrus.AddHook(noiseReduceHook)
		}

		sloglogrus.LogLevels[slog.LevelExtraDebug] = logrus.DebugLevel
		sloglogrus.LogLevels[slog.LevelTrace] = logrus.TraceLevel
		slog.SetDefault(slog.New(slogOpts.NewLogrusHandler()))

		bklog.G(context.Background()).Debugf("engine name: %s", engineName)

		if cfg.GRPC.DebugAddress != "" {
			if err := setupDebugHandlers(cfg.GRPC.DebugAddress); err != nil {
				return err
			}
		}

		bklog.G(ctx).Debug("creating engine GRPC server")
		grpcServer := grpc.NewServer(grpc.StatsHandler(otelgrpc.NewServerHandler()))

		// relative path does not work with nightlyone/lockfile
		root, err := filepath.Abs(cfg.Root)
		if err != nil {
			return err
		}
		cfg.Root = root

		if err := os.MkdirAll(root, 0o700); err != nil {
			return fmt.Errorf("failed to create %s: %w", root, err)
		}

		bklog.G(ctx).Debug("creating engine lockfile")
		lockPath := filepath.Join(root, "buildkitd.lock")
		lock := flock.New(lockPath)
		locked, err := lock.TryLock()
		if err != nil {
			return fmt.Errorf("could not lock %s: %w", lockPath, err)
		}
		if !locked {
			return fmt.Errorf("could not lock %s, another instance running?", lockPath)
		}
		defer func() {
			lock.Unlock()
			os.RemoveAll(lockPath)
		}()

		ents := c.GlobalStringSlice("allow-insecure-entitlement")
		if len(ents) > 0 {
			cfg.Entitlements = []string{}
			for _, e := range ents {
				switch e {
				case "security.insecure":
					cfg.Entitlements = append(cfg.Entitlements, e)
				case "network.host":
					cfg.Entitlements = append(cfg.Entitlements, e)
				default:
					return fmt.Errorf("invalid entitlement : %s", e)
				}
			}
		}

		bklog.G(ctx).Debug("creating engine server")
		srv, err := server.NewServer(ctx, &server.NewServerOpts{
			Config: &cfg,
			Name:   engineName,
		})
		if err != nil {
			return fmt.Errorf("failed to create engine: %w", err)
		}
		defer srv.Close()

		go logMetrics(context.Background(), cfg.Root, srv)
		if cfg.Trace {
			go logTraceMetrics(context.Background())
		}

		bklog.G(ctx).Debug("starting optional cache mount synchronization")
		err = srv.SolverCache.StartCacheMountSynchronization(ctx)
		if err != nil {
			bklog.G(ctx).WithError(err).Error("failed to start cache mount synchronization")
			// continue on, doesn't need to be fatal
		}

		// start serving on the listeners for actual clients
		bklog.G(ctx).Debug("starting main engine api listeners")
		srv.Register(grpcServer)
		http2Server := &http2.Server{}
		httpServer := &http.Server{
			ReadHeaderTimeout: 30 * time.Second,
			Handler: h2c.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.ProtoMajor == 2 && strings.HasPrefix(r.Header.Get("content-type"), "application/grpc") {
					// The docs on grpcServer.ServeHTTP warn that some features are missing vs. serving fully "native" gRPC,
					// but in practice it seems to work fine for us and only be relevant for some advanced features we don't use.
					grpcServer.ServeHTTP(w, r)
					return
				}
				srv.ServeHTTP(w, r)
			}), http2Server),
		}
		if err := http2.ConfigureServer(httpServer, http2Server); err != nil {
			return fmt.Errorf("failed to configure http2 server: %w", err)
		}
		errCh := make(chan error, 1)
		if err := serveAPI(cfg.GRPC, httpServer, errCh); err != nil {
			return err
		}

		select {
		case serverErr := <-errCh:
			err = serverErr
			cancel(fmt.Errorf("server error: %w", serverErr))
		case <-ctx.Done():
			// context should only be cancelled when a signal is received, which
			// isn't an error
			if ctx.Err() != context.Canceled {
				err = ctx.Err()
			}
		}

		// TODO:(sipsma) make timeouts configurable
		bklog.G(ctx).Debug("stopping cache manager")
		stopCacheCtx, cancelCacheCtx := context.WithTimeout(context.Background(), 600*time.Second)
		defer cancelCacheCtx()
		stopCacheErr := srv.SolverCache.Close(stopCacheCtx)
		if stopCacheErr != nil {
			bklog.G(ctx).WithError(stopCacheErr).Error("failed to stop cache")
		}
		err = errors.Join(err, stopCacheErr)
		cancelNetworking(errors.New("shutdown"))

		bklog.G(ctx).Infof("stopping server")
		if os.Getenv("NOTIFY_SOCKET") != "" {
			notified, notifyErr := sddaemon.SdNotify(false, sddaemon.SdNotifyStopping)
			bklog.G(ctx).Debugf("SdNotifyStopping notified=%v, err=%v", notified, notifyErr)
		}
		grpcServer.GracefulStop()
		return err
	}

	app.After = func(*cli.Context) error {
		telemetry.Close()
		return nil
	}

	profiler.Attach(app)

	if err := app.Run(os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "buildkitd: %+v\n", err)
		os.Exit(1)
	}
}

func serveAPI(
	cfg config.GRPCConfig,
	httpServer *http.Server,
	errCh chan error,
) error {
	addrs := cfg.Address
	if len(addrs) == 0 {
		return errors.New("--addr cannot be empty")
	}
	tlsConfig, err := serverCredentials(cfg.TLS)
	if err != nil {
		return err
	}
	eg, _ := errgroup.WithContext(context.Background())
	listeners := make([]net.Listener, 0, len(addrs))
	for _, addr := range addrs {
		l, err := getListener(addr, *cfg.UID, *cfg.GID, tlsConfig)
		if err != nil {
			for _, l := range listeners {
				l.Close()
			}
			return err
		}
		listeners = append(listeners, l)
	}

	if os.Getenv("NOTIFY_SOCKET") != "" {
		notified, notifyErr := sddaemon.SdNotify(false, sddaemon.SdNotifyReady)
		logrus.Debugf("SdNotifyReady notified=%v, err=%v", notified, notifyErr)
	}
	for _, l := range listeners {
		func(l net.Listener) {
			eg.Go(func() error {
				defer l.Close()
				logrus.Infof("running server on %s", l.Addr())

				err := httpServer.Serve(l)
				if err != nil {
					return fmt.Errorf("serve: %w", err)
				}
				return nil
			})
		}(l)
	}
	go func() {
		errCh <- eg.Wait()
	}()
	return nil
}

//nolint:gocyclo
func applyMainFlags(c *cli.Context, cfg *config.Config) error {
	if c.IsSet("debug") {
		cfg.Debug = c.Bool("debug")
	}
	if c.IsSet("trace") {
		cfg.Trace = c.Bool("trace")
	}
	if c.IsSet("root") {
		cfg.Root = c.String("root")
	}

	if c.IsSet("addr") || len(cfg.GRPC.Address) == 0 {
		cfg.GRPC.Address = c.StringSlice("addr")
	}

	if c.IsSet("allow-insecure-entitlement") {
		// override values from config
		cfg.Entitlements = c.StringSlice("allow-insecure-entitlement")
	}

	if c.IsSet("debugaddr") {
		cfg.GRPC.DebugAddress = c.String("debugaddr")
	}

	if cfg.GRPC.UID == nil {
		uid := os.Getuid()
		cfg.GRPC.UID = &uid
	}

	if cfg.GRPC.GID == nil {
		gid := os.Getgid()
		cfg.GRPC.GID = &gid
	}

	if group := c.String("group"); group != "" {
		gid, err := grouptoGID(group)
		if err != nil {
			return err
		}
		cfg.GRPC.GID = &gid
	}

	if tlscert := c.String("tlscert"); tlscert != "" {
		cfg.GRPC.TLS.Cert = tlscert
	}
	if tlskey := c.String("tlskey"); tlskey != "" {
		cfg.GRPC.TLS.Key = tlskey
	}
	if tlsca := c.String("tlscacert"); tlsca != "" {
		cfg.GRPC.TLS.CA = tlsca
	}

	labels, err := attrMap(c.GlobalStringSlice("oci-worker-labels"))
	if err != nil {
		return err
	}
	if cfg.Workers.OCI.Labels == nil {
		cfg.Workers.OCI.Labels = make(map[string]string)
	}
	for k, v := range labels {
		cfg.Workers.OCI.Labels[k] = v
	}

	if c.GlobalIsSet("oci-worker-snapshotter") {
		cfg.Workers.OCI.Snapshotter = c.GlobalString("oci-worker-snapshotter")
	}

	if c.GlobalIsSet("rootless") || c.GlobalBool("rootless") {
		cfg.Workers.OCI.Rootless = c.GlobalBool("rootless")
	}
	if c.GlobalIsSet("oci-worker-rootless") {
		if !userns.RunningInUserNS() || os.Geteuid() > 0 {
			return errors.New("rootless mode requires to be executed as the mapped root in a user namespace; you may use RootlessKit for setting up the namespace")
		}
		cfg.Workers.OCI.Rootless = c.GlobalBool("oci-worker-rootless")
	}
	if c.GlobalIsSet("oci-worker-no-process-sandbox") {
		cfg.Workers.OCI.NoProcessSandbox = c.GlobalBool("oci-worker-no-process-sandbox")
	}

	if platforms := c.GlobalStringSlice("oci-worker-platform"); len(platforms) != 0 {
		cfg.Workers.OCI.Platforms = platforms
	}

	if c.GlobalIsSet("oci-worker-gc") {
		v := c.GlobalBool("oci-worker-gc")
		cfg.Workers.OCI.GC = &v
	}

	if c.GlobalIsSet("oci-worker-gc-keepstorage") {
		gc, err := stringToGCConfig(c.GlobalString("oci-worker-gc-keepstorage"))
		if err != nil {
			return err
		}
		cfg.Workers.OCI.GCReservedSpace = gc.GCReservedSpace
		cfg.Workers.OCI.GCMaxUsedSpace = gc.GCMaxUsedSpace
		cfg.Workers.OCI.GCMinFreeSpace = gc.GCMinFreeSpace
	}

	if c.GlobalIsSet("oci-worker-net") {
		cfg.Workers.OCI.NetworkConfig.Mode = c.GlobalString("oci-worker-net")
	}
	if c.GlobalIsSet("oci-cni-config-path") {
		cfg.Workers.OCI.NetworkConfig.CNIConfigPath = c.GlobalString("oci-cni-worker-path")
	}
	if c.GlobalIsSet("oci-cni-binary-dir") {
		cfg.Workers.OCI.NetworkConfig.CNIBinaryPath = c.GlobalString("oci-cni-binary-dir")
	}
	if c.GlobalIsSet("oci-cni-pool-size") {
		cfg.Workers.OCI.NetworkConfig.CNIPoolSize = c.GlobalInt("oci-cni-pool-size")
	}
	if c.GlobalIsSet("oci-worker-binary") {
		cfg.Workers.OCI.Binary = c.GlobalString("oci-worker-binary")
	}
	if c.GlobalIsSet("oci-worker-proxy-snapshotter-path") {
		cfg.Workers.OCI.ProxySnapshotterPath = c.GlobalString("oci-worker-proxy-snapshotter-path")
	}
	if c.GlobalIsSet("oci-worker-apparmor-profile") {
		cfg.Workers.OCI.ApparmorProfile = c.GlobalString("oci-worker-apparmor-profile")
	}
	if c.GlobalIsSet("oci-worker-selinux") {
		cfg.Workers.OCI.SELinux = c.GlobalBool("oci-worker-selinux")
	}
	if c.GlobalIsSet("oci-max-parallelism") {
		maxParallelismStr := c.GlobalString("oci-max-parallelism")
		var maxParallelism int
		if maxParallelismStr == "num-cpu" {
			maxParallelism = runtime.NumCPU()
		} else {
			maxParallelism, err = strconv.Atoi(maxParallelismStr)
			if err != nil {
				return fmt.Errorf("failed to parse oci-max-parallelism, should be positive integer, 0 for unlimited, or 'num-cpu' for setting to the number of CPUs: %w", err)
			}
		}
		cfg.Workers.OCI.MaxParallelism = maxParallelism
	}

	return nil
}

// Convert a string containing either a group name or a stringified gid into a numeric id)
func grouptoGID(group string) (int, error) {
	if group == "" {
		return os.Getgid(), nil
	}

	var (
		err error
		id  int
	)

	// Try and parse as a number, if the error is ErrSyntax
	// (i.e. its not a number) then we carry on and try it as a
	// name.
	if id, err = strconv.Atoi(group); err == nil {
		return id, nil
	} else if err.(*strconv.NumError).Err != strconv.ErrSyntax {
		return 0, err
	}

	ginfo, err := user.LookupGroup(group)
	if err != nil {
		return 0, err
	}
	group = ginfo.Gid

	if id, err = strconv.Atoi(group); err != nil {
		return 0, err
	}

	return id, nil
}

func getListener(addr string, uid, gid int, tlsConfig *tls.Config) (net.Listener, error) {
	addrSlice := strings.SplitN(addr, "://", 2)
	if len(addrSlice) < 2 {
		return nil, fmt.Errorf("address %s does not contain proto, you meant unix://%s ?",
			addr, addr)
	}
	proto := addrSlice[0]
	listenAddr := addrSlice[1]
	switch proto {
	case "unix", "npipe":
		if tlsConfig != nil {
			logrus.Warnf("TLS is disabled for %s", addr)
		}
		return sys.GetLocalListener(listenAddr, uid, gid)
	case "fd":
		return listenFD(listenAddr, tlsConfig)
	case "tcp":
		l, err := net.Listen("tcp", listenAddr)
		if err != nil {
			return nil, err
		}

		if tlsConfig == nil {
			logrus.Warnf("TLS is not enabled for %s. enabling mutual TLS authentication is highly recommended", addr)
			return l, nil
		}
		return tls.NewListener(l, tlsConfig), nil
	default:
		return nil, fmt.Errorf("addr %s not supported", addr)
	}
}

func serverCredentials(cfg config.TLSConfig) (*tls.Config, error) {
	certFile := cfg.Cert
	keyFile := cfg.Key
	caFile := cfg.CA
	if certFile == "" && keyFile == "" {
		return nil, nil
	}
	err := errors.New("you must specify key and cert file if one is specified")
	if certFile == "" {
		return nil, err
	}
	if keyFile == "" {
		return nil, err
	}
	certificate, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("could not load server key pair: %w", err)
	}
	tlsConf := &tls.Config{
		Certificates: []tls.Certificate{certificate},
		MinVersion:   tls.VersionTLS12,
	}
	if caFile != "" {
		certPool := x509.NewCertPool()
		ca, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("could not read ca certificate: %w", err)
		}
		// Append the client certificates from the CA
		if ok := certPool.AppendCertsFromPEM(ca); !ok {
			return nil, errors.New("failed to append ca cert")
		}
		tlsConf.ClientAuth = tls.RequireAndVerifyClientCert
		tlsConf.ClientCAs = certPool
	}
	return tlsConf, nil
}

func attrMap(sl []string) (map[string]string, error) {
	m := map[string]string{}
	for _, v := range sl {
		parts := strings.SplitN(v, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid value %s", v)
		}
		m[parts[0]] = parts[1]
	}
	return m, nil
}

type networkConfig struct {
	NetName       string
	NetCIDR       string
	Bridge        net.IP
	CNIConfigPath string
}

func setupNetwork(ctx context.Context, netName, netCIDR string) (*networkConfig, error) {
	bridge, err := network.BridgeFromCIDR(netCIDR)
	if err != nil {
		return nil, fmt.Errorf("bridge from cidr: %w", err)
	}

	// NB: this is needed for the Dagger shim worker at the moment for host alias
	// resolution
	err = netinst.InstallResolvconf(netName, bridge.String())
	if err != nil {
		return nil, fmt.Errorf("install resolv.conf: %w", err)
	}

	err = netinst.InstallDnsmasq(ctx, netName)
	if err != nil {
		return nil, fmt.Errorf("install dnsmasq: %w", err)
	}

	cniConfigPath, err := netinst.InstallCNIConfig(netName, netCIDR)
	if err != nil {
		return nil, fmt.Errorf("install cni: %w", err)
	}

	return &networkConfig{
		NetName:       netName,
		NetCIDR:       netCIDR,
		Bridge:        bridge,
		CNIConfigPath: cniConfigPath,
	}, nil
}
