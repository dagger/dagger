package buildkit

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"

	"github.com/containerd/containerd/content"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/session/networks"
	"github.com/dagger/dagger/network"
	"github.com/moby/buildkit/identity"
	bksession "github.com/moby/buildkit/session"
	sessioncontent "github.com/moby/buildkit/session/content"
	"github.com/moby/buildkit/session/secrets/secretsprovider"
	"github.com/moby/buildkit/session/sshforward"
	"github.com/moby/buildkit/util/bklog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// OCIStoreName is the name of the OCI content store used for OCI tarball
// imports.
const OCIStoreName = "dagger-oci"

func (c *Client) newSession(ctx context.Context) (*bksession.Session, error) {
	sess, err := bksession.NewSession(ctx, identity.NewID(), "")
	if err != nil {
		return nil, err
	}

	// TODO: enforce that callers are granted access to the given resources.
	sess.Allow(secretsprovider.NewSecretProvider(c.SecretStore))
	sess.Allow(&socketProxy{c})
	sess.Allow(&authProxy{c})
	sess.Allow(&fileSendServerProxy{c: c})
	sess.Allow(&fileSyncServerProxy{c})
	sess.Allow(sessioncontent.NewAttachable(map[string]content.Store{
		// the "oci:" prefix is actually interpreted by buildkit, not just for show
		"oci:" + OCIStoreName: c.Worker.ContentStore(),
	}))

	searchDomains := []string{}
	for _, id := range c.Metadata.ClientIDs() {
		searchDomains = append(searchDomains, network.ClientDomain(id))
	}

	sess.Allow(networks.NewConfigProvider(func(id string) *networks.Config {
		switch id {
		case network.DaggerNetwork:
			return &networks.Config{
				Dns: &networks.DNSConfig{
					SearchDomains: searchDomains,
				},
			}
		default:
			return nil
		}
	}))

	clientConn, serverConn := net.Pipe()
	dialer := func(ctx context.Context, proto string, meta map[string][]string) (net.Conn, error) {
		go func() {
			defer serverConn.Close()
			err := c.SessionManager.HandleConn(ctx, serverConn, meta)
			if err != nil {
				lg := bklog.G(ctx).WithError(err)
				if errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
					lg.Debug("session conn ended")
				} else {
					// TODO: cancel the whole buildkit client
					lg.Error("failed to handle session conn")
				}
			}
		}()
		return clientConn, nil
	}
	go func() {
		defer clientConn.Close()
		defer sess.Close()
		err := sess.Run(ctx, dialer)
		if err != nil {
			lg := bklog.G(ctx).WithError(err)
			if errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
				lg.Debug("client session in dagger frontend ended")
			} else {
				lg.Fatal("failed to run dagger frontend session")
			}
		}
	}()
	return sess, nil
}

func (c *Client) GetSessionCaller(ctx context.Context, clientID string) (bksession.Caller, error) {
	waitForSession := true
	return c.SessionManager.Get(ctx, clientID, !waitForSession)
}

type sessionStreamResourceData struct {
	// the id of the client that made the request
	requesterClientID string

	//
	// only one of these should be set
	//
	importLocalDirData *importLocalDirData
	exportLocalDirData *exportLocalDirData
	socketData         *socketData
}

type importLocalDirData struct {
	session bksession.Caller
	path    string
}

type exportLocalDirData struct {
	session bksession.Caller
	path    string
	// TODO: this is ugly, but buildkit uses two different message types on the same stream depending on how it's called
	useBytesMessageType bool
}

type socketData struct {
	session bksession.Caller
	path    string
}

func (c *Client) getSocketDataFromID(ctx context.Context, id string) (*socketData, error) {
	jsonBytes, err := base64.URLEncoding.DecodeString(id)
	if err != nil {
		return nil, fmt.Errorf("invalid socket id: %q", id)
	}
	var opts hostSocketOpts
	if err := json.Unmarshal(jsonBytes, &opts); err != nil {
		return nil, fmt.Errorf("invalid socket id: %q", id)
	}
	data := &socketData{
		path: opts.HostPath,
	}
	clientID, err := c.getClientIDByHostname(opts.ClientHostname)
	if err != nil {
		// TODO:
		return nil, fmt.Errorf("failed to get client hostname for socket: %w: %s", err, string(jsonBytes))
	}
	sess, err := c.SessionManager.Get(ctx, clientID, false)
	if err != nil {
		return nil, err
	}
	data.session = sess
	return data, nil
}

// TODO: just split this method out into one for each resource type, never need multiple at once, cleanup with above method too
func (c *Client) GetSessionResourceData(stream grpc.ServerStream) (context.Context, *sessionStreamResourceData, error) {
	incomingMD, incomingOk := metadata.FromIncomingContext(stream.Context())
	outgoingMD, outgoingOk := metadata.FromOutgoingContext(stream.Context())
	if !incomingOk && !outgoingOk {
		return nil, nil, fmt.Errorf("no grpc metadata")
	}
	md := metadata.Join(incomingMD, outgoingMD)

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

	sessData := &sessionStreamResourceData{}

	requesterClientID, err := getVal(engine.ClientIDMetaKey)
	if err != nil {
		return nil, nil, err
	}
	if requesterClientID != "" {
		sessData.requesterClientID = requesterClientID
	}

	localDirImportDirName, err := getVal(engine.LocalDirImportDirNameMetaKey)
	if err != nil {
		return nil, nil, err
	}
	if localDirImportDirName != "" {
		jsonBytes, err := base64.URLEncoding.DecodeString(localDirImportDirName)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid import local dir name: %q", engine.LocalDirImportDirNameMetaKey)
		}
		var opts LocalImportOpts
		if err := json.Unmarshal(jsonBytes, &opts); err != nil {
			return nil, nil, fmt.Errorf("invalid import local dir name: %q", engine.LocalDirImportDirNameMetaKey)
		}
		sess, err := c.SessionManager.Get(stream.Context(), opts.OwnerClientID, false)
		if err != nil {
			return nil, nil, err
		}
		sessData.importLocalDirData = &importLocalDirData{
			session: sess,
			path:    opts.Path,
		}
		// TODO: validation that requester has access
		md[engine.LocalDirImportDirNameMetaKey] = []string{sessData.importLocalDirData.path}
		ctx = metadata.NewIncomingContext(ctx, md) // TODO: needed too?
		ctx = metadata.NewOutgoingContext(ctx, md)
		return ctx, sessData, nil
	}

	localDirExportDestClientID, err := getVal(engine.LocalDirExportDestClientIDMetaKey)
	if err != nil {
		return nil, nil, err
	}
	if localDirExportDestClientID != "" {
		// for now, require that the requester is the owner of the session, i.e. you
		// can only export to yourself, not to others
		if sessData.requesterClientID != localDirExportDestClientID {
			return nil, nil, errors.New("local dir export requester is not the owner of the dest session")
		}

		localDirExportDestPath, err := getVal(engine.LocalDirExportDestPathMetaKey)
		if err != nil {
			return nil, nil, err
		}
		if localDirExportDestPath == "" {
			return nil, nil, fmt.Errorf("missing %s", engine.LocalDirExportDestPathMetaKey)
		}

		sess, err := c.SessionManager.Get(stream.Context(), localDirExportDestClientID, false)
		if err != nil {
			return nil, nil, err
		}
		sessData.exportLocalDirData = &exportLocalDirData{
			session: sess,
			path:    localDirExportDestPath,
		}
		return ctx, sessData, nil
	}

	socketID, err := getVal(sshforward.KeySSHID)
	if err != nil {
		return nil, nil, err
	}
	if socketID != "" {
		data, err := c.getSocketDataFromID(stream.Context(), socketID)
		if err != nil {
			return nil, nil, err
		}
		sessData.socketData = data
		// TODO: validation that requester has access
		ctx = metadata.NewIncomingContext(ctx, md) // TODO: needed too?
		ctx = metadata.NewOutgoingContext(ctx, md)
		return ctx, sessData, nil
	}

	return nil, nil, fmt.Errorf("unhandled session resource stream %T", stream)
}
