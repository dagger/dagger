package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"

	"github.com/dagger/dagger/internal/buildkit/session/auth/authprovider"
	"github.com/dagger/dagger/internal/buildkit/session/filesync"
	"github.com/dagger/dagger/internal/buildkit/session/secrets"
	"github.com/dagger/dagger/internal/buildkit/session/sshforward"
	"github.com/docker/cli/cli/config"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client/secretprovider"
	"github.com/dagger/dagger/engine/session/git"
	"github.com/dagger/dagger/engine/session/h2c"
)

// HostPolicyEnv is the environment variable from which the CLI reads a
// HostPolicy, as JSON. An empty or unset value means no policy.
const HostPolicyEnv = "_EXPERIMENTAL_DAGGER_HOST_POLICY"

// HostPolicy limits what a client does on its host when the engine asks it to.
// A client without a policy serves every host request. A client with a policy
// serves only what the policy allows:
//
//   - secrets from the listed provider schemes,
//   - reads of files and directories under the listed absolute paths,
//   - Git credentials and configuration for remote repositories,
//   - registry credentials.
//
// The client does not serve anything else: file writes, host sockets and
// network, host port tunnels, local Git repositories, terminals, prompts,
// standard input and output, and image loading. The client does not register
// the services that the engine may probe, so the engine sees a client without
// them. Outside the read paths, the host looks empty: a stat finds nothing,
// except the directories that contain a read path. The client refuses
// requests for secrets, file contents, Git configuration, sockets and tunnels
// that the policy does not allow, and the first refusal stops the session:
// later requests fail with the refusal, and Close returns it.
type HostPolicy struct {
	// Version must be 1.
	Version int `json:"version"`
	// Reason names who set the policy. Refusal messages include it.
	Reason string `json:"reason,omitempty"`

	Secrets HostPolicySecrets `json:"secrets"`
	Files   HostPolicyFiles   `json:"files"`
	Git     HostPolicyGit     `json:"git"`

	// RegistryAuth allows registry credentials from the host's Docker
	// configuration.
	RegistryAuth bool `json:"registryAuth,omitempty"`
}

type HostPolicySecrets struct {
	// Providers lists the secret provider schemes that the client resolves,
	// such as "op" or "vault". A provider runs on the host with the client's
	// access, and the read paths do not limit it: "env", "file", "cmd" and
	// "llmconfig" give access to the host environment, files and commands.
	Providers []string `json:"providers,omitempty"`
}

type HostPolicyFiles struct {
	// Read lists absolute paths. The client serves reads of these paths and
	// of everything under them, after it resolves symlinks.
	Read []string `json:"read,omitempty"`
}

type HostPolicyGit struct {
	// Credentials allows Git credentials and the host's Git configuration for
	// remote repositories.
	Credentials bool `json:"credentials,omitempty"`
}

// ParseHostPolicy parses a policy from JSON. It returns nil for an empty
// value. It returns an error for unknown fields, an unsupported version,
// unknown secret providers and relative paths, so that a client never runs
// with a policy that it does not fully understand.
func ParseHostPolicy(value string) (*HostPolicy, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	dec := json.NewDecoder(strings.NewReader(value))
	dec.DisallowUnknownFields()
	var policy HostPolicy
	if err := dec.Decode(&policy); err != nil {
		return nil, fmt.Errorf("parse host policy: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("parse host policy: unexpected data after the policy")
	}
	if policy.Version != 1 {
		return nil, fmt.Errorf("host policy version %d is not supported", policy.Version)
	}
	schemes := secretprovider.Schemes()
	for _, scheme := range policy.Secrets.Providers {
		if !slices.Contains(schemes, scheme) {
			return nil, fmt.Errorf("host policy: unknown secret provider %q", scheme)
		}
	}
	for i, p := range policy.Files.Read {
		if !filepath.IsAbs(p) {
			return nil, fmt.Errorf("host policy: read path %q is not absolute", p)
		}
		policy.Files.Read[i] = filepath.Clean(p)
	}
	return &policy, nil
}

// AllowsRead reports whether p is under a read path of the policy, without
// resolving symlinks. The client checks every read again with symlinks
// resolved.
func (p *HostPolicy) AllowsRead(path string) bool {
	if p == nil {
		return true
	}
	path = filepath.Clean(path)
	return slices.ContainsFunc(p.Files.Read, func(root string) bool {
		return pathWithin(path, root)
	})
}

// hostPolicyEnforcer applies a HostPolicy to one client session.
type hostPolicyEnforcer struct {
	policy *HostPolicy
	// readRoots are the policy's read paths with symlinks resolved.
	readRoots []string

	// stopped is cancelled with the first refusal.
	stopped   context.Context
	stop      context.CancelCauseFunc
	violation atomic.Pointer[error]
}

func newHostPolicyEnforcer(policy *HostPolicy) (*hostPolicyEnforcer, error) {
	e := &hostPolicyEnforcer{policy: policy}
	for _, root := range policy.Files.Read {
		resolved, err := resolveHostPath(root)
		if err != nil {
			return nil, fmt.Errorf("host policy: resolve read path %s: %w", root, err)
		}
		e.readRoots = append(e.readRoots, resolved)
	}
	e.stopped, e.stop = context.WithCancelCause(context.Background())
	return e, nil
}

func (e *hostPolicyEnforcer) message(format string, args ...any) string {
	msg := "host access denied by session policy"
	if e.policy.Reason != "" {
		msg += " (" + e.policy.Reason + ")"
	}
	return msg + ": " + fmt.Sprintf(format, args...)
}

// hidden returns the not-found error for a stat outside the read paths. It
// is not a refusal: the engine stats paths to find files, for example
// workspace configuration in the parents of a directory.
func (e *hostPolicyEnforcer) hidden(path string) error {
	return status.Error(codes.NotFound, e.message("%s is outside the read paths", path))
}

// deny records a refusal and returns the error for the engine. The first
// refusal stops the session.
func (e *hostPolicyEnforcer) deny(format string, args ...any) error {
	msg := e.message(format, args...)
	err := errors.New(msg)
	if e.violation.CompareAndSwap(nil, &err) {
		e.stop(err)
	}
	return status.Error(codes.PermissionDenied, msg)
}

// err returns the first refusal, or nil.
func (e *hostPolicyEnforcer) err() error {
	if err := e.violation.Load(); err != nil {
		return *err
	}
	return nil
}

// canRead reports whether p is under a read root, with symlinks resolved.
func (e *hostPolicyEnforcer) canRead(p string) bool {
	resolved, err := resolveHostPath(p)
	if err != nil {
		return false
	}
	return slices.ContainsFunc(e.readRoots, func(root string) bool {
		return pathWithin(resolved, root)
	})
}

// canStat reports whether a stat of p shows the host's answer. That is true
// under a read root and for the directories that contain one: a sync of a
// read root stats each of its parents.
func (e *hostPolicyEnforcer) canStat(p string) bool {
	resolved, err := resolveHostPath(p)
	if err != nil {
		return false
	}
	return slices.ContainsFunc(e.readRoots, func(root string) bool {
		return pathWithin(resolved, root) || pathWithin(root, resolved)
	})
}

// checkRead returns a refusal when a requested path is not under a read root.
func (e *hostPolicyEnforcer) checkRead(reqPath string) error {
	abs, err := Filesyncer{}.fullRootPathAndBaseName(reqPath, false)
	if err != nil {
		return err
	}
	if !e.canRead(abs) {
		return e.deny("reading %s is not allowed", abs)
	}
	return nil
}

func (e *hostPolicyEnforcer) attachables(ctx context.Context) ([]SessionAttachable, error) {
	attachables := []SessionAttachable{
		policySecretProvider{policy: e},
		// Sockets and tunnels are registered only to refuse them: the engine
		// calls them only for an explicit host socket, host service or tunnel.
		policyDeniedSockets{policy: e},
		policyDeniedTunnels{policy: e},
	}
	if e.policy.RegistryAuth {
		attachables = append(attachables, authprovider.NewDockerAuthProvider(config.LoadDefaultConfigFile(os.Stderr), nil))
	}
	if e.policy.Git.Credentials {
		attachables = append(attachables, policyGitCredentials{GitAttachable: git.NewGitAttachable(ctx), policy: e})
	}
	// The file source is always registered: the engine stats the client's
	// working directory and its parents at the start of every session.
	filesyncer, err := NewFilesyncer()
	if err != nil {
		return nil, fmt.Errorf("new filesyncer: %w", err)
	}
	attachables = append(attachables, policyFilesyncSource{inner: filesyncer.AsSource(), policy: e})
	return attachables, nil
}

// pathWithin reports whether p is root or under it. Both must be clean.
func pathWithin(p, root string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// resolveHostPath resolves every symlink in an absolute path. For a path that
// does not exist, it resolves its deepest existing parent.
func resolveHostPath(p string) (string, error) {
	p = filepath.Clean(p)
	var missing []string
	for {
		resolved, err := filepath.EvalSymlinks(p)
		if err == nil {
			return filepath.Join(append([]string{resolved}, missing...)...), nil
		}
		if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, syscall.ENOTDIR) {
			return "", err
		}
		parent := filepath.Dir(p)
		if parent == p {
			return filepath.Join(append([]string{p}, missing...)...), nil
		}
		missing = append([]string{filepath.Base(p)}, missing...)
		p = parent
	}
}

type policySecretProvider struct {
	policy *hostPolicyEnforcer
}

func (p policySecretProvider) Register(server *grpc.Server) {
	secrets.RegisterSecretsServer(server, p)
}

func (p policySecretProvider) GetSecret(ctx context.Context, req *secrets.GetSecretRequest) (*secrets.GetSecretResponse, error) {
	scheme, _, _ := strings.Cut(req.ID, "://")
	allowed := p.policy.policy.Secrets.Providers
	if !slices.Contains(allowed, scheme) {
		if len(allowed) == 0 {
			return nil, p.policy.deny("secret provider %q is not allowed (no secret providers are allowed)", scheme)
		}
		return nil, p.policy.deny("secret provider %q is not allowed (allowed: %s)", scheme, strings.Join(allowed, ", "))
	}
	return secretprovider.NewSecretProvider().GetSecret(ctx, req)
}

type policyFilesyncSource struct {
	inner  FilesyncSource
	policy *hostPolicyEnforcer
}

func (s policyFilesyncSource) Register(server *grpc.Server) {
	filesync.RegisterFileSyncServer(server, s)
}

func (s policyFilesyncSource) DiffCopy(stream filesync.FileSync_DiffCopyServer) error {
	opts, err := engine.LocalImportOptsFromContext(stream.Context())
	if err != nil {
		return fmt.Errorf("get local import opts: %w", err)
	}
	switch {
	case opts.GetAbsPathOnly:
		// An absolute path request reads nothing on the host.
	case opts.StatPathOnly:
		abs, err := Filesyncer{}.fullRootPathAndBaseName(opts.Path, false)
		if err != nil {
			return err
		}
		if !s.policy.canStat(abs) {
			return s.policy.hidden(abs)
		}
	default:
		if err := s.policy.checkRead(opts.Path); err != nil {
			return err
		}
	}
	return s.inner.DiffCopy(stream)
}

// TarStream is not served: the policy checks only the requests that it knows.
func (s policyFilesyncSource) TarStream(filesync.FileSync_TarStreamServer) error {
	return status.Error(codes.Unimplemented, "tarstream not supported")
}

// FindUp searches only directories under the read roots. Like a stat, it
// finds nothing elsewhere and refuses nothing: a search above a workspace is
// normal.
func (s policyFilesyncSource) FindUp(ctx context.Context, req *filesync.FindUpRequest) (*filesync.FindUpResponse, error) {
	start, err := Filesyncer{}.fullRootPathAndBaseName(req.Start, false)
	if err != nil {
		return nil, fmt.Errorf("get full root path: %w", err)
	}
	return findUp(start, req.Names, s.policy.canRead)
}

type policyGitCredentials struct {
	git.GitAttachable
	policy *hostPolicyEnforcer
}

func (a policyGitCredentials) Register(server *grpc.Server) {
	git.RegisterRemoteCredentialsServer(server, a)
}

// GetConfig serves the host's Git configuration. A request for the
// configuration of a local checkout is a read of that checkout.
func (a policyGitCredentials) GetConfig(ctx context.Context, req *git.GitConfigRequest) (*git.GitConfigResponse, error) {
	if req.GetCheckoutPath() != "" {
		if err := a.policy.checkRead(req.GetCheckoutPath()); err != nil {
			return nil, err
		}
	}
	return a.GitAttachable.GetConfig(ctx, req)
}

type policyDeniedSockets struct {
	policy *hostPolicyEnforcer
}

func (p policyDeniedSockets) Register(server *grpc.Server) {
	sshforward.RegisterSSHServer(server, p)
}

func (p policyDeniedSockets) CheckAgent(ctx context.Context, req *sshforward.CheckAgentRequest) (*sshforward.CheckAgentResponse, error) {
	return nil, p.policy.deny("host socket %s is not allowed", req.ID)
}

func (p policyDeniedSockets) ForwardAgent(stream sshforward.SSH_ForwardAgentServer) error {
	socket := "connection"
	if md, ok := metadata.FromIncomingContext(stream.Context()); ok {
		if v := md.Get(engine.SocketURLEncodedKey); len(v) > 0 && v[0] != "" {
			socket = v[0]
		}
	}
	return p.policy.deny("host socket %s is not allowed", socket)
}

type policyDeniedTunnels struct {
	policy *hostPolicyEnforcer
}

func (p policyDeniedTunnels) Register(server *grpc.Server) {
	h2c.RegisterTunnelListenerServer(server, p)
}

func (p policyDeniedTunnels) Listen(h2c.TunnelListener_ListenServer) error {
	return p.policy.deny("listening on host ports is not allowed")
}
