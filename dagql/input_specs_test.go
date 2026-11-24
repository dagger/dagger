package dagql

import (
	"testing"

	"gotest.tools/v3/assert"

	"github.com/dagger/dagger/dagql/call"
)

func inputNames(specs []InputSpec) []string {
	names := make([]string, 0, len(specs))
	for _, spec := range specs {
		names = append(names, spec.Name+":"+spec.Description)
	}
	return names
}

// A field's inputs are worked out once per view and kept. Each view still sees
// exactly its own inputs, however the views are interleaved, and an input added
// later is seen in every view.
func TestInputSpecsPerView(t *testing.T) {
	specs := NewInputSpecs(
		InputSpec{Name: "path", Description: "all", Type: String("")},
		InputSpec{Name: "old", Description: "v1 only", Type: String(""), ViewFilter: ExactView("v1")},
		InputSpec{Name: "path", Description: "v2's own", Type: String(""), ViewFilter: ExactView("v2")},
		InputSpec{Name: "new", Description: "v2 only", Type: String(""), ViewFilter: ExactView("v2")},
	)
	want := map[call.View][]string{
		"":   {"path:all"},
		"v1": {"path:all", "old:v1 only"},
		"v2": {"path:v2's own", "new:v2 only"},
	}
	for _, view := range []call.View{"v1", "v2", "", "v2", "v1", ""} {
		assert.DeepEqual(t, inputNames(specs.Inputs(view)), want[view])
		assert.DeepEqual(t, inputNames(specs.inputs(view)), want[view])
	}

	specs.Add(InputSpec{Name: "added", Description: "all", Type: String("")})
	assert.DeepEqual(t, inputNames(specs.Inputs("v1")), []string{"path:all", "old:v1 only", "added:all"})
	assert.DeepEqual(t, inputNames(specs.Inputs("v2")), []string{"path:v2's own", "new:v2 only", "added:all"})
}

// A copy of the specs taken before an Add keeps seeing the inputs it had.
func TestInputSpecsCopyBeforeAdd(t *testing.T) {
	specs := NewInputSpecs(InputSpec{Name: "a", Type: String("")})
	before := specs
	assert.DeepEqual(t, inputNames(before.Inputs("")), []string{"a:"})
	specs.Add(InputSpec{Name: "b", Type: String("")})
	assert.DeepEqual(t, inputNames(specs.Inputs("")), []string{"a:", "b:"})
	assert.DeepEqual(t, inputNames(before.Inputs("")), []string{"a:"})
}

// Inputs gives its caller a slice of its own: changing it changes no later
// result.
func TestInputSpecsInputsIsTheCallersOwn(t *testing.T) {
	specs := NewInputSpecs(InputSpec{Name: "a", Type: String("")}, InputSpec{Name: "b", Type: String("")})
	got := specs.Inputs("")
	got[0].Name = "changed"
	assert.DeepEqual(t, inputNames(specs.Inputs("")), []string{"a:", "b:"})
}

// Reading a field's inputs again in a view allocates nothing: a field's inputs
// are read several times on every call of it.
func TestInputSpecsSecondReadAllocatesNothing(t *testing.T) {
	specs := NewInputSpecs(
		InputSpec{Name: "a", Type: String("")},
		InputSpec{Name: "b", Type: String(""), ViewFilter: ExactView("v1")},
		InputSpec{Name: "c", Type: String("")},
	)
	specs.inputs("v1")
	allocs := testing.AllocsPerRun(100, func() {
		if len(specs.inputs("v1")) != 3 {
			t.Fatal("wrong inputs")
		}
	})
	assert.Equal(t, allocs, 0.0)
}
