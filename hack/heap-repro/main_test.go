package main

import (
	"testing"

	"github.com/google/pprof/profile"
)

func stack(names ...string) *profile.Sample {
	s := &profile.Sample{}
	for _, name := range names {
		s.Location = append(s.Location, &profile.Location{
			Line: []profile.Line{{Function: &profile.Function{Name: name}}},
		})
	}
	return s
}

func TestSiteLabel(t *testing.T) {
	for _, tc := range []struct {
		name   string
		sample *profile.Sample
		want   string
	}{
		{
			name:   "dagger frame is trimmed",
			sample: stack("github.com/dagger/dagger/dagql.(*Server).Fork", "github.com/dagger/dagger/core.foo"),
			want:   "dagql.(*Server).Fork",
		},
		{
			name:   "otel frame is trimmed",
			sample: stack("go.opentelemetry.io/otel/sdk/log.newRing", "go.opentelemetry.io/otel/sdk/log.NewBatchProcessor"),
			want:   "otel/sdk/log.newRing",
		},
		{
			name:   "stdlib helper is attributed to its caller",
			sample: stack("bufio.NewWriterSize", "encoding/json.Marshal", "github.com/dagger/dagger/engine/clientdb.(*DB).write"),
			want:   "engine/clientdb.(*DB).write",
		},
		{
			name: "generic stdlib helper with package paths in its type arguments is still stdlib",
			sample: stack(
				"maps.Clone[go.shape.map[string]*github.com/dagger/dagger/dagql.Field[go.shape.*uint8]]",
				"github.com/dagger/dagger/dagql.(*Server).SchemaForView.func1",
			),
			want: "dagql.(*Server).SchemaForView.func1",
		},
		{
			name:   "generic non-stdlib frame keeps its name without type arguments",
			sample: stack("github.com/dagger/dagger/engine/clientdb.(*logStream[go.shape.struct { ID int64; Body []uint8 }]).append"),
			want:   "engine/clientdb.(*logStream).append",
		},
		{
			name:   "all stdlib falls back to the leaf",
			sample: stack("runtime.malg", "runtime.newproc1"),
			want:   "runtime.malg",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := siteLabel(tc.sample); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
