package server

import (
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/engine/session/git"
	"github.com/dagger/dagger/util/gitutil"
)

// Which delegated pushes ask the owner, and what they ask.
//
// A push to a remote's default branch, or to any ref that is not a branch,
// asks about that exact ref every time (gitPushApproval), as it is what
// deploys, releases or otherwise matters downstream. Pushes to any other
// branch, e.g. a pull request's, are routine development: those only need
// the remote to be approved once (gitPushRemoteApproval), unless it is the
// remote the owner's own workspace was checked out from, which is approved
// already.
//
// The remote grant cannot be dropped: approving a push also lets it use the
// owner's credentials (its Git credential helper for HTTPS, its SSH agent for
// SSH) on a URL a module chose. Host-scoped helpers answer nothing for a host
// they do not know, but catch-all helpers, e.g. `!f() { echo
// password=$GITHUB_TOKEN; }; f` as is common in CI, hand their token to any
// host asking. Without a gate, pushing a "feature branch" to a host the module
// controls would send it the owner's token.

const approvalKindGitPushRemote = "git-push-remote"

// gitPushRemoteApproval is the approval for routine pushes to remote: to any
// of its branches except its default one (as far as known, see
// gitPushNeedsRefApproval), which still asks per push. The question names the
// branches that keep asking, and whether force-pushes are included, as the
// grant covers every other branch.
func gitPushRemoteApproval(remote string, remoteDefault gitRemoteDefault) approvalSpec {
	subject := literalText(remote)
	excluded := slices.Clone(gitWellKnownDefaultBranches)
	if branch, ok := strings.CutPrefix(remoteDefault.branch, "refs/heads/"); ok && !gitPushRefAlwaysAsks(remoteDefault.branch) {
		// The remote chose this name: literal text, like the subject.
		excluded = append([]string{literalText(branch)}, excluded...)
	}
	pushes := "pushes and force-pushes"
	if gitForcePushToOtherBranchesAsks {
		pushes = "pushes"
	}
	return approvalSpec{
		Kind:    approvalKindGitPushRemote,
		Subject: subject,
		// Grants are keyed by the subject, not the question: an answer holds
		// whatever the remote later reports as its default branch.
		Question: "Allow " + pushes + " to " + subject + " branches other than " +
			strings.Join(excluded[:len(excluded)-1], ", ") + " and " + excluded[len(excluded)-1],
	}
}

// gitForcePushToOtherBranchesAsks is whether a force-push to a branch that
// is not the remote's default asks about that exact ref, as a force-push to
// the default branch does. It does not: rebasing a pull request's branch is
// as routine as pushing to it, so it only needs the remote grant. Flip it to
// ask about every force-push.
const gitForcePushToOtherBranchesAsks = false

// gitWellKnownDefaultBranches are asked about whatever the remote's HEAD
// says, so that a misdetected or unknown default branch never skips asking
// for the usual ones.
var gitWellKnownDefaultBranches = []string{"main", "master"}

// gitRemoteDefault is what the engine learned about a push destination's
// default branch (see core.GitRemoteHead).
type gitRemoteDefault struct {
	// reached is whether the remote answered at all.
	reached bool
	// branch is the full ref its HEAD points to, "" when it did not say.
	branch string
}

// gitPushRefAlwaysAsks is whether a push of ref asks about that exact ref
// whatever the remote's default branch is.
//
// Every ref outside refs/heads/ does: tags can trigger releases, and other
// refs (notes, review refs, ...) mean what the remote decides. Deleting a
// ref would too, but a push always sends a commit: the push API cannot
// express a deletion.
func gitPushRefAlwaysAsks(ref string) bool {
	branch, ok := strings.CutPrefix(ref, "refs/heads/")
	if !ok {
		return true
	}
	for _, name := range gitWellKnownDefaultBranches {
		// Refs may be stored case-insensitively by the remote's filesystem.
		if strings.EqualFold(branch, name) {
			return true
		}
	}
	return false
}

// gitPushNeedsRefApproval is whether a delegated push of ref, forced or not,
// asks about that exact ref rather than relying on the remote grant, given
// what is known of the remote's default branch.
//
// A remote that could not be reached at all asks as before: nothing is known
// about it. One that answered without naming its default branch (it requires
// authentication, is empty, or does not advertise HEAD) leaves only the
// well-known names asking: the owner's credentials are not consulted to find
// out before the push is approved.
func gitPushNeedsRefApproval(ref string, force bool, remote gitRemoteDefault) bool {
	switch {
	case gitPushRefAlwaysAsks(ref):
		return true
	case !remote.reached:
		return true
	case remote.branch != "" && strings.EqualFold(ref, remote.branch):
		return true
	case force:
		return gitForcePushToOtherBranchesAsks
	default:
		return false
	}
}

// gitPushPolicyKey is one owner's push destination (as resolved and
// validated), or one owner's workspace checkout.
type gitPushPolicyKey struct {
	owner, remote string
}

type pendingGitRemoteDefault struct {
	done   chan struct{}
	result gitRemoteDefault
}

// gitRemoteDefaults remembers each remote's default branch for a session, so
// it is asked once. Only remotes that answered are remembered: an
// unreachable one, perhaps briefly, is asked again by the next push.
// Concurrent pushes share the question in flight.
type gitRemoteDefaults struct {
	mu      sync.Mutex
	results map[gitPushPolicyKey]*pendingGitRemoteDefault
}

func (d *gitRemoteDefaults) get(ctx context.Context, key gitPushPolicyKey, probe func(context.Context) gitRemoteDefault) gitRemoteDefault {
	d.mu.Lock()
	if pending, ok := d.results[key]; ok {
		d.mu.Unlock()
		select {
		case <-ctx.Done():
			return gitRemoteDefault{}
		case <-pending.done:
			return pending.result
		}
	}
	pending := &pendingGitRemoteDefault{done: make(chan struct{})}
	if d.results == nil {
		d.results = make(map[gitPushPolicyKey]*pendingGitRemoteDefault)
	}
	d.results[key] = pending
	d.mu.Unlock()
	// Concurrent pushes share this probe: one caller giving up must not fail
	// it for the others. The probe bounds itself (core.GitRemoteHeadTimeout).
	pending.result = probe(context.WithoutCancel(ctx))
	d.mu.Lock()
	if !pending.result.reached {
		delete(d.results, key)
	}
	close(pending.done)
	d.mu.Unlock()
	return pending.result
}

// gitRemoteDefault returns what is known of remote's default branch, asking
// the remote anonymously the first time in the session (see
// core.GitRemoteHead): no credentials are used before the push is approved.
func (srv *Server) gitRemoteDefault(ctx context.Context, owner *clientRuntime, remote string) gitRemoteDefault {
	key := gitPushPolicyKey{owner: owner.clientID, remote: remote}
	return owner.daggerSession.gitRemoteDefaults.get(ctx, key, func(ctx context.Context) gitRemoteDefault {
		probe := core.GitRemoteHead
		if srv.gitRemoteHead != nil {
			probe = srv.gitRemoteHead
		}
		branch, err := probe(ctx, remote)
		if err != nil {
			return gitRemoteDefault{}
		}
		return gitRemoteDefault{reached: true, branch: branch}
	})
}

// ownerOrigins remembers, for a session, the origin remotes of the owners'
// workspace checkouts (see Server.gitPushToOwnerOrigin).
type ownerOrigins struct {
	mu      sync.Mutex
	origins map[gitPushPolicyKey][]*gitutil.GitURL
}

// gitPushToOwnerOrigin is whether remote is the repository the owner's own
// workspace checkout was cloned from: its origin remote, as configured or as
// the owner's URL rewrites resolve it for a push. The owner pushes there with
// these credentials anyway, so routine pushes there need no remote grant.
//
// The origin is read from the owner's client, in the checkout the engine
// detected as the owner's workspace (never a path a module chose), and
// compared as a repository (gitutil.SameRepository: host and path, whatever
// the transport). Anything that does not work out, e.g. a workspace that is
// not a local Git checkout, a client that cannot read its config, or a
// checkout without an origin, is simply not the origin.
func (srv *Server) gitPushToOwnerOrigin(ctx context.Context, owner *clientRuntime, remote string) bool {
	destination, err := gitutil.ParseURL(remote)
	if err != nil {
		return false
	}
	owner.workspaceMu.Lock()
	ws := owner.workspace
	owner.workspaceMu.Unlock()
	if ws == nil || ws.HostPath() == "" {
		return false
	}
	for _, origin := range srv.ownerOrigins(ctx, owner, ws.HostPath()) {
		// SameRepository ignores the transport: never let a module downgrade
		// the owner's origin to a plaintext one, which would send the owner's
		// credentials unencrypted.
		if gitPlaintextTransport(destination.Scheme) && destination.Scheme != origin.Scheme {
			continue
		}
		if gitutil.SameRepository(origin, destination) {
			return true
		}
	}
	return false
}

func gitPlaintextTransport(scheme string) bool {
	return scheme != gitutil.HTTPSProtocol && scheme != gitutil.SSHProtocol && scheme != gitutil.EntireProtocol
}

// ownerOrigins returns the URLs of the origin remote of the owner's checkout
// at root, as configured and as resolved for a push, read from the owner's
// client once per session.
func (srv *Server) ownerOrigins(ctx context.Context, owner *clientRuntime, root string) []*gitutil.GitURL {
	cache := &owner.daggerSession.ownerOrigins
	key := gitPushPolicyKey{owner: owner.clientID, remote: root}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if origins, ok := cache.origins[key]; ok {
		return origins
	}
	ctx, cancel := context.WithTimeout(ctx, core.GitRemoteHeadTimeout)
	defer cancel()
	conn, available, err := srv.SpecificClientAttachableConn(ctx, owner.clientID, core.SpecificClientAttachableConnOpts{IfAvailable: true})
	if err != nil || !available {
		return nil
	}
	response, err := git.NewGitClient(conn).GetConfig(ctx, &git.GitConfigRequest{CheckoutPath: root})
	if err != nil || response.GetConfig() == nil {
		// Not remembered: the client may answer next time.
		return nil
	}
	entries := response.GetConfig().GetEntries()
	var origins []*gitutil.GitURL
	for _, entry := range entries {
		if !strings.EqualFold(entry.GetKey(), "remote.origin.url") {
			continue
		}
		for _, url := range []string{entry.GetValue(), git.ResolvePushURL(entry.GetValue(), entries)} {
			if parsed, err := gitutil.ParseURL(url); err == nil && parsed.Host != "" {
				origins = append(origins, parsed)
			}
		}
	}
	if cache.origins == nil {
		cache.origins = make(map[gitPushPolicyKey][]*gitutil.GitURL)
	}
	cache.origins[key] = origins
	return origins
}
