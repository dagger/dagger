package embedded

import (
	"context"
	"log"
	"net"
	"net/url"

	"dagger.io/dagger/internal/engineconn"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/router"
)

func init() {
	engineconn.Register("embedded", New)
}

var _ engineconn.EngineConn = &Embedded{}

type Embedded struct {
	stopCh chan struct{}
	doneCh chan error
}

func New(_ *url.URL) (engineconn.EngineConn, error) {
	return &Embedded{
		stopCh: make(chan struct{}),
		doneCh: make(chan error, 1),
	}, nil
}

func (c *Embedded) Addr() string {
	return "http://dagger"
}

func (c *Embedded) Connect(ctx context.Context, cfg *engineconn.Config) (engineconn.Dialer, error) {
	started := make(chan struct{})
	var dialer engineconn.Dialer

	engineCfg := &engine.Config{
		LogOutput: cfg.LogOutput,
	}
	go func() {
		defer close(c.doneCh)
		log.Println("!!!!!!!!!!!! ENGINE.START")
		err := engine.Start(ctx, engineCfg, func(ctx context.Context, r *router.Router) error {
			dialer = func(_ context.Context) (net.Conn, error) {
				// TODO: not efficient, but whatever
				serverConn, clientConn := net.Pipe()
				go r.ServeConn(serverConn)

				return clientConn, nil
			}
			close(started)
			log.Println("!!!!!!!!!!!! ENGINE.WAITING")
			<-c.stopCh
			return nil
		})
		log.Println("!!!!!!!!!!!! ENGINE.DONE")
		c.doneCh <- err
	}()

	select {
	case <-started:
		return dialer, nil
	case err := <-c.doneCh:
		return dialer, err
	}
}

func (c *Embedded) Close() error {
	log.Println("!!!!!!!!!!!! ENGINE.CLOSING")
	// Check if it's already closed
	select {
	case <-c.stopCh:
		return nil
	default:
	}

	close(c.stopCh)
	return <-c.doneCh
}
