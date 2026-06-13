package schema

import (
	"context"
	"fmt"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
)

// legacyIDView is the view in which per-type FooID scalars and the
// load<Foo>FromID root fields are exposed. Older client engine
// versions expected this surface; modern clients use the unified ID
// scalar plus node(id:).
var legacyIDView = BeforeVersion("v0.22.0")

// legacyIDHook installs a per-type FooID scalar and a
// load<Foo>FromID root field for every idable object or interface as
// it is registered on the server. It runs for both the core schema
// and any module-installed types, so user-defined types automatically
// join the legacy surface for older clients.
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
	h.installLegacyID(class.TypeName(), class.Typed(), nil, legacyIDObjectView(class))
}

func (h *legacyIDHook) InstallInterface(iface *dagql.Interface, _ ...*ast.Directive) {
	h.installLegacyID(iface.TypeName(), iface.Typed(), iface, legacyIDView)
}

// installLegacyID registers the FooID scalar and load<Foo>FromID root
// field for a single type. When the type is an interface, iface is
// non-nil and is used to verify that the loaded result implements it.
func (h *legacyIDHook) installLegacyID(typeName string, returnType dagql.Typed, iface *dagql.Interface, view dagql.ViewFilter) {
	if strings.HasPrefix(typeName, "_") {
		return
	}
	idScalar := dagql.NewScalar(typeName+"ID", dagql.AnyID{})
	h.server.InstallScalar(idScalar, view)
	h.server.Root().ObjectType().Extend(dagql.FieldSpec{
		Name:        fmt.Sprintf("load%sFromID", typeName),
		Description: fmt.Sprintf("Load a %s from its ID.", typeName),
		Type:        returnType,
		Args: dagql.NewInputSpecs(dagql.InputSpec{
			Name: "id",
			Type: idScalar,
		}),
		ViewFilter: view,
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
			return nil, fmt.Errorf("load %s: %w", typeName, err)
		}
		gotName := res.Type().Name()
		if iface == nil && gotName != typeName {
			return nil, fmt.Errorf("load %s: expected %s, got %s", typeName, typeName, gotName)
		}
		return res, nil
	})
}

func legacyIDObjectView(class dagql.ObjectType) dagql.ViewFilter {
	viewFiltered, ok := class.(interface{ ViewFilter() dagql.ViewFilter })
	if !ok || viewFiltered.ViewFilter() == nil {
		return legacyIDView
	}
	return legacyIDAndView{legacyIDView, viewFiltered.ViewFilter()}
}

type legacyIDAndView []dagql.ViewFilter

func (filters legacyIDAndView) Contains(view call.View) bool {
	for _, filter := range filters {
		if filter != nil && !filter.Contains(view) {
			return false
		}
	}
	return true
}
