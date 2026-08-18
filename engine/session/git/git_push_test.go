package git

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// These tests drive the Push handler against real local repositories built
// with the host's git, feeding it a fake client-streaming stream that replays
// queued requests and records the response — the same approach as the
// PackCheckout tests.

// fakePushServer is a stand-in for the generated Git_PushServer stream. It
// embeds grpc.ServerStream (so it satisfies the interface's method set) and
// overrides only Recv, SendAndClose and Context, which are the sole methods
// the Push handler touches.
type fakePushServer struct {
	grpc.ServerStream
	ctx      context.Context
	requests []*PushRequest
	response *PushResponse
}

var _ Git_PushServer = (*fakePushServer)(nil)

func (s *fakePushServer) Context() context.Context {
	if s.ctx != nil {
		return s.ctx
	}
	return context.Background()
}

func (s *fakePushServer) Recv() (*PushRequest, error) {
	if len(s.requests) == 0 {
		return nil, io.EOF
	}
	req := s.requests[0]
	s.requests = s.requests[1:]
	return req, nil
}

func (s *fakePushServer) SendAndClose(resp *PushResponse) error {
	s.response = resp
	return nil
}

// push runs the Push handler over the given messages and returns the recorded
// response.
func push(t *testing.T, meta *PushMetadata, bundle []byte) *PushResponse {
	t.Helper()
	srv := &fakePushServer{ctx: context.Background()}
	if meta != nil {
		srv.requests = append(srv.requests, &PushRequest{
			Msg: &PushRequest_Metadata{Metadata: meta},
		})
	}
	if len(bundle) > 0 {
		srv.requests = append(srv.requests, &PushRequest{
			Msg: &PushRequest_Chunk{Chunk: bundle},
		})
	}
	require.NoError(t, GitAttachable{}.Push(srv))
	require.NotNil(t, srv.response, "handler must respond")
	return srv.response
}

// requirePushed asserts a successful push result and returns it.
func requirePushed(t *testing.T, resp *PushResponse) *PushResult {
	t.Helper()
	if e := resp.GetError(); e != nil {
		t.Fatalf("unexpected error result: %s (%s)", e.GetType(), e.GetMessage())
	}
	return resp.GetPushed()
}

// requirePushError asserts an error result of the given type.
func requirePushError(t *testing.T, resp *PushResponse, errorType ErrorInfo_ErrorType) *ErrorInfo {
	t.Helper()
	e := resp.GetError()
	require.NotNil(t, e, "expected an error result, got %v", resp.GetPushed())
	require.Equal(t, errorType, e.GetType(), "unexpected error type: %s", e.GetMessage())
	return e
}

// initBareRemote creates a bare repository to push to.
func initBareRemote(t *testing.T, home string) string {
	t.Helper()
	remote := filepath.Join(t.TempDir(), "remote.git")
	gitCmd(t, home, "", "init", "--bare", remote)
	return remote
}

func TestPushHeadDefaultsToCurrentBranch(t *testing.T) {
	skipIfNoGit(t)

	repo, home := initRepo(t, "main")
	commitFile(t, repo, home, "a.txt", "one", "first")
	head := gitCmd(t, home, repo, "rev-parse", "HEAD")
	remote := initBareRemote(t, home)

	resp := push(t, &PushMetadata{
		CheckoutPath: repo,
		Remote:       remote,
		TargetSha:    head,
	}, nil)
	pushed := requirePushed(t, resp)
	require.Equal(t, "refs/heads/main", pushed.GetDestRef(),
		"an empty destination must resolve to the checked-out branch")
	require.Equal(t, head, pushed.GetSha())
	require.Equal(t, head, gitCmd(t, home, remote, "rev-parse", "refs/heads/main"))
}

func TestPushToNamedBranch(t *testing.T) {
	skipIfNoGit(t)

	repo, home := initRepo(t, "main")
	commitFile(t, repo, home, "a.txt", "one", "first")
	head := gitCmd(t, home, repo, "rev-parse", "HEAD")
	remote := initBareRemote(t, home)

	resp := push(t, &PushMetadata{
		CheckoutPath: repo,
		Remote:       remote,
		DestRef:      "refs/heads/feature",
		TargetSha:    head,
	}, nil)
	pushed := requirePushed(t, resp)
	require.Equal(t, "refs/heads/feature", pushed.GetDestRef())
	require.Equal(t, head, gitCmd(t, home, remote, "rev-parse", "refs/heads/feature"))
}

func TestPushToConfiguredRemoteName(t *testing.T) {
	skipIfNoGit(t)

	repo, home := initRepo(t, "main")
	commitFile(t, repo, home, "a.txt", "one", "first")
	head := gitCmd(t, home, repo, "rev-parse", "HEAD")
	remote := initBareRemote(t, home)
	gitCmd(t, home, repo, "remote", "add", "origin", remote)

	resp := push(t, &PushMetadata{
		CheckoutPath: repo,
		Remote:       "origin",
		TargetSha:    head,
	}, nil)
	requirePushed(t, resp)
	require.Equal(t, head, gitCmd(t, home, remote, "rev-parse", "refs/heads/main"))
}

// TestPushBundledCommits is the staged-commit path: the commit being pushed
// does not exist in the checkout and arrives as a bundle. It must land on the
// remote while the checkout's own refs, HEAD and work tree stay untouched.
func TestPushBundledCommits(t *testing.T) {
	skipIfNoGit(t)

	repo, home := initRepo(t, "main")
	commitFile(t, repo, home, "a.txt", "one", "first")
	baseHead := gitCmd(t, home, repo, "rev-parse", "HEAD")
	remote := initBareRemote(t, home)

	// Build the "staged" commit in a clone, exactly one commit ahead, and
	// bundle exactly that range under the staged-commits ref — the same
	// shape the engine produces.
	clone := filepath.Join(t.TempDir(), "clone")
	gitCmd(t, home, "", "clone", repo, clone)
	commitFile(t, clone, home, "b.txt", "two", "staged")
	stagedHead := gitCmd(t, home, clone, "rev-parse", "HEAD")
	const bundleRef = "refs/dagger/staged-commits"
	gitCmd(t, home, clone, "update-ref", bundleRef, stagedHead)
	bundlePath := filepath.Join(t.TempDir(), "staged.bundle")
	gitCmd(t, home, clone, "bundle", "create", bundlePath, bundleRef, "--not", baseHead)
	bundle, err := os.ReadFile(bundlePath)
	require.NoError(t, err)

	resp := push(t, &PushMetadata{
		CheckoutPath: repo,
		Remote:       remote,
		DestRef:      "refs/heads/feature",
		TargetSha:    stagedHead,
		BundleRef:    bundleRef,
	}, bundle)
	pushed := requirePushed(t, resp)
	require.Equal(t, "refs/heads/feature", pushed.GetDestRef())
	require.Equal(t, stagedHead, pushed.GetSha())

	// The staged commit landed on the remote...
	require.Equal(t, stagedHead, gitCmd(t, home, remote, "rev-parse", "refs/heads/feature"))

	// ...while the checkout is exactly as it was: HEAD and branch unmoved,
	// no ref created for the bundled commits, work tree clean.
	require.Equal(t, baseHead, gitCmd(t, home, repo, "rev-parse", "HEAD"))
	require.Equal(t, baseHead, gitCmd(t, home, repo, "rev-parse", "refs/heads/main"))
	require.Equal(t, "", gitCmd(t, home, repo, "for-each-ref", "refs/dagger"))
	require.Equal(t, "", gitCmd(t, home, repo, "status", "--porcelain"))
}

func TestPushDetachedHeadNeedsBranch(t *testing.T) {
	skipIfNoGit(t)

	repo, home := initRepo(t, "main")
	commitFile(t, repo, home, "a.txt", "one", "first")
	head := gitCmd(t, home, repo, "rev-parse", "HEAD")
	gitCmd(t, home, repo, "checkout", "--detach")
	remote := initBareRemote(t, home)

	resp := push(t, &PushMetadata{
		CheckoutPath: repo,
		Remote:       remote,
		TargetSha:    head,
	}, nil)
	requirePushError(t, resp, DETACHED_HEAD)

	// Naming the branch makes the same push work.
	resp = push(t, &PushMetadata{
		CheckoutPath: repo,
		Remote:       remote,
		DestRef:      "refs/heads/main",
		TargetSha:    head,
	}, nil)
	requirePushed(t, resp)
	require.Equal(t, head, gitCmd(t, home, remote, "rev-parse", "refs/heads/main"))
}

func TestPushNonFastForward(t *testing.T) {
	skipIfNoGit(t)

	repo, home := initRepo(t, "main")
	commitFile(t, repo, home, "a.txt", "one", "first")
	first := gitCmd(t, home, repo, "rev-parse", "HEAD")
	commitFile(t, repo, home, "b.txt", "two", "second")
	second := gitCmd(t, home, repo, "rev-parse", "HEAD")
	remote := initBareRemote(t, home)

	// Remote main is at the second commit; pushing the first is a rewind.
	requirePushed(t, push(t, &PushMetadata{
		CheckoutPath: repo,
		Remote:       remote,
		TargetSha:    second,
	}, nil))

	resp := push(t, &PushMetadata{
		CheckoutPath: repo,
		Remote:       remote,
		TargetSha:    first,
	}, nil)
	requirePushError(t, resp, PUSH_FAILED)
	require.Equal(t, second, gitCmd(t, home, remote, "rev-parse", "refs/heads/main"),
		"a refused push must leave the remote alone")

	// force allows the non-fast-forward update.
	resp = push(t, &PushMetadata{
		CheckoutPath: repo,
		Remote:       remote,
		TargetSha:    first,
		Force:        true,
	}, nil)
	requirePushed(t, resp)
	require.Equal(t, first, gitCmd(t, home, remote, "rev-parse", "refs/heads/main"))
}

func TestPushUnknownCommit(t *testing.T) {
	skipIfNoGit(t)

	repo, home := initRepo(t, "main")
	commitFile(t, repo, home, "a.txt", "one", "first")
	remote := initBareRemote(t, home)

	resp := push(t, &PushMetadata{
		CheckoutPath: repo,
		Remote:       remote,
		TargetSha:    "0123456789012345678901234567890123456789",
	}, nil)
	e := requirePushError(t, resp, PUSH_FAILED)
	require.Contains(t, e.GetMessage(), "not found in the local repository")
}

func TestPushInvalidRequests(t *testing.T) {
	skipIfNoGit(t)

	t.Run("missing metadata", func(t *testing.T) {
		resp := push(t, nil, nil)
		requirePushError(t, resp, INVALID_REQUEST)
	})

	t.Run("missing required fields", func(t *testing.T) {
		resp := push(t, &PushMetadata{CheckoutPath: "/somewhere"}, nil)
		requirePushError(t, resp, INVALID_REQUEST)
	})

	t.Run("bundle without bundle ref", func(t *testing.T) {
		resp := push(t, &PushMetadata{
			CheckoutPath: "/somewhere",
			Remote:       "origin",
			TargetSha:    "0123456789012345678901234567890123456789",
		}, []byte("not really a bundle"))
		requirePushError(t, resp, INVALID_REQUEST)
	})

	t.Run("chunk before metadata", func(t *testing.T) {
		srv := &fakePushServer{ctx: context.Background()}
		srv.requests = []*PushRequest{
			{Msg: &PushRequest_Chunk{Chunk: []byte("data")}},
		}
		require.NoError(t, GitAttachable{}.Push(srv))
		require.NotNil(t, srv.response)
		requirePushError(t, srv.response, INVALID_REQUEST)
	})
}
