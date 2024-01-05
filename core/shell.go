package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/dagger/dagger/engine"
	"github.com/gorilla/websocket"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	bkgwpb "github.com/moby/buildkit/frontend/gateway/pb"
	"github.com/moby/buildkit/identity"
	"github.com/moby/buildkit/util/bklog"
	"github.com/dagger/dagger/dagql/idproto"
	"golang.org/x/sync/errgroup"
)

func (container *Container) ShellEndpoint(svcID *idproto.ID) (string, http.Handler, error) {
	shellID := identity.NewID()
	endpoint := "shells/" + shellID
	return endpoint, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clientMetadata, err := engine.ClientMetadataFromContext(r.Context())
		if err != nil {
			panic(err)
		}

		var upgrader = websocket.Upgrader{}
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			bklog.G(r.Context()).WithError(err).Error("shell handler failed to upgrade")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		defer ws.Close()

		bklog.G(r.Context()).Debugf("shell handler for %s has been upgraded", endpoint)
		defer bklog.G(context.Background()).Debugf("shell handler for %s finished", endpoint)

		if err := container.runShell(r.Context(), svcID, ws, clientMetadata); err != nil {
			bklog.G(r.Context()).WithError(err).Error("shell handler failed")
			err = ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			if err != nil {
				bklog.G(r.Context()).WithError(err).Error("shell handler failed to write close message")
			}
		}
	}), nil
}

func (container *Container) runShell(
	ctx context.Context,
	svcID *idproto.ID,
	conn *websocket.Conn,
	clientMetadata *engine.ClientMetadata,
) error {
	svc, err := container.Service(ctx)
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
		ctx,
		svcID,
		true,
		func(w io.Writer, svcProc bkgw.ContainerProcess) {
			eg.Go(func() error {
				for {
					_, buff, err := conn.ReadMessage()
					if err != nil {
						return err
					}
					switch {
					case bytes.HasPrefix(buff, []byte(engine.StdinPrefix)):
						_, err = w.Write(bytes.TrimPrefix(buff, []byte(engine.StdinPrefix)))
						if err != nil {
							return err
						}
					case bytes.HasPrefix(buff, []byte(engine.ResizePrefix)):
						sizeMessage := string(bytes.TrimPrefix(buff, []byte(engine.ResizePrefix)))
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
						return fmt.Errorf("unknown message: %s", buff)
					}
				}
			})
		},
		forwardFD([]byte(engine.StdoutPrefix)),
		forwardFD([]byte(engine.StderrPrefix)),
	)
	if err != nil {
		return err
	}

	// handle shutdown
	eg.Go(func() error {
		waitErr := runningSvc.Wait(ctx)
		var exitCode int
		if waitErr != nil {
			exitCode = 1
			var exitErr *bkgwpb.ExitError
			if errors.As(waitErr, &exitErr) {
				exitCode = int(exitErr.ExitCode)
			}
		}

		message := []byte(engine.ExitPrefix)
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
