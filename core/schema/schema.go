package schema

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime/debug"
	"strings"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/containerd/containerd/content"
	"github.com/dagger/dagger/auth"
	"github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/tracing"
	"github.com/iancoleman/strcase"
	"github.com/moby/buildkit/util/bklog"
	"github.com/moby/buildkit/util/leaseutil"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"github.com/vito/progrock"
)

type InitializeArgs struct {
	BuildkitClient *buildkit.Client
	Platform       specs.Platform
	ProgSockPath   string
	OCIStore       content.Store
	LeaseManager   *leaseutil.Manager
	Auth           *auth.RegistryAuthProvider
	Secrets        *core.SecretStore
}

type APIServer struct {
	// The root of the schema, housing all of the state and dependencies for the
	// server for easy access from descendent objects.
	root *core.Query
}

func New(ctx context.Context, params InitializeArgs) (*APIServer, error) {
	svcs := core.NewServices(params.BuildkitClient)

	root := core.NewRoot()
	root.Buildkit = params.BuildkitClient
	root.Services = svcs
	root.ProgrockSocketPath = params.ProgSockPath
	root.Platform = core.Platform(params.Platform)
	root.Secrets = params.Secrets
	root.OCIStore = params.OCIStore
	root.LeaseManager = params.LeaseManager
	root.Auth = params.Auth

	dag := dagql.NewServer(root)

	// stash away the cache so we can share it between other servers
	root.Cache = dag.Cache

	dag.Around(tracing.AroundFunc)

	coreMod := &CoreMod{dag: dag}
	if err := coreMod.Install(ctx, dag); err != nil {
		return nil, err
	}

	// the main client caller starts out with the core API loaded
	root.InstallDefaultClientContext(
		core.NewModDeps(root, []core.Mod{coreMod}),
	)

	return &APIServer{
		root: root,
	}, nil
}

func (s *APIServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	errorOut := func(err error, code int) {
		bklog.G(ctx).WithError(err).Error("failed to serve request")
		http.Error(w, err.Error(), code)
	}

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		errorOut(err, http.StatusInternalServerError)
		return
	}

	callContext, ok := s.root.ClientCallContext(clientMetadata.ModuleCallerDigest)
	if !ok {
		errorOut(fmt.Errorf("client call %s not found", clientMetadata.ModuleCallerDigest), http.StatusInternalServerError)
		return
	}

	rec := progrock.FromContext(ctx)
	if header := r.Header.Get("X-Progrock-Parent"); header != "" {
		rec = rec.WithParent(header)
	} else if callContext.ProgrockParent != "" {
		rec = rec.WithParent(callContext.ProgrockParent)
	}
	ctx = progrock.ToContext(ctx, rec)

	schema, err := callContext.Deps.Schema(ctx)
	if err != nil {
		// TODO: technically this is not *always* bad request, should ideally be more specific and differentiate
		errorOut(err, http.StatusBadRequest)
		return
	}

	defer func() {
		if v := recover(); v != nil {
			bklog.G(context.TODO()).Errorf("panic serving schema: %v %s", v, string(debug.Stack()))
			// check whether this is a hijacked connection, if so we can't write any http errors to it
			_, err := w.Write(nil)
			if err == http.ErrHijacked {
				return
			}
			gqlErr := &gqlerror.Error{
				Message: "Internal Server Error",
			}
			code := http.StatusInternalServerError
			switch v := v.(type) {
			case error:
				gqlErr.Err = v
				gqlErr.Message = v.Error()
			case string:
				gqlErr.Message = v
			}
			res := graphql.Response{
				Errors: gqlerror.List{gqlErr},
			}
			bytes, err := json.Marshal(res)
			if err != nil {
				panic(err)
			}
			http.Error(w, string(bytes), code)
		}
	}()

	srv := handler.NewDefaultServer(schema)
	// NB: break glass when needed:
	// srv.AroundResponses(func(ctx context.Context, next graphql.ResponseHandler) *graphql.Response {
	// 	res := next(ctx)
	// 	pl, err := json.Marshal(res)
	// 	slog.Debug("graphql response", "response", string(pl), "error", err)
	// 	return res
	// })
	mux := http.NewServeMux()
	mux.Handle("/query", srv)
	mux.Handle("/shutdown", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		bklog.G(ctx).Debugf("shutting down client %s", clientMetadata.ClientID)
		if err := s.root.Services.StopClientServices(ctx, clientMetadata); err != nil {
			bklog.G(ctx).WithError(err).Error("failed to shutdown")
		}
	}))

	s.root.MuxEndpoints(mux)

	r = r.WithContext(ctx)

	var handler http.Handler = mux
	handler = flushAfterNBytes(buildkit.MaxFileContentsChunkSize)(handler)
	handler.ServeHTTP(w, r)
}

func (s *APIServer) ShutdownClient(ctx context.Context, client *engine.ClientMetadata) error {
	return s.root.Services.StopClientServices(ctx, client)
}

func (s *APIServer) CurrentServedDeps(ctx context.Context) (*core.ModDeps, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}
	callCtx, ok := s.root.ClientCallContext(clientMetadata.ModuleCallerDigest)
	if !ok {
		return nil, fmt.Errorf("client call %s not found", clientMetadata.ModuleCallerDigest)
	}
	return callCtx.Deps, nil
}

func (s *APIServer) Introspect(ctx context.Context) (string, error) {
	return s.root.DefaultDeps.SchemaIntrospectionJSON(ctx, false)
}

type SchemaResolvers interface {
	Install()
}

func schemaIntrospectionJSON(ctx context.Context, dag *dagql.Server) (json.RawMessage, error) {
	data, err := dag.Query(ctx, introspection.Query, nil)
	if err != nil {
		return nil, fmt.Errorf("introspection query failed: %w", err)
	}
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal introspection result: %w", err)
	}
	return json.RawMessage(jsonBytes), nil
}

func gqlObjectName(name string) string {
	// gql object name is capitalized camel case
	return strcase.ToCamel(name)
}

func namespaceObject(objName, namespace string) string {
	gqlObjName := gqlObjectName(objName)
	if rest := strings.TrimPrefix(gqlObjName, gqlObjectName(namespace)); rest != gqlObjName {
		if len(rest) == 0 {
			// objName equals namespace, don't namespace this
			return gqlObjName
		}
		// we have this case check here to check for a boundary
		// e.g. if objName="Postman" and namespace="Post", then we should still namespace
		// this to "PostPostman" instead of just going for "Postman" (but we should do that
		// if objName="PostMan")
		if 'A' <= rest[0] && rest[0] <= 'Z' {
			// objName has namespace prefixed, don't namespace this
			return gqlObjName
		}
	}

	return gqlObjectName(namespace + "_" + objName)
}

func gqlFieldName(name string) string {
	// gql field name is uncapitalized camel case
	return strcase.ToLowerCamel(name)
}
