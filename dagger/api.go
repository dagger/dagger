package dagger

import (
	"context"
	"net"

	apitypes "github.com/moby/buildkit/api/types"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	gwpb "github.com/moby/buildkit/frontend/gateway/pb"
	"github.com/moby/buildkit/identity"
	"github.com/moby/buildkit/session/sshforward"
	solverpb "github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/util/grpcerrors"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/net/http2"
	"google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	daggerSockName = "dagger-sock"
)

func newAPIServer(control *client.Client, gw bkgw.Client) *apiServer {
	s := &apiServer{
		control:    control,
		gw:         gw,
		refs:       make(map[string]bkgw.Reference),
		httpServer: &http2.Server{},
		grpcServer: grpc.NewServer(grpc.UnaryInterceptor(grpcerrors.UnaryServerInterceptor), grpc.StreamInterceptor(grpcerrors.StreamServerInterceptor)),
	}
	grpc_health_v1.RegisterHealthServer(s.grpcServer, health.NewServer())
	gwpb.RegisterLLBBridgeServer(s.grpcServer, s)
	return s
}

type apiServer struct {
	control    *client.Client
	gw         bkgw.Client
	refs       map[string]bkgw.Reference
	httpServer *http2.Server
	grpcServer *grpc.Server
}

func (s *apiServer) serve(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	go s.httpServer.ServeConn(conn, &http2.ServeConnOpts{Handler: s.grpcServer})
	<-ctx.Done()
}

// TODO: just re-using buildkit's llb bridge api for now because easier, in long-run this should be our own api for more control+stability
var _ gwpb.LLBBridgeServer = &apiServer{}

func (s *apiServer) Ping(ctx context.Context, req *gwpb.PingRequest) (*gwpb.PongResponse, error) {
	workers, err := s.control.ListWorkers(ctx)
	if err != nil {
		return nil, err
	}
	pbWorkers := make([]*apitypes.WorkerRecord, 0, len(workers))
	for _, w := range workers {
		pbWorkers = append(pbWorkers, &apitypes.WorkerRecord{
			ID:        w.ID,
			Labels:    w.Labels,
			Platforms: solverpb.PlatformsFromSpec(w.Platforms),
		})
	}

	return &gwpb.PongResponse{
		FrontendAPICaps: gwpb.Caps.All(),
		Workers:         pbWorkers,
		LLBCaps:         solverpb.Caps.All(),
	}, nil
}

func (s *apiServer) Solve(ctx context.Context, req *gwpb.SolveRequest) (*gwpb.SolveResponse, error) {
	var cacheImports []bkgw.CacheOptionsEntry
	for _, im := range req.CacheImports {
		cacheImports = append(cacheImports, bkgw.CacheOptionsEntry{
			Type:  im.Type,
			Attrs: im.Attrs,
		})
	}

	// TODO: silly hack, will be less ugly when this is our own api rather than a hack of buildkit's
	if req.Frontend == "dagger" {
		req.Frontend = ""

		// in this case, the req definition is just the input for the dagger action
		defop, err := llb.NewDefinitionOp(req.Definition)
		if err != nil {
			return nil, err
		}
		input := llb.NewState(defop)

		pkg, ok := req.FrontendOpt["pkg"]
		if !ok {
			return nil, status.Errorf(codes.InvalidArgument, "missing pkg")
		}
		action, ok := req.FrontendOpt["action"]
		if !ok {
			return nil, status.Errorf(codes.InvalidArgument, "missing action")
		}
		req.FrontendOpt = nil

		// TODO: generate on the fly without pulling a specific image
		st := llb.Image(pkg).Run(
			llb.Args([]string{"/entrypoint", "-a", action}),
			llb.AddSSHSocket(llb.SSHID(daggerSockName), llb.SSHSocketTarget("/dagger.sock")),
			llb.AddMount("/inputs", input, llb.Readonly),
			llb.ReadonlyRootFS(),
		)
		outputMnt := st.AddMount("/outputs", llb.Scratch())

		outputDef, err := outputMnt.Marshal(ctx) // TODO: options
		if err != nil {
			return nil, err
		}
		req.Definition = outputDef.ToPB()
	}

	res, err := s.gw.Solve(ctx, bkgw.SolveRequest{
		Evaluate:       req.Evaluate,
		Definition:     req.Definition,
		Frontend:       req.Frontend,
		FrontendOpt:    req.FrontendOpt,
		FrontendInputs: req.FrontendInputs,
		CacheImports:   cacheImports,
	})
	if err != nil {
		return nil, err
	}

	resp := &gwpb.SolveResponse{
		Result: &gwpb.Result{Metadata: res.Metadata},
	}
	if res.Ref != nil {
		id := identity.NewID() // TODO: consistent hash would be better, be careful when NoCache is enabled though
		s.refs[id] = res.Ref
		resp.Result.Result = &gwpb.Result_Ref{Ref: &gwpb.Ref{Id: id, Def: req.Definition}}
	} else if len(res.Refs) > 0 {
		refs := make(map[string]*gwpb.Ref, len(res.Refs))
		for _, ref := range res.Refs {
			id := identity.NewID()
			s.refs[id] = ref
			refs[id] = &gwpb.Ref{Id: id, Def: req.Definition}
		}
		resp.Result.Result = &gwpb.Result_Refs{Refs: &gwpb.RefMap{Refs: refs}}
	}
	return resp, nil
}

func (s *apiServer) ReadFile(ctx context.Context, req *gwpb.ReadFileRequest) (*gwpb.ReadFileResponse, error) {
	ref, ok := s.refs[req.Ref]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "ref %s not found", req.Ref)
	}

	bkreq := bkgw.ReadRequest{
		Filename: req.FilePath,
	}
	if req.Range != nil {
		bkreq.Range = &bkgw.FileRange{
			Offset: int(req.Range.Offset),
			Length: int(req.Range.Length),
		}
	}
	bytes, err := ref.ReadFile(ctx, bkreq)
	if err != nil {
		return nil, err
	}
	return &gwpb.ReadFileResponse{Data: bytes}, nil
}

func (s *apiServer) ReadDir(ctx context.Context, req *gwpb.ReadDirRequest) (*gwpb.ReadDirResponse, error) {
	ref, ok := s.refs[req.Ref]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "ref %s not found", req.Ref)
	}
	entries, err := ref.ReadDir(ctx, bkgw.ReadDirRequest{
		Path:           req.DirPath,
		IncludePattern: req.IncludePattern,
	})
	if err != nil {
		return nil, err
	}
	return &gwpb.ReadDirResponse{Entries: entries}, nil
}

func (s *apiServer) StatFile(ctx context.Context, req *gwpb.StatFileRequest) (*gwpb.StatFileResponse, error) {
	ref, ok := s.refs[req.Ref]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "ref %s not found", req.Ref)
	}
	stat, err := ref.StatFile(ctx, bkgw.StatRequest{
		Path: req.Path,
	})
	if err != nil {
		return nil, err
	}
	return &gwpb.StatFileResponse{Stat: stat}, nil
}

func (s *apiServer) ResolveImageConfig(ctx context.Context, req *gwpb.ResolveImageConfigRequest) (*gwpb.ResolveImageConfigResponse, error) {
	dgst, config, err := s.gw.ResolveImageConfig(ctx, req.Ref, llb.ResolveImageConfigOpt{
		Platform: &ocispecs.Platform{
			OS:           req.Platform.OS,
			Architecture: req.Platform.Architecture,
			Variant:      req.Platform.Variant,
			OSVersion:    req.Platform.OSVersion,
			OSFeatures:   req.Platform.OSFeatures,
		},
		ResolveMode: req.ResolveMode,
		LogName:     req.LogName,
	})
	if err != nil {
		return nil, err
	}
	return &gwpb.ResolveImageConfigResponse{
		Digest: dgst,
		Config: config,
	}, nil
}

func (s *apiServer) Return(ctx context.Context, req *gwpb.ReturnRequest) (*gwpb.ReturnResponse, error) {
	// NOTE: not implemented, we are implementing returns via ExecOp mount outputs, which enables getting the same output even if the action execution is cached
	return &gwpb.ReturnResponse{}, nil
}

func (s *apiServer) Inputs(ctx context.Context, req *gwpb.InputsRequest) (*gwpb.InputsResponse, error) {
	// NOTE: just ignoring for now, we are currently implementing inputs using ExecOp mounts
	return &gwpb.InputsResponse{}, nil
}

func (s *apiServer) Warn(ctx context.Context, req *gwpb.WarnRequest) (*gwpb.WarnResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Warn not implemented")
}

func (s *apiServer) NewContainer(ctx context.Context, req *gwpb.NewContainerRequest) (*gwpb.NewContainerResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method NewContainer not implemented")
}

func (s *apiServer) ReleaseContainer(ctx context.Context, req *gwpb.ReleaseContainerRequest) (*gwpb.ReleaseContainerResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method ReleaseContainer not implemented")
}

func (s *apiServer) ExecProcess(srv gwpb.LLBBridge_ExecProcessServer) error {
	return status.Errorf(codes.Unimplemented, "method ExecProcess not implemented")
}

type clientAdapter struct {
	*apiServer
}

var _ gwpb.LLBBridgeClient = &clientAdapter{}

func (c clientAdapter) ResolveImageConfig(ctx context.Context, in *gwpb.ResolveImageConfigRequest, opts ...grpc.CallOption) (*gwpb.ResolveImageConfigResponse, error) {
	return c.apiServer.ResolveImageConfig(ctx, in)
}

func (c clientAdapter) Solve(ctx context.Context, in *gwpb.SolveRequest, opts ...grpc.CallOption) (*gwpb.SolveResponse, error) {
	return c.apiServer.Solve(ctx, in)
}

func (c clientAdapter) ReadFile(ctx context.Context, in *gwpb.ReadFileRequest, opts ...grpc.CallOption) (*gwpb.ReadFileResponse, error) {
	return c.apiServer.ReadFile(ctx, in)
}

func (c clientAdapter) ReadDir(ctx context.Context, in *gwpb.ReadDirRequest, opts ...grpc.CallOption) (*gwpb.ReadDirResponse, error) {
	return c.apiServer.ReadDir(ctx, in)
}

func (c clientAdapter) StatFile(ctx context.Context, in *gwpb.StatFileRequest, opts ...grpc.CallOption) (*gwpb.StatFileResponse, error) {
	return c.apiServer.StatFile(ctx, in)
}

func (c clientAdapter) Ping(ctx context.Context, in *gwpb.PingRequest, opts ...grpc.CallOption) (*gwpb.PongResponse, error) {
	return c.apiServer.Ping(ctx, in)
}

func (c clientAdapter) Return(ctx context.Context, in *gwpb.ReturnRequest, opts ...grpc.CallOption) (*gwpb.ReturnResponse, error) {
	return c.apiServer.Return(ctx, in)
}

func (c clientAdapter) Inputs(ctx context.Context, in *gwpb.InputsRequest, opts ...grpc.CallOption) (*gwpb.InputsResponse, error) {
	return c.apiServer.Inputs(ctx, in)
}

func (c clientAdapter) NewContainer(ctx context.Context, in *gwpb.NewContainerRequest, opts ...grpc.CallOption) (*gwpb.NewContainerResponse, error) {
	return c.apiServer.NewContainer(ctx, in)
}

func (c clientAdapter) ReleaseContainer(ctx context.Context, in *gwpb.ReleaseContainerRequest, opts ...grpc.CallOption) (*gwpb.ReleaseContainerResponse, error) {
	return c.apiServer.ReleaseContainer(ctx, in)
}

func (c clientAdapter) ExecProcess(ctx context.Context, opts ...grpc.CallOption) (gwpb.LLBBridge_ExecProcessClient, error) {
	return nil, status.Errorf(codes.Unimplemented, "method ExecProcess not implemented")
}

func (c clientAdapter) Warn(ctx context.Context, in *gwpb.WarnRequest, opts ...grpc.CallOption) (*gwpb.WarnResponse, error) {
	return c.apiServer.Warn(ctx, in)
}

func newAPISocketProvider() *apiSocketProvider {
	return &apiSocketProvider{conns: make(map[string]net.Conn)}
}

type apiSocketProvider struct {
	api   *apiServer
	conns map[string]net.Conn
}

func (p *apiSocketProvider) Register(server *grpc.Server) {
	sshforward.RegisterSSHServer(server, p)
}

func (p *apiSocketProvider) CheckAgent(ctx context.Context, req *sshforward.CheckAgentRequest) (*sshforward.CheckAgentResponse, error) {
	return &sshforward.CheckAgentResponse{}, nil
}

func (p *apiSocketProvider) ForwardAgent(stream sshforward.SSH_ForwardAgentServer) error {
	opts, ok := metadata.FromIncomingContext(stream.Context())
	if !ok {
		return status.Errorf(codes.Internal, "no metadata in context")
	}
	v, ok := opts[sshforward.KeySSHID]
	if !ok || len(v) == 0 || v[0] == "" {
		return status.Errorf(codes.Internal, "no sshid in metadata")
	}
	id := v[0]

	if id == daggerSockName {
		serverConn, clientConn := net.Pipe()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go p.api.serve(ctx, serverConn) // TODO: better synchronization
		return sshforward.Copy(context.TODO(), clientConn, stream, nil)
	}

	conn, ok := p.conns[id]
	if !ok {
		return status.Errorf(codes.Internal, "no api connection for id %s", id)
	}

	return sshforward.Copy(context.TODO(), conn, stream, nil)
}
