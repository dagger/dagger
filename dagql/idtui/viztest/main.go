package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"dagger/viztest/internal/dagger"

	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/trace"
)

type Viztest struct {
	Num int
}

type ModuleTypeReturn struct {
	Container *dagger.Container
}

// HelloWorld returns the string "Hello, world!"
// +cache="session"
func (*Viztest) HelloWorld() string {
	return "Hello, world!"
}

// LogThroughput logs the current time in a tight loop.
// +cache="session"
func (*Viztest) Spam() *dagger.Container {
	for {
		fmt.Println(time.Now())
	}
}

// Encapsulate calls a failing function, but ultimately succeeds.
// +cache="session"
func (v *Viztest) Encapsulate(ctx context.Context) error {
	_ = v.FailLog(ctx)
	return nil // no error, that's the point
}

// Demonstrate that error logs are not hoisted as long as their enclosing span
// did not fail, and how UNSET spans interact with the hoisting logic.
// +cache="session"
func (*Viztest) FailEncapsulated(ctx context.Context) error {
	// Scenario 1: UNSET span under ERROR span - should hoist
	(func() (rerr error) {
		ctx, span := Tracer().Start(ctx, "failing outer span")
		defer telemetry.End(span, func() error { return rerr }) //nolint:staticcheck
		(func() {
			ctx, span := Tracer().Start(ctx, "unset middle span")
			defer span.End() // UNSET
			(func() (rerr error) {
				ctx, span := Tracer().Start(ctx, "failing inner span")
				defer telemetry.End(span, func() error { return rerr }) //nolint:staticcheck
				stdio := telemetry.SpanStdio(ctx, "")
				fmt.Fprintln(stdio.Stdout, "this should be hoisted - ancestor failed")
				return errors.New("inner failure")
			})()
		})()
		return errors.New("outer failure")
	})()

	// Scenario 2: UNSET span under OK span - should NOT hoist
	(func() (rerr error) {
		ctx, span := Tracer().Start(ctx, "succeeding outer span")
		defer telemetry.End(span, func() error { return rerr }) //nolint:staticcheck
		(func() {
			ctx, span := Tracer().Start(ctx, "unset middle span")
			defer span.End() // UNSET
			(func() (rerr error) {
				ctx, span := Tracer().Start(ctx, "failing inner span")
				defer telemetry.End(span, func() error { return rerr }) //nolint:staticcheck
				stdio := telemetry.SpanStdio(ctx, "")
				fmt.Fprintln(stdio.Stdout, "this should NOT be hoisted - ancestor succeeded")
				return errors.New("inner failure")
			})()
		})()
		return nil // outer span succeeds
	})()

	return errors.New("i failed on the outside")
}

// FailEffect returns a function whose effects will fail when it runs.
// +cache="session"
func (*Viztest) FailEffect() *dagger.Container {
	return dag.Container().
		From("alpine").
		WithExec([]string{"sh", "-c", "echo this is a failing effect; exit 1"})
}

// FailMulti bubbles up two error origins.
// +cache="session"
func (*Viztest) FailMulti(ctx context.Context) (rerr error) {
	ctx, span := Tracer().Start(ctx, "roll-up pseudo-check span",
		trace.WithAttributes(
			attribute.Bool("dagger.io/ui.rollup.spans", true),
		))
	defer telemetry.End(span, func() error { return rerr }) //nolint:staticcheck
	// NB: theoretically this would be from a concurrency pool or something but
	// we'll simulate it instead to reduce randomness

	ctx1, span := Tracer().Start(ctx, "sub-thing 1")
	_, err1 := dag.Container().
		From("alpine").
		WithExec([]string{"sh", "-c", "echo this is a failing effect; exit 1"}).
		Sync(ctx1)
	telemetry.End(span, func() error { return err1 }) //nolint:staticcheck

	ctx2, span := Tracer().Start(ctx, "sub-thing 2")
	_, err2 := dag.Container().
		From("alpine").
		WithExec([]string{"sh", "-c", "echo this is another failing effect; exit 1"}).
		Sync(ctx2)
	telemetry.End(span, func() error { return err2 }) //nolint:staticcheck

	return errors.Join(err1, err2)
}

const testSummarySuite = "viztest summary"

// TestSummary emits a deterministic OpenTelemetry test summary.
// +cache="never"
// +check
func (*Viztest) TestSummary(ctx context.Context) error {
	ctx, suite := Tracer().Start(ctx, "viztest summary suite",
		trace.WithAttributes(
			attribute.String("test.suite.name", testSummarySuite),
			attribute.String("test.suite.run.status", "success"),
		))
	defer suite.End()

	emitTestSummaryCase(ctx, "passing test 01", "pass")
	emitTestSummaryCase(ctx, "passing test 02", "pass")
	for i := 1; i <= 2; i++ {
		emitTestSummaryCase(ctx, fmt.Sprintf("skipped test %02d", i), "skipped")
	}
	emitTestSummaryCase(ctx, "failed test 01", "fail")
	return nil
}

func emitTestSummaryCase(ctx context.Context, name, status string) {
	ctx, span := Tracer().Start(ctx, name,
		trace.WithAttributes(
			attribute.String("test.case.name", testSummarySuite+"/"+name),
			attribute.String("test.suite.name", testSummarySuite),
			attribute.String("test.case.result.status", status),
		))
	stdio := telemetry.SpanStdio(ctx, "")
	fmt.Fprintf(stdio.Stdout, "%s log line 1\n", name)
	fmt.Fprintf(stdio.Stdout, "%s log line 2\n", name)
	stdio.Close()
	span.End()
}

// +cache="session"
func (*Viztest) LogStdout() {
	fmt.Println("Hello, world!")
}

// +cache="session"
func (*Viztest) Terminal() *dagger.Container {
	return dag.Container().
		From("alpine").
		WithExec([]string{"apk", "add", "htop", "vim"}).
		Terminal()
}

// +cache="session"
func (*Viztest) Changeset() *dagger.Changeset {
	return dag.Directory().
		WithNewFile("test", "hello").
		Changes(dag.Directory())
}

// +cache="session"
func (*Viztest) PrimaryLines(n int) string {
	buf := new(strings.Builder)
	for i := 1; i <= n; i++ {
		fmt.Fprintln(buf, "This is line", i, "of", n)
	}
	return buf.String()
}

// +cache="session"
func (*Viztest) ManyLines(n int) {
	for i := 1; i <= n; i++ {
		fmt.Println("This is line", i, "of", n)
	}
}

// PartialProgress emits synthetic streaming-progress log records (the
// dagger.io/progress.* convention) with hard-coded values that never reach
// completion, so the final frame deterministically renders partially filled
// braille bars: complete, in-flight at various fractions, untouched, and
// indeterminate (unknown total). A child span overflows the per-row item
// cap to exercise +N truncation.
// +cache="session"
func (*Viztest) PartialProgress(ctx context.Context) {
	// one cell per fill state on the function's own span
	emitProgress(ctx, "layer-complete", 10_000_000, 10_000_000)
	emitProgress(ctx, "layer-almost", 9_000_000, 10_000_000)
	emitProgress(ctx, "layer-half", 5_000_000, 10_000_000)
	emitProgress(ctx, "layer-started", 1_000_000, 10_000_000)
	emitProgress(ctx, "layer-untouched", 0, 10_000_000)
	emitProgress(ctx, "layer-indeterminate", 5_000_000, 0)

	func() {
		ctx, span := Tracer().Start(ctx, "overflow")
		defer span.End()
		for i := range 50 {
			emitProgress(ctx, fmt.Sprintf("item-%d", i), 1024, 1024)
		}
	}()
}

// emitProgress emits one streaming-progress log record following the
// dagger.io/progress.* convention.
func emitProgress(ctx context.Context, item string, current, total int64) {
	rec := log.Record{}
	rec.SetTimestamp(time.Now())
	// explicit empty body: progress records are not log text, and an
	// unset body does not survive the OTLP round-trip
	rec.SetBody(log.StringValue(""))
	rec.AddAttributes(
		log.String("dagger.io/progress.item", item),
		log.Int64("dagger.io/progress.current", current),
		log.Int64("dagger.io/progress.total", total),
		log.String("dagger.io/progress.unit", "bytes"),
	)
	telemetry.Logger(ctx, "dagger.io/progress").Emit(ctx, rec)
}

// TransientProgress runs a long-lived "syncing layers" span (collapsed by
// default, so its descendants' progress rolls up onto its row) containing
// a storm of transfers that complete instantly and one that stays in
// flight. Exercises the roll-up's quick-transfer fold: the quick ones
// never earn a live row and merge into one summary line in the final
// report ("1 transfer, 3 fetches ..."), while the in-flight transfer
// appears live once it's been active past the threshold and keeps its own
// row in the report.
// +cache="session"
func (*Viztest) TransientProgress(ctx context.Context) {
	ctx, outer := Tracer().Start(ctx, "syncing layers")
	defer outer.End()

	func() {
		ctx, span := Tracer().Start(ctx, "transfer-done")
		defer span.End()
		emitProgress(ctx, "blob", 10_000_000, 10_000_000)
	}()

	for i := range 3 {
		func() {
			ctx, span := Tracer().Start(ctx, fmt.Sprintf("fetching https://example.com/pkg-%d.apk", i))
			defer span.End()
			emitProgress(ctx, "body", 2_000_000, 2_000_000)
		}()
	}

	ctx, span := Tracer().Start(ctx, "transfer-live")
	defer span.End()
	for i := int64(1); i <= 8; i++ {
		emitProgress(ctx, "blob", i*1_000_000, 10_000_000)
		time.Sleep(time.Second)
	}
}

// +cache="session"
func (v *Viztest) CustomSpan(ctx context.Context) (res string, rerr error) {
	ctx, span := Tracer().Start(ctx, "custom span")
	defer telemetry.End(span, func() error { return rerr }) //nolint:staticcheck
	return v.Echo(ctx, "hello from Go! it is currently "+time.Now().String())
}

// +cache="session"
func (v *Viztest) RevealedSpans(ctx context.Context) (res string, rerr error) {
	func() {
		_, span := Tracer().Start(ctx, "custom span")
		span.End()
	}()
	func() {
		_, span := Tracer().Start(ctx, "revealed span",
			trace.WithAttributes(attribute.Bool("dagger.io/ui.reveal", true)))
		span.End()
	}()
	func() {
		ctx, span := Tracer().Start(ctx, "revealed message",
			trace.WithAttributes(attribute.Bool("dagger.io/ui.reveal", true)),
			trace.WithAttributes(attribute.String("dagger.io/ui.actor.emoji", "😊")),
			trace.WithAttributes(attribute.String("dagger.io/ui.message", "received")),
		)
		span.End()
		stdio := telemetry.SpanStdio(ctx, "doesnt matter", log.String("dagger.io/content.type", "text/markdown"))
		defer stdio.Close()
		fmt.Fprintln(stdio.Stdout, "sometimes you gotta be **bold**")
	}()
	func() {
		_, span := Tracer().Start(ctx, "revealed span")
		span.End()
	}()
	return v.Echo(ctx, "hello from Go! it is currently "+time.Now().String())
}

// +cache="session"
func (v *Viztest) RevealAndLog(ctx context.Context) (res string, rerr error) {
	ctx, span := Tracer().Start(ctx, "revealed span",
		trace.WithAttributes(attribute.Bool("dagger.io/ui.reveal", true)))
	res, err := v.Echo(ctx, "hello from Go! it is currently "+time.Now().String())
	if err != nil {
		return "", err
	}
	span.End()
	fmt.Println("i did stuff, here's the result:", res)
	return res, nil
}

// +cache="session"
func (*Viztest) ManySpans(
	ctx context.Context,
	n int,
	// +default=0
	delayMs int,
) {
	for i := 1; i <= n; i++ {
		_, span := Tracer().Start(ctx, fmt.Sprintf("span %d", i))
		time.Sleep(time.Duration(delayMs) * time.Millisecond)
		span.End()
	}
}

// Continuously prints batches of logs on an interval (default 1 per second).
// +cache="session"
func (*Viztest) StreamingLogs(
	ctx context.Context,
	// +optional
	// +default=1
	batchSize int,
	// +optional
	// +default=1000
	delayMs int,
) {
	ticker := time.NewTicker(time.Duration(delayMs) * time.Millisecond)
	lineNo := 1
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for i := 0; i < batchSize; i++ {
				fmt.Printf("%d: %d\n", lineNo, time.Now().UnixNano())
				lineNo += 1
			}
		}
	}
}

// Continuously prints batches of logs on an interval (default 1 per second).
// +cache="session"
func (*Viztest) StreamingChunks(
	ctx context.Context,
	// +optional
	// +default=1
	batchSize int,
	// +optional
	// +default=1000
	delayMs int,
) {
	ticker := time.NewTicker(time.Duration(delayMs) * time.Millisecond)
	lineNo := 1
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for i := 0; i < batchSize; i++ {
				fmt.Printf("%d: %d; ", lineNo, time.Now().UnixNano())
				lineNo += 1
			}
		}
	}
}

// +cache="session"
func (*Viztest) Echo(ctx context.Context, message string) (string, error) {
	return dag.Container().
		From("alpine").
		WithExec([]string{"echo", message}).
		Stdout(ctx)
}

// +cache="session"
func (*Viztest) Uppercase(ctx context.Context, message string) (string, error) {
	out, err := dag.Container().
		From("alpine").
		WithExec([]string{"echo", message}).
		Stdout(ctx)
	return strings.ToUpper(out), err
}

// +cache="session"
func (*Viztest) SameDiffClients(ctx context.Context, message string) (string, error) {
	return dag.Container().
		From("alpine").
		WithExec([]string{"sh", "-exc", "for i in $(seq 10); do echo $RANDOM: $0; sleep 1; done", message}).
		Stdout(ctx)
}

// Accounting returns a container that sleeps for 1 second and then sleeps for
// 2 seconds.
//
// It can be used to test UI cues for tracking down the place where a slow
// operation is configured, which is more interesting than the place where it
// is un-lazied when you're trying to figure out where to optimize.
// +cache="session"
func (*Viztest) Accounting() *dagger.Container {
	return dag.Container().
		From("alpine").
		WithEnvVariable("NOW", time.Now().String()).
		WithExec([]string{"sleep", "1"}).
		WithExec([]string{"sleep", "2"})
}

// DeepSleep sleeps forever.
// +cache="session"
func (*Viztest) DeepSleep() *dagger.Container {
	return dag.Container().
		From("alpine").
		WithExec([]string{"sleep", "infinity"})
}

// +cache="session"
func (v Viztest) Add(
	// +optional
	// +default=1
	diff int,
) *Viztest {
	v.Num += diff
	return &v
}

// +cache="session"
func (v Viztest) CountFiles(ctx context.Context, dir *dagger.Directory) (*Viztest, error) {
	ents, err := dir.Entries(ctx)
	if err != nil {
		return nil, err
	}
	v.Num += len(ents)
	return &v, nil
}

// +cache="session"
func (*Viztest) LogStderr() {
	fmt.Fprintln(os.Stderr, "Hello, world!")
}

// FailLog runs a container that logs a message and then fails.
// +cache="session"
func (*Viztest) FailLog(ctx context.Context) error {
	_, err := dag.Container().
		From("alpine").
		WithEnvVariable("NOW", time.Now().String()).
		WithExec([]string{"sh", "-c", "echo im doing a lot of work; echo and then failing; exit 1"}).
		Sync(ctx)
	return err
}

// FailLogNative prints a message and then returns an error.
// +cache="session"
func (*Viztest) FailLogNative(ctx context.Context) error {
	fmt.Println("im doing a lot of work")
	fmt.Println("and then failing")
	return errors.New("i failed")
}

// FailSlow fails after waiting for a certain amount of time.
// +cache="session"
func (*Viztest) FailSlow(ctx context.Context,
	// +optional
	// +default="10"
	after string,
) error {
	_, err := dag.Container().
		From("alpine").
		WithEnvVariable("NOW", time.Now().String()).
		WithExec([]string{"sleep", after}).
		WithExec([]string{"false"}).
		Sync(ctx)
	return err
}

// +cache="session"
func (*Viztest) CachedExecService() *dagger.Service {
	return dag.Container().
		From("busybox").
		WithExec([]string{"echo", "exec-service cached for good"}).
		WithExec([]string{"echo", "im also cached for good"}).
		WithExec([]string{"echo", "im a buster", time.Now().String()}).
		WithExec([]string{"sleep", "1"}).
		WithExec([]string{"echo", "im busted by that buster"}).
		WithNewFile("/srv/index.html", "<h1>hello, world!</h1>").
		WithExposedPort(80).
		AsService(dagger.ContainerAsServiceOpts{Args: []string{"httpd", "-v", "-h", "/srv", "-f"}})
}

// +cache="session"
func (*Viztest) CachedExecs(ctx context.Context) error {
	_, err := dag.Container().
		From("alpine").
		WithExec([]string{"echo", "cached-execs cached for good"}).
		WithExec([]string{"echo", "im also cached for good"}).
		WithExec([]string{"echo", "im a buster", time.Now().String()}).
		WithExec([]string{"sleep", "1"}).
		WithExec([]string{"echo", "im busted by that buster"}).
		Sync(ctx)
	return err
}

// +cache="session"
func (v *Viztest) UseCachedExecService(ctx context.Context) error {
	_, err := dag.Container().
		From("alpine").
		WithServiceBinding("exec-service", v.CachedExecService()).
		WithEnvVariable("NOW", time.Now().String()).
		WithExec([]string{"wget", "-q", "-O-", "http://exec-service"}).
		Sync(ctx)
	return err
}

// +cache="session"
func (*Viztest) ExecService() *dagger.Service {
	return dag.Container().
		From("busybox").
		WithNewFile("/srv/index.html",
			"<h1>hello, world!</h1><p>the time is "+time.Now().String()+"</p>").
		WithExposedPort(80).
		AsService(dagger.ContainerAsServiceOpts{Args: []string{"httpd", "-v", "-h", "/srv", "-f"}})
}

// +cache="session"
func (v *Viztest) UseExecService(ctx context.Context) error {
	_, err := dag.Container().
		From("alpine").
		WithServiceBinding("exec-service", v.ExecService()).
		WithEnvVariable("NOW", time.Now().String()).
		WithExec([]string{"wget", "-q", "-O-", "http://exec-service"}).
		Sync(ctx)
	return err
}

// ServiceErrorAttribution binds a container to a service that logs and then
// exits non-zero, exercising service-exit failure attribution back to the
// .asService call that returned it.
// +cache="session"
func (*Viztest) ServiceErrorAttribution(ctx context.Context) error {
	crashy := dag.Container().
		From("alpine").
		AsService(dagger.ContainerAsServiceOpts{
			Args: []string{"sh", "-c", "echo service is starting; sleep 1; echo service is crashing >&2; exit 42"},
		})

	_, err := dag.Container().
		From("alpine").
		WithServiceBinding("crashy", crashy).
		WithEnvVariable("NOW", time.Now().String()).
		WithExec([]string{"sh", "-c", "echo client is waiting; sleep 10; echo should not happen"}).
		Sync(ctx)
	return err
}

// +cache="session"
func (*Viztest) NoExecService() *dagger.Service {
	return dag.Container().
		From("redis:7.4.3").
		WithExposedPort(6379). // TODO: would be great to infer this
		AsService()
}

// +cache="session"
func (v *Viztest) UseNoExecService(ctx context.Context) (string, error) {
	return dag.Container().
		From("redis:7.4.3").
		WithServiceBinding("redis", v.NoExecService()).
		WithEnvVariable("NOW", time.Now().String()).
		WithExec([]string{"redis-cli", "-h", "redis", "ping"}).
		Stdout(ctx)
}

// +cache="session"
func (*Viztest) Pending(ctx context.Context) error {
	_, err := dag.Container().
		From("alpine").
		WithEnvVariable("NOW", time.Now().String()).
		WithExec([]string{"sleep", "1"}). // wait a bit to help eyeballing
		WithExec([]string{"false"}).      // fail the pipeline
		WithExec([]string{"sleep", "1"}). // will be pending at the end
		Sync(ctx)
	return err
}

// +cache="session"
func (*Viztest) Colors16(ctx context.Context) (string, error) {
	src := dag.Git("https://gitlab.com/dwt1/shell-color-scripts").
		Branch("master").
		Tree()

	return dag.Container().From("alpine").
		WithEnvVariable("TERM", "xterm-256color").
		WithExec([]string{"apk", "add", "bash", "make", "ncurses"}).
		WithMountedDirectory("/src", src).
		WithWorkdir("/src").
		WithExec([]string{"make", "install"}).
		WithExec([]string{"colorscript", "--all"}).
		Stdout(ctx)
}

// +cache="session"
func (*Viztest) Colors256(ctx context.Context) (string, error) {
	src := dag.Git("https://gitlab.com/phoneybadger/pokemon-colorscripts.git").
		Branch("main").
		Tree()
	return dag.Container().From("python").
		WithMountedDirectory("/src", src).
		WithWorkdir("/src").
		WithExec([]string{"./install.sh"}).
		WithEnvVariable("BUST", time.Now().String()).
		WithExec([]string{"pokemon-colorscripts", "-r", "1"}).
		Stdout(ctx)
}

// NOTE: All Dockerfile examples must use different images to ensure they don't
// steal spans from each other when run in parallel.

// +cache="session"
func (*Viztest) DockerBuildCached() *dagger.Container {
	return dag.Directory().
		WithNewFile("Dockerfile", `FROM busybox:1.36
RUN echo hello, world!
RUN echo we are both cached
`).
		DockerBuild()
}

// +cache="session"
func (*Viztest) DockerBuild() *dagger.Container {
	return dag.Directory().
		WithNewFile("Dockerfile", `FROM busybox:1.35
RUN echo the time is currently `+time.Now().String()+`
RUN echo hello, world!
RUN echo what is up?
RUN echo im another layer
`).
		DockerBuild()
}

// +cache="session"
func (*Viztest) DockerBuildFail() *dagger.Container {
	return dag.Directory().
		WithNewFile("Dockerfile", `FROM busybox:1.34
RUN echo the time is currently `+time.Now().String()+`
RUN echo hello, world!
RUN echo im failing && false
`).
		DockerBuild()
}

// +cache="session"
func (*Viztest) ModuleTypeReturnFail() *ModuleTypeReturn {
	return &ModuleTypeReturn{
		Container: dag.Container().
			From("alpine").
			WithEnvVariable("NOW", time.Now().String()).
			WithExec([]string{"sh", "-c", "echo module type container failing; exit 1"}).
			WithEnvVariable("AFTER", "should stay pending"),
	}
}

// +cache="session"
func (*Viztest) DiskMetrics(ctx context.Context) (string, error) {
	return dag.Container().From("alpine").
		WithEnvVariable("cache_bust", time.Now().String()).
		WithExec([]string{"sh", "-c", "dd if=/dev/urandom of=random_file bs=1M count=1000 && sync"}).
		Stdout(ctx)
}

// +cache="session"
func (*Viztest) List(ctx context.Context, dir *dagger.Directory) (string, error) {
	ents, err := dir.Entries(ctx)
	if err != nil {
		return "", err
	}
	return strings.Join(ents, "\n"), nil
}

// +cache="session"
func (*Viztest) GitReadme(ctx context.Context, remote string, version string) (string, error) {
	result, err := dag.Git(remote).Tag(version).Tree().File("README.md").Contents(ctx)
	result, _, _ = strings.Cut(result, "\n")
	return result, err
}

// +cache="session"
func (*Viztest) HTTPReadme(ctx context.Context, remote string, version string) (string, error) {
	p, err := url.Parse(remote)
	if err != nil {
		return "", err
	}
	if p.Host != "github.com" {
		return "", fmt.Errorf("expected github.com url, got %q", p.Host)
	}
	repo := strings.Trim(p.Path, "/")
	repo = strings.TrimSuffix(repo, ".git")

	url := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/README.md", repo, version)
	result, err := dag.HTTP(url).Contents(ctx)
	result, _, _ = strings.Cut(result, "\n")
	return result, err
}

// +cache="session"
func (*Viztest) ObjectLists(ctx context.Context) (string, error) {
	filePtrs, err := dag.Dep().GetFiles(ctx)
	if err != nil {
		return "", err
	}
	files := make([]*dagger.File, len(filePtrs))
	for i, f := range filePtrs {
		files[i] = &f
	}
	return dag.Dep().FileContents(ctx, files)
}

// +cache="session"
func (*Viztest) NestedCalls(ctx context.Context) ([]string, error) {
	return dag.Container().
		WithDirectory("/level-1",
			dag.Directory().
				WithFile("file", dag.File("file", "hey"), dagger.DirectoryWithFileOpts{
					Permissions: 0644,
				})).
		WithDirectory("/level-2",
			dag.Directory().
				WithDirectory("sub",
					dag.Directory().
						WithFile("file", dag.File("file", "hey"), dagger.DirectoryWithFileOpts{
							Permissions: 0644,
						}))).
		Rootfs().
		Entries(ctx)
}

// +cache="session"
func (*Viztest) PathArgs(
	ctx context.Context,
	file *dagger.File,
	dir *dagger.Directory,
	// +defaultPath=main.go
	contextFile *dagger.File,
	// +defaultPath=.
	contextDir *dagger.Directory,
) error {
	return nil
}

// +cache="session"
func (*Viztest) CallFailingDep(ctx context.Context) error {
	return dag.Dep().FailingFunction(ctx)
}

// +cache="session"
func (*Viztest) CallBubblingDep(ctx context.Context) error {
	return dag.Dep().BubblingFunction(ctx)
}

// +cache="session"
func (*Viztest) TraceFunctionCalls(ctx context.Context) error {
	files, err := dag.Dep().GetFiles(ctx)
	if err != nil {
		return err
	}
	// unlazy one of them to verify it shows up as cached
	f := files[0]
	_, err = f.Sync(ctx)
	if err != nil {
		return err
	}

	return nil
}

// +cache="session"
func (*Viztest) TraceRemoteFunctionCalls(ctx context.Context) error {
	dag.Versioned().Hello(ctx)
	return nil
}

// +cache="session"
func (v *Viztest) LogWithChildren(ctx context.Context) string {
	fmt.Println("Hey I'm a message.")
	defer fmt.Println("Hey I'm another message.")
	_, _ = dag.Container().
		From("alpine").
		WithEnvVariable("BUST", time.Now().String()).
		WithExec([]string{"sh", "-c", "echo this is a failing effect; exit 1"}).
		Sync(ctx)
	_, _ = dag.Container().
		From("alpine").
		WithEnvVariable("BUST", time.Now().String()).
		WithExec([]string{"sh", "-c", "echo whatup im another echo"}).
		Sync(ctx)
	return "This is the result of the call."
}
