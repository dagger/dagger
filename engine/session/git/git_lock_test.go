package git

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

func TestContextKeyedLocker(t *testing.T) {
	var locks contextKeyedLocker
	unlockA, err := locks.lock(context.Background(), "/checkout/a")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = locks.lock(ctx, "/checkout/a")
	require.ErrorIs(t, err, context.Canceled)

	unlockB, err := locks.lock(context.Background(), "/checkout/b")
	require.NoError(t, err, "an unrelated checkout must not be blocked")
	unlockB()
	unlockA()
	require.Empty(t, locks.entries)
}

func TestLimitedGitPackWriter(t *testing.T) {
	var buf bytes.Buffer
	w := &limitedGitPackWriter{w: &buf, remaining: 5}
	n, err := w.Write([]byte("1234"))
	require.NoError(t, err)
	require.Equal(t, 4, n)
	_, err = w.Write([]byte("56"))
	require.ErrorContains(t, err, "exceeds limit")
	require.Equal(t, "1234", buf.String())
}

type blockingPackServer struct {
	grpc.ServerStream
	ctx     context.Context
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (srv *blockingPackServer) Context() context.Context {
	return srv.ctx
}

func (srv *blockingPackServer) block() {
	srv.once.Do(func() { close(srv.started) })
	<-srv.release
}

type blockingCheckoutServer struct{ *blockingPackServer }

func (srv *blockingCheckoutServer) Send(*PackCheckoutResponse) error {
	srv.block()
	return nil
}

type blockingUncommittedServer struct{ *blockingPackServer }

func (srv *blockingUncommittedServer) Send(*PackUncommittedResponse) error {
	srv.block()
	return nil
}

func TestPackCheckoutReleasesLockBeforeStreaming(t *testing.T) {
	skipIfNoGit(t)
	repo, home := initRepo(t, "main")
	commitFile(t, repo, home, "file.txt", "content", "initial")

	block := &blockingPackServer{
		ctx:     context.Background(),
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	srv := &blockingCheckoutServer{blockingPackServer: block}
	released := false
	defer func() {
		if !released {
			close(block.release)
		}
	}()
	errCh := make(chan error, 1)
	go func() {
		errCh <- (GitAttachable{}).PackCheckout(&PackCheckoutRequest{CheckoutPath: repo}, srv)
	}()

	select {
	case <-block.started:
	case <-time.After(5 * time.Second):
		t.Fatal("pack did not begin streaming")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	state, err := (GitAttachable{}).CheckoutState(ctx, &CheckoutStateRequest{CheckoutPath: repo})
	require.NoError(t, err)
	require.NotEmpty(t, state.GetStateDigest(), "checkout lock remained held during streaming")
	close(block.release)
	released = true
	require.NoError(t, <-errCh)
}

func TestPackUncommittedReleasesLockBeforeStreaming(t *testing.T) {
	skipIfNoGit(t)
	repo, home := initRepo(t, "main")
	commitFile(t, repo, home, "file.txt", "content", "initial")
	head := gitCmd(t, home, repo, "rev-parse", "HEAD")

	block := &blockingPackServer{
		ctx:     context.Background(),
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	srv := &blockingUncommittedServer{blockingPackServer: block}
	released := false
	defer func() {
		if !released {
			close(block.release)
		}
	}()
	errCh := make(chan error, 1)
	go func() {
		errCh <- (GitAttachable{}).PackUncommitted(&PackUncommittedRequest{
			CheckoutPath:    repo,
			ExpectedHeadSha: head,
		}, srv)
	}()

	select {
	case <-block.started:
	case <-time.After(5 * time.Second):
		t.Fatal("uncommitted pack did not begin streaming")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	state, err := (GitAttachable{}).CheckoutState(ctx, &CheckoutStateRequest{CheckoutPath: repo})
	require.NoError(t, err)
	require.NotEmpty(t, state.GetStateDigest(), "checkout lock remained held during streaming")
	close(block.release)
	released = true
	require.NoError(t, <-errCh)
}
