//go:build linux

// Command init-session serves the session attachables of a nested Dagger
// client from inside its container. The injected /.init starts it, mounted at
// distconsts.InitSessionContainerPath, before the user's command when the
// exec is a nested client, alongside the user's command.
package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/client/secretprovider"
	"github.com/dagger/dagger/engine/session/git"
	"github.com/dagger/dagger/engine/session/h2c"
)

func main() {
	if err := mainSession(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func mainSession() error {
	ctx := context.Background()

	portStr, ok := os.LookupEnv("DAGGER_SESSION_PORT")
	if !ok {
		return fmt.Errorf("DAGGER_SESSION_PORT not set")
	}
	_, err := strconv.Atoi(portStr)
	if err != nil {
		return fmt.Errorf("DAGGER_SESSION_PORT invalid: %w", err)
	}

	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", "127.0.0.1:"+portStr)
	if err != nil {
		return fmt.Errorf("failed to connect to session server: %w", err)
	}

	attachables := []client.SessionAttachable{
		// secrets
		secretprovider.NewSecretProvider(),
		// sockets
		client.SocketProvider{EnableHostNetworkAccess: true},
		// host=>container networking
		h2c.NewTunnelListenerAttachable(ctx),
		// Git attachable
		git.NewGitAttachable(ctx),
	}
	// filesync
	filesyncer, err := client.NewFilesyncer()
	if err != nil {
		return err
	}
	attachables = append(attachables, filesyncer.AsSource(), filesyncer.AsTarget())

	nestedClientID := os.Getenv(engine.NestedClientIDEnv)
	if nestedClientID == "" {
		return fmt.Errorf("%s not set", engine.NestedClientIDEnv)
	}
	headers := engine.ClientMetadata{ClientID: nestedClientID}.AppendToHTTPHeaders(http.Header{})
	sessionSrv, err := client.ConnectSessionAttachables(ctx, conn, headers, attachables...)
	if err != nil {
		return err
	}
	defer sessionSrv.Stop()

	sessionSrv.Run(ctx)

	return nil
}
