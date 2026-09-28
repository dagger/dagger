package idtui

import (
	"fmt"
	"io"
	"testing"

	callpbv1 "github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/muesli/termenv"
)

// An argument spine, not a receiver spine: every source is another call. The
// withDirectory variant also exercises DSL conversion before generic rendering.
func nestedRenderCalls(field string, depth int) (*dagui.DB, *callpbv1.Call) {
	db := dagui.NewDB()
	leaf := &callpbv1.Call{Digest: "leaf", Field: "directory", Type: &callpbv1.Type{NamedType: "Directory"}}
	db.Calls[leaf.Digest] = leaf
	root := leaf
	for i := range depth {
		root = &callpbv1.Call{
			Digest: fmt.Sprintf("call-%d", i), Field: field,
			Type: &callpbv1.Type{NamedType: "Directory"},
			Args: []*callpbv1.Argument{
				{Name: "path", Value: &callpbv1.Literal{Value: &callpbv1.Literal_String_{String_: "src"}}},
				{Name: "source", Value: &callpbv1.Literal{Value: &callpbv1.Literal_CallDigest{CallDigest: root.Digest}}},
			},
		}
		db.Calls[root.Digest] = root
	}
	return db, root
}

func TestRenderCallNestedWork(t *testing.T) {
	// Count DSL visits rather than asserting elapsed time. Before the fix each
	// level expanded source during JSON conversion and then expanded it again
	// for display, including inside each successive width probe.
	const field = "countedDirectory"
	for _, depth := range []int{16, 64, 256} {
		t.Run(fmt.Sprint(depth), func(t *testing.T) {
			visits := 0
			FieldRendererRegistry[field] = func() FieldRenderer {
				visits++
				if visits > 2*depth {
					t.Fatalf("more than one display and one width visit per call: %d at depth %d", visits, depth)
				}
				return &WithDirectoryArgs{}
			}
			t.Cleanup(func() { delete(FieldRendererRegistry, field) })
			db, root := nestedRenderCalls(field, depth)
			r := newRenderer(db, -1, dagui.FrontendOpts{}, true)
			r.enableCallSimplification(80)
			out := termenv.NewOutput(io.Discard, termenv.WithProfile(termenv.Ascii))
			if err := r.renderCall(out, nil, root, "", false, 0, false, nil, false); err != nil {
				t.Fatal(err)
			}
			if visits != 2*depth {
				t.Fatalf("got %d visits, want %d", visits, 2*depth)
			}
			if r.widthProbe != nil {
				t.Fatal("width measurements retained after root render")
			}
		})
	}
}

func TestRenderCallSharedWidthWork(t *testing.T) {
	const depth = 64
	const field = "countedSharedDirectory"
	visits := 0
	FieldRendererRegistry[field] = func() FieldRenderer {
		visits++
		if visits > depth {
			t.Fatal("width probe expanded a shared subtree more than once")
		}
		return &WithDirectoryArgs{}
	}
	t.Cleanup(func() { delete(FieldRendererRegistry, field) })
	db, root := nestedRenderCalls(field, depth)
	for _, call := range db.Calls {
		if len(call.Args) > 0 {
			call.Args = append(call.Args, &callpbv1.Argument{Name: "other", Value: call.Args[1].Value})
		}
	}
	// The DAG has 65 calls, but fully expanding it would print 2^64 leaves.
	// Only measure it: actual display still necessarily costs its output size.
	r := newRenderer(db, -1, dagui.FrontendOpts{}, true)
	r.enableCallSimplification(80)
	if width := r.compactRenderedLen(nil, root, "", false, 0, false, nil, false); width != 81 {
		t.Fatalf("got width %d, want saturated width 81", width)
	}
	if visits != depth {
		t.Fatalf("got %d width visits, want %d", visits, depth)
	}
}

func BenchmarkRenderCallNested(b *testing.B) {
	for _, field := range []string{"withDirectory", "custom"} {
		depths := []int{4, 8, 12, 44}
		if field == "custom" {
			depths = []int{32, 128, 512}
		}
		for _, depth := range depths {
			b.Run(fmt.Sprintf("%s/%d", field, depth), func(b *testing.B) {
				db, root := nestedRenderCalls(field, depth)
				out := termenv.NewOutput(io.Discard, termenv.WithProfile(termenv.Ascii))
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					r := newRenderer(db, -1, dagui.FrontendOpts{}, true)
					r.enableCallSimplification(80)
					if err := r.renderCall(out, nil, root, "", false, 0, false, nil, false); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
