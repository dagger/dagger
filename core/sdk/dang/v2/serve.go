package dangv2

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/dagger/dagger/core"
	dangshared "github.com/dagger/dagger/core/sdk/dang/shared"
	"github.com/dagger/dagger/core/sdk/entrypoint"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/engineutil"
	"github.com/dagger/dagger/util/hashutil"
	telemetry "github.com/dagger/otel-go"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"github.com/vito/dang/v2/pkg/dang"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// EntrypointServeMember is the optional entrypoint member that returns the
// container whose default arguments start the module's server. The server
// runs the module's calls of a session one at a time in one process, in place
// of a process per call. A call that finds it starting or busy runs the call
// member instead. hack/designs/python-persistent-module-server.md has the
// protocol.
const EntrypointServeMember = "serve"

const (
	moduleServerAddr = "127.0.0.1:8737"
	// The first call of a session waits for the server to listen, so a
	// short interval keeps it near the cost of the process it replaces.
	moduleServerDialInterval = 5 * time.Millisecond
)

type moduleServerKey struct {
	sessionID string
	digest    digest.Digest
}

// moduleServer is an entry of the server table. busy is set while the server
// starts and while it runs a call.
type moduleServer struct {
	running *core.RunningService
	args    []string
	busy    bool
	exited  chan struct{}
}

var moduleServers = struct {
	sync.Mutex
	byKey map[moduleServerKey]*moduleServer
}{byKey: map[moduleServerKey]*moduleServer{}}

// acquireModuleServer marks key's server busy and returns it, with start set
// when the caller must start it. It returns nil when the server is busy.
func acquireModuleServer(key moduleServerKey) (srv *moduleServer, start bool) {
	moduleServers.Lock()
	defer moduleServers.Unlock()
	srv, ok := moduleServers.byKey[key]
	switch {
	case !ok:
		srv = &moduleServer{busy: true, exited: make(chan struct{})}
		moduleServers.byKey[key] = srv
		return srv, true
	case srv.busy:
		return nil, false
	default:
		srv.busy = true
		return srv, false
	}
}

func (srv *moduleServer) release() {
	moduleServers.Lock()
	defer moduleServers.Unlock()
	srv.busy = false
}

func forgetModuleServer(key moduleServerKey, srv *moduleServer) {
	moduleServers.Lock()
	defer moduleServers.Unlock()
	if moduleServers.byKey[key] == srv {
		delete(moduleServers.byKey, key)
	}
}

// callModuleServer runs fnCall on the server of the module's serve member and
// returns its result. It reports false, with no error, when the call must run
// the entrypoint's call member instead: the server is starting or busy, did
// not start, or never received the call.
func callModuleServer(
	ctx context.Context,
	dag *dagql.Server,
	query *core.Query,
	env dang.ValueScope,
	entrypointName string,
	fnCall *core.FunctionCall,
	moduleContext dagql.ObjectResult[*core.Module],
) (_ []byte, served bool, _ error) {
	bk, err := query.Engine(ctx)
	if err != nil {
		return nil, false, err
	}
	svcs, err := query.Services(ctx)
	if err != nil {
		return nil, false, err
	}
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, false, err
	}
	value, err := callEntrypointMethod(ctx, env, entrypointName, EntrypointServeMember, moduleContext, nil)
	if err != nil {
		return nil, false, fmt.Errorf("call module entrypoint %s: %w", EntrypointServeMember, err)
	}
	ctr, err := loadEntrypointObject[*core.Container](ctx, dag, EntrypointServeMember, value)
	if err != nil {
		return nil, false, err
	}
	ctrDigest, err := ctr.ContentPreferredDigest(ctx)
	if err != nil {
		return nil, false, err
	}
	scoped, err := core.ImplementationScopedModule(ctx, moduleContext)
	if err != nil {
		return nil, false, err
	}
	modDigest, err := scoped.ContentPreferredDigest(ctx)
	if err != nil {
		return nil, false, err
	}
	key := moduleServerKey{sessionID: clientMetadata.SessionID, digest: hashutil.HashStrings(ctrDigest.String(), modDigest.String())}
	clientID := rand.Text()
	payload, err := callFramePayload(fnCall, clientID)
	if err != nil {
		return nil, false, err
	}

	fallBack := func(step string, err error) ([]byte, bool, error) {
		slog.WarnContext(ctx, "module server did not get the call, running the call member", "step", step, "error", err)
		return nil, false, nil
	}
	srv, start := acquireModuleServer(key)
	if srv == nil {
		return nil, false, nil
	}
	if start {
		if err := srv.start(ctx, svcs, key, ctr, scoped); err != nil {
			forgetModuleServer(key, srv)
			return fallBack("start", err)
		}
	}
	var conn net.Conn
	var killOnce sync.Once
	kill := func() {
		killOnce.Do(func() {
			if conn != nil {
				conn.Close()
			}
			// Stop gives up when its context ends, and the call's may have.
			_ = svcs.StopRunning(context.WithoutCancel(ctx), srv.running, true)
			forgetModuleServer(key, srv)
		})
	}
	unbind, err := bindCallClient(ctx, bk, srv, clientID, moduleContext)
	if err != nil {
		// A server that started for this call has not answered yet, so it
		// must not wait idle for the next call.
		if start {
			kill()
		} else {
			srv.release()
		}
		return fallBack("bind", err)
	}
	defer unbind()
	conn, err = dialModuleServer(ctx, bk, srv, start)
	if err != nil {
		kill()
		return fallBack("dial", err)
	}
	defer conn.Close()
	// Before the request, so a server that stops reading cannot hold a
	// cancelled call.
	stopKill := context.AfterFunc(ctx, kill)
	if err := writeFrame(conn, 'C', payload); err != nil {
		if !stopKill() {
			return nil, true, context.Cause(ctx)
		}
		kill()
		return fallBack("send", err)
	}

	stdio := telemetry.SpanStdio(trace.ContextWithSpanContext(ctx, fnCall.ProcessSpanContext()), core.InstrumentationLibrary)
	defer stdio.Close()
	var stdout, stderr tailBuffer
	result, failed, err := readCallFrames(conn, io.MultiWriter(stdio.Stdout, &stdout), io.MultiWriter(stdio.Stderr, &stderr))
	if !stopKill() {
		return nil, true, context.Cause(ctx)
	}
	if err != nil {
		kill()
		return nil, true, telemetry.TrackOrigin(err, fnCall.ProcessSpanContext())
	}
	srv.release()
	if failed != "" {
		// The frame carries no exit status, so a served failure reports 1.
		execErr := &core.ExecError{
			Err:      telemetry.TrackOrigin(errors.New(failed), fnCall.ProcessSpanContext()),
			Cmd:      srv.args,
			ExitCode: 1,
			Stdout:   strings.TrimSpace(stdout.String()),
			Stderr:   strings.TrimSpace(stderr.String()),
		}
		return nil, true, &gqlerror.Error{Message: execErr.Error(), Extensions: execErr.Extensions()}
	}
	return result, true, nil
}

// tailBuffer keeps the last bytes written to it, as many as an exec error
// keeps of its output.
type tailBuffer struct {
	buf     []byte
	omitted int
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.buf = append(b.buf, p...)
	if over := len(b.buf) - engineutil.MaxExecErrorOutputBytes; over > 0 {
		b.omitted += over
		b.buf = append(b.buf[:0], b.buf[over:]...)
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	if b.omitted == 0 {
		return string(b.buf)
	}
	return fmt.Sprintf(engineutil.TruncationMessage, b.omitted) + string(b.buf)
}

// start starts the server through Services, which holds its only binding
// until it exits or the session ends, and forgets key once it exits.
func (srv *moduleServer) start(
	ctx context.Context,
	svcs *core.Services,
	key moduleServerKey,
	ctr dagql.ObjectResult[*core.Container],
	scoped dagql.ObjectResult[*core.Module],
) error {
	svc, err := ctr.Self().AsService(ctx, ctr, core.ContainerAsServiceArgs{ExperimentalPrivilegedNesting: true})
	if err != nil {
		return err
	}
	svc.ModuleContext = scoped
	// A new start must not join a stopped server that Services still lists.
	dig := hashutil.HashStrings(key.digest.String(), rand.Text())
	running, err := svcs.StartWithOpts(ctx, dig, svc, core.ServiceStartOpts{})
	if err != nil {
		return err
	}
	srv.running = running
	srv.args = svc.Args
	go func() {
		_ = running.Wait(context.Background())
		forgetModuleServer(key, srv)
		close(srv.exited)
	}()
	return nil
}

// bindCallClient registers clientID in the server's proxy as a child of the
// call's entrypoint client, whose server WithNestedClientServer runs for ctx.
func bindCallClient(ctx context.Context, bk *engineutil.Client, srv *moduleServer, clientID string, moduleContext dagql.ObjectResult[*core.Module]) (func(), error) {
	scopeCtx, release, err := dangshared.NestedClientScope(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	return bk.BindNestedClient(scopeCtx, srv.running.ContainerID, clientID, moduleContext)
}

// dialModuleServer connects to the server in its network namespace. With
// wait, it dials again until the server listens or exits.
func dialModuleServer(ctx context.Context, bk *engineutil.Client, srv *moduleServer, wait bool) (net.Conn, error) {
	ns := engineutil.NewDirectNS(srv.running.ContainerID)
	for {
		conn, err := engineutil.RunInNetNS(ctx, bk, ns, func() (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", moduleServerAddr)
		})
		if err == nil || !wait {
			return conn, err
		}
		select {
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		case <-srv.exited:
			return nil, errors.New("module server exited before it listened")
		case <-time.After(moduleServerDialInterval):
		}
	}
}

// callFrame is the payload of a C frame. Its request is what the call
// member's stdin carries, with the receiver and the arguments as JSON text.
type callFrame struct {
	Request     callRequest `json:"request"`
	ClientID    string      `json:"clientID"`
	Traceparent string      `json:"traceparent"`
}

type callRequest struct {
	ReceiverType  string `json:"receiverType"`
	ReceiverValue string `json:"receiverValue"`
	FnName        string `json:"fnName"`
	FnArgs        string `json:"fnArgs"`
}

func callFramePayload(fnCall *core.FunctionCall, clientID string) ([]byte, error) {
	fnArgs, err := entrypoint.FunctionArgsJSON(fnCall.InputArgs)
	if err != nil {
		return nil, err
	}
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(trace.ContextWithSpanContext(context.Background(), fnCall.ProcessSpanContext()), carrier)
	return json.Marshal(callFrame{
		Request:     callRequest{fnCall.ParentName, string(fnCall.Parent), fnCall.Name, string(fnArgs)},
		ClientID:    clientID,
		Traceparent: carrier.Get("traceparent"),
	})
}

func writeFrame(w io.Writer, kind byte, payload []byte) error {
	frame := make([]byte, 5, 5+len(payload))
	frame[0] = kind
	binary.BigEndian.PutUint32(frame[1:], uint32(len(payload)))
	_, err := w.Write(append(frame, payload...))
	return err
}

// readCallFrames copies a call's output frames to stdout and stderr until its
// result frame. failed is the message of a call that the server reports
// failed. A result or failure frame may be as large as a file whose contents
// the API returns.
func readCallFrames(r io.Reader, stdout, stderr io.Writer) (result []byte, failed string, _ error) {
	for {
		var header [5]byte
		if _, err := io.ReadFull(r, header[:]); err != nil {
			return nil, "", fmt.Errorf("module server exited during the call: %w", err)
		}
		kind, size := header[0], int64(binary.BigEndian.Uint32(header[1:]))
		switch kind {
		case 'O', 'E':
			out := stdout
			if kind == 'E' {
				out = stderr
			}
			if _, err := io.CopyN(out, r, size); err != nil {
				return nil, "", fmt.Errorf("module server exited during the call: %w", err)
			}
			continue
		case 'R', 'X':
		default:
			return nil, "", fmt.Errorf("module server sent a frame of unknown kind %q", kind)
		}
		if size > engineutil.MaxFileContentsSize {
			return nil, "", fmt.Errorf("module server sent a %q frame of %d bytes, over the limit of %d", kind, size, engineutil.MaxFileContentsSize)
		}
		payload := make([]byte, size)
		if _, err := io.ReadFull(r, payload); err != nil {
			return nil, "", fmt.Errorf("module server exited during the call: %w", err)
		}
		if kind == 'R' {
			return payload, "", nil
		}
		return nil, string(payload), nil
	}
}
