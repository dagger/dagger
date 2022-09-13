package core

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dagger/cloak/core/filesystem"
	"github.com/dagger/cloak/core/service"
	"github.com/dagger/cloak/router"
	"github.com/gorilla/websocket"
	"github.com/graphql-go/graphql"
	"golang.org/x/sync/errgroup"
)

var (
	stdinPrefix  = []byte{0, byte(',')}
	stdoutPrefix = []byte{1, byte(',')}
	stderrPrefix = []byte{2, byte(',')}
	resizePrefix = []byte("resize,")
	exitPrefix   = []byte("exit,")
)

type Service struct {
	ID string
}

type StartInput struct {
	Args    []string
	Mounts  []MountInput
	Workdir string
	Env     []ExecEnvInput
}

type serviceSchema struct {
	*baseSchema
	svcs map[string]*service.Service
	l    sync.RWMutex
}

func (s *serviceSchema) Name() string {
	return "service"
}

func (s *serviceSchema) Schema() string {
	return `
"Synchronous command execution"
type Service {
	id: ID!
	fs: Filesystem!
	args: [String!]!
	running: Boolean!

	"Stop the service"
	stop(
		"Seconds to wait for stop before killing it"
		wait: Int = 10
	): Service!
}

input StartInput {
	"""
	Command to execute
	Example: ["echo", "hello, world!"]
	"""
	args: [String!]!

	"Filesystem mounts"
	mounts: [MountInput!]

	"Working directory"
	workdir: String

	"Env vars"
	env: [ExecEnvInput!]
}

extend type Filesystem {
	"execute a synchronous command inside this filesystem"
 	start(input: StartInput!): Service!
}

extend type Core {
	services: [Service!]!
	service(id: String!): Service!
}
	`
}

func (s *serviceSchema) Operations() string {
	return ``
}

func (s *serviceSchema) Resolvers() router.Resolvers {
	return router.Resolvers{
		"Core": router.ObjectResolver{
			"services": s.services,
			"service":  s.service,
		},
		"Filesystem": router.ObjectResolver{
			"start": s.start,
		},
		"Service": router.ObjectResolver{
			"fs":      s.fs,
			"args":    s.args,
			"running": s.running,
			"stop":    s.stop,
		},
	}
}

func (s *serviceSchema) Dependencies() []router.ExecutableSchema {
	return nil
}

func (s *serviceSchema) getService(id string) *service.Service {
	s.l.RLock()
	defer s.l.RUnlock()

	return s.svcs[id]
}

func (s *serviceSchema) addService(svc *service.Service) {
	s.l.Lock()
	defer s.l.Unlock()

	s.svcs[svc.ID()] = svc
}

func (s *serviceSchema) service(p graphql.ResolveParams) (any, error) {
	svc := s.getService(p.Args["id"].(string))
	return Service{
		ID: svc.ID(),
	}, nil
}

func (s *serviceSchema) services(p graphql.ResolveParams) (any, error) {
	s.l.RLock()
	defer s.l.RUnlock()

	services := make([]Service, 0, len(s.svcs))
	for _, s := range s.svcs {
		services = append(services, Service{
			ID: s.ID(),
		})
	}
	return services, nil
}

func (s *serviceSchema) start(p graphql.ResolveParams) (any, error) {
	var input StartInput
	if err := convertArg(p.Args["input"], &input); err != nil {
		return nil, err
	}

	obj, err := filesystem.FromSource(p.Source)
	if err != nil {
		return nil, err
	}

	mountTable := map[string]*filesystem.Filesystem{
		"/": obj,
	}
	for _, mount := range input.Mounts {
		mountTable[filepath.Clean(mount.Path)] = filesystem.New(mount.FS)
	}

	env := make([]string, 0, len(input.Env))
	for _, e := range input.Env {
		env = append(env, fmt.Sprintf("%s=%s", e.Name, e.Value))
	}

	svc := service.New(s.gw, &service.Config{
		Mounts:  mountTable,
		Args:    input.Args,
		Env:     env,
		Workdir: input.Workdir,
	})

	handler, err := newServiceAttachHandler(svc)
	if err != nil {
		return nil, err
	}
	s.router.Handle("/ws/services/"+svc.ID(), handler)

	// Start using a background context to make sure the container is not killed
	// at the end of *this* request.
	if err := svc.Start(context.Background()); err != nil {
		return nil, err
	}
	s.addService(svc)

	return Service{
		ID: svc.ID(),
	}, nil
}

func newServiceAttachHandler(svc *service.Service) (http.HandlerFunc, error) {
	stdin, err := svc.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := svc.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := svc.StderrPipe()
	if err != nil {
		return nil, err
	}

	return func(w http.ResponseWriter, r *http.Request) {
		var upgrader = websocket.Upgrader{}
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			panic(err)
		}
		defer ws.Close()

		eg, ctx := errgroup.WithContext(r.Context())

		// forward a io.Reader to websocket
		forwardFD := func(r io.Reader, prefix []byte) error {
			for {
				b := make([]byte, 512)
				n, err := r.Read(b)
				if err != nil {
					if err == io.EOF {
						fmt.Fprintf(os.Stderr, "[%s] closed\n", string(prefix))
						return nil
					}

					return err
				}
				message := append([]byte{}, prefix...)
				message = append(message, b[:n]...)
				err = ws.WriteMessage(websocket.BinaryMessage, message)
				if err != nil {
					return err
				}
			}
		}

		// stream stdout
		eg.Go(func() error {
			return forwardFD(stdout, stdoutPrefix)
		})

		// stream stderr
		eg.Go(func() error {
			return forwardFD(stderr, stderrPrefix)
		})

		// handle stdin
		eg.Go(func() error {
			for {
				_, buff, err := ws.ReadMessage()
				if err != nil {
					return err
				}
				switch {
				case bytes.HasPrefix(buff, stdinPrefix):
					_, err = stdin.Write(bytes.TrimPrefix(buff, stdinPrefix))
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

					svc.Resize(ctx, cols, rows)
				default:
					fmt.Fprintf(os.Stderr, "err: invalid message: %s\n", string(buff))
				}
			}
		})

		// handle shutdown
		eg.Go(func() error {
			<-svc.Done()

			message := append([]byte{}, exitPrefix...)
			message = append(message, []byte(fmt.Sprintf("%d", svc.ExitCode()))...)
			err := ws.WriteMessage(websocket.BinaryMessage, message)
			if err != nil {
				fmt.Fprintf(os.Stderr, "err: %v\n", err)
			}
			err = ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			if err != nil {
				fmt.Fprintf(os.Stderr, "err: %v\n", err)
			}
			time.Sleep(10 * time.Second)
			ws.Close()
			return err
		})

		err = eg.Wait()
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERR: %v\n", err)
		}
	}, nil
}

func (s *serviceSchema) stop(p graphql.ResolveParams) (any, error) {
	obj := p.Source.(Service)
	svc := s.getService(obj.ID)
	wait := p.Args["wait"].(int)
	return obj, svc.Stop(p.Context, time.Duration(wait)*time.Second)
}

func (s *serviceSchema) fs(p graphql.ResolveParams) (any, error) {
	obj := p.Source.(Service)
	svc := s.getService(obj.ID)
	return svc.Config().Mounts["/"], nil
}

func (s *serviceSchema) args(p graphql.ResolveParams) (any, error) {
	obj := p.Source.(Service)
	svc := s.getService(obj.ID)
	return svc.Config().Args, nil
}

func (s *serviceSchema) running(p graphql.ResolveParams) (any, error) {
	obj := p.Source.(Service)
	svc := s.getService(obj.ID)
	select {
	case <-svc.Done():
		return false, nil
	default:
		return true, nil
	}
}
