package daggercmd

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/rs/cors"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/client"
)

var (
	listenAddress string
	disableHostRW bool
	allowCORS     bool
)

var apiListenCmd = newListenCmd(true)
var listenCmd = newListenCmd(true)

func newListenCmd(hidden bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "listen [options]",
		Aliases: []string{"l"},
		RunE:    optionalModCmdWrapper(Listen, os.Getenv("DAGGER_SESSION_TOKEN")),
		Hidden:  hidden,
		Short:   "Starts the engine server",
		Annotations: map[string]string{
			showFinalProgressKey: "true",
		},
	}
	addListenFlags(cmd)
	return cmd
}

func addListenFlags(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&listenAddress, "listen", "", "127.0.0.1:8080", "Listen on network address ADDR")
	cmd.Flags().BoolVar(&disableHostRW, "disable-host-read-write", false, "disable host read/write access")
	cmd.Flags().BoolVar(&allowCORS, "allow-cors", false, "allow Cross-Origin Resource Sharing (CORS) requests")
}

func Listen(ctx context.Context, engineClient *client.Client, _ *dagger.Module, cmd *cobra.Command, _ []string) error {
	stderr := cmd.OutOrStderr()

	sessionL, err := net.Listen("tcp", listenAddress)
	if err != nil {
		return fmt.Errorf("session listen: %w", err)
	}
	defer sessionL.Close()

	var handler http.Handler = engineClient

	if allowCORS {
		handler = cors.AllowAll().Handler(handler)
	}

	handler = otelhttp.NewHandler(handler, "listen", otelhttp.WithSpanNameFormatter(func(o string, r *http.Request) string {
		return fmt.Sprintf("%s: HTTP %s %s", o, r.Method, r.URL.Path)
	}))

	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)

	srv := &http.Server{
		Handler: handler,
		// Gosec G112: prevent slowloris attacks
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext: func(_ net.Listener) context.Context {
			return ctx
		},
		Protocols: protocols,
	}

	go func() {
		<-ctx.Done()
		fmt.Fprintln(stderr, "==> server shutting down")
		srv.Shutdown(context.Background())
	}()

	fmt.Fprintf(stderr, "==> server listening on http://%s/query\n", listenAddress)

	return srv.Serve(sessionL)
}
