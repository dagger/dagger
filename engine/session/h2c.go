package session

import (
	"io"
	"log"
	"net"
	"sync"

	"google.golang.org/grpc"
)

// type ProxyDialerAttachable struct {
// 	UnimplementedProxyDialerServer
// }

// func (s ProxyDialerAttachable) Register(srv *grpc.Server) {
// 	RegisterProxyDialerServer(srv, s)
// }

// func (s ProxyDialerAttachable) Dial(stream ProxyDialer_DialServer) error {
// 	var conn net.Conn
// 	for {
// 		req, err := stream.Recv()
// 		if err == io.EOF {
// 			// Client closed the stream
// 			return nil
// 		}
// 		if err != nil {
// 			// An error occurred
// 			return err
// 		}

// 		if conn == nil {
// 			conn, err = net.Dial(req.GetProtocol(), req.GetAddr())
// 			if err != nil {
// 				return err
// 			}

// 			go func() {
// 				for {
// 					// Read data from the connection
// 					data := make([]byte, 1024)
// 					n, err := conn.Read(data)
// 					if err != nil {
// 						return
// 					}

// 					err = stream.Send(&DialResponse{
// 						Data: data[:n],
// 					})
// 					if err != nil {
// 						return
// 					}
// 				}
// 			}()
// 		}

// 		// Write the received data to the connection
// 		_, err = conn.Write(req.Data)
// 		if err != nil {
// 			return err
// 		}
// 	}
// }

type ProxyListenerAttachable struct {
	UnimplementedProxyListenerServer
}

func NewProxyListenerAttachable() ProxyListenerAttachable {
	return ProxyListenerAttachable{}
}

func (s ProxyListenerAttachable) Register(srv *grpc.Server) {
	RegisterProxyListenerServer(srv, &s)
}

func (s ProxyListenerAttachable) Listen(srv ProxyListener_ListenServer) error {
	req, err := srv.Recv()
	if err != nil {
		return err
	}

	log.Println("!!! LISTEN", req.GetProtocol(), req.GetAddr())
	l, err := net.Listen(req.GetProtocol(), req.GetAddr())
	if err != nil {
		return err
	}

	log.Println("!!! LISTENING", l.Addr().String())

	err = srv.Send(&ListenResponse{
		Addr: l.Addr().String(),
	})
	if err != nil {
		return err
	}

	conns := map[string]net.Conn{}
	connsL := &sync.Mutex{}
	sendL := &sync.Mutex{}

	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				log.Println("!!! ERR", err)
				return
			}

			log.Println("!!! ACCEPTED", conn.RemoteAddr().String())

			connId := conn.RemoteAddr().String()

			connsL.Lock()
			conns[connId] = conn
			connsL.Unlock()

			sendL.Lock()
			log.Println("!!! SEND CONNID", connId)
			err = srv.Send(&ListenResponse{
				ConnId: connId,
			})
			sendL.Unlock()
			if err != nil {
				log.Println("!!! ERR", err)
				return
			}

			go func() {
				for {
					// Read data from the connection
					data := make([]byte, 1024)
					n, err := conn.Read(data)
					if err != nil {
						return
					}

					log.Printf("!!! CONN READ: %q", string(data[:n]))

					sendL.Lock()
					err = srv.Send(&ListenResponse{
						ConnId: connId,
						Data:   data[:n],
					})
					sendL.Unlock()
					log.Println("!!! DATA SENT")
					if err != nil {
						log.Println("!!! SEND ERR", err)
						return
					}
				}
			}()
		}
	}()

	for {
		req, err := srv.Recv()
		log.Println("!!! RECV", req, err)
		if err == io.EOF {
			// Client closed the stream
			return nil
		}
		if err != nil {
			// An error occurred
			return err
		}

		connId := req.GetConnId()
		if req.GetConnId() == "" {
			log.Println("!!! ERR", err)
			continue
		}

		connsL.Lock()
		conn, ok := conns[connId]
		connsL.Unlock()
		if !ok {
			log.Println("!!! ERR", err)
			continue
		}

		switch {
		case req.GetClose():
			if err := conn.Close(); err != nil {
				log.Println("!!! ERR", err)
				continue
			}
			connsL.Lock()
			delete(conns, connId)
			connsL.Unlock()
		case req.Data != nil:
			log.Println("!!! CONN WRITE", connId, string(req.GetData()))
			_, err = conn.Write(req.GetData())
			if err != nil {
				log.Println("!!! ERR", err)
				continue
			}
		}
	}
}
