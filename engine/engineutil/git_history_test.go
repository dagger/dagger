package engineutil

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/dagger/dagger/engine/session/git"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type historyTestClient struct {
	git.GitClient
	responses []*git.PackCheckoutResponse
	err       error
}

func (c historyTestClient) PackCommit(context.Context, *git.PackCommitRequest, ...grpc.CallOption) (git.Git_PackCommitClient, error) {
	return &historyTestStream{responses: c.responses, err: c.err}, nil
}

type historyTestStream struct {
	grpc.ClientStream
	responses []*git.PackCheckoutResponse
	err       error
}

func (s *historyTestStream) Recv() (*git.PackCheckoutResponse, error) {
	if len(s.responses) == 0 {
		if s.err != nil {
			return nil, s.err
		}
		return nil, io.EOF
	}
	next := s.responses[0]
	s.responses = s.responses[1:]
	return next, nil
}

func TestReceiveGitCommitPack(t *testing.T) {
	req := &git.PackCommitRequest{CheckoutPath: "/approved", ExpectedStateDigest: "captured", CommitSha: strings.Repeat("a", 40)}
	metadata := func(sha string) *git.PackCheckoutResponse {
		return &git.PackCheckoutResponse{Msg: &git.PackCheckoutResponse_Metadata{Metadata: &git.PackCheckoutMetadata{HeadSha: sha, StateDigest: req.ExpectedStateDigest, ObjectFormat: "sha1"}}}
	}
	chunk := &git.PackCheckoutResponse{Msg: &git.PackCheckoutResponse_Chunk{Chunk: []byte("pack bytes are validated by importer")}}
	for _, test := range []struct {
		name                          string
		responses                     []*git.PackCheckoutResponse
		err                           error
		unavailable, valid, cancelled bool
	}{
		{name: "valid", responses: []*git.PackCheckoutResponse{metadata(req.CommitSha), chunk}, valid: true},
		{name: "wrong identity", responses: []*git.PackCheckoutResponse{metadata(strings.Repeat("b", 40)), chunk}},
		{name: "bytes first", responses: []*git.PackCheckoutResponse{chunk}},
		{name: "duplicate metadata", responses: []*git.PackCheckoutResponse{metadata(req.CommitSha), metadata(req.CommitSha), chunk}},
		{name: "empty pack", responses: []*git.PackCheckoutResponse{metadata(req.CommitSha)}},
		{name: "old client", err: status.Error(codes.Unimplemented, "unsupported"), unavailable: true},
		{name: "gone client", err: status.Error(codes.Unavailable, "gone"), unavailable: true},
		{name: "broken stream", err: status.Error(codes.Internal, "bad")},
		{name: "cancelled", err: status.Error(codes.Unavailable, "gone"), cancelled: true},
		{name: "missing donor", responses: []*git.PackCheckoutResponse{{Msg: &git.PackCheckoutResponse_Metadata{Metadata: &git.PackCheckoutMetadata{Error: &git.ErrorInfo{Type: git.HISTORY_UNAVAILABLE}}}}}, unavailable: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := t.Context()
			if test.cancelled {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			pack, err := ReceiveGitCommitPack(ctx, historyTestClient{responses: test.responses, err: test.err}, req)
			if test.valid {
				require.NoError(t, err)
				require.NotNil(t, pack)
				path := pack.BundlePath
				data, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Equal(t, chunk.GetChunk(), data)
				require.NoError(t, pack.Close())
				require.NoFileExists(t, path)
				return
			}
			require.Error(t, err)
			require.Nil(t, pack)
			require.Equal(t, test.unavailable, errors.Is(err, ErrGitHistoryUnavailable))
			if test.cancelled {
				require.ErrorIs(t, err, context.Canceled)
			}
		})
	}
}
