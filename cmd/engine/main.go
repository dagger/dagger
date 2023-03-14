package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	goerrors "errors"
	"fmt"
	"net"
	"os"
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
	daggerremotecache "github.com/dagger/dagger/engine/remotecache"
	"github.com/docker/docker/pkg/reexec"
	"github.com/gofrs/flock"
	grpc_middleware "github.com/grpc-ecosystem/go-grpc-middleware"
	"github.com/moby/buildkit/cache/remotecache"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/cmd/buildkitd/config"
	"github.com/moby/buildkit/control"
	"github.com/moby/buildkit/executor/oci"
	"github.com/moby/buildkit/frontend"
	dockerfile "github.com/moby/buildkit/frontend/dockerfile/builder"
	"github.com/moby/buildkit/frontend/gateway"
	"github.com/moby/buildkit/frontend/gateway/forwarder"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/solver/bboltcachestorage"
	"github.com/moby/buildkit/util/apicaps"
	"github.com/moby/buildkit/util/appcontext"
	"github.com/moby/buildkit/util/appdefaults"
	"github.com/moby/buildkit/util/archutil"
	"github.com/moby/buildkit/util/bklog"
	"github.com/moby/buildkit/util/grpcerrors"
	"github.com/moby/buildkit/util/profiler"
	"github.com/moby/buildkit/util/resolver"
	"github.com/moby/buildkit/util/stack"
	"github.com/moby/buildkit/util/tracing/detect"
	_ "github.com/moby/buildkit/util/tracing/detect/jaeger"
	_ "github.com/moby/buildkit/util/tracing/env"
	"github.com/moby/buildkit/util/tracing/transform"
	"github.com/moby/buildkit/version"
	"github.com/moby/buildkit/worker"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
	"go.etcd.io/bbolt"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	tracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
)

const (
	autoMode = "auto"
)

func init() {
	apicaps.ExportedProduct = "buildkit"
	stack.SetVersionInfo(version.Version, version.Revision)

	//nolint:staticcheck // SA1019 deprecated
	seed.WithTimeAndRand()
	if reexec.Init() {
		os.Exit(0)
	}

	// enable in memory recording for buildkitd traces
	detect.Recorder = detect.NewTraceRecorder()
}

var propagators = propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{})

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

		cfg, err := config.LoadFile(c.GlobalString("config"))
		if err != nil {
			return err
		}

		if err := setDaggerDefaults(&cfg); err != nil {
			return err
		}

		setDefaultConfig(&cfg)

		if err := applyMainFlags(c, &cfg); err != nil {
			return err
		}

		logrus.SetFormatter(&logrus.TextFormatter{FullTimestamp: true})
		if cfg.Debug {
			logrus.SetLevel(logrus.DebugLevel)
		}

		if cfg.GRPC.DebugAddress != "" {
			if err := setupDebugHandlers(cfg.GRPC.DebugAddress); err != nil {
				return err
			}
		}

		tp, err := detect.TracerProvider()
		if err != nil {
			return err
		}

		streamTracer := otelgrpc.StreamServerInterceptor(otelgrpc.WithTracerProvider(tp), otelgrpc.WithPropagators(propagators))

		// NOTE: using context.Background because otherwise when the outer context is cancelled the server
		// stops working. Server shutdown based on context cancellation is handled later in this func.
		unary := grpc_middleware.ChainUnaryServer(unaryInterceptor(context.Background(), tp), grpcerrors.UnaryServerInterceptor)
		stream := grpc_middleware.ChainStreamServer(streamTracer, grpcerrors.StreamServerInterceptor)

		opts := []grpc.ServerOption{grpc.UnaryInterceptor(unary), grpc.StreamInterceptor(stream)}
		server := grpc.NewServer(opts...)

		// relative path does not work with nightlyone/lockfile
		root, err := filepath.Abs(cfg.Root)
		if err != nil {
			return err
		}
		cfg.Root = root

		if err := os.MkdirAll(root, 0700); err != nil {
			return errors.Wrapf(err, "failed to create %s", root)
		}

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

		controller, remoteCacheDoneCh, err := newController(ctx, c, &cfg)
		if err != nil {
			return err
		}
		defer controller.Close()

		controller.Register(server)

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

		// Create a "private" in memory listener for the server that allows us to do some
		// server setup before officially starting the server on the unix socket listeners
		// used by actual clients. This enables, for example, clients to wait for cache
		// mounts to be synchronized before actually connecting and starting to use them.
		memListener := newInMemListener(server)
		memClient, err := memListener.NewClient(ctx)
		if err != nil {
			return err
		}

		daggerClient, stopOperatorSession, err := NewOperatorClient(ctx, memClient)
		if err != nil {
			return err
		}
		defer stopOperatorSession()
		stopCacheMountSync, err := daggerremotecache.StartCacheMountSynchronization(ctx, daggerClient)
		if err != nil {
			cancel()
			bklog.G(ctx).WithError(err).Error("failed to start cache mount synchronization")
			return err
		}

		// start serving on the listeners for actual clients
		errCh := make(chan error, 1)
		if err := serveGRPC(cfg.GRPC, server, errCh); err != nil {
			return err
		}

		select {
		case serverErr := <-errCh:
			err = serverErr
			cancel()
		case <-ctx.Done():
			err = ctx.Err()
		}

		bklog.G(ctx).Infof("stopping server")
		// TODO:(sipsma) make timeouts configurable
		stopCacheSyncCtx, cancelCacheSync := context.WithTimeout(context.Background(), 300*time.Second)
		defer cancelCacheSync()
		stopCacheMountSyncErr := stopCacheMountSync(stopCacheSyncCtx)
		if stopCacheMountSyncErr != nil {
			bklog.G(ctx).WithError(stopCacheMountSyncErr).Error("failed to stop cache mount synchronization")
		}
		err = goerrors.Join(err, stopCacheMountSyncErr)
		stopOperatorSession()

		if os.Getenv("NOTIFY_SOCKET") != "" {
			notified, notifyErr := sddaemon.SdNotify(false, sddaemon.SdNotifyStopping)
			bklog.G(ctx).Debugf("SdNotifyStopping notified=%v, err=%v", notified, notifyErr)
		}
		select {
		case <-remoteCacheDoneCh:
		case <-time.After(60 * time.Second):
		}
		server.GracefulStop()
		return err
	}

	app.After = func(_ *cli.Context) error {
		return detect.Shutdown(context.TODO())
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

func unaryInterceptor(globalCtx context.Context, tp trace.TracerProvider) grpc.UnaryServerInterceptor {
	withTrace := otelgrpc.UnaryServerInterceptor(otelgrpc.WithTracerProvider(tp), otelgrpc.WithPropagators(propagators))

	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp interface{}, err error) {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		go func() {
			select {
			case <-ctx.Done():
			case <-globalCtx.Done():
				cancel()
			}
		}()

		if strings.HasSuffix(info.FullMethod, "opentelemetry.proto.collector.trace.v1.TraceService/Export") {
			return handler(ctx, req)
		}

		resp, err = withTrace(ctx, req, info, handler)
		if err != nil {
			logrus.Errorf("%s returned error: %v", info.FullMethod, err)
			if logrus.GetLevel() >= logrus.DebugLevel {
				fmt.Fprintf(os.Stderr, "%+v", stack.Formatter(grpcerrors.FromGRPC(err)))
			}
		}
		return
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

func newController(ctx context.Context, c *cli.Context, cfg *config.Config) (*control.Controller, <-chan struct{}, error) {
	sessionManager, err := session.NewManager()
	if err != nil {
		return nil, nil, err
	}

	tc, err := detect.Exporter()
	if err != nil {
		return nil, nil, err
	}

	var traceSocket string
	if tc != nil {
		traceSocket = filepath.Join(cfg.Root, "otel-grpc.sock")
		if err := runTraceController(traceSocket, tc); err != nil {
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
	frontends := map[string]frontend.Frontend{}
	frontends["dockerfile.v0"] = forwarder.NewGatewayForwarder(wc, dockerfile.Build)
	frontends["gateway.v0"] = gateway.NewGatewayFrontend(wc)

	cacheStorage, err := bboltcachestorage.NewStore(filepath.Join(cfg.Root, "cache.db"))
	if err != nil {
		return nil, nil, err
	}

	historyDB, err := bbolt.Open(filepath.Join(cfg.Root, "history.db"), 0600, nil)
	if err != nil {
		return nil, nil, err
	}

	resolverFn := resolverFunc(cfg)

	w, err := wc.GetDefault()
	if err != nil {
		return nil, nil, err
	}

	cacheExporterFunc, cacheImporterFunc, remoteCacheDoneCh, err := daggerremotecache.StartDaggerCache(ctx,
		sessionManager,
		w.ContentStore(),
		w.LeaseManager(),
		resolverFn,
	)
	if err != nil {
		return nil, nil, err
	}

	remoteCacheExporterFuncs := map[string]remotecache.ResolveCacheExporterFunc{
		"dagger": cacheExporterFunc,
	}
	remoteCacheImporterFuncs := map[string]remotecache.ResolveCacheImporterFunc{
		"dagger": cacheImporterFunc,
	}
	ctrler, err := control.NewController(control.Opt{
		SessionManager:            sessionManager,
		WorkerController:          wc,
		Frontends:                 frontends,
		ResolveCacheExporterFuncs: remoteCacheExporterFuncs,
		ResolveCacheImporterFuncs: remoteCacheImporterFuncs,
		CacheKeyStorage:           cacheStorage,
		Entitlements:              cfg.Entitlements,
		TraceCollector:            tc,
		HistoryDB:                 historyDB,
		LeaseManager:              w.LeaseManager(),
		ContentStore:              w.ContentStore(),
		HistoryConfig:             cfg.History,
	})
	if err != nil {
		return nil, nil, err
	}
	return ctrler, remoteCacheDoneCh, nil
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
		cfg.GCPolicy = config.DefaultGCPolicy(root, cfg.GCKeepStorage)
	}
	out := make([]client.PruneInfo, 0, len(cfg.GCPolicy))
	for _, rule := range cfg.GCPolicy {
		out = append(out, client.PruneInfo{
			Filter:       rule.Filters,
			All:          rule.All,
			KeepBytes:    rule.KeepBytes,
			KeepDuration: time.Duration(rule.KeepDuration) * time.Second,
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

func runTraceController(p string, exp sdktrace.SpanExporter) error {
	server := grpc.NewServer()
	tracev1.RegisterTraceServiceServer(server, &traceCollector{exporter: exp})
	uid := os.Getuid()
	l, err := sys.GetLocalListener(p, uid, uid)
	if err != nil {
		return err
	}
	if err := os.Chmod(p, 0666); err != nil {
		l.Close()
		return err
	}
	go server.Serve(l)
	return nil
}

type traceCollector struct {
	*tracev1.UnimplementedTraceServiceServer
	exporter sdktrace.SpanExporter
}

func (t *traceCollector) Export(ctx context.Context, req *tracev1.ExportTraceServiceRequest) (*tracev1.ExportTraceServiceResponse, error) {
	err := t.exporter.ExportSpans(ctx, transform.Spans(req.GetResourceSpans()))
	if err != nil {
		return nil, err
	}
	return &tracev1.ExportTraceServiceResponse{}, nil
}
