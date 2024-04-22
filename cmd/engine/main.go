package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	goerrors "errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/containerd/containerd/pkg/seed" //nolint:staticcheck // SA1019 deprecated
	"github.com/containerd/containerd/pkg/userns"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/remotes/docker"
	"github.com/containerd/containerd/sys"
	sddaemon "github.com/coreos/go-systemd/v22/daemon"
	"github.com/docker/docker/pkg/reexec"
	"github.com/gofrs/flock"
	"github.com/moby/buildkit/cache/remotecache"
	"github.com/moby/buildkit/cache/remotecache/azblob"
	"github.com/moby/buildkit/cache/remotecache/gha"
	inlineremotecache "github.com/moby/buildkit/cache/remotecache/inline"
	localremotecache "github.com/moby/buildkit/cache/remotecache/local"
	registryremotecache "github.com/moby/buildkit/cache/remotecache/registry"
	s3remotecache "github.com/moby/buildkit/cache/remotecache/s3"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/cmd/buildkitd/config"
	"github.com/moby/buildkit/executor/oci"
	"github.com/moby/buildkit/frontend"
	dockerfile "github.com/moby/buildkit/frontend/dockerfile/builder"
	"github.com/moby/buildkit/frontend/gateway"
	"github.com/moby/buildkit/frontend/gateway/forwarder"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/solver"
	"github.com/moby/buildkit/solver/bboltcachestorage"
	"github.com/moby/buildkit/solver/llbsolver/mounts"
	"github.com/moby/buildkit/util/apicaps"
	"github.com/moby/buildkit/util/appcontext"
	"github.com/moby/buildkit/util/appdefaults"
	"github.com/moby/buildkit/util/archutil"
	"github.com/moby/buildkit/util/bklog"
	"github.com/moby/buildkit/util/profiler"
	"github.com/moby/buildkit/util/resolver"
	"github.com/moby/buildkit/util/stack"
	"github.com/moby/buildkit/version"
	"github.com/moby/buildkit/worker"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	sloglogrus "github.com/samber/slog-logrus/v2"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	logsv1 "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	metricsv1 "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"

	"github.com/dagger/dagger/cmd/engine/cacerts"
	"github.com/dagger/dagger/engine/cache"
	"github.com/dagger/dagger/engine/server"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/network"
	"github.com/dagger/dagger/network/netinst"
	"github.com/dagger/dagger/telemetry"
)

const (
	autoMode              = "auto"
	daggerCacheServiceURL = "https://api.dagger.cloud/magicache"
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

type workerInitializerOpt struct {
	config         *config.Config
	sessionManager *session.Manager
	traceSocket    string
}

type workerInitializer struct {
	fn func(c *cli.Context, common workerInitializerOpt) ([]worker.Worker, error)
	// less priority number, more preferred
	priority int
}

var (
	appFlags           []cli.Flag
	workerInitializers []workerInitializer
)

func registerWorkerInitializer(wi workerInitializer, flags ...cli.Flag) {
	workerInitializers = append(workerInitializers, wi)
	sort.Slice(workerInitializers,
		func(i, j int) bool {
			return workerInitializers[i].priority < workerInitializers[j].priority
		})
	appFlags = append(appFlags, flags...)
}

func main() { //nolint:gocyclo
	cli.VersionPrinter = func(c *cli.Context) {
		fmt.Println(c.App.Name, version.Package, c.App.Version, version.Revision)
	}
	app := cli.NewApp()
	app.Name = "buildkitd"
	app.Usage = "build daemon"
	app.Version = version.Version

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
	)
	app.Flags = append(app.Flags, appFlags...)

	app.Action = func(c *cli.Context) error {
		// TODO: On Windows this always returns -1. The actual "are you admin" check is very Windows-specific.
		// See https://github.com/golang/go/issues/28804#issuecomment-505326268 for the "short" version.
		if os.Geteuid() > 0 {
			return errors.New("rootless mode requires to be executed as the mapped root in a user namespace; you may use RootlessKit for setting up the namespace")
		}
		ctx, cancel := context.WithCancel(appcontext.Context())
		defer cancel()

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

		ctx, pubsub := InitTelemetry(ctx)

		bklog.G(ctx).Debug("loading engine config file")
		cfg, err := config.LoadFile(c.GlobalString("config"))
		if err != nil {
			return err
		}

		bklog.G(ctx).Debug("setting up engine networking")
		networkContext, cancelNetworking := context.WithCancel(context.Background())
		defer cancelNetworking()
		netConf, err := setupNetwork(networkContext,
			c.GlobalString("network-name"),
			c.GlobalString("network-cidr"),
		)
		if err != nil {
			return err
		}

		bklog.G(ctx).Debug("setting engine configs from defaults and flags")
		if err := setDaggerDefaults(&cfg, netConf); err != nil {
			return err
		}

		setDefaultConfig(&cfg)

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

		lf, err := os.OpenFile(filepath.Join(cfg.Root, "engine.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		defer lf.Close()
		logrus.SetOutput(lf)

		// TODO:
		// TODO:
		// TODO:
		lch := make(chan time.Time, 999999)
		go func() {
			for t := range time.Tick(200 * time.Millisecond) {
				lch <- t
			}
		}()
		go func() {
			i := 0
			for range lch {
				bklog.G(ctx).Debugf("LOG SENTINEL: %d", i)
				i++
			}
		}()

		if cfg.GRPC.DebugAddress != "" {
			if err := setupDebugHandlers(cfg.GRPC.DebugAddress); err != nil {
				return err
			}
		}

		bklog.G(ctx).Debug("creating engine GRPC server")
		server := grpc.NewServer(grpc.StatsHandler(otelgrpc.NewServerHandler()))

		// relative path does not work with nightlyone/lockfile
		root, err := filepath.Abs(cfg.Root)
		if err != nil {
			return err
		}
		cfg.Root = root

		if err := os.MkdirAll(root, 0o700); err != nil {
			return errors.Wrapf(err, "failed to create %s", root)
		}

		bklog.G(ctx).Debug("creating engine lockfile")
		lockPath := filepath.Join(root, "buildkitd.lock")
		lock := flock.New(lockPath)
		locked, err := lock.TryLock()
		if err != nil {
			return errors.Wrapf(err, "could not lock %s", lockPath)
		}
		if !locked {
			return errors.Errorf("could not lock %s, another instance running?", lockPath)
		}
		defer func() {
			lock.Unlock()
			os.RemoveAll(lockPath)
		}()

		bklog.G(ctx).Debug("creating engine controller")
		controller, cacheManager, err := newController(ctx, c, &cfg, pubsub)
		if err != nil {
			return err
		}
		defer controller.Close()

		controller.Register(server)

		go logMetrics(context.Background(), cfg.Root, controller)
		if cfg.Trace {
			go logTraceMetrics(context.Background())
		}

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
					return errors.Errorf("invalid entitlement : %s", e)
				}
			}
		}

		bklog.G(ctx).Debug("starting optional cache mount synchronization")
		err = cacheManager.StartCacheMountSynchronization(ctx)
		if err != nil {
			bklog.G(ctx).WithError(err).Error("failed to start cache mount synchronization")
			// continue on, doesn't need to be fatal
		}

		// start serving on the listeners for actual clients
		bklog.G(ctx).Debug("starting main engine grpc listeners")
		errCh := make(chan error, 1)
		if err := serveGRPC(cfg.GRPC, server, errCh); err != nil {
			return err
		}

		select {
		case serverErr := <-errCh:
			err = serverErr
			cancel()
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
		stopCacheErr := cacheManager.Close(stopCacheCtx)
		if stopCacheErr != nil {
			bklog.G(ctx).WithError(stopCacheErr).Error("failed to stop cache")
		}
		err = goerrors.Join(err, stopCacheErr)
		cancelNetworking()

		bklog.G(ctx).Infof("stopping server")
		if os.Getenv("NOTIFY_SOCKET") != "" {
			notified, notifyErr := sddaemon.SdNotify(false, sddaemon.SdNotifyStopping)
			bklog.G(ctx).Debugf("SdNotifyStopping notified=%v, err=%v", notified, notifyErr)
		}
		server.GracefulStop()
		return err
	}

	app.After = func(_ *cli.Context) error {
		CloseTelemetry()
		return nil
	}

	profiler.Attach(app)

	if err := app.Run(os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "buildkitd: %+v\n", err)
		os.Exit(1)
	}
}

func serveGRPC(cfg config.GRPCConfig, server *grpc.Server, errCh chan error) error {
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
				return server.Serve(l)
			})
		}(l)
	}
	go func() {
		errCh <- eg.Wait()
	}()
	return nil
}

func defaultConfigPath() string {
	if userns.RunningInUserNS() {
		return filepath.Join(appdefaults.UserConfigDir(), "buildkitd.toml")
	}
	return filepath.Join(appdefaults.ConfigDir, "buildkitd.toml")
}

func defaultConf() (config.Config, error) {
	cfg, err := config.LoadFile(defaultConfigPath())
	if err != nil {
		var pe *os.PathError
		if !errors.As(err, &pe) {
			return config.Config{}, err
		}
		logrus.Warnf("failed to load default config: %v", err)
	}
	setDefaultConfig(&cfg)

	return cfg, nil
}

func setDefaultNetworkConfig(nc config.NetworkConfig) config.NetworkConfig {
	if nc.Mode == "" {
		nc.Mode = autoMode
	}
	if nc.CNIConfigPath == "" {
		nc.CNIConfigPath = appdefaults.DefaultCNIConfigPath
	}
	if nc.CNIBinaryPath == "" {
		nc.CNIBinaryPath = appdefaults.DefaultCNIBinDir
	}
	return nc
}

func setDefaultConfig(cfg *config.Config) {
	orig := *cfg

	if cfg.Root == "" {
		cfg.Root = appdefaults.Root
	}

	if len(cfg.GRPC.Address) == 0 {
		cfg.GRPC.Address = []string{appdefaults.Address}
	}

	if cfg.Workers.OCI.Platforms == nil {
		cfg.Workers.OCI.Platforms = formatPlatforms(archutil.SupportedPlatforms(false))
	}
	if cfg.Workers.Containerd.Platforms == nil {
		cfg.Workers.Containerd.Platforms = formatPlatforms(archutil.SupportedPlatforms(false))
	}

	cfg.Workers.OCI.NetworkConfig = setDefaultNetworkConfig(cfg.Workers.OCI.NetworkConfig)
	cfg.Workers.Containerd.NetworkConfig = setDefaultNetworkConfig(cfg.Workers.Containerd.NetworkConfig)

	if userns.RunningInUserNS() {
		// if buildkitd is being executed as the mapped-root (not only EUID==0 but also $USER==root)
		// in a user namespace, we need to enable the rootless mode but
		// we don't want to honor $HOME for setting up default paths.
		if u := os.Getenv("USER"); u != "" && u != "root" {
			if orig.Root == "" {
				cfg.Root = appdefaults.UserRoot()
			}
			if len(orig.GRPC.Address) == 0 {
				cfg.GRPC.Address = []string{appdefaults.UserAddress()}
			}
			appdefaults.EnsureUserAddressDir()
		}
	}
}

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
		return nil, errors.Errorf("address %s does not contain proto, you meant unix://%s ?",
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
		return nil, errors.Errorf("addr %s not supported", addr)
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
		return nil, errors.Wrap(err, "could not load server key pair")
	}
	tlsConf := &tls.Config{
		Certificates: []tls.Certificate{certificate},
		MinVersion:   tls.VersionTLS12,
	}
	if caFile != "" {
		certPool := x509.NewCertPool()
		ca, err := os.ReadFile(caFile)
		if err != nil {
			return nil, errors.Wrap(err, "could not read ca certificate")
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

func newController(ctx context.Context, c *cli.Context, cfg *config.Config, pubsub *telemetry.PubSub) (*server.BuildkitController, cache.Manager, error) {
	sessionManager, err := session.NewManager()
	if err != nil {
		return nil, nil, err
	}

	var traceSocket string
	if pubsub != nil {
		traceSocket = filepath.Join(cfg.Root, "otel-grpc.sock")
		if err := runOtelController(traceSocket, pubsub); err != nil {
			logrus.Warnf("failed set up otel-grpc controller: %v", err)
			traceSocket = ""
		}
	}

	wc, err := newWorkerController(c, workerInitializerOpt{
		config:         cfg,
		sessionManager: sessionManager,
		traceSocket:    traceSocket,
	})
	if err != nil {
		return nil, nil, err
	}
	w, err := wc.GetDefault()
	if err != nil {
		return nil, nil, err
	}

	frontends := map[string]frontend.Frontend{}
	frontends["dockerfile.v0"] = forwarder.NewGatewayForwarder(wc.Infos(), dockerfile.Build)
	frontends["gateway.v0"] = gateway.NewGatewayFrontend(wc.Infos())

	cacheStorage, err := bboltcachestorage.NewStore(filepath.Join(cfg.Root, "cache.db"))
	if err != nil {
		return nil, nil, err
	}

	cacheServiceURL := os.Getenv("_EXPERIMENTAL_DAGGER_CACHESERVICE_URL")
	cacheServiceToken := os.Getenv("_EXPERIMENTAL_DAGGER_CACHESERVICE_TOKEN")

	// add DAGGER_CLOUD_TOKEN in a backwards compat way.
	// TODO: deprecate in a future release
	if v, ok := os.LookupEnv("DAGGER_CLOUD_TOKEN"); ok {
		cacheServiceToken = v
	}

	if cacheServiceURL == "" {
		cacheServiceURL = daggerCacheServiceURL
	}
	cacheManager, err := cache.NewManager(ctx, cache.ManagerConfig{
		KeyStore:     cacheStorage,
		ResultStore:  worker.NewCacheResultStorage(wc),
		Worker:       w,
		MountManager: mounts.NewMountManager("dagger-cache", w.CacheManager(), sessionManager),
		ServiceURL:   cacheServiceURL,
		Token:        cacheServiceToken,
		EngineID:     engineName,
	})
	if err != nil {
		return nil, nil, err
	}

	resolverFn := resolverFunc(cfg)
	remoteCacheExporterFuncs := map[string]remotecache.ResolveCacheExporterFunc{
		"registry": registryremotecache.ResolveCacheExporterFunc(sessionManager, resolverFn),
		"local":    localremotecache.ResolveCacheExporterFunc(sessionManager),
		"inline":   inlineremotecache.ResolveCacheExporterFunc(),
		"gha":      gha.ResolveCacheExporterFunc(),
		"s3":       s3remotecache.ResolveCacheExporterFunc(),
		"azblob":   azblob.ResolveCacheExporterFunc(),
		// for backwards compatibility:
		"dagger": func(ctx context.Context, g session.Group, attrs map[string]string) (remotecache.Exporter, error) {
			return nil, nil
		},
	}
	remoteCacheImporterFuncs := map[string]remotecache.ResolveCacheImporterFunc{
		"registry": registryremotecache.ResolveCacheImporterFunc(sessionManager, w.ContentStore(), resolverFn),
		"local":    localremotecache.ResolveCacheImporterFunc(sessionManager),
		"gha":      gha.ResolveCacheImporterFunc(),
		"s3":       s3remotecache.ResolveCacheImporterFunc(),
		"azblob":   azblob.ResolveCacheImporterFunc(),
		// for backwards compatibility:
		"dagger": func(ctx context.Context, g session.Group, attrs map[string]string) (remotecache.Importer, ocispecs.Descriptor, error) {
			return &noopCacheImporter{}, ocispecs.Descriptor{}, nil
		},
	}

	bkLogsW := io.Discard
	/* TODO:
	if cfg.Debug {
		bkLogsW = os.Stderr
	}
	*/

	bklog.G(context.Background()).Debugf("engine name: %s", engineName)
	ctrler, err := server.NewBuildkitController(server.BuildkitControllerOpts{
		WorkerController:       wc,
		SessionManager:         sessionManager,
		CacheManager:           cacheManager,
		ContentStore:           w.ContentStore(),
		LeaseManager:           w.LeaseManager(),
		Entitlements:           cfg.Entitlements,
		EngineName:             engineName,
		Frontends:              frontends,
		TelemetryPubSub:        pubsub,
		UpstreamCacheExporters: remoteCacheExporterFuncs,
		UpstreamCacheImporters: remoteCacheImporterFuncs,
		DNSConfig:              getDNSConfig(cfg.DNS),
		BuildkitLogSink:        bkLogsW,
	})
	if err != nil {
		return nil, nil, err
	}

	return ctrler, cacheManager, nil
}

func resolverFunc(cfg *config.Config) docker.RegistryHosts {
	return resolver.NewRegistryConfig(cfg.Registries)
}

func newWorkerController(c *cli.Context, wiOpt workerInitializerOpt) (*worker.Controller, error) {
	wc := &worker.Controller{}
	nWorkers := 0
	for _, wi := range workerInitializers {
		ws, err := wi.fn(c, wiOpt)
		if err != nil {
			return nil, err
		}
		for _, w := range ws {
			p := w.Platforms(false)
			logrus.Infof("found worker %q, labels=%v, platforms=%v", w.ID(), w.Labels(), formatPlatforms(p))
			archutil.WarnIfUnsupported(p)
			if err = wc.Add(w); err != nil {
				return nil, err
			}
			nWorkers++
		}
	}
	if nWorkers == 0 {
		return nil, errors.New("no worker found, rebuild the buildkit daemon?")
	}
	defaultWorker, err := wc.GetDefault()
	if err != nil {
		return nil, err
	}
	logrus.Infof("found %d workers, default=%q", nWorkers, defaultWorker.ID())
	logrus.Warn("currently, only the default worker can be used.")
	return wc, nil
}

func attrMap(sl []string) (map[string]string, error) {
	m := map[string]string{}
	for _, v := range sl {
		parts := strings.SplitN(v, "=", 2)
		if len(parts) != 2 {
			return nil, errors.Errorf("invalid value %s", v)
		}
		m[parts[0]] = parts[1]
	}
	return m, nil
}

func formatPlatforms(p []ocispecs.Platform) []string {
	str := make([]string, 0, len(p))
	for _, pp := range p {
		str = append(str, platforms.Format(platforms.Normalize(pp)))
	}
	return str
}

func parsePlatforms(platformsStr []string) ([]ocispecs.Platform, error) {
	out := make([]ocispecs.Platform, 0, len(platformsStr))
	for _, s := range platformsStr {
		p, err := platforms.Parse(s)
		if err != nil {
			return nil, err
		}
		out = append(out, platforms.Normalize(p))
	}
	return out, nil
}

func getGCPolicy(cfg config.GCConfig, root string) []client.PruneInfo {
	if cfg.GC != nil && !*cfg.GC {
		return nil
	}
	if len(cfg.GCPolicy) == 0 {
		cfg.GCPolicy = config.DefaultGCPolicy(cfg.GCKeepStorage)
	}
	out := make([]client.PruneInfo, 0, len(cfg.GCPolicy))
	for _, rule := range cfg.GCPolicy {
		out = append(out, client.PruneInfo{
			Filter:       rule.Filters,
			All:          rule.All,
			KeepBytes:    rule.KeepBytes.AsBytes(root),
			KeepDuration: rule.KeepDuration.Duration,
		})
	}
	return out
}

func getBuildkitVersion() client.BuildkitVersion {
	return client.BuildkitVersion{
		Package:  version.Package,
		Version:  version.Version,
		Revision: version.Revision,
	}
}

func getDNSConfig(cfg *config.DNSConfig) *oci.DNSConfig {
	var dns *oci.DNSConfig
	if cfg != nil {
		dns = &oci.DNSConfig{
			Nameservers:   cfg.Nameservers,
			Options:       cfg.Options,
			SearchDomains: cfg.SearchDomains,
		}
	}
	return dns
}

// parseBoolOrAuto returns (nil, nil) if s is "auto"
func parseBoolOrAuto(s string) (*bool, error) {
	if s == "" || strings.EqualFold(s, autoMode) {
		return nil, nil
	}
	b, err := strconv.ParseBool(s)
	return &b, err
}

// Run a separate gRPC serving _only_ the trace/log exporter services.
func runOtelController(p string, pubsub *telemetry.PubSub) error {
	server := grpc.NewServer()
	tracev1.RegisterTraceServiceServer(server, &telemetry.TraceServer{PubSub: pubsub})
	logsv1.RegisterLogsServiceServer(server, &telemetry.LogsServer{PubSub: pubsub})
	metricsv1.RegisterMetricsServiceServer(server, &telemetry.MetricsServer{PubSub: pubsub})
	uid := os.Getuid()
	l, err := sys.GetLocalListener(p, uid, uid)
	if err != nil {
		return err
	}
	if err := os.Chmod(p, 0o666); err != nil {
		l.Close()
		return err
	}
	go server.Serve(l)
	return nil
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

type noopCacheImporter struct{}

var _ remotecache.Importer = &noopCacheImporter{}

func (i *noopCacheImporter) Resolve(ctx context.Context, desc ocispecs.Descriptor, id string, w worker.Worker) (solver.CacheManager, error) {
	return nil, nil
}
