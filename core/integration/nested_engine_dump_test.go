package core

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"runtime/pprof"
	"strings"
	"sync"
	"time"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/buildkit/identity"
)

// Opt-in hang diagnostic for native tests that run nested dev engines.
//
// A nested engine's goroutines are invisible from the test process, and its
// logs never reach the test output, so a test parked in one call to a wedged
// nested engine looks the same as a slow one until the package timeout fires,
// and that timeout prints only the test process. Set
//
//	_DAGGER_TESTS_NESTED_ENGINE_DUMP_AFTER=<duration>
//
// in the test process's environment, or in the env file given to
// `engine-dev test --env-file`, to a duration a little below the run's
// --timeout. If the test process is still running after that long it writes,
// once, its own goroutines and a goroutine dump of every watched nested engine
// to stderr, which a failed `engine-dev test` run prints.
//
// When the variable is unset nothing changes: nested engines get no debug
// endpoint, watchNestedEngine registers nothing and no goroutine is started.
const (
	nestedEngineDumpEnv     = "_DAGGER_TESTS_NESTED_ENGINE_DUMP_AFTER"
	nestedEngineDumpEnvFile = "/dagger.env"
	nestedEngineDebugAddr   = "0.0.0.0:6060"
	nestedEngineDebugPort   = 6060
	nestedEngineDumpBound   = 45 * time.Second
)

type nestedEngine struct {
	label  string
	client *dagger.Client
	svc    *dagger.Service
}

type nestedEngineDumper struct {
	after   time.Duration
	started time.Time
	out     io.Writer

	// identify and fetch go through the client that started the engine: a
	// service bound through another session would be started a second time
	// on the same state volume instead of being attached to.
	identify func(context.Context, nestedEngine) (string, error)
	fetch    func(context.Context, nestedEngine) (string, error)

	mu      sync.Mutex
	next    int
	engines map[int]nestedEngine
	start   sync.Once
	done    chan struct{}
}

var (
	nestedEngineDumpsOnce sync.Once
	nestedEngineDumps     *nestedEngineDumper
	nestedEngineDumpsErr  error
	processStarted        = time.Now()
)

// nestedEngineDumpAfter reads the opt-in from the environment, then from the
// env file `engine-dev test --env-file` mounts. Zero means off.
func nestedEngineDumpAfter(getenv func(string) string, envFile io.Reader) (time.Duration, error) {
	value := getenv(nestedEngineDumpEnv)
	if value == "" && envFile != nil {
		scanner := bufio.NewScanner(envFile)
		for scanner.Scan() {
			key, v, ok := strings.Cut(strings.TrimSpace(scanner.Text()), "=")
			if ok && strings.TrimSpace(key) == nestedEngineDumpEnv {
				value = strings.Trim(strings.TrimSpace(v), `"'`)
			}
		}
		if err := scanner.Err(); err != nil {
			return 0, err
		}
	}
	if value == "" {
		return 0, nil
	}
	after, err := time.ParseDuration(value)
	if err != nil || after <= 0 {
		return 0, fmt.Errorf("%s=%q: want a positive duration such as 5m30s", nestedEngineDumpEnv, value)
	}
	return after, nil
}

// nestedEngineDumper returns the process's dumper, or nil when the diagnostic
// is off. A malformed opt-in is an error, never a silent off.
func currentNestedEngineDumper() (*nestedEngineDumper, error) {
	nestedEngineDumpsOnce.Do(func() {
		var envFile io.Reader
		if f, err := os.Open(nestedEngineDumpEnvFile); err == nil {
			defer f.Close()
			envFile = f
		}
		after, err := nestedEngineDumpAfter(os.Getenv, envFile)
		if err != nil || after == 0 {
			nestedEngineDumpsErr = err
			return
		}
		nestedEngineDumps = newNestedEngineDumper(after, processStarted, os.Stderr)
	})
	return nestedEngineDumps, nestedEngineDumpsErr
}

func newNestedEngineDumper(after time.Duration, started time.Time, out io.Writer) *nestedEngineDumper {
	return &nestedEngineDumper{
		after:   after,
		started: started,
		out:     out,
		engines: map[int]nestedEngine{},
		done:    make(chan struct{}),
		identify: func(ctx context.Context, e nestedEngine) (string, error) {
			id, err := e.svc.ID(ctx)
			return string(id), err
		},
		fetch: func(ctx context.Context, e nestedEngine) (string, error) {
			return e.client.Container().From(alpineImage).
				WithServiceBinding("nested-engine", e.svc).
				WithEnvVariable("CACHEBUST", identity.NewID()).
				WithExec([]string{"wget", "-qO-", fmt.Sprintf("http://nested-engine:%d/debug/pprof/goroutine?debug=2", nestedEngineDebugPort)}).
				Stdout(ctx)
		},
	}
}

// withNestedEngineDebugEndpoint exposes a nested engine's debug endpoint when
// the diagnostic is on, and returns the container unchanged when it is off.
func withNestedEngineDebugEndpoint(ctr *dagger.Container, args []string) (*dagger.Container, []string) {
	dumper, err := currentNestedEngineDumper()
	if err != nil {
		panic(err)
	}
	if dumper == nil {
		return ctr, args
	}
	ctr = ctr.WithExposedPort(nestedEngineDebugPort, dagger.ContainerWithExposedPortOpts{Protocol: dagger.NetworkProtocolTcp})
	return ctr, append([]string{"--debugaddr", nestedEngineDebugAddr}, args...)
}

// watchNestedEngine includes a nested engine, started by client, in the dump.
// Call the returned function when the test stops the engine: a dump binds the
// service, and binding a stopped service would start it again. It is a no-op
// when the diagnostic is off.
func watchNestedEngine(t interface {
	Helper()
	Fatalf(string, ...any)
	Cleanup(func())
}, client *dagger.Client, svc *dagger.Service, label string) (unwatch func()) {
	t.Helper()
	dumper, err := currentNestedEngineDumper()
	if err != nil {
		t.Fatalf("nested engine dump: %v", err)
	}
	if dumper == nil {
		return func() {}
	}
	unwatch = dumper.watch(nestedEngine{label: label, client: client, svc: svc})
	t.Cleanup(unwatch)
	return unwatch
}

func (d *nestedEngineDumper) watch(e nestedEngine) (unwatch func()) {
	d.mu.Lock()
	key := d.next
	d.next++
	d.engines[key] = e
	d.mu.Unlock()
	d.start.Do(func() { go d.run() })
	var once sync.Once
	return func() {
		once.Do(func() {
			d.mu.Lock()
			delete(d.engines, key)
			d.mu.Unlock()
		})
	}
}

func (d *nestedEngineDumper) run() {
	defer close(d.done)
	timer := time.NewTimer(time.Until(d.started.Add(d.after)))
	defer timer.Stop()
	<-timer.C

	fmt.Fprintf(d.out, "\nNESTED-ENGINE-DUMP test process still running after %s; test-process goroutines:\n", d.after)
	_ = pprof.Lookup("goroutine").WriteTo(d.out, 2)
	fmt.Fprintf(d.out, "\nNESTED-ENGINE-DUMP end of test-process goroutines\n")

	d.mu.Lock()
	engines := make([]nestedEngine, 0, len(d.engines))
	for _, e := range d.engines {
		engines = append(engines, e)
	}
	d.mu.Unlock()

	// A restarted engine is the same service under a new registration.
	seen := map[string]bool{}
	var wg sync.WaitGroup
	var out sync.Mutex
	for _, e := range engines {
		ctx, cancel := context.WithTimeout(context.Background(), nestedEngineDumpBound)
		id, err := d.identify(ctx, e)
		if err == nil {
			if seen[id] {
				cancel()
				continue
			}
			seen[id] = true
		}
		wg.Add(1)
		go func(e nestedEngine, identifyErr error) {
			defer wg.Done()
			defer cancel()
			dump, err := "", identifyErr
			if err == nil {
				dump, err = d.fetch(ctx, e)
			}
			if err != nil {
				dump = "dump failed: " + err.Error()
			}
			out.Lock()
			defer out.Unlock()
			fmt.Fprintf(d.out, "\nNESTED-ENGINE-DUMP-BEGIN %s\n%s\nNESTED-ENGINE-DUMP-END %s\n", e.label, dump, e.label)
		}(e, err)
	}
	wg.Wait()
	fmt.Fprintf(d.out, "\nNESTED-ENGINE-DUMP all dumps written\n")
}
