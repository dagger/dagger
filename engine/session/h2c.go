package session

import (
	context "context"
	"errors"
	"io"
	"net"
	"sync"

	"github.com/dagger/dagger/telemetry"
	"github.com/moby/buildkit/util/grpcerrors"
	"google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
)

type TunnelListenerAttachable struct {
	rootCtx context.Context

	UnimplementedTunnelListenerServer
}

func NewTunnelListenerAttachable(rootCtx context.Context) TunnelListenerAttachable {
	return TunnelListenerAttachable{rootCtx: rootCtx}
}

func (s TunnelListenerAttachable) Register(srv *grpc.Server) {
	RegisterTunnelListenerServer(srv, &s)
}

func (s TunnelListenerAttachable) Listen(srv TunnelListener_ListenServer) error {
	log := telemetry.GlobalLogger(s.rootCtx)

	req, err := srv.Recv()
	if err != nil {
		return err
	}

	l, err := net.Listen(req.GetProtocol(), req.GetAddr())
	if err != nil {
		return err
	}
	defer l.Close()

	err = srv.Send(&ListenResponse{
		Addr: l.Addr().String(),
	})
	if err != nil {
		return err
	}

	conns := map[string]net.Conn{}
	connsL := &sync.Mutex{}
	sendL := &sync.Mutex{}

	defer func() {
		connsL.Lock()
		for _, conn := range conns {
			conn.Close()
		}
		connsL.Unlock()
	}()

	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				if !errors.Is(err, net.ErrClosed) {
					log.Warn("accept error", "error", err)
				}
				return
			}

			connID := conn.RemoteAddr().String()

			connsL.Lock()
			conns[connID] = conn
			connsL.Unlock()

			sendL.Lock()
			err = srv.Send(&ListenResponse{
				ConnId: connID,
			})
			sendL.Unlock()
			if err != nil {
				log.Warn("send connID error", "error", err)
				return
			}

			go func() {
				for {
					// Read data from the connection
					data := make([]byte, 1024)
					n, err := conn.Read(data)
					if err != nil {
						if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
							// conn closed
							return
						}

						log.Warn("conn read error", "error", err)
						return
					}

					sendL.Lock()
					err = srv.Send(&ListenResponse{
						ConnId: connID,
						Data:   data[:n],
					})
					sendL.Unlock()
					if err != nil {
						log.Warn("listener send response error", "error", err)
						return
					}
				}
			}()
		}
	}()

	for {
		req, err := srv.Recv()
		if err != nil {
			if errors.Is(err, context.Canceled) || grpcerrors.Code(err) == codes.Canceled {
				// canceled
				return nil
			}

			if errors.Is(err, io.EOF) {
				// stopped
				return nil
			}

			if grpcerrors.Code(err) == codes.Unavailable {
				// client disconnected (i.e. quitting Dagger out)
				return nil
			}

			log.Error("listener receive request error", "error", err)
			return err
		}

		connID := req.GetConnId()
		if req.GetConnId() == "" {
			log.Warn("listener request with no connID")
			continue
		}

		connsL.Lock()
		conn, ok := conns[connID]
		connsL.Unlock()
		if !ok {
			log.Warn("listener request for unknown connID", "connID", connID)
			continue
		}

		switch {
		case req.GetClose():
			if err := conn.Close(); err != nil {
				log.Warn("conn close error", "error", err)
				continue
			}
			connsL.Lock()
			delete(conns, connID)
			connsL.Unlock()
		case req.Data != nil:
			_, err = conn.Write(req.GetData())
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					// conn closed
					return nil
				}

				log.Warn("conn write error", "error", err)
				continue
			}
		}
	}
}
