package core

import (
	"context"
	"fmt"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// TestInterfaceNameNormalization records how a module's interface function
// and argument names come out next to an object's, on either side of the cut.
//
// Installing an interface (core/interface.go) runs FieldName/ArgName again on
// names its typedef already normalized; objects take them as they are. The
// new rules are idempotent, so that's harmless from the cut on. The legacy
// strcase rules aren't: e2eTest is e2ETest once and e2Etest twice. So a
// pre-cut module's interface is named e2Etest(e2Earg:) while an object
// declaring the same function is named e2ETest(e2EArg:), and the module's own
// object doesn't implement its interface: an inline fragment on the interface
// matches nothing. ModuleSource.naming's fieldNames answers the single pass
// (the object's names); applying it twice gives the interface's.
//
// This pins the current pre-cut wire names; it isn't an endorsement of them.
func (ModuleNamingSuite) TestInterfaceNameNormalization(ctx context.Context, t *testctx.T) {
	for _, tc := range []struct {
		version                 string
		ifaceField, ifaceArg    string
		objField, objArg        string
		implementsItsInterfaces bool
	}{
		{"v1.0.0-beta.20", "e2Etest", "e2Earg", "e2ETest", "e2EArg", false},
		{namingCut, "e2eTest", "e2eArg", "e2eTest", "e2eArg", true},
	} {
		t.Run(tc.version, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			ws := goGitBase(t, c).
				WithNewFile("dagger.toml", "[modules.iface]\nsource = \"./iface\"\n").
				WithNewFile("iface/dagger-module.toml", "name = \"iface\"\nengineVersion = \""+tc.version+"\"\nsource = \".\"\n\n[runtime]\nsource = \"dang\"\n").
				WithNewFile("iface/main.dang", `interface Tester {
  pub e2eTest(e2eArg: String!): String!
}

type Impl implements Tester {
  pub e2eTest(e2eArg: String!): String! {
    "ok " + e2eArg
  }
}

type Iface {
  pub impl: Impl! {
    Impl
  }
}
`)
			out, err := ws.With(daggerQuery(`{moduleSource(refString: "./iface") { naming {
  once: fieldNames(names: ["e2eTest", "e2eArg"])
} }}`)).Stdout(ctx)
			require.NoError(t, err)
			once := gjsonStrings(gjson.Get(out, "moduleSource.naming.once"))
			require.Equal(t, []string{tc.objField, tc.objArg}, once, "fieldNames answers the object's names")
			out, err = ws.With(daggerQuery(`{moduleSource(refString: "./iface") { naming { twice: fieldNames(names: %s) } }}`, gqlStrings(once))).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, []string{tc.ifaceField, tc.ifaceArg}, gjsonStrings(gjson.Get(out, "moduleSource.naming.twice")), "applied twice, the interface's")

			out, err = ws.With(daggerQueryAt("iface", `{
  iface: __type(name: "IfaceTester") { fields { name args { name } } }
  obj: __type(name: "IfaceImpl") { fields { name args { name } } }
  impl { id }
}`)).Stdout(ctx)
			require.NoError(t, err)
			for _, typ := range []struct{ key, field, arg string }{
				{"iface", tc.ifaceField, tc.ifaceArg},
				{"obj", tc.objField, tc.objArg},
			} {
				require.ElementsMatch(t, []string{"id", typ.field}, gjsonStrings(gjson.Get(out, typ.key+".fields.#.name")), out)
				require.Equal(t, typ.arg, gjson.Get(out, fmt.Sprintf("%s.fields.#(name==%q).args.0.name", typ.key, typ.field)).String(), out)
			}

			id := gjson.Get(out, "impl.id").String()
			out, err = ws.With(daggerQueryAt("iface", `{ node(id: %q) { ... on IfaceTester { %s(%s: "a") } } }`, id, tc.ifaceField, tc.ifaceArg)).Stdout(ctx)
			require.NoError(t, err)
			if tc.implementsItsInterfaces {
				require.JSONEq(t, fmt.Sprintf(`{"node": {%q: "ok a"}}`, tc.ifaceField), out)
			} else {
				require.JSONEq(t, `{"node": {}}`, out, "the module's object doesn't implement its interface")
			}
		})
	}
}
