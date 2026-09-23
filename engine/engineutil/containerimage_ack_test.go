package engineutil

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/session/store"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/emptypb"
)

type imageAckObservation struct {
	bytes int
	eof   bool
}

type imageAckLoader struct {
	store.UnimplementedBasicStoreServer
	reject bool
	done   chan imageAckObservation
}

func (loader *imageAckLoader) WriteTarball(stream store.BasicStore_WriteTarballServer) error {
	var observed imageAckObservation
	defer func() { loader.done <- observed }()
	for {
		data, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			observed.eof = true
			break
		}
		if err != nil {
			return err
		}
		observed.bytes += len(data.Data)
	}
	if loader.reject {
		return status.Error(codes.FailedPrecondition, "fixture loader rejected image after upload")
	}
	return stream.SendAndClose(&emptypb.Empty{})
}

type imageAckCaller struct{ conn *grpc.ClientConn }

func (caller imageAckCaller) Conn() *grpc.ClientConn { return caller.conn }
func (imageAckCaller) Supports(method string) bool {
	return strings.HasPrefix(method, "/dagger.store.BasicStore/")
}

func TestExportContainerImageLoaderAcknowledgment(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		reject bool
		empty  bool
	}{
		{name: "accepted"},
		{name: "rejected-after-upload", reject: true},
		{name: "assembly-and-close-errors", reject: true, empty: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			t.Cleanup(cancel)
			listener := bufconn.Listen(1 << 20)
			server := grpc.NewServer()
			loader := &imageAckLoader{reject: tc.reject, done: make(chan imageAckObservation, 1)}
			store.RegisterBasicStoreServer(server, loader)
			served := make(chan struct{})
			go func() {
				defer close(served)
				_ = server.Serve(listener)
			}()
			t.Cleanup(func() {
				cancel()
				stopped := make(chan struct{})
				go func() {
					defer close(stopped)
					server.Stop()
					_ = listener.Close()
				}()
				cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cleanupCancel()
				for _, done := range []<-chan struct{}{stopped, served} {
					select {
					case <-done:
					case <-cleanupCtx.Done():
						t.Error("image loader server cleanup exceeded its bound")
						return
					}
				}
			})
			conn, err := grpc.NewClient("passthrough:///image-ack", grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }))
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, conn.Close()) })
			snapshots := testutil.NewStore(t)
			client, err := NewClient(ctx, &Opts{
				Snapshotter: snapshots.Snapshots, ContentStore: snapshots.Content, LeaseManager: snapshots.Leases,
				GetHostServiceCaller: func(context.Context, string) (SessionCaller, error) { return imageAckCaller{conn}, nil },
			})
			require.NoError(t, err)
			t.Cleanup(func() { client.cancel(errors.New("test complete")) })
			ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{ClientID: "image-ack"})
			input := map[string]ContainerExport{"linux/amd64": {}}
			if tc.empty {
				input = nil
			}
			result, err := client.ExportContainerImage(ctx, "fixture:ack", input, "uncompressed", false, "", true)
			if tc.reject {
				require.Error(t, err, "export must return the loader's final acknowledgment error")
				require.Equal(t, codes.FailedPrecondition, status.Code(err))
				require.ErrorContains(t, err, "fixture loader rejected image after upload")
				require.Nil(t, result)
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)
			}
			if tc.empty {
				require.ErrorContains(t, err, "image export request has no platforms")
			}
			select {
			case observed := <-loader.done:
				require.True(t, observed.eof, "the loader receives end-of-upload before acknowledging")
				if tc.empty {
					require.Zero(t, observed.bytes)
				} else {
					require.Positive(t, observed.bytes)
				}
			case <-ctx.Done():
				t.Fatal("image loader did not finish after export returned")
			}
		})
	}
}
