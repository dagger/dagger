package client

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"

	"github.com/dagger/dagger/internal/buildkit/session/auth"
	"github.com/dagger/dagger/internal/buildkit/session/auth/authprovider"
	"github.com/dagger/dagger/internal/buildkit/session/filesync"
	"github.com/dagger/dagger/internal/buildkit/session/secrets"
	"github.com/dagger/dagger/internal/buildkit/session/sshforward"
	"github.com/docker/cli/cli/config"
	toml "github.com/pelletier/go-toml"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client/secretprovider"
	"github.com/dagger/dagger/engine/session/git"
	"github.com/dagger/dagger/engine/session/h2c"
	"github.com/dagger/dagger/internal/userconfig"
)

// hostPolicyTable is the table of the user config file that holds a
// HostPolicy.
const hostPolicyTable = "host_policy"

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
// that the policy does not allow. A refusal fails the session: Close returns
// the first refusal, also when the caller handled the error of the request.
//
// The client checks a read path and then opens it, so nothing untrusted must
// be able to change the read paths while a session runs.
type HostPolicy struct {
	// Version must be 1.
	Version int `toml:"version"`
	// Reason names who set the policy. Refusal messages include it.
	Reason string `toml:"reason"`

	Secrets HostPolicySecrets `toml:"secrets"`
	Files   HostPolicyFiles   `toml:"files"`
	Git     HostPolicyGit     `toml:"git"`

	// RegistryAuth allows registry credentials from the host's Docker
	// configuration.
	RegistryAuth bool `toml:"registry_auth"`
}

type HostPolicySecrets struct {
	// Providers lists the secret provider schemes that the client resolves,
	// such as "op" or "vault". A provider runs on the host with the client's
	// access, and the read paths do not limit it: "env", "file", "cmd" and
	// "llmconfig" give access to the host environment, files and commands.
	Providers []string `toml:"providers"`
}

type HostPolicyFiles struct {
	// Read lists absolute paths. The client serves reads of these paths and
	// of everything under them, after it resolves symlinks.
	Read []string `toml:"read"`
}

type HostPolicyGit struct {
	// Credentials allows Git credentials and the host's Git configuration for
	// remote repositories.
	Credentials bool `toml:"credentials"`
}

// LoadHostPolicy reads the [host_policy] table of the user config file
// (userconfig.Path). It returns nil when the file or the table does not
// exist. See ParseHostPolicy.
func LoadHostPolicy() (*HostPolicy, error) {
	return loadHostPolicy(userconfig.Path())
}

func loadHostPolicy(path string) (*HostPolicy, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		// The file can have a policy.
		return nil, fmt.Errorf("read host policy: %w", err)
	}
	policy, err := ParseHostPolicy(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return policy, nil
}

// ParseHostPolicy parses the [host_policy] table of a user config file. It
// returns nil when the table does not exist.
//
// Other settings of the file are read leniently, but a policy fails closed. A
// file that does not parse and contains "host_policy", unknown fields, an
// unsupported version, unknown secret providers and relative paths are
// errors, so that a client never runs with a policy that it does not fully
// understand.
func ParseHostPolicy(data []byte) (*HostPolicy, error) {
	doc, err := toml.LoadBytes(data)
	if err != nil {
		if bytes.Contains(data, []byte(hostPolicyTable)) {
			return nil, fmt.Errorf("parse host policy: %w", err)
		}
		return nil, nil
	}
	value := doc.Get(hostPolicyTable)
	if value == nil {
		return nil, nil
	}
	table, ok := value.(*toml.Tree)
	if !ok {
		return nil, fmt.Errorf("parse host policy: %s is not a table", hostPolicyTable)
	}
	var policy HostPolicy
	if err := toml.NewDecoder(strings.NewReader(table.String())).Strict(true).Decode(&policy); err != nil {
		return nil, fmt.Errorf("parse host policy: %w", err)
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

// AllowsRead reports whether an absolute path is under a read path of the
// policy, with symlinks resolved.
func (p *HostPolicy) AllowsRead(path string) bool {
	if p == nil {
		return true
	}
	if !filepath.IsAbs(path) {
		return false
	}
	resolved, err := resolveHostPath(path)
	if err != nil {
		return false
	}
	return slices.ContainsFunc(p.Files.Read, func(root string) bool {
		resolvedRoot, err := resolveHostPath(root)
		return err == nil && pathWithin(resolved, resolvedRoot)
	})
}

// hostPolicyEnforcer applies a HostPolicy to one client session.
type hostPolicyEnforcer struct {
	policy *HostPolicy
	// readRoots are the policy's read paths with symlinks resolved.
	readRoots []string

	// violation is the first refusal.
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
// refusal is the result of the session.
func (e *hostPolicyEnforcer) deny(format string, args ...any) error {
	msg := e.message(format, args...)
	err := errors.New(msg)
	e.violation.CompareAndSwap(nil, &err)
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
		provider, ok := authprovider.NewDockerAuthProvider(config.LoadDefaultConfigFile(os.Stderr), nil).(auth.AuthServer)
		if !ok {
			return nil, errors.New("host policy: the registry auth provider does not serve credentials")
		}
		attachables = append(attachables, policyRegistryAuth{AuthServer: provider})
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
// does not exist, it resolves its deepest existing parent. A symlink whose
// target does not exist resolves to that target.
func resolveHostPath(p string) (string, error) {
	p = filepath.Clean(p)
	var missing []string
	for links := 0; ; {
		resolved, err := filepath.EvalSymlinks(p)
		if err == nil {
			return filepath.Join(append([]string{resolved}, missing...)...), nil
		}
		if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, syscall.ENOTDIR) {
			return "", err
		}
		if target, err := os.Readlink(p); err == nil {
			if links++; links > 255 {
				return "", &os.PathError{Op: "resolve", Path: p, Err: syscall.ELOOP}
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(p), target)
			}
			p = filepath.Clean(target)
			continue
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

// policyRegistryAuth serves only registry credentials. The engine does not
// ask the client to fetch tokens.
type policyRegistryAuth struct {
	auth.AuthServer
}

func (a policyRegistryAuth) Register(server *grpc.Server) {
	auth.RegisterCredentialsServer(server, a)
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

// FindUp searches only under the read roots: in their directories, for names
// whose paths stay under them. Like a stat, it finds nothing elsewhere and
// refuses nothing: a search above a workspace is normal.
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

// Listen refuses a tunnel. It reads the listen request first, so that the
// engine gets the refusal and not a closed stream.
func (p policyDeniedTunnels) Listen(stream h2c.TunnelListener_ListenServer) error {
	_, _ = stream.Recv()
	return p.policy.deny("listening on host ports is not allowed")
}
