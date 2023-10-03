package core

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/gorilla/websocket"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/identity"
	"github.com/moby/buildkit/util/bklog"
	"golang.org/x/sync/errgroup"
)

func (container *Container) ShellEndpoint(bk *buildkit.Client, progSock string, svcs *Services) (string, http.Handler, error) {
	shellID := identity.NewID()
	endpoint := "shells/" + shellID
	return endpoint, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// TODO:
		bklog.G(r.Context()).Debugf("SHELL HANDLER FOR %s", endpoint)

		clientMetadata, err := engine.ClientMetadataFromContext(r.Context())
		if err != nil {
			panic(err)
		}

		var upgrader = websocket.Upgrader{} // TODO: timeout?
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			// FIXME: send error
			panic(err)
		}
		defer ws.Close()

		// TODO:
		bklog.G(r.Context()).Debugf("SHELL HANDLER FOR %s HAS BEEN UPGRADED", endpoint)

		if err := container.runShell(r.Context(), ws, bk, progSock, clientMetadata, svcs); err != nil {
			bklog.G(r.Context()).WithError(err).Error("shell handler failed")
			err = ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			if err != nil {
				bklog.G(r.Context()).WithError(err).Error("shell handler failed to write close message")
			}
		}
	}), nil
}

var (
	// TODO:dedupe w/ same thing in cmd/dagger
	stdinPrefix  = []byte{0, byte(',')}
	stdoutPrefix = []byte{1, byte(',')}
	stderrPrefix = []byte{2, byte(',')}
	resizePrefix = []byte("resize,")
	exitPrefix   = []byte("exit,")
)

func (container *Container) runShell(
	ctx context.Context,
	conn *websocket.Conn,
	bk *buildkit.Client,
	progSock string,
	clientMetadata *engine.ClientMetadata,
	svcs *Services,
) error {
	svc, err := container.Service(ctx, bk, progSock)
	if err != nil {
		return err
	}

	eg, egctx := errgroup.WithContext(ctx)

	// forward a io.Reader to websocket
	forwardFD := func(prefix []byte) func(r io.Reader) {
		return func(r io.Reader) {
			eg.Go(func() error {
				for {
					b := make([]byte, 512)
					n, err := r.Read(b)
					if err != nil {
						if err == io.EOF {
							return nil
						}
						return err
					}
					message := append([]byte{}, prefix...)
					message = append(message, b[:n]...)
					err = conn.WriteMessage(websocket.BinaryMessage, message)
					if err != nil {
						return err
					}
				}
			})
		}
	}

	runningSvc, err := svc.Start(
		ctx, bk, svcs,
		true,
		func(w io.Writer, svcProc bkgw.ContainerProcess) {
			eg.Go(func() error {
				for {
					_, buff, err := conn.ReadMessage()
					if err != nil {
						return err
					}
					switch {
					case bytes.HasPrefix(buff, stdinPrefix):
						_, err = w.Write(bytes.TrimPrefix(buff, stdinPrefix))
						if err != nil {
							return err
						}
					case bytes.HasPrefix(buff, resizePrefix):
						sizeMessage := string(bytes.TrimPrefix(buff, resizePrefix))
						size := strings.SplitN(sizeMessage, ";", 2)
						cols, err := strconv.Atoi(size[0])
						if err != nil {
							return err
						}
						rows, err := strconv.Atoi(size[1])
						if err != nil {
							return err
						}

						svcProc.Resize(egctx, bkgw.WinSize{Rows: uint32(rows), Cols: uint32(cols)})
					default:
						// FIXME: send error message
						panic("invalid message")
					}
				}
			})
		},
		forwardFD(stdoutPrefix),
		forwardFD(stderrPrefix),
	)
	if err != nil {
		return err
	}

	// handle shutdown
	eg.Go(func() error {
		waitErr := runningSvc.Wait()
		var exitCode int
		if waitErr != nil {
			// TODO:
			exitCode = 1
		}

		message := append([]byte{}, exitPrefix...)
		message = append(message, []byte(fmt.Sprintf("%d", exitCode))...)
		err := conn.WriteMessage(websocket.BinaryMessage, message)
		if err != nil {
			return fmt.Errorf("write exit: %w", err)
		}
		err = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		if err != nil {
			return fmt.Errorf("write close: %w", err)
		}
		conn.Close()
		return err
	})

	return eg.Wait()
}
