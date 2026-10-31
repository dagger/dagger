package core

import (
	"context"
	"fmt"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// TestInterfaceNameNormalization checks that a module's interface function
// and argument names are the same as an object's declaring the same function,
// on either side of the cut, and that the module's own object implements its
// interface.
//
// Interface members used to be normalized a second time when the interface
// was installed. The new rules are idempotent, but the legacy strcase rules
// aren't (e2eTest is e2ETest once, e2Etest twice), so a pre-cut module's
// interface was served as e2Etest(e2Earg:) while its object was
// e2ETest(e2EArg:), and the object didn't implement the interface.
// ModuleSource.naming's fieldNames names both.
func (ModuleNamingSuite) TestInterfaceNameNormalization(ctx context.Context, t *testctx.T) {
	for _, tc := range []struct {
		version    string
		field, arg string
	}{
		{"v1.0.0-beta.20", "e2ETest", "e2EArg"},
		{namingCut, "e2eTest", "e2eArg"},
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
			out, err := ws.With(daggerQuery(`{moduleSource(refString: "./iface") { naming { fieldNames(names: ["e2eTest", "e2eArg"]) } }}`)).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, []string{tc.field, tc.arg}, gjsonStrings(gjson.Get(out, "moduleSource.naming.fieldNames")))

			out, err = ws.With(daggerQueryAt("iface", `{
  iface: __type(name: "IfaceTester") { fields { name args { name } } }
  obj: __type(name: "IfaceImpl") { fields { name args { name } } }
  impl { id }
}`)).Stdout(ctx)
			require.NoError(t, err)
			for _, typ := range []string{"iface", "obj"} {
				require.ElementsMatch(t, []string{"id", tc.field}, gjsonStrings(gjson.Get(out, typ+".fields.#.name")), out)
				require.Equal(t, tc.arg, gjson.Get(out, fmt.Sprintf("%s.fields.#(name==%q).args.0.name", typ, tc.field)).String(), out)
			}

			// The module's object implements its interface.
			id := gjson.Get(out, "impl.id").String()
			out, err = ws.With(daggerQueryAt("iface", `{ node(id: %q) { ... on IfaceTester { %s(%s: "a") } } }`, id, tc.field, tc.arg)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, fmt.Sprintf(`{"node": {%q: "ok a"}}`, tc.field), out)
		})
	}
}
