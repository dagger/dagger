package core

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.dagger.io/dagger/core/filesystem"
	"go.dagger.io/dagger/core/service"
	"go.dagger.io/dagger/router"

	"github.com/gorilla/websocket"
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

	  "exitCode of the service. Will block until the service exits"
	  exitCode: Int!

	  "Stop the service"
	  stop("Seconds to wait for stop before killing it" wait: Int = 10): Service!
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
			"service":  router.ToResolver(s.service),
			"services": router.ToResolver(s.services),
		},
		"Filesystem": router.ObjectResolver{
			"start": router.ToResolver(s.start),
		},
		"Service": router.ObjectResolver{
			"fs":       router.ToResolver(s.fs),
			"args":     router.ToResolver(s.args),
			"running":  router.ToResolver(s.running),
			"stop":     router.ToResolver(s.stop),
			"exitCode": router.ToResolver(s.exitCode),
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

type serviceArgs struct {
	ID string
}

func (s *serviceSchema) service(ctx *router.Context, parent any, args serviceArgs) (*Service, error) {
	svc := s.getService(args.ID)
	return &Service{
		ID: svc.ID(),
	}, nil
}

func (s *serviceSchema) services(ctx *router.Context, parent any, args any) ([]*Service, error) {
	s.l.RLock()
	defer s.l.RUnlock()

	services := make([]*Service, 0, len(s.svcs))
	for _, s := range s.svcs {
		services = append(services, &Service{
			ID: s.ID(),
		})
	}
	return services, nil
}

type startArgs struct {
	Input StartInput
}

func (s *serviceSchema) start(ctx *router.Context, parent *filesystem.Filesystem, args startArgs) (*Service, error) {
	mountTable := map[string]*filesystem.Filesystem{
		"/": parent,
	}
	for _, mount := range args.Input.Mounts {
		mountTable[filepath.Clean(mount.Path)] = filesystem.New(mount.FS)
	}

	env := make([]string, 0, len(args.Input.Env))
	for _, e := range args.Input.Env {
		env = append(env, fmt.Sprintf("%s=%s", e.Name, e.Value))
	}

	svc := service.New(s.gw, &service.Config{
		Mounts:  mountTable,
		Args:    args.Input.Args,
		Env:     env,
		Workdir: args.Input.Workdir,
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

	return &Service{
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
			// FIXME: send error
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
					// FIXME: send error message
					panic("invalid message")
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
				// FIXME: send error message
				panic(err)
			}
			err = ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			if err != nil {
				// FIXME: send error message
				panic(err)
			}
			time.Sleep(10 * time.Second)
			ws.Close()
			return err
		})

		err = eg.Wait()
		if err != nil {
			// FIXME: send error message
			panic(err)
		}
	}, nil
}

type stopArgs struct {
	Wait int
}

func (s *serviceSchema) stop(ctx *router.Context, parent *Service, args stopArgs) (*Service, error) {
	svc := s.getService(parent.ID)
	return parent, svc.Stop(ctx, time.Duration(args.Wait)*time.Second)
}

func (s *serviceSchema) fs(ctx *router.Context, parent *Service, args any) (*filesystem.Filesystem, error) {
	svc := s.getService(parent.ID)
	return svc.Config().Mounts["/"], nil
}

func (s *serviceSchema) args(ctx *router.Context, parent *Service, args any) ([]string, error) {
	svc := s.getService(parent.ID)
	return svc.Config().Args, nil
}

func (s *serviceSchema) running(ctx *router.Context, parent *Service, args any) (bool, error) {
	svc := s.getService(parent.ID)
	select {
	case <-svc.Done():
		return false, nil
	default:
		return true, nil
	}
}

func (s *serviceSchema) exitCode(ctx *router.Context, parent *Service, args any) (int, error) {
	svc := s.getService(parent.ID)
	return svc.ExitCode(), nil
}
