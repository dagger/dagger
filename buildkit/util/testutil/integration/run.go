package integration

import (
	"bytes"
	"context"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/remotes/docker"
	"github.com/gofrs/flock"
	"github.com/moby/buildkit/util/appcontext"
	"github.com/moby/buildkit/util/contentutil"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/semaphore"
)

var sandboxLimiter *semaphore.Weighted

func init() {
	sandboxLimiter = semaphore.NewWeighted(int64(runtime.GOMAXPROCS(0)))
}

// Backend is the minimal interface that describes a testing backend.
type Backend interface {
	Address() string
	DockerAddress() string
	ContainerdAddress() string

	Rootless() bool
	NetNSDetached() bool
	Snapshotter() string
	ExtraEnv() []string
	Supports(feature string) bool
}

type Sandbox interface {
	Backend

	Context() context.Context
	Cmd(...string) *exec.Cmd
	Logs() map[string]*bytes.Buffer
	PrintLogs(*testing.T)
	ClearLogs()
	NewRegistry() (string, error)
	Value(string) interface{} // chosen matrix value
	Name() string
}

// BackendConfig is used to configure backends created by a worker.
type BackendConfig struct {
	Logs         map[string]*bytes.Buffer
	DaemonConfig []ConfigUpdater
}

type Worker interface {
	New(context.Context, *BackendConfig) (Backend, func() error, error)
	Close() error
	Name() string
	Rootless() bool
	NetNSDetached() bool
}

type ConfigUpdater interface {
	UpdateConfigFile(string) string
}

type Test interface {
	Name() string
	Run(t *testing.T, sb Sandbox)
}

type testFunc struct {
	name string
	run  func(t *testing.T, sb Sandbox)
}

func (f testFunc) Name() string {
	return f.name
}

func (f testFunc) Run(t *testing.T, sb Sandbox) {
	t.Helper()
	f.run(t, sb)
}

func TestFuncs(funcs ...func(t *testing.T, sb Sandbox)) []Test {
	var tests []Test
	names := map[string]struct{}{}
	for _, f := range funcs {
		name := getFunctionName(f)
		if _, ok := names[name]; ok {
			panic("duplicate test: " + name)
		}
		names[name] = struct{}{}
		tests = append(tests, testFunc{name: name, run: f})
	}
	return tests
}

var defaultWorkers []Worker

func Register(w Worker) {
	defaultWorkers = append(defaultWorkers, w)
}

func List() []Worker {
	return defaultWorkers
}

// TestOpt is an option that can be used to configure a set of integration
// tests.
type TestOpt func(*testConf)

func WithMatrix(key string, m map[string]interface{}) TestOpt {
	return func(tc *testConf) {
		if tc.matrix == nil {
			tc.matrix = map[string]map[string]interface{}{}
		}
		tc.matrix[key] = m
	}
}

func WithMirroredImages(m map[string]string) TestOpt {
	return func(tc *testConf) {
		if tc.mirroredImages == nil {
			tc.mirroredImages = map[string]string{}
		}
		for k, v := range m {
			tc.mirroredImages[k] = v
		}
	}
}

type testConf struct {
	matrix         map[string]map[string]interface{}
	mirroredImages map[string]string
}

func Run(t *testing.T, testCases []Test, opt ...TestOpt) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	if os.Getenv("SKIP_INTEGRATION_TESTS") == "1" {
		t.Skip("skipping integration tests")
	}

	var tc testConf
	for _, o := range opt {
		o(&tc)
	}

	mirror, cleanup, err := runMirror(t, tc.mirroredImages)
	require.NoError(t, err)

	t.Cleanup(func() { _ = cleanup() })

	matrix := prepareValueMatrix(tc)

	list := List()
	if os.Getenv("BUILDKIT_WORKER_RANDOM") == "1" && len(list) > 0 {
		rng := rand.New(rand.NewSource(time.Now().UnixNano())) //nolint:gosec // using math/rand is fine in a test utility
		list = []Worker{list[rng.Intn(len(list))]}
	}
	t.Cleanup(func() {
		for _, br := range list {
			_ = br.Close()
		}
	})

	for _, br := range list {
		for _, tc := range testCases {
			for _, mv := range matrix {
				fn := tc.Name()
				name := fn + "/worker=" + br.Name() + mv.functionSuffix()
				func(fn, testName string, br Worker, tc Test, mv matrixValue) {
					ok := t.Run(testName, func(t *testing.T) {
						if strings.Contains(fn, "NoRootless") && br.Rootless() {
							// skip sandbox setup
							t.Skip("rootless")
						}
						ctx := appcontext.Context()
						if !strings.HasSuffix(fn, "NoParallel") {
							t.Parallel()
						}
						require.NoError(t, sandboxLimiter.Acquire(context.TODO(), 1))
						defer sandboxLimiter.Release(1)

						sb, closer, err := newSandbox(ctx, br, mirror, mv)
						require.NoError(t, err)
						t.Cleanup(func() { _ = closer() })
						defer func() {
							if t.Failed() {
								sb.PrintLogs(t)
							}
						}()
						tc.Run(t, sb)
					})
					require.True(t, ok)
				}(fn, name, br, tc, mv)
			}
		}
	}
}

func getFunctionName(i interface{}) string {
	fullname := runtime.FuncForPC(reflect.ValueOf(i).Pointer()).Name()
	dot := strings.LastIndex(fullname, ".") + 1
	return strings.Title(fullname[dot:]) //nolint:staticcheck // ignoring "SA1019: strings.Title is deprecated", as for our use we don't need full unicode support
}

var localImageCache map[string]map[string]struct{}

func copyImagesLocal(t *testing.T, host string, images map[string]string) error {
	for to, from := range images {
		if localImageCache == nil {
			localImageCache = map[string]map[string]struct{}{}
		}
		if _, ok := localImageCache[host]; !ok {
			localImageCache[host] = map[string]struct{}{}
		}
		if _, ok := localImageCache[host][to]; ok {
			continue
		}
		localImageCache[host][to] = struct{}{}

		var desc ocispecs.Descriptor
		var provider content.Provider
		var err error
		if strings.HasPrefix(from, "local:") {
			var closer func()
			desc, provider, closer, err = providerFromBinary(strings.TrimPrefix(from, "local:"))
			if err != nil {
				return err
			}
			if closer != nil {
				defer closer()
			}
		} else {
			desc, provider, err = contentutil.ProviderFromRef(from)
			if err != nil {
				return err
			}
		}

		// already exists check
		_, _, err = docker.NewResolver(docker.ResolverOptions{}).Resolve(context.TODO(), host+"/"+to)
		if err == nil {
			continue
		}

		ingester, err := contentutil.IngesterFromRef(host + "/" + to)
		if err != nil {
			return err
		}
		if err := contentutil.CopyChain(context.TODO(), ingester, provider, desc); err != nil {
			return err
		}
		t.Logf("copied %s to local mirror %s", from, host+"/"+to)
	}
	return nil
}

func OfficialImages(names ...string) map[string]string {
	ns := runtime.GOARCH
	if ns == "arm64" {
		ns = "arm64v8"
	} else if ns != "amd64" {
		ns = "library"
	}
	m := map[string]string{}
	for _, name := range names {
		ref := "docker.io/" + ns + "/" + name
		if pns, ok := pins[name]; ok {
			if dgst, ok := pns[ns]; ok {
				ref += "@" + dgst
			}
		}
		m["library/"+name] = ref
	}
	return m
}

func withMirrorConfig(mirror string) ConfigUpdater {
	return mirrorConfig(mirror)
}

type mirrorConfig string

func (mc mirrorConfig) UpdateConfigFile(in string) string {
	return fmt.Sprintf(`%s

[registry."docker.io"]
mirrors=["%s"]
`, in, mc)
}

func WriteConfig(updaters []ConfigUpdater) (string, error) {
	tmpdir, err := os.MkdirTemp("", "bktest_config")
	if err != nil {
		return "", err
	}
	if err := os.Chmod(tmpdir, 0711); err != nil {
		return "", err
	}

	s := ""
	for _, upt := range updaters {
		s = upt.UpdateConfigFile(s)
	}

	if err := os.WriteFile(filepath.Join(tmpdir, buildkitdConfigFile), []byte(s), 0644); err != nil {
		return "", err
	}
	return filepath.Join(tmpdir, buildkitdConfigFile), nil
}

func runMirror(t *testing.T, mirroredImages map[string]string) (host string, _ func() error, err error) {
	mirrorDir := os.Getenv("BUILDKIT_REGISTRY_MIRROR_DIR")

	var lock *flock.Flock
	if mirrorDir != "" {
		if err := os.MkdirAll(mirrorDir, 0700); err != nil {
			return "", nil, err
		}
		lock = flock.New(filepath.Join(mirrorDir, "lock"))
		if err := lock.Lock(); err != nil {
			return "", nil, err
		}
		defer func() {
			if err != nil {
				lock.Unlock()
			}
		}()
	}

	mirror, cleanup, err := NewRegistry(mirrorDir)
	if err != nil {
		return "", nil, err
	}
	defer func() {
		if err != nil {
			cleanup()
		}
	}()

	if err := copyImagesLocal(t, mirror, mirroredImages); err != nil {
		return "", nil, err
	}

	if mirrorDir != "" {
		if err := lock.Unlock(); err != nil {
			return "", nil, err
		}
	}

	return mirror, cleanup, err
}

type matrixValue struct {
	fn     []string
	values map[string]matrixValueChoice
}

func (mv matrixValue) functionSuffix() string {
	if len(mv.fn) == 0 {
		return ""
	}
	sort.Strings(mv.fn)
	sb := &strings.Builder{}
	for _, f := range mv.fn {
		sb.Write([]byte("/" + f + "=" + mv.values[f].name))
	}
	return sb.String()
}

type matrixValueChoice struct {
	name  string
	value interface{}
}

func newMatrixValue(key, name string, v interface{}) matrixValue {
	return matrixValue{
		fn: []string{key},
		values: map[string]matrixValueChoice{
			key: {
				name:  name,
				value: v,
			},
		},
	}
}

func prepareValueMatrix(tc testConf) []matrixValue {
	m := []matrixValue{}
	for featureName, values := range tc.matrix {
		current := m
		m = []matrixValue{}
		for featureValue, v := range values {
			if len(current) == 0 {
				m = append(m, newMatrixValue(featureName, featureValue, v))
			}
			for _, c := range current {
				vv := newMatrixValue(featureName, featureValue, v)
				vv.fn = append(vv.fn, c.fn...)
				for k, v := range c.values {
					vv.values[k] = v
				}
				m = append(m, vv)
			}
		}
	}
	if len(m) == 0 {
		m = append(m, matrixValue{})
	}
	return m
}

// Skips tests on Windows
func SkipOnPlatform(t *testing.T, goos string) {
	if runtime.GOOS == goos {
		t.Skipf("Skipped on %s", goos)
	}
}
