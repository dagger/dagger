package gitutil

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"slices"
	"strings"

	"dagger.io/dagger/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// GitCLI carries config to pass to the git cli to make running multiple
// commands less repetitive.
type GitCLI struct {
	git  string
	exec func(context.Context, *exec.Cmd) error

	args    []string
	dir     string
	streams StreamFunc

	workTree string
	gitDir   string

	sshAuthSock   string
	sshKnownHosts string

	ignoreError bool
	config      map[string]string

	indexFile string
}

// Option provides a variadic option for configuring the git client.
type Option func(b *GitCLI)

// WithGitBinary sets the git binary path.
func WithGitBinary(path string) Option {
	return func(b *GitCLI) {
		b.git = path
	}
}

// WithExec sets the command exec function.
func WithExec(exec func(context.Context, *exec.Cmd) error) Option {
	return func(b *GitCLI) {
		b.exec = exec
	}
}

// WithArgs sets extra args.
func WithArgs(args ...string) Option {
	return func(b *GitCLI) {
		b.args = append(b.args, args...)
	}
}

// WithDir sets working directory.
//
// This should be a path to any directory within a standard git repository.
func WithDir(dir string) Option {
	return func(b *GitCLI) {
		b.dir = dir
	}
}

// WithWorkTree sets the --work-tree arg.
//
// This should be the path to the top-level directory of the checkout. When
// setting this, you also likely need to set WithGitDir.
func WithWorkTree(workTree string) Option {
	return func(b *GitCLI) {
		b.workTree = workTree
	}
}

// WithGitDir sets the --git-dir arg.
//
// This should be the path to the .git directory. When setting this, you may
// also need to set WithWorkTree, unless you are working with a bare
// repository.
func WithGitDir(gitDir string) Option {
	return func(b *GitCLI) {
		b.gitDir = gitDir
	}
}

// WithSSHAuthSock sets the ssh auth sock.
func WithSSHAuthSock(sshAuthSock string) Option {
	return func(b *GitCLI) {
		b.sshAuthSock = sshAuthSock
	}
}

// WithSSHKnownHosts sets the known hosts file.
func WithSSHKnownHosts(sshKnownHosts string) Option {
	return func(b *GitCLI) {
		b.sshKnownHosts = sshKnownHosts
	}
}

// WithIgnoreError ignores all errors from the command.
func WithIgnoreError() Option {
	return func(b *GitCLI) {
		b.ignoreError = true
	}
}

// WithConfig merges git config key-value pairs into the environment using
// GIT_CONFIG_COUNT/KEY_i/VALUE_i so they propagate to all child processes.
func WithConfig(entries map[string]string) Option {
	return func(b *GitCLI) {
		if len(entries) == 0 {
			return
		}
		if b.config == nil {
			b.config = make(map[string]string, len(entries))
		}
		maps.Copy(b.config, entries)
	}
}

// WithHTTPTokenAuth scopes an Authorization header built from a token to the
// given remote's host using http.<base>/.extraheader so sub-commands inherit it
func WithHTTPTokenAuth(remote *GitURL, token, username string) Option {
	if remote.Scheme != HTTPProtocol && remote.Scheme != HTTPSProtocol {
		return func(*GitCLI) {}
	}

	if username == "" {
		if remote.Host == "bitbucket.org" {
			username = "x-token-auth"
		} else {
			username = "x-access-token"
		}
	}

	creds := username + ":" + token
	header := "Basic " + base64.StdEncoding.EncodeToString([]byte(creds))
	return WithHTTPAuthorizationHeader(remote, header)
}

// WithHTTPAuthorizationHeader scopes a prebuilt Authorization header
// (e.g., "Basic ...") to http.<base>/.extraheader for the given remote.
func WithHTTPAuthorizationHeader(remote *GitURL, header string) Option {
	if remote.Scheme != HTTPProtocol && remote.Scheme != HTTPSProtocol {
		return func(*GitCLI) {}
	}

	base := remote.Scheme + "://" + remote.Host
	return WithConfig(map[string]string{
		"http." + base + "/.extraheader": "Authorization: " + header,
	})
}

type StreamFunc func(context.Context) (io.WriteCloser, io.WriteCloser, func())

// WithStreams configures a callback for getting the streams for a command. The
// stream callback will be called once for each command, and both writers will
// be closed after the command has finished.
func WithStreams(streams StreamFunc) Option {
	return func(b *GitCLI) {
		b.streams = streams
	}
}

// WithIndexFile sets the GIT_INDEX_FILE environment variable for the git commands.
func WithIndexFile(indexFile string) Option {
	return func(b *GitCLI) {
		b.indexFile = indexFile
	}
}

// New initializes a new git client
func NewGitCLI(opts ...Option) *GitCLI {
	c := &GitCLI{}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// New returns a new git client with the same config as the current one, but
// with the given options applied on top.
func (cli *GitCLI) New(opts ...Option) *GitCLI {
	clone := *cli
	clone.args = slices.Clone(cli.args)

	for _, opt := range opts {
		opt(&clone)
	}
	return &clone
}

// Run executes a git command with the given args.
func (cli *GitCLI) Run(ctx context.Context, args ...string) (_ []byte, rerr error) {
	ctx, span := Tracer(ctx).Start(ctx, strings.Join(append([]string{"git"}, args...), " "), trace.WithAttributes(
		attribute.Bool(telemetry.UIEncapsulatedAttr, true),
	))
	defer telemetry.End(span, func() error { return rerr })

	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary)
	defer stdio.Close()

	gitBinary := "git"
	if cli.git != "" {
		gitBinary = cli.git
	}
	proxyEnvVars := [...]string{
		"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "ALL_PROXY",
		"http_proxy", "https_proxy", "no_proxy", "all_proxy",
	}

	var cmd *exec.Cmd
	if cli.exec == nil {
		cmd = exec.CommandContext(ctx, gitBinary)
	} else {
		cmd = exec.Command(gitBinary)
	}

	cmd.Dir = cli.dir
	if cmd.Dir == "" {
		cmd.Dir = cli.workTree
	}

	// Block sneaky repositories from using repos from the filesystem as submodules.
	cmd.Args = append(cmd.Args, "-c", "protocol.file.allow=user")
	if cli.workTree != "" {
		cmd.Args = append(cmd.Args, "--work-tree", cli.workTree)
	}
	if cli.gitDir != "" {
		cmd.Args = append(cmd.Args, "--git-dir", cli.gitDir)
	}
	cmd.Args = append(cmd.Args, cli.args...)
	cmd.Args = append(cmd.Args, args...)

	buf := bytes.NewBuffer(nil)
	errbuf := bytes.NewBuffer(nil)
	cmd.Stdin = nil
	cmd.Stdout = io.MultiWriter(buf, stdio.Stdout)
	cmd.Stderr = io.MultiWriter(errbuf, stdio.Stderr)
	if cli.streams != nil {
		stdout, stderr, flush := cli.streams(ctx)
		if stdout != nil {
			cmd.Stdout = io.MultiWriter(stdout, cmd.Stdout)
		}
		if stderr != nil {
			cmd.Stderr = io.MultiWriter(stderr, cmd.Stderr)
		}
		defer stdout.Close()
		defer stderr.Close()
		defer func() {
			if rerr != nil {
				flush()
			}
		}()
	}

	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_SSH_COMMAND=" + getGitSSHCommand(cli.sshKnownHosts),
		//	"GIT_TRACE=1",
		"GIT_ASKPASS=echo",      // Ensure git does not ask for a password (avoids cryptic error message)
		"GIT_CONFIG_NOSYSTEM=1", // Disable reading from system gitconfig.
		"HOME=/dev/null",        // Disable reading from user gitconfig.
		"LC_ALL=C",              // Ensure consistent output.
	}
	for _, ev := range proxyEnvVars {
		if v, ok := os.LookupEnv(ev); ok {
			cmd.Env = append(cmd.Env, ev+"="+v)
		}
	}
	if cli.sshAuthSock != "" {
		cmd.Env = append(cmd.Env, "SSH_AUTH_SOCK="+cli.sshAuthSock)
	}

	if len(cli.config) > 0 {
		cmd.Env = MergeGitConfigEnv(cmd.Env, cli.config)
	}
	if cli.indexFile != "" {
		cmd.Env = append(cmd.Env, "GIT_INDEX_FILE="+cli.indexFile)
	}

	var err error
	if cli.exec != nil {
		// remote git commands spawn helper processes that inherit FDs and don't
		// handle parent death signal so exec.CommandContext can't be used
		err = cli.exec(ctx, cmd)
	} else {
		err = cmd.Run()
	}

	if err != nil {
		if cli.ignoreError {
			return buf.Bytes(), nil
		}

		select {
		case <-ctx.Done():
			cerr := context.Cause(ctx)
			if cerr != nil {
				return buf.Bytes(), fmt.Errorf("context completed: %w", cerr)
			}
		default:
		}
		return buf.Bytes(), fmt.Errorf("git error: %w", translateError(err, errbuf.String()))
	}
	return buf.Bytes(), nil
}

func getGitSSHCommand(knownHosts string) string {
	gitSSHCommand := "ssh -F /dev/null"
	if knownHosts != "" {
		gitSSHCommand += " -o UserKnownHostsFile=" + knownHosts
	} else {
		gitSSHCommand += " -o StrictHostKeyChecking=no"
	}
	return gitSSHCommand
}
