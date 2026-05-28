package schema

import (
	"context"
	"fmt"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql"
)

// legacyIDView is the view in which per-type FooID scalars and the
// load<Foo>FromID root fields are exposed. Older client engine
// versions expected this surface; modern clients use the unified ID
// scalar plus node(id:).
var legacyIDView = BeforeVersion("v0.21.1")

// legacyIDHook installs a per-type FooID scalar and a
// load<Foo>FromID root field for every idable object as it is
// registered on the server. It runs for both the core schema and any
// module-installed objects, so user-defined types automatically join
// the legacy surface for older clients.
type legacyIDHook struct {
	server *dagql.Server
}

func (h *legacyIDHook) ForkInstallHook(server *dagql.Server) dagql.InstallHook {
	return &legacyIDHook{server: server}
}

func (h *legacyIDHook) InstallObject(class dagql.ObjectType, _ ...*ast.Directive) {
	if _, ok := class.IDType(); !ok {
		return
	}
	objName := class.TypeName()
	if strings.HasPrefix(objName, "_") {
		return
	}
	idScalar := dagql.NewScalar(objName+"ID", dagql.AnyID{})
	h.server.InstallScalar(idScalar, legacyIDView)
	h.server.Root().ObjectType().Extend(dagql.FieldSpec{
		Name:        fmt.Sprintf("load%sFromID", objName),
		Description: fmt.Sprintf("Load a %s from its ID.", objName),
		Type:        class.Typed(),
		Args: dagql.NewInputSpecs(dagql.InputSpec{
			Name: "id",
			Type: idScalar,
		}),
		ViewFilter: legacyIDView,
	}, func(ctx context.Context, _ dagql.AnyResult, args map[string]dagql.Input) (dagql.AnyResult, error) {
		idable, ok := dagql.UnwrapAs[dagql.IDable](args["id"])
		if !ok {
			return nil, fmt.Errorf("expected IDable, got %T", args["id"])
		}
		id, err := idable.ID()
		if err != nil {
			return nil, fmt.Errorf("expected valid ID: %w", err)
		}
		srv := dagql.CurrentDagqlServer(ctx)
		if srv == nil {
			return nil, fmt.Errorf("current dagql server not found")
		}
		res, err := srv.Load(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("load %s: %w", objName, err)
		}
		if res.Type().Name() != objName {
			return nil, fmt.Errorf("load %s: expected %s, got %s", objName, objName, res.Type().Name())
		}
		return res, nil
	})
}
