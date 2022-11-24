package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/adrg/xdg"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/router"
	"github.com/dagger/dagger/tracing"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"
)

var (
	configPath string
	workdir    string
)

func init() {
	rootCmd.PersistentFlags().StringVar(&workdir, "workdir", "", "")
	rootCmd.PersistentFlags().StringVarP(&configPath, "project", "p", "", "")
}

var rootCmd = &cobra.Command{
	Use: "WARNING: this is an internal-only command used by Dagger SDKs to communicate with the Dagger engine. It is not intended to be used by humans directly.",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		var err error
		workdir, configPath, err = engine.NormalizePaths(workdir, configPath)
		return err
	},
	Run: EngineSession,
}

func EngineSession(cmd *cobra.Command, args []string) {
	startOpts := &engine.Config{
		Workdir:    workdir,
		ConfigPath: configPath,
		LogOutput:  os.Stderr,
	}
	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, syscall.SIGINT, syscall.SIGTERM)

	randomUuid, err := uuid.NewRandom()
	if err != nil {
		panic(err)
	}
	sessionId := randomUuid.String()

	sockPath := filepath.Join(runtimeDir(), "dagger-session-"+sessionId+".sock")
	l, err := createListener(sockPath)
	if err != nil {
		panic(err)
	}
	defer os.Remove(sockPath)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// shutdown if requested via signal
	go func() {
		defer cancel()
		defer l.Close()
		<-signalCh
	}()

	// shutdown if our parent closes stdin
	go func() {
		defer cancel()
		defer l.Close()
		io.Copy(io.Discard, os.Stdin)
	}()

	err = engine.Start(ctx, startOpts, func(ctx context.Context, r *router.Router) error {
		srv := http.Server{
			Handler:           r,
			ReadHeaderTimeout: 30 * time.Second,
		}

		go func() {
			if _, err := fmt.Fprintf(os.Stdout, "%s\n", sockPath); err != nil {
				panic(err)
			}
		}()

		err := srv.Serve(l)
		// if error is "use of closed network connection", it's expected
		if err != nil && !errors.Is(err, net.ErrClosed) {
			return err
		}
		return nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runtimeDir() string {
	// Try to use the proper runtime dir
	runtimeDir := xdg.RuntimeDir
	if err := os.MkdirAll(runtimeDir, 0700); err == nil {
		return runtimeDir
	}
	// Sometimes systems are misconfigured such that the runtime dir
	// doesn't exist but also can't be created by non-root users, so
	// fallback to a tmp dir
	return os.TempDir()
}

func createListener(sockPath string) (net.Listener, error) {
	// TODO: use named pipe on Windows

	// the permissions of the socket file are governed by umask, so we assume
	// that nothing else is writing files right now and set umask such that
	// the socket starts without any group or other permissions
	oldMask := unix.Umask(0077)
	defer unix.Umask(oldMask)

	l, err := net.Listen("unix", sockPath)
	if err != nil {
		panic(err)
	}
	return l, nil
}

func main() {
	closer := tracing.Init()
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		closer.Close()
		os.Exit(1)
	}
	closer.Close()
}
