package session

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"syscall"

	"github.com/containerd/containerd/content"
	"github.com/dagger/dagger/auth"
	"github.com/docker/cli/cli/config"
	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/exporter"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/gateway/container"
	"github.com/moby/buildkit/identity"
	"github.com/moby/buildkit/session"
	sessioncontent "github.com/moby/buildkit/session/content"
	"github.com/moby/buildkit/util/bklog"
	"github.com/moby/buildkit/worker"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

const (
	ServerIDMetaKey           = "dagger-server-id"
	RequesterSessionIDMetaKey = "dagger-requester-session-id"

	// local dir import
	localDirImportDirNameMetaKey = "dir-name" // from buildkit

	// local dir export
	localDirExportDestSessionIDMetaKey = "dagger-local-dir-export-dest-session-id"
	LocalDirExportDestPathMetaKey      = "dagger-local-dir-export-dest-path"

	// worker label
	DaggerFrontendSessionIDLabel = "dagger-frontend-session-id"
)

func ContextWithSessionMetadata(ctx context.Context, serverID, requesterSessionID string) context.Context {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		md = metadata.MD{}
	}
	md[ServerIDMetaKey] = []string{serverID}
	md[RequesterSessionIDMetaKey] = []string{requesterSessionID}
	return metadata.NewIncomingContext(ctx, md)
}

func SessionMetadataFromContext(ctx context.Context) (string, string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", "", fmt.Errorf("failed to get metadata from context")
	}
	if len(md[ServerIDMetaKey]) != 1 {
		return "", "", fmt.Errorf("failed to get %s from metadata", ServerIDMetaKey)
	}
	if len(md[RequesterSessionIDMetaKey]) != 1 {
		return "", "", fmt.Errorf("failed to get %s from metadata", RequesterSessionIDMetaKey)
	}
	return md[ServerIDMetaKey][0], md[RequesterSessionIDMetaKey][0], nil
}

func serverIDFromContext(ctx context.Context) (string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", fmt.Errorf("failed to get metadata from context")
	}
	if len(md[ServerIDMetaKey]) != 1 {
		return "", fmt.Errorf("failed to get %s from metadata", ServerIDMetaKey)
	}
	return md[ServerIDMetaKey][0], nil
}

type Manager struct {
	bkSessionManager *session.Manager
	worker           worker.Worker
	baseSession      *session.Session
}

const OCIStoreName = "dagger-oci"

func NewManager(ctx context.Context, w worker.Worker, bkSessionManager *session.Manager) (*Manager, error) {
	sm := &Manager{
		bkSessionManager: bkSessionManager,
		worker:           w,
	}

	baseSession, err := session.NewSession(ctx, identity.NewID(), "")
	if err != nil {
		return nil, err
	}
	sm.baseSession = baseSession
	sm.baseSession.Allow(&fileSendServerProxy{sm})
	sm.baseSession.Allow(&fileSyncServerProxy{sm})
	sm.baseSession.Allow(sessioncontent.NewAttachable(map[string]content.Store{
		// the "oci:" prefix is actually interpreted by buildkit, not just for show
		"oci:" + OCIStoreName: w.ContentStore(),
	}))
	// TODO: this should proxy out to the right session, this is just to unblock dockerhub rate limits for now
	sm.baseSession.Allow(auth.NewRegistryAuthProvider(config.LoadDefaultConfigFile(os.Stderr)))

	// TODO: not sure if safe to use net.Pipe due to possible library assumptions about buffering, but would be nice...
	clientConn, serverConn := net.Pipe()
	/*
		clientConn, serverConn, err := socketpair()
		if err != nil {
			return nil, err
		}
	*/

	dialer := func(ctx context.Context, proto string, meta map[string][]string) (net.Conn, error) {
		go func() {
			defer serverConn.Close()
			err := bkSessionManager.HandleConn(ctx, serverConn, meta)
			if err != nil {
				lg := bklog.G(ctx).WithError(err)
				if errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
					lg.Debug("session conn in dagger frontend ended")
				} else {
					lg.Fatal("failed to handle session conn in dagger frontend")
				}
			}
		}()
		return clientConn, nil
	}
	go func() {
		defer clientConn.Close()
		defer baseSession.Close()
		err := baseSession.Run(ctx, dialer)
		if err != nil {
			lg := bklog.G(ctx).WithError(err)
			if errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
				lg.Debug("client session in dagger frontend ended")
			} else {
				lg.Fatal("failed to run dagger frontend session")
			}
		}
	}()

	w.Labels()[DaggerFrontendSessionIDLabel] = baseSession.ID() // TODO: const
	return sm, nil
}

func (sm *Manager) ID() string {
	return sm.baseSession.ID()
}

func (sm *Manager) NewContainer(ctx context.Context, req container.NewContainerRequest) (bkgw.Container, error) {
	return container.NewContainer(
		ctx,
		sm.worker,
		sm.bkSessionManager,
		session.NewGroup(sm.ID()),
		req,
	)
}

func (sm *Manager) GetCaller(ctx context.Context, clientSessionID string) (session.Caller, error) {
	waitForSession := true
	return sm.bkSessionManager.Get(ctx, clientSessionID, !waitForSession)
}

type localImportOpts struct {
	ServerID       string `json:"serverID,omitempty"`
	OwnerSessionID string `json:"ownerSessionID,omitempty"`
	Path           string `json:"path,omitempty"`
}

func (sm *Manager) LocalLLBName(ctx context.Context, path string) (string, error) {
	serverID, destSessionID, err := SessionMetadataFromContext(ctx)
	if err != nil {
		return "", err
	}
	nameBytes, err := json.Marshal(localImportOpts{
		ServerID: serverID,
		// TODO: for now, the requester is always the owner of the local dir
		// when the dir is initially created in LLB (i.e. you can't request a
		// a new local dir from another session, you can only be passed one
		// from another session already created).
		OwnerSessionID: destSessionID,
		Path:           path,
	})
	if err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(nameBytes), nil
}

func (sm *Manager) LocalExport(ctx context.Context, res *exporter.Source, destPath string) (rerr error) {
	exporter, err := sm.worker.Exporter(bkclient.ExporterLocal, sm.bkSessionManager)
	if err != nil {
		return err
	}

	expInstance, err := exporter.Resolve(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to resolve exporter: %s", err)
	}

	_, destSessionID, err := SessionMetadataFromContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to get requester session ID: %s", err)
	}

	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		md = metadata.MD{}
	}
	md[localDirExportDestSessionIDMetaKey] = []string{destSessionID}
	md[LocalDirExportDestPathMetaKey] = []string{destPath}

	ctx = metadata.NewOutgoingContext(ctx, md)

	// TODO: could optimize by just calling relevant session methods directly, not
	// all that much code involved
	_, _, err = expInstance.Export(ctx, res, sm.ID())
	if err != nil {
		return fmt.Errorf("failed to export: %s", err)
	}
	return nil
}

type sessionData struct {
	//
	// these are currently set in router for each incoming request
	//
	// the id of the frontend instance this request went through
	serverID string
	// the id of the session that made the request
	requesterSessionID string

	//
	// only one of these should be set
	//
	importLocalDirData *importLocalDirData
	exportLocalDirData *exportLocalDirData
}

type importLocalDirData struct {
	session session.Caller
	path    string
}

type exportLocalDirData struct {
	session session.Caller
	path    string
}

func (sm *Manager) Get(stream grpc.ServerStream) (context.Context, *sessionData, error) {
	md, ok := metadata.FromIncomingContext(stream.Context())
	if !ok {
		return nil, nil, fmt.Errorf("missing metadata")
	}

	getVal := func(key string) (string, error) {
		vals, ok := md[key]
		if !ok || len(vals) == 0 {
			return "", nil
		}
		if len(vals) != 1 {
			return "", fmt.Errorf("expected exactly one %s, got %d", key, len(vals))
		}
		return vals[0], nil
	}
	ctx := metadata.NewOutgoingContext(stream.Context(), md)

	sessData := &sessionData{}

	serverID, err := getVal(ServerIDMetaKey)
	if err != nil {
		return nil, nil, err
	}
	if serverID != "" {
		sessData.serverID = serverID
	}

	requesterSessionID, err := getVal(RequesterSessionIDMetaKey)
	if err != nil {
		return nil, nil, err
	}
	if requesterSessionID != "" {
		sessData.requesterSessionID = requesterSessionID
	}

	localDirImportDirName, err := getVal(localDirImportDirNameMetaKey)
	if err != nil {
		return nil, nil, err
	}
	if localDirImportDirName != "" {
		jsonBytes, err := base64.URLEncoding.DecodeString(localDirImportDirName)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid import local dir name: %q", localDirImportDirNameMetaKey)
		}
		var opts localImportOpts
		if err := json.Unmarshal(jsonBytes, &opts); err != nil {
			return nil, nil, fmt.Errorf("invalid import local dir name: %q", localDirImportDirNameMetaKey)
		}
		sessData.serverID = opts.ServerID
		sess, err := sm.bkSessionManager.Get(stream.Context(), opts.OwnerSessionID, false)
		if err != nil {
			return nil, nil, err
		}
		sessData.importLocalDirData = &importLocalDirData{
			session: sess,
			path:    opts.Path,
		}
		// TODO: validation that requester has access
		md[localDirImportDirNameMetaKey] = []string{sessData.importLocalDirData.path}
		ctx = metadata.NewIncomingContext(ctx, md) // TODO: needed too?
		ctx = metadata.NewOutgoingContext(ctx, md)
		return ctx, sessData, nil
	}

	localDirExportDestSessionID, err := getVal(localDirExportDestSessionIDMetaKey)
	if err != nil {
		return nil, nil, err
	}
	if localDirExportDestSessionID != "" {
		// for now, require that the requester is the owner of the session, i.e. you
		// can only export to yourself, not to others
		if sessData.requesterSessionID != localDirExportDestSessionID {
			return nil, nil, errors.New("local dir export requester is not the owner of the dest session")
		}

		localDirExportDestPath, err := getVal(LocalDirExportDestPathMetaKey)
		if err != nil {
			return nil, nil, err
		}
		if localDirExportDestPath == "" {
			return nil, nil, fmt.Errorf("missing %s", LocalDirExportDestPathMetaKey)
		}

		sess, err := sm.bkSessionManager.Get(stream.Context(), localDirExportDestSessionID, false)
		if err != nil {
			return nil, nil, err
		}
		sessData.exportLocalDirData = &exportLocalDirData{
			session: sess,
			path:    localDirExportDestPath,
		}
		return ctx, sessData, nil
	}

	return nil, nil, fmt.Errorf("unhandled session resource stream %T", stream)
}

// TODO: delete if unused
func socketpair() (net.Conn, net.Conn, error) {
	fds, err := syscall.Socketpair(syscall.AF_LOCAL, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, nil, err
	}
	c1, err := fdToFileConn(fds[0])
	if err != nil {
		return nil, nil, err
	}
	c2, err := fdToFileConn(fds[1])
	if err != nil {
		c1.Close()
		return nil, nil, err
	}
	return c1, c2, err
}

// TODO: delete if unused
func fdToFileConn(fd int) (net.Conn, error) {
	f := os.NewFile(uintptr(fd), "")
	defer f.Close()
	return net.FileConn(f)
}
