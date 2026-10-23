package dagql

import (
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClassForkHistoryIsolation(t *testing.T) {
	for _, extend := range []bool{false, true} {
		t.Run(fmt.Sprintf("extend=%t", extend), func(t *testing.T) {
			srv := newDagqlServerForTest(t, &reflectedAccessorTestObject{})
			parent := NewClass[*reflectedAccessorTestObject](srv)
			add := func(class Class[*reflectedAccessorTestObject], view, description string) {
				spec := FieldSpec{
					Name:        "versioned",
					Type:        String(""),
					Description: description,
					ViewFilter:  ExactView(view),
				}
				if extend {
					class.Extend(spec, nil)
				} else {
					class.Install(Field[*reflectedAccessorTestObject]{Spec: &spec})
				}
			}
			for i := range 3 {
				add(parent, fmt.Sprint(i), "before fork")
			}
			// Give the backing array spare capacity before it is shared with the
			// fork, without relying on append's growth strategy.
			history := parent.fields["versioned"]
			parent.fields["versioned"] = slices.Grow(history, 1)
			object, err := parent.ForkObjectType(srv)
			require.NoError(t, err)
			fork := object.(Class[*reflectedAccessorTestObject])
			add(parent, "new", "parent")
			add(fork, "new", "fork")
			parentSpec, ok := parent.FieldSpec("versioned", "new")
			require.True(t, ok)
			require.Equal(t, "parent", parentSpec.Description)
			forkSpec, ok := fork.FieldSpec("versioned", "new")
			require.True(t, ok)
			require.Equal(t, "fork", forkSpec.Description)
		})
	}
}
