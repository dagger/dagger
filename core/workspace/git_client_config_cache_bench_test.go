package workspace

import (
	"os"
	"strconv"
	"testing"
)

func BenchmarkGitClientDeclarations(b *testing.B) {
	fixture := os.Getenv("DAGGER_DECLARATIONS_BENCH_CONFIG")
	if fixture == "" {
		fixture = "testdata/git-client-declarations.toml"
	}
	data, err := os.ReadFile(fixture)
	if err != nil {
		b.Fatal(err)
	}
	for _, scenario := range []string{"repeated-content", "distinct-content", "parallel-repeated-content"} {
		b.Run(scenario, func(b *testing.B) {
			current := data
			read := func(string) ([]byte, bool, error) { return current, true, nil }
			ctx := b.Context()
			check := func() {
				got, err := DeclaredGitClients(ctx, ".", read)
				if err != nil || len(got) != 0 {
					b.Errorf("want no Git clients: got %v, err %v", got, err)
				}
			}
			check()
			b.ReportAllocs()
			b.ResetTimer()
			if scenario == "parallel-repeated-content" {
				b.RunParallel(func(pb *testing.PB) {
					for pb.Next() {
						check()
					}
				})
				return
			}
			for i := range b.N {
				if scenario == "distinct-content" {
					current = append(append([]byte(nil), data...), '\n', '#')
					current = strconv.AppendInt(current, int64(i), 10)
				}
				check()
			}
		})
	}
}
