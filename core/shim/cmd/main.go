package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	stdinPath     = "/.dagger_meta_mount/stdin"
	exitCodePath  = "/.dagger_meta_mount/exitCode"
	httpProxyHost = "localhost:14242"
)

var (
	stdoutPath = "/.dagger_meta_mount/stdout"
	stderrPath = "/.dagger_meta_mount/stderr"
)

func run() int {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: %s <path> [<args>]\n", os.Args[0])
		return 1
	}

	if daggerHost := os.Getenv("DAGGER_HOST"); strings.HasPrefix(daggerHost, "unix://") {
		srv := apiProxy(daggerHost, httpProxyHost)
		os.Setenv("DAGGER_HOST", "http://"+httpProxyHost)
		go srv.ListenAndServe()
	}

	name := os.Args[1]
	args := []string{}
	if len(os.Args) > 2 {
		args = os.Args[2:]
	}
	cmd := exec.Command(name, args...)
	cmd.Env = os.Environ()

	if stdinFile, err := os.Open(stdinPath); err == nil {
		defer stdinFile.Close()
		cmd.Stdin = stdinFile
	} else {
		cmd.Stdin = nil
	}

	stdoutRedirect, found := internalEnv("_DAGGER_REDIRECT_STDOUT")
	if found {
		stdoutPath = stdoutRedirect
	}

	stdoutFile, err := os.Create(stdoutPath)
	if err != nil {
		panic(err)
	}
	defer stdoutFile.Close()
	cmd.Stdout = io.MultiWriter(stdoutFile, os.Stdout)

	stderrRedirect, found := internalEnv("_DAGGER_REDIRECT_STDERR")
	if found {
		stderrPath = stderrRedirect
	}

	stderrFile, err := os.Create(stderrPath)
	if err != nil {
		panic(err)
	}
	defer stderrFile.Close()
	cmd.Stderr = io.MultiWriter(stderrFile, os.Stderr)

	exitCode := 0
	if err := cmd.Run(); err != nil {
		exitCode = 1
		if exiterr, ok := err.(*exec.ExitError); ok {
			exitCode = exiterr.ExitCode()
		}
	}

	if err := os.WriteFile(exitCodePath, []byte(fmt.Sprintf("%d", exitCode)), 0600); err != nil {
		panic(err)
	}

	return exitCode
}

func apiProxy(from, to string) *http.Server {
	u, err := url.Parse(from)
	if err != nil {
		panic(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(&url.URL{
		Scheme: "http",
		Host:   "localhost",
	})
	proxy.Transport = &http.Transport{
		DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
			return net.Dial("unix", u.Path)
		},
	}

	srv := &http.Server{
		Addr:              to,
		Handler:           proxy,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return srv
}

func internalEnv(name string) (string, bool) {
	val, found := os.LookupEnv(name)
	if !found {
		return "", false
	}

	os.Unsetenv(name)

	return val, true
}

func main() {
	os.Exit(run())
}
