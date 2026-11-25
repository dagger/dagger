package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"

	"github.com/dagger/dagger/dagql"
)

// stopExported is an export's reply shaped as the content exports of
// stopping engines are: held rows, each with its recipe and a few links,
// records in full with a payload and a call, and outputs on some of the
// rows. A typical one sends about ten thousand rows and a few megabytes,
// of which about a hundred rows in full and as many outputs; a large one,
// tens of thousands of rows, a third of them in full, in tens of megabytes.
func stopExported(rows, full, outputs int) Exported {
	payload, _ := json.Marshal(map[string]any{
		"form":     "snapshot",
		"platform": map[string]string{"os": "linux", "architecture": "arm64"},
		"dir":      "/src/" + strings.Repeat("sdk/", 8),
		"lazyJSON": map[string]any{"kind": "directory.withDirectory", "path": "/work", "include": []string{"**/*.go", "go.mod", "go.sum"}, "exclude": []string{"**/testdata"}},
	})
	bundle := dagql.ValueBundle{Version: dagql.ValueBundleVersion, Roots: []dagql.TransferredRoot{{Ordinal: dagql.TransferOrdinal(rows)}}}
	for i := range rows {
		value := dagql.TransferredValue{Ordinal: dagql.TransferOrdinal(i + 1), SenderNumber: uint64(1_000_000 + i), SenderReplacements: 1, ExpiresAtUnix: 1795300000}
		for d := 1; d <= 3 && i-d >= 0; d++ {
			value.DependencyIDs = append(value.DependencyIDs, uint64(i-d+1))
		}
		if i < rows-full {
			value.Held = digest.FromString(fmt.Sprint("recipe ", i))
			value.Record = dagql.PersistedRecord{ResultID: value.SenderNumber}
		} else {
			value.Record = dagql.PersistedRecord{
				ResultID: value.SenderNumber,
				Envelope: dagql.PersistedResultEnvelope{ResultID: value.SenderNumber, TypeName: "Directory", ObjectCodec: "Directory", ObjectJSON: payload},
				Call: &dagql.ResultCall{
					Type:     &dagql.ResultCallType{NamedType: "Directory"},
					Field:    "withDirectory",
					Receiver: &dagql.ResultCallRef{ResultID: value.SenderNumber - 1},
					Args: []*dagql.ResultCallArg{
						{Name: "path", Value: &dagql.ResultCallLiteral{Kind: dagql.ResultCallLiteralKindString, StringValue: "/work"}},
						{Name: "directory", Value: &dagql.ResultCallLiteral{Kind: dagql.ResultCallLiteralKindResultRef, ResultRef: &dagql.ResultCallRef{ResultID: value.SenderNumber - 2}}},
					},
				},
			}
		}
		bundle.Values = append(bundle.Values, value)
	}
	for i := range outputs {
		bundle.Outputs = append(bundle.Outputs, dagql.TransferredOutput{
			Ordinal: dagql.TransferOrdinal(rows - i),
			Address: dagql.PersistedPartAddress{Part: "snapshot"},
			State:   "completed",
			Value:   &dagql.SnapshotValue{Kind: "directory", Path: "/"},
			Prefix:  &dagql.ChainPrefix{Ordinal: dagql.TransferOrdinal(rows), Address: dagql.PersistedPartAddress{Part: "snapshot"}, Layers: 1 + i%40},
			Owner:   &dagql.PersistedOfferOwner{},
		})
	}
	return Exported{ExportID: "0123456789abcdef01234567", Bundle: &bundle}
}

// BenchmarkExportedReply times an export's reply as the engine encodes it:
// its body, then its envelope, which checks and copies the body's bytes
// again (twice), against the envelope and body in one pass, as the service
// encodes what it sends (once).
func BenchmarkExportedReply(b *testing.B) {
	for _, shape := range []struct {
		name                string
		rows, full, outputs int
	}{
		{"median", 10_252, 100, 93},
		{"longest", 32_353, 9_829, 7_676},
	} {
		exported := stopExported(shape.rows, shape.full, shape.outputs)
		once, err := EncodeEnvelope(1, 2, TypeExported, exported)
		if err != nil {
			b.Fatal(err)
		}
		b.Run(shape.name+"/twice", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(once)))
			for b.Loop() {
				env, err := NewEnvelope(1, 2, TypeExported, exported)
				if err != nil {
					b.Fatal(err)
				}
				if _, err := json.Marshal(env); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(shape.name+"/once", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(once)))
			for b.Loop() {
				if _, err := EncodeEnvelope(1, 2, TypeExported, exported); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
