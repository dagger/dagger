package cniprovider

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	cni "github.com/containerd/go-cni"
	"github.com/dagger/dagger/engine/ebpf/nettracer"
	resourcestypes "github.com/dagger/dagger/internal/buildkit/executor/resources/types"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/dagger/internal/buildkit/util/bklog"
	"github.com/dagger/dagger/internal/buildkit/util/network"
	"github.com/dagger/dagger/network/dnshosts"
	"github.com/gofrs/flock"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
)

const aboveTargetGracePeriod = 5 * time.Minute

// backgroundReleaseShutdownTimeout bounds how long Close waits for named
// namespaces still being released in the background. Any left over are
// released by the next engine start, like every namespace left behind.
const backgroundReleaseShutdownTimeout = time.Minute

// releaseGate, if set, is a directory that holds the release of namespaces
// that have a hostname, standing in for a host whose network lock is
// contended. Tests only: a release creates "entered-<id>" in it, waits for a
// file named "open", releases, and writes "ok" or its error to "done-<id>".
var releaseGate = os.Getenv("_DAGGER_TEST_CNI_RELEASE_GATE")

// releaseNamed releases a namespace that has a hostname, through releaseGate
// if set.
func releaseNamed(ns *cniNS) error {
	if releaseGate == "" {
		return ns.release()
	}
	writeGateMarker("entered-"+ns.id, "")
	for deadline := time.Now().Add(10 * time.Minute); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if _, err := os.Stat(filepath.Join(releaseGate, "open")); err == nil {
			break
		}
	}
	err := ns.release()
	result := "ok"
	if err != nil {
		result = err.Error()
	}
	writeGateMarker("done-"+ns.id, result)
	return err
}

// writeGateMarker publishes a marker with its whole content: it is written
// under a temporary name and renamed into place.
func writeGateMarker(name, content string) {
	tmp := filepath.Join(releaseGate, ".tmp-"+name)
	err := os.WriteFile(tmp, []byte(content), 0o600)
	if err == nil {
		err = os.Rename(tmp, filepath.Join(releaseGate, name))
	}
	if err != nil {
		bklog.L.Errorf("writing network release gate marker %s: %s", name, err)
	}
}

type Opt struct {
	Root       string
	ConfigPath string
	BinaryDir  string
	PoolSize   int
}

func New(opt Opt) (network.Provider, error) {
	if _, err := os.Stat(opt.ConfigPath); err != nil {
		return nil, errors.Wrapf(err, "failed to read cni config %q", opt.ConfigPath)
	}
	if _, err := os.Stat(opt.BinaryDir); err != nil {
		return nil, errors.Wrapf(err, "failed to read cni binary dir %q", opt.BinaryDir)
	}

	cniOptions := []cni.Opt{cni.WithPluginDir([]string{opt.BinaryDir}), cni.WithInterfacePrefix("eth")}

	// Windows doesn't use CNI for loopback.
	if runtime.GOOS != "windows" {
		cniOptions = append([]cni.Opt{cni.WithMinNetworkCount(2)}, cniOptions...)
		cniOptions = append(cniOptions, cni.WithLoNetwork)
	}

	if strings.HasSuffix(opt.ConfigPath, ".conflist") {
		cniOptions = append(cniOptions, cni.WithConfListFile(opt.ConfigPath))
	} else {
		cniOptions = append(cniOptions, cni.WithConfFile(opt.ConfigPath))
	}

	var cniHandle cni.CNI
	fn := func(_ context.Context) error {
		var err error
		cniHandle, err = cni.New(cniOptions...)
		return err
	}
	if err := withDetachedNetNSIfAny(context.TODO(), fn); err != nil {
		return nil, err
	}

	dnsFiles, err := dnsnameFiles(opt.ConfigPath)
	if err != nil {
		return nil, err
	}

	cp := &cniProvider{
		CNI:           cniHandle,
		root:          opt.Root,
		netAccounting: nettracer.Active(),
		dnsFiles:      dnsFiles,
	}
	cleanOldNamespaces(cp)

	cp.nsPool = &cniPool{targetSize: opt.PoolSize, provider: cp}
	if err := cp.initNetwork(true); err != nil {
		return nil, err
	}
	go cp.nsPool.fillPool(context.TODO())
	return cp, nil
}

type cniProvider struct {
	cni.CNI
	root          string
	nsPool        *cniPool
	release       func() error
	netAccounting *nettracer.Tracer
	// prefixesAdded records that the bridge's addresses are classified as
	// internal; every namespace shares the same bridge.
	prefixesAdded atomic.Bool
	// dnsFiles are the dnsname plugin's files, if the network resolves
	// container hostnames with it.
	dnsFiles *dnshosts.Files

	// Named namespaces released in the background, and whether Close has
	// stopped accepting more.
	releasesMu     sync.Mutex
	releasesClosed bool
	releases       sync.WaitGroup
	releasing      atomic.Int64
}

// dnsnameFiles returns the dnsname plugin's files from the network
// configuration list at path, or nil if it does not use the plugin.
func dnsnameFiles(path string) (*dnshosts.Files, error) {
	if !strings.HasSuffix(path, ".conflist") {
		return nil, nil
	}
	dt, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var conf struct {
		Plugins []struct {
			Type     string `json:"type"`
			Hosts    string `json:"hosts"`
			Lockfile string `json:"lockfile"`
			Pidfile  string `json:"pidfile"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(dt, &conf); err != nil {
		return nil, errors.Wrapf(err, "parsing cni config %q", path)
	}
	for _, plugin := range conf.Plugins {
		if plugin.Type == "dnsname" {
			return &dnshosts.Files{Hosts: plugin.Hosts, Lockfile: plugin.Lockfile, Pidfile: plugin.Pidfile}, nil
		}
	}
	return nil, nil
}

func (c *cniProvider) initNetwork(lock bool) error {
	if lock {
		unlock, err := initLock()
		if err != nil {
			return err
		}
		defer unlock()
	}
	ns, err := c.New(context.TODO(), "")
	if err != nil {
		return err
	}
	return ns.Close()
}

func (c *cniProvider) Close() error {
	c.nsPool.close()
	c.waitForReleases(backgroundReleaseShutdownTimeout)
	if c.release != nil {
		return c.release()
	}
	return nil
}

func initLock() (func() error, error) {
	if v := os.Getenv("BUILDKIT_CNI_INIT_LOCK_PATH"); v != "" {
		l := flock.New(v)
		if err := l.Lock(); err != nil {
			return nil, err
		}
		return l.Unlock, nil
	}
	return func() error { return nil }, nil
}

type cniPool struct {
	provider   *cniProvider
	mu         sync.Mutex
	targetSize int
	actualSize int
	// LIFO: Ordered least recently used to most recently used
	available []*cniNS
	closed    bool
}

func (pool *cniPool) close() {
	bklog.L.Tracef("cleaning up cni pool")

	pool.mu.Lock()
	pool.closed = true
	defer pool.mu.Unlock()
	for len(pool.available) > 0 {
		_ = pool.available[0].release()
		pool.available = pool.available[1:]
		pool.actualSize--
	}
}

func (pool *cniPool) fillPool(ctx context.Context) {
	for {
		pool.mu.Lock()
		if pool.closed {
			pool.mu.Unlock()
			return
		}
		actualSize := pool.actualSize
		pool.mu.Unlock()
		if actualSize >= pool.targetSize {
			return
		}
		ns, err := pool.getNew(ctx)
		if err != nil {
			bklog.G(ctx).Errorf("failed to create new network namespace while prefilling pool: %s", err)
			return
		}
		pool.put(ns)
	}
}

func (pool *cniPool) get(ctx context.Context) (*cniNS, error) {
	pool.mu.Lock()
	if len(pool.available) > 0 {
		ns := pool.available[len(pool.available)-1]
		pool.available = pool.available[:len(pool.available)-1]
		pool.mu.Unlock()
		bklog.G(ctx).Tracef("returning network namespace %s from pool", ns.id)
		return ns, nil
	}
	pool.mu.Unlock()

	return pool.getNew(ctx)
}

func (pool *cniPool) getNew(ctx context.Context) (*cniNS, error) {
	var ns *cniNS
	fn := func(ctx context.Context) error {
		var err error
		ns, err = pool.provider.newNS(ctx, "")
		return err
	}
	err := withDetachedNetNSIfAny(ctx, fn)
	if err != nil {
		return nil, err
	}
	ns.pool = pool

	pool.mu.Lock()
	defer pool.mu.Unlock()
	if pool.closed {
		return nil, errors.New("cni pool is closed")
	}
	pool.actualSize++
	return ns, nil
}

func (pool *cniPool) put(ns *cniNS) {
	putTime := time.Now()
	ns.lastUsed = putTime

	pool.mu.Lock()
	defer pool.mu.Unlock()
	if pool.closed {
		_ = ns.release()
		return
	}
	pool.available = append(pool.available, ns)
	actualSize := pool.actualSize

	if actualSize > pool.targetSize {
		// We have more network namespaces than our target number, so
		// schedule a shrinking pass.
		time.AfterFunc(aboveTargetGracePeriod, pool.cleanupToTargetSize)
	}
}

func (pool *cniPool) cleanupToTargetSize() {
	var toRelease []*cniNS
	defer func() {
		for _, poolNS := range toRelease {
			_ = poolNS.release()
		}
	}()

	pool.mu.Lock()
	defer pool.mu.Unlock()
	for pool.actualSize > pool.targetSize &&
		len(pool.available) > 0 &&
		time.Since(pool.available[0].lastUsed) >= aboveTargetGracePeriod {
		bklog.L.Tracef("releasing network namespace %s since it was last used at %s", pool.available[0].id, pool.available[0].lastUsed)
		toRelease = append(toRelease, pool.available[0])
		pool.available = pool.available[1:]
		pool.actualSize--
	}
}

func (c *cniProvider) New(ctx context.Context, hostname string) (network.Namespace, error) {
	// We can't use the pool for namespaces that need a custom hostname.
	// We also avoid using it on windows because we don't have a cleanup
	// mechanism for Windows yet.
	if hostname == "" || runtime.GOOS == "windows" {
		return c.nsPool.get(ctx)
	}
	var res network.Namespace
	fn := func(ctx context.Context) error {
		var err error
		res, err = c.newNS(ctx, hostname)
		return err
	}
	if err := withDetachedNetNSIfAny(ctx, fn); err != nil {
		return nil, err
	}
	return res, nil
}

func (c *cniProvider) newNS(ctx context.Context, hostname string) (*cniNS, error) {
	id := identity.NewID()
	bklog.G(ctx).Tracef("creating new network namespace %s", id)
	nativeID, err := createNetNS(c, id)
	if err != nil {
		return nil, err
	}
	bklog.G(ctx).Tracef("finished creating network namespace %s", id)

	nsOpts := []cni.NamespaceOpts{}

	if hostname != "" {
		nsOpts = append(nsOpts,
			// NB: K8S_POD_NAME is a semi-well-known arg set by k8s and podman and
			// leveraged by the dnsname CNI plugin. a more generic name would be nice.
			cni.WithArgs("K8S_POD_NAME", hostname),

			// must be set for plugins that don't understand K8S_POD_NAME
			cni.WithArgs("IgnoreUnknown", "1"))
	}

	var cniRes *cni.Result
	if ctx.Value(contextKeyDetachedNetNS) == nil {
		cniRes, err = c.CNI.Setup(context.TODO(), id, nativeID, nsOpts...)
	} else {
		// Parallel Setup cannot be used, apparently due to the goroutine issue with setns
		cniRes, err = c.CNI.SetupSerially(context.TODO(), id, nativeID, nsOpts...)
	}
	if err != nil {
		deleteNetNS(nativeID)
		return nil, errors.Wrap(err, "CNI setup error")
	}
	bklog.G(ctx).Tracef("finished setting up network namespace %s", id)

	vethName := ""
	for k := range cniRes.Interfaces {
		if strings.HasPrefix(k, "veth") {
			if vethName != "" {
				// invalid config
				vethName = ""
				break
			}
			vethName = k
		}
	}

	ns := &cniNS{
		nativeID: nativeID,
		id:       id,
		handle:   c.CNI,
		opts:     nsOpts,
		vethName: vethName,
	}
	if hostname != "" {
		ns.provider = c
		ns.hostname = hostname
		ns.ips = sandboxIPs(cniRes)
	}
	if c.netAccounting != nil && ns.vethName != "" && !c.prefixesAdded.Load() {
		if err := c.netAccounting.AddInternalPrefixesForVeth(ns.vethName); err != nil {
			bklog.G(ctx).Debugf(
				"classifying bridge addresses for %s as internal: %s",
				ns.vethName, err,
			)
		} else {
			c.prefixesAdded.Store(true)
		}
	}

	if ns.vethName != "" {
		sample, err := ns.sample()
		if err == nil && sample != nil {
			ns.canSample = true
			ns.offsetSample = sample
		}
	}

	return ns, nil
}

type cniNS struct {
	pool          *cniPool
	provider      *cniProvider
	hostname      string
	ips           []net.IP
	handle        cni.CNI
	id            string
	nativeID      string
	opts          []cni.NamespaceOpts
	lastUsed      time.Time
	vethName      string
	canSample     bool
	offsetSample  *resourcestypes.NetworkSample
	prevSample    *resourcestypes.NetworkSample
}

func (ns *cniNS) Set(s *specs.Spec) error {
	return setNetNS(s, ns.nativeID)
}

func (ns *cniNS) Close() error {
	if ns.prevSample != nil {
		ns.offsetSample = ns.prevSample
	}
	switch {
	case ns.pool != nil:
		ns.pool.put(ns)
		return nil
	case ns.provider != nil:
		return ns.provider.closeNamed(ns)
	default:
		return ns.release()
	}
}

// sandboxIPs returns the addresses of a namespace's own interfaces, which the
// dnsname plugin maps its hostname to.
func sandboxIPs(res *cni.Result) []net.IP {
	var ips []net.IP
	for name, iface := range res.Interfaces {
		if !strings.HasPrefix(name, "eth") || iface.Sandbox == "" {
			continue
		}
		for _, ipConfig := range iface.IPConfigs {
			ips = append(ips, ipConfig.IP)
		}
	}
	return ips
}

// closeNamed releases a namespace that has a hostname. Its DNS entry is
// removed right away, so the hostname stops resolving to it, and a restart
// under the same hostname resolves only to the new namespace. The rest of the
// release can wait a long time for the host's network lock under load, so it
// runs in the background: the namespace's address and devices are its own
// until then.
func (c *cniProvider) closeNamed(ns *cniNS) error {
	var err error
	if c.dnsFiles != nil && len(ns.ips) > 0 {
		if err = c.dnsFiles.RemoveHost(ns.hostname, ns.ips); err != nil {
			err = errors.Wrapf(err, "removing DNS entry of %s", ns.hostname)
		}
	}

	c.releasesMu.Lock()
	if c.releasesClosed {
		c.releasesMu.Unlock()
		return stderrors.Join(err, releaseNamed(ns))
	}
	c.releases.Add(1)
	c.releasing.Add(1)
	c.releasesMu.Unlock()

	go func() {
		defer c.releases.Done()
		defer c.releasing.Add(-1)
		start := time.Now()
		if err := releaseNamed(ns); err != nil {
			bklog.L.Warnf("releasing network namespace %s of %s in the background after %s: %s", ns.id, ns.hostname, time.Since(start), err)
		}
	}()
	return err
}

// waitForReleases stops releasing named namespaces in the background and
// waits up to timeout for those still being released.
func (c *cniProvider) waitForReleases(timeout time.Duration) {
	c.releasesMu.Lock()
	c.releasesClosed = true
	c.releasesMu.Unlock()

	done := make(chan struct{})
	go func() {
		c.releases.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		bklog.L.Warnf("%d network namespaces still being released after %s; the next engine start releases them", c.releasing.Load(), timeout)
	}
}

func (ns *cniNS) Sample() (*resourcestypes.NetworkSample, error) {
	if !ns.canSample {
		return nil, nil
	}
	var s *resourcestypes.NetworkSample
	fn := func(_ context.Context) error {
		var err error
		s, err = ns.sample()
		return err
	}
	err := withDetachedNetNSIfAny(context.TODO(), fn)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, nil
	}
	if ns.offsetSample != nil {
		s.TxBytes -= ns.offsetSample.TxBytes
		s.RxBytes -= ns.offsetSample.RxBytes
		s.TxPackets -= ns.offsetSample.TxPackets
		s.RxPackets -= ns.offsetSample.RxPackets
		s.TxErrors -= ns.offsetSample.TxErrors
		s.RxErrors -= ns.offsetSample.RxErrors
		s.TxDropped -= ns.offsetSample.TxDropped
		s.RxDropped -= ns.offsetSample.RxDropped
		s.InternalRxBytes -= ns.offsetSample.InternalRxBytes
		s.InternalTxBytes -= ns.offsetSample.InternalTxBytes
		s.ExternalRxBytes -= ns.offsetSample.ExternalRxBytes
		s.ExternalTxBytes -= ns.offsetSample.ExternalTxBytes
	}
	return s, nil
}

func (ns *cniNS) release() error {
	bklog.L.Tracef("releasing cni network namespace %s", ns.id)
	err := ns.handle.Remove(context.TODO(), ns.id, ns.nativeID, ns.opts...)
	if err1 := unmountNetNS(ns.nativeID); err1 != nil && err == nil {
		err = err1
	}
	if err1 := deleteNetNS(ns.nativeID); err1 != nil && err == nil {
		err = err1
	}
	return err
}

type contextKeyT string

// contextKeyDetachedNetNS is associated with a string value that denotes RootlessKit's detached-netns.
var contextKeyDetachedNetNS = contextKeyT("buildkit/util/network/cniprovider/detached-netns")
