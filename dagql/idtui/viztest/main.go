package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"dagger/viztest/internal/dagger"
	"dagger/viztest/internal/telemetry"
)

type Viztest struct {
	Num int
}

// HelloWorld returns the string "Hello, world!"
func (*Viztest) HelloWorld() string {
	return "Hello, world!"
}

// LogThroughput logs the current time in a tight loop.
func (*Viztest) Spam() *dagger.Container {
	for {
		fmt.Println(time.Now())
	}
}

// Encapsulate calls a failing function, but ultimately succeeds.
func (v *Viztest) Encapsulate(ctx context.Context) error {
	_ = v.FailLog(ctx)
	return nil // no error, that's the point
}

// FailEffect returns a function whose effects will fail when it runs.
func (*Viztest) FailEffect() *dagger.Container {
	return dag.Container().
		From("alpine").
		WithExec([]string{"sh", "-c", "echo this is a failing effect; exit 1"})
}

func (*Viztest) LogStdout() {
	fmt.Println("Hello, world!")
}

func (*Viztest) Terminal() *dagger.Container {
	return dag.Container().
		From("alpine").
		WithExec([]string{"apk", "add", "htop", "vim"}).
		Terminal()
}

func (*Viztest) PrimaryLines(n int) string {
	buf := new(strings.Builder)
	for i := 1; i <= n; i++ {
		fmt.Fprintln(buf, "This is line", i, "of", n)
	}
	return buf.String()
}

func (*Viztest) ManyLines(n int) {
	for i := 1; i <= n; i++ {
		fmt.Println("This is line", i, "of", n)
	}
}

func (v *Viztest) CustomSpan(ctx context.Context) (res string, rerr error) {
	ctx, span := Tracer().Start(ctx, "custom span")
	defer telemetry.End(span, func() error { return rerr })
	return v.Echo(ctx, "hello from Go! it is currently "+time.Now().String())
}

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

func (*Viztest) Echo(ctx context.Context, message string) (string, error) {
	return dag.Container().
		From("alpine").
		WithExec([]string{"echo", message}).
		Stdout(ctx)
}

func (*Viztest) Uppercase(ctx context.Context, message string) (string, error) {
	out, err := dag.Container().
		From("alpine").
		WithExec([]string{"echo", message}).
		Stdout(ctx)
	return strings.ToUpper(out), err
}

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
func (*Viztest) Accounting() *dagger.Container {
	return dag.Container().
		From("alpine").
		WithEnvVariable("NOW", time.Now().String()).
		WithExec([]string{"sleep", "1"}).
		WithExec([]string{"sleep", "2"})
}

// DeepSleep sleeps forever.
func (*Viztest) DeepSleep() *dagger.Container {
	return dag.Container().
		From("alpine").
		WithExec([]string{"sleep", "infinity"})
}

func (v Viztest) Add(
	// +optional
	// +default=1
	diff int,
) *Viztest {
	v.Num += diff
	return &v
}

func (v Viztest) CountFiles(ctx context.Context, dir *dagger.Directory) (*Viztest, error) {
	ents, err := dir.Entries(ctx)
	if err != nil {
		return nil, err
	}
	v.Num += len(ents)
	return &v, nil
}

func (*Viztest) LogStderr() {
	fmt.Fprintln(os.Stderr, "Hello, world!")
}

// FailLog runs a container that logs a message and then fails.
func (*Viztest) FailLog(ctx context.Context) error {
	_, err := dag.Container().
		From("alpine").
		WithEnvVariable("NOW", time.Now().String()).
		WithExec([]string{"sh", "-c", "echo im doing a lot of work; echo and then failing; exit 1"}).
		Sync(ctx)
	return err
}

// FailLogNative prints a message and then returns an error.
func (*Viztest) FailLogNative(ctx context.Context) error {
	fmt.Println("im doing a lot of work")
	fmt.Println("and then failing")
	return errors.New("i failed")
}

// FailSlow fails after waiting for a certain amount of time.
func (*Viztest) FailSlow(ctx context.Context,
	// +optional
	// +default="10"
	after string) error {
	_, err := dag.Container().
		From("alpine").
		WithEnvVariable("NOW", time.Now().String()).
		WithExec([]string{"sleep", after}).
		WithExec([]string{"false"}).
		Sync(ctx)
	return err
}

func (*Viztest) CachedExecService() *dagger.Service {
	return dag.Container().
		From("busybox").
		WithExec([]string{"echo", "exec-service cached for good"}).
		WithExec([]string{"echo", "im also cached for good"}).
		WithExec([]string{"echo", "im a buster", time.Now().String()}).
		WithExec([]string{"sleep", "1"}).
		WithExec([]string{"echo", "im busted by that buster"}).
		WithNewFile("/srv/index.html", "<h1>hello, world!</h1>").
		WithExec([]string{"httpd", "-v", "-h", "/srv", "-f"}).
		WithExposedPort(80).
		AsService()
}

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

func (v *Viztest) UseCachedExecService(ctx context.Context) error {
	_, err := dag.Container().
		From("alpine").
		WithServiceBinding("exec-service", v.CachedExecService()).
		WithEnvVariable("NOW", time.Now().String()).
		WithExec([]string{"wget", "-q", "-O-", "http://exec-service"}).
		Sync(ctx)
	return err
}

func (*Viztest) ExecService() *dagger.Service {
	return dag.Container().
		From("busybox").
		WithNewFile("/srv/index.html",
			"<h1>hello, world!</h1><p>the time is "+time.Now().String()+"</p>").
		WithExec([]string{"httpd", "-v", "-h", "/srv", "-f"}).
		WithExposedPort(80).
		AsService()
}

func (v *Viztest) UseExecService(ctx context.Context) error {
	_, err := dag.Container().
		From("alpine").
		WithServiceBinding("exec-service", v.ExecService()).
		WithEnvVariable("NOW", time.Now().String()).
		WithExec([]string{"wget", "-q", "-O-", "http://exec-service"}).
		Sync(ctx)
	return err
}

func (*Viztest) NoExecService() *dagger.Service {
	return dag.Container().
		From("redis").
		WithExposedPort(6379). // TODO: would be great to infer this
		AsService()
}

func (v *Viztest) UseNoExecService(ctx context.Context) (string, error) {
	return dag.Container().
		From("redis").
		WithServiceBinding("redis", v.NoExecService()).
		WithEnvVariable("NOW", time.Now().String()).
		WithExec([]string{"redis-cli", "-h", "redis", "ping"}).
		Stdout(ctx)
}

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

func (*Viztest) DockerBuildCached() *dagger.Container {
	return dag.Directory().
		WithNewFile("Dockerfile", `FROM busybox:1.36
RUN echo hello, world!
RUN echo we are both cached
`).
		DockerBuild()
}

func (*Viztest) DockerBuild() *dagger.Container {
	return dag.Directory().
		WithNewFile("Dockerfile", `FROM busybox:1.35
RUN echo the time is curently `+time.Now().String()+`
RUN echo hello, world!
RUN echo what is up?
RUN echo im another layer
`).
		DockerBuild()
}

func (*Viztest) DockerBuildFail() *dagger.Container {
	return dag.Directory().
		WithNewFile("Dockerfile", `FROM busybox:1.34
RUN echo the time is curently `+time.Now().String()+`
RUN echo hello, world!
RUN echo im failing && false
`).
		DockerBuild()
}
