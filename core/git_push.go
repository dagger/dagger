package core

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/util/gitutil"
)

const GitPushTimeout = 2 * time.Minute

type GitPushOpts struct {
	Branch            string
	ExpectedRemoteSHA string
}

func (opts GitPushOpts) Ref(source *gitutil.Ref) (string, error) {
	branch := opts.Branch
	if strings.HasPrefix(branch, "-") {
		return "", fmt.Errorf("invalid push destination ref %q", branch)
	}
	if branch == "" {
		if source == nil || !strings.HasPrefix(source.Name, "refs/heads/") {
			return "", fmt.Errorf("push requires an explicit branch for a detached or non-branch ref")
		}
		branch = source.Name
	}
	if !strings.HasPrefix(branch, "refs/") {
		branch = "refs/heads/" + branch
	}
	if opts.ExpectedRemoteSHA != "" && !gitPushSHA(opts.ExpectedRemoteSHA) {
		return "", fmt.Errorf("expectedRemoteSHA must be a full lowercase object ID, or empty for a non-force push")
	}
	return branch, nil
}

func gitPushSHA(sha string) bool {
	if len(sha) != 40 && len(sha) != 64 {
		return false
	}
	_, err := hex.DecodeString(sha)
	return err == nil && sha == strings.ToLower(sha) && strings.Trim(sha, "0") != ""
}

// Push copies only the selected history into a disposable repository. Neither
// checkout configuration/hooks nor source credentials can affect the push.
func (ref *GitRef) Push(ctx context.Context, destination *RemoteGitRepository, opts GitPushOpts) (*GitPushResult, error) {
	name, err := opts.Ref(ref.Ref)
	if err != nil {
		return nil, err
	}
	if ref.Ref == nil || !gitPushSHA(ref.Ref.SHA) {
		return nil, fmt.Errorf("push requires a resolved source commit")
	}
	local := gitutil.NewGitCLI(gitPushCLIOptions()...)
	if _, err := local.Run(ctx, "check-ref-format", name); err != nil {
		return nil, fmt.Errorf("invalid push destination ref %q: %w", name, err)
	}
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	// HTTP userinfo may contain a token even without a password. Never put
	// embedded credentials in a permission prompt or in Git's output.
	if destination.URL.User != nil {
		_, hasPassword := destination.URL.User.Password()
		if hasPassword || destination.URL.Scheme == gitutil.HTTPProtocol || destination.URL.Scheme == gitutil.HTTPSProtocol {
			return nil, fmt.Errorf("push destination must not contain embedded credentials; use a secret instead")
		}
	}
	owner, err := query.AuthorizeGitPush(ctx, destination.URL.Remote(), name, opts.ExpectedRemoteSHA != "")
	if err != nil {
		return nil, err
	}
	// Prepare history before asking the owner to unlock a key. Local fetch also
	// spawns upload-pack; cancel its entire process group on timeout.
	local = local.New(gitutil.WithExec(runProcessGroup))
	tmp, err := os.MkdirTemp("", "dagger-git-push-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	local = local.New(gitutil.WithGitDir(tmp))
	prepareCtx, prepareCancel := context.WithTimeout(ctx, GitPushTimeout)
	defer prepareCancel()
	format := "sha1"
	if len(ref.Ref.SHA) == 64 {
		format = "sha256"
	}
	if _, err := local.Run(prepareCtx, "init", "--bare", "--object-format="+format); err != nil {
		return nil, err
	}
	err = ref.Repo.Self().Backend.mount(prepareCtx, 0, false, []GitRefBackend{ref.Backend}, func(source *gitutil.GitCLI) error {
		url, err := source.URL(prepareCtx)
		if err != nil {
			return err
		}
		_, err = local.Run(prepareCtx, "fetch", "--quiet", "--no-tags", "--no-write-fetch-head", "--no-recurse-submodules", url, ref.Ref.SHA+":refs/dagger/push/source")
		return err
	})
	prepareCancel()
	if err != nil {
		return nil, fmt.Errorf("prepare push history: %w", err)
	}
	// Preparing owner credentials can ask for a passphrase. Only a push reaches
	// this request, after authorization; reads never initialize/unlock an agent.
	sshAuthPath, err := prepareGitPushSSHAuth(ctx, query, owner, destination)
	if err != nil {
		return nil, err
	}
	// Human approval and key unlocking do not consume the network deadline.
	ctx, cancel := context.WithTimeout(ctx, GitPushTimeout)
	defer cancel()
	authCtx := engine.ContextWithClientMetadata(ctx, owner)
	var sshAuthSock string
	if destination.URL.Scheme == gitutil.SSHProtocol && destination.SSHAuthSocket.Self() == nil && sshAuthPath != "" {
		// Mount only for this operation, without registering a socket handle or
		// returning an authenticated GitRepository to the module.
		socket := &Socket{
			Kind:           SocketKindUnixOpaque,
			URLVal:         (&url.URL{Scheme: "unix", Path: sshAuthPath}).String(),
			SourceClientID: owner.ClientID,
		}
		var cleanup func() error
		sshAuthSock, cleanup, err = socket.MountSSHAgent(authCtx)
		if err != nil {
			return nil, err
		}
		defer cleanup()
	}
	svcs, err := query.Services(ctx)
	if err != nil {
		return nil, err
	}
	detach, _, err := svcs.StartBindings(ctx, destination.Services)
	if err != nil {
		return nil, err
	}
	defer detach()
	pushGit, cleanup, err := destination.setupWithSSHAuthSock(ctx, sshAuthSock)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	pushGit = pushGit.New(gitPushCLIOptions()...)
	// Fetch may be public while receive-pack requires authentication. Approval
	// permits operation-local owner credentials, not a transferable capability.
	if destination.AuthToken.Self() == nil && destination.AuthHeader.Self() == nil &&
		(destination.URL.Scheme == gitutil.HTTPProtocol || destination.URL.Scheme == gitutil.HTTPSProtocol) {
		bk, err := query.Engine(authCtx)
		if err != nil {
			return nil, err
		}
		credential, err := bk.GetCredential(authCtx, destination.URL.Scheme, destination.URL.Host, destination.URL.Path)
		if err == nil {
			pushGit = pushGit.New(gitutil.WithHTTPTokenAuth(destination.URL, credential.Password, credential.Username))
		} // Missing credentials are permitted for anonymous writable remotes.
	}
	return runGitPush(ctx, pushGit.New(gitutil.WithGitDir(tmp)), destination.URL.Remote(), name, ref.Ref.SHA, opts.ExpectedRemoteSHA)
}

func prepareGitPushSSHAuth(ctx context.Context, query *Query, owner *engine.ClientMetadata, destination *RemoteGitRepository) (string, error) {
	if destination.URL.Scheme != gitutil.SSHProtocol || destination.SSHAuthSocket.Self() != nil || owner.SSHAuthSocketPath != "" {
		return owner.SSHAuthSocketPath, nil
	}
	authCtx := engine.ContextWithClientMetadata(ctx, owner)
	bk, err := query.Engine(authCtx)
	if err != nil {
		return "", err
	}
	path, err := bk.PrepareGitPushSSHAuth(authCtx, destination.URL.Remote())
	if err != nil {
		return "", fmt.Errorf("prepare SSH push authentication: %w", err)
	}
	return path, nil
}

func gitPushCLIOptions() []gitutil.Option {
	return []gitutil.Option{
		gitutil.WithConfig(map[string]string{
			"http.followRedirects": "false",
			"core.hooksPath":       "/dev/null", "core.abbrev": "no",
			"maintenance.auto": "false", "gc.auto": "0", "push.followTags": "false",
			"push.gpgSign": "false", "push.recurseSubmodules": "no",
		}),
		gitutil.WithStreams(func(context.Context) (io.WriteCloser, io.WriteCloser, func()) {
			return new(gitPushOutputLimit), new(gitPushOutputLimit), func() {}
		}),
	}
}

type gitPushOutputLimit struct{ size int }

func (out *gitPushOutputLimit) Write(p []byte) (int, error) {
	if len(p) > (16<<20)-out.size {
		return 0, fmt.Errorf("git push command output exceeds 16 MiB")
	}
	out.size += len(p)
	return len(p), nil
}
func (*gitPushOutputLimit) Close() error { return nil }

func runGitPush(ctx context.Context, git *gitutil.GitCLI, url, name, sha, expected string) (*GitPushResult, error) {
	args := []string{"push", "--porcelain", "--no-verify", "--no-follow-tags", "--recurse-submodules=no", "--signed=false"}
	if expected != "" {
		args = append(args, "--force-with-lease="+name+":"+expected)
	}
	args = append(args, "--", url, sha+":"+name)
	out, err := git.Run(ctx, args...)
	if err != nil {
		// Porcelain includes the rejected ref and its reason; stderr stays in
		// Git's trace, without copying credential-bearing configuration here.
		return nil, fmt.Errorf("push %s failed: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	result, err := parseGitPushResult(string(out), name, sha)
	if err != nil {
		return nil, fmt.Errorf("push completed but its result could not be decoded; inspect remote %s before retrying: %w", name, err)
	}
	// Git skips lease checking when the remote already equals the source.
	// Enforce the caller's precondition for no-op pushes as well.
	if expected != "" && result.PreviousSHA != expected {
		return nil, fmt.Errorf("push %s: stale lease: expected %q, remote was %q", name, expected, result.PreviousSHA)
	}
	return result, nil
}

func parseGitPushResult(output, name, sha string) (*GitPushResult, error) {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) != 3 || len(fields[0]) != 1 {
			continue
		}
		_, dest, ok := strings.Cut(fields[1], ":")
		if !ok || dest != name {
			continue
		}
		result := &GitPushResult{Ref: name, SHA: sha}
		switch fields[0] {
		case "*":
			result.Disposition = GitPushCreated
		case "=":
			result.Disposition, result.PreviousSHA = GitPushUpToDate, sha
		case " ", "+":
			result.Disposition = GitPushFastForward
			separator := ".."
			if fields[0] == "+" {
				result.Disposition, separator = GitPushForced, "..."
			}
			summary := strings.Fields(fields[2])
			if len(summary) == 0 {
				return nil, fmt.Errorf("empty push range")
			}
			previous, next, ok := strings.Cut(summary[0], separator)
			if !ok || !gitPushSHA(previous) || next != sha {
				return nil, fmt.Errorf("invalid push range %q", fields[2])
			}
			result.PreviousSHA = previous
		default:
			return nil, fmt.Errorf("unexpected push status %q", line)
		}
		return result, nil
	}
	return nil, fmt.Errorf("missing status for %s", name)
}
