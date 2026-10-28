package core

import (
	"encoding/json"
	"fmt"
	"testing"
)

func BenchmarkJSONValueFieldLookup(b *testing.B) {
	for _, packages := range []int{100, 1000, 10000} {
		entries := make(map[string]any, packages)
		for i := range packages {
			entries[fmt.Sprintf("pkg/%05d", i)] = map[string]any{"module": ".", "tests": []string{"TestFirst", "TestSecond"}}
		}
		data, err := json.Marshal(map[string]any{"packages": entries})
		if err != nil {
			b.Fatal(err)
		}
		for _, indexed := range []bool{false, true} {
			b.Run(fmt.Sprintf("packages=%d/indexed=%t", packages, indexed), func(b *testing.B) {
				value := &JSONValue{Data: data}
				if indexed {
					value = NewIndexedJSONValue(data)
				}
				path := []string{"packages", "pkg/00000", "tests"}
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					field, err := value.FieldAt(path)
					if err != nil || len(field.Data) == 0 {
						b.Fatalf("lookup: %v", err)
					}
				}
			})
		}
	}
}
