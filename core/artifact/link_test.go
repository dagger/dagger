package artifact

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPlanLink(t *testing.T) {
	unit := Path{Type: "Check", Module: "go", Key: "go/packages/tests/unit", Dimensions: []string{"/go/packages", "/go/packages/tests"}}
	file := Path{Type: "File", Module: "go", Key: "go/packages/tests/log", Dimensions: unit.Dimensions}
	race := Path{Type: "Check", Module: "go", Key: "go/packages/tests/race", Dimensions: unit.Dimensions}
	docs := Path{Type: "Check", Module: "docs", Key: "docs/links"}
	name := func(value string) string { return value }
	require.Equal(t, &Link{}, PlanLink(Paths{unit, file, docs}, unit, true, true, name))
	require.Equal(t, &Link{TypeKey: "unit"}, PlanLink(Paths{unit, file, docs}, unit, true, false, name))
	require.Equal(t, &Link{TypeKey: "unit"}, PlanLink(Paths{unit, race, docs}, unit, true, true, name))
	require.Equal(t, &Link{ExactDimensions: true}, PlanLink(Paths{unit, file}, unit, false, true, name))

	// Both a module and a field can identify this path. Prefer the field.
	require.Equal(t, &Link{TypeKey: "links"}, PlanLink(Paths{unit, docs}, docs, true, true, name))
	require.Equal(t, &Link{Module: true, ExactDimensions: true}, PlanLink(Paths{docs}, docs, false, true, name))
}
