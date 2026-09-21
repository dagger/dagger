package dagql

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
)

// The parts are already complete: this fixture tests discovery and opening,
// without invoking a lazy body or a remote provider.
type inlineCaptureTestValue struct {
	transferTestValue
	host                *PartHost
	captures            atomic.Int32
	changes             atomic.Int32
	changeNextCapture   atomic.Bool
	changeAfterMetadata bool
	metadataOpens       atomic.Int32
	snapshotOpens       atomic.Int32
	onEncode            func() error
}

func (*inlineCaptureTestValue) Type() *ast.Type {
	return &ast.Type{NamedType: "InlineCaptureTestValue", NonNull: true}
}

func (v *inlineCaptureTestValue) BindPartHost(host *PartHost) { v.host = host }

func (v *inlineCaptureTestValue) EncodePersistedObject(ctx context.Context, enc *PersistEncodeContext) (PersistedObjectEncoding, error) {
	v.captures.Add(1)
	if v.onEncode != nil {
		if err := v.onEncode(); err != nil {
			return PersistedObjectEncoding{}, err
		}
	}
	if v.changeNextCapture.Swap(false) {
		// The codec has already recorded the revision. Model a publication
		// before probePart checks that revision, deterministically and once.
		v.rev.Add(1)
		v.changes.Add(1)
	}
	return v.transferTestValue.EncodePersistedObject(ctx, enc)
}

func (v *inlineCaptureTestValue) OpenPart(_ context.Context, address PersistedPartAddress) error {
	switch address.Part {
	case "metadata":
		if v.metadataOpens.Add(1) == 1 && v.changeAfterMetadata {
			v.changeNextCapture.Store(true)
		}
	case "snapshot":
		v.snapshotOpens.Add(1)
	default:
		return errors.New("unexpected inline test part")
	}
	return nil
}

type inlineCaptureTestCodec struct{ transferTestCodec }

func (inlineCaptureTestCodec) MapSnapshotParts(v PersistedPayloadVisit) ([]CapturedCodecOutput, error) {
	return []CapturedCodecOutput{
		{Address: PersistedPartAddress{OutputPath: v.Path, Part: "metadata"}, State: "absent"},
		{Address: PersistedPartAddress{OutputPath: v.Path, Part: "snapshot"}, State: "absent"},
	}, nil
}

func (inlineCaptureTestCodec) DescribeParts(v PersistedPayloadVisit) ([]PartProbe, error) {
	return []PartProbe{
		{Descriptor: PartDescriptor{Address: PersistedPartAddress{OutputPath: v.Path, Part: "metadata"}, Absent: true}, LocalComplete: true},
		{Descriptor: PartDescriptor{Address: PersistedPartAddress{OutputPath: v.Path, Part: "snapshot"}, Absent: true}, LocalComplete: true},
	}, nil
}

func init() {
	RegisterPersistedObjectFamily(PersistedObjectFamily{
		Name: "dagql_test.InlineCapture", Typed: (*inlineCaptureTestValue)(nil),
		Visitor: PersistedNoReferences{}, Transfer: inlineCaptureTestCodec{},
	})
}

func TestPartHostInlineAllPartsRetriesCapture(t *testing.T) {
	for _, mode := range []string{"before-metadata", "after-metadata", "joined-error", "cancellation"} {
		t.Run(mode, func(t *testing.T) {
			ctx, c, srv := persistedListTestCache(t, "")
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			srv.InstallObject(NewClass(srv, ClassOpts[*inlineCaptureTestValue]{}))
			value := &inlineCaptureTestValue{}
			sibling := &inlineCaptureTestValue{}
			item, err := NewResultForCall(value, persistCodecFrame("item", value))
			require.NoError(t, err)
			other, err := NewResultForCall(sibling, persistCodecFrame("sibling", sibling))
			require.NoError(t, err)
			root := persistedListTestResult(t, ctx, c, srv, "inline", ResultArray[*inlineCaptureTestValue]{item, other})
			require.Zero(t, item.cacheSharedResult().id, "the target must remain inline")
			require.NotNil(t, value.host)
			require.Equal(t, PersistedRefPath{}.Field("items").Index(0), value.host.path)
			row := root.cacheSharedResult()
			c.egraphMu.Lock()
			gate := row.partGate.loadOrCreate()
			gate.mu.Lock()
			gate.managed = true
			row.partGate.active.Store(true)
			gate.mu.Unlock()
			before := row.incomingOwnershipCount
			c.egraphMu.Unlock()

			realErr := errors.New("inline capture failed")
			switch mode {
			case "before-metadata":
				value.changeNextCapture.Store(true)
			case "after-metadata":
				value.changeAfterMetadata = true
			case "joined-error":
				value.onEncode = func() error {
					if value.captures.Load() == 1 {
						return errors.Join(ErrPersistStateNotReady, realErr)
					}
					return nil
				}
			case "cancellation":
				value.onEncode = func() error { cancel(); return nil }
			}

			// Call the inline host directly with no named parts. Root EvaluateParts
			// would conceal the missing inline retry by applying its own loop.
			released := armLazyAttemptReleased(c)
			row.lazyMu.Lock()
			generationBefore := row.lazyGeneration
			row.lazyMu.Unlock()
			err = value.host.Evaluate(ctx)
			// Each acquire task wakes its caller before its deferred row release.
			// Count the attempts this call started, including any probe retries,
			// and await their release hooks before observing ownership.
			row.lazyMu.Lock()
			attempts := row.lazyGeneration - generationBefore
			row.lazyMu.Unlock()
			for range attempts {
				waitLazyAttemptReleased(t, released)
			}
			t.Logf("observed %d acquisition attempt releases", attempts)
			c.egraphMu.RLock()
			after := row.incomingOwnershipCount
			c.egraphMu.RUnlock()
			require.Equal(t, before, after, "each discovery attempt releases its row hold")
			require.Zero(t, sibling.metadataOpens.Load())
			require.Zero(t, sibling.snapshotOpens.Load(), "the demand stays scoped to the target")
			switch mode {
			case "joined-error":
				require.ErrorIs(t, err, ErrPersistStateNotReady)
				require.ErrorIs(t, err, realErr)
				require.EqualValues(t, 1, value.captures.Load(), "a joined real failure must not retry")
			case "cancellation":
				require.ErrorIs(t, err, context.Canceled)
				require.EqualValues(t, 1, value.captures.Load())
			default:
				require.EqualValues(t, 1, value.changes.Load(), "the discovery capture changed exactly once")
				if mode == "after-metadata" {
					require.Positive(t, value.metadataOpens.Load(), "the changed capture follows metadata demand")
				}
				require.NoError(t, err, "a discovery capture refusal must not escape inline evaluation")
				require.Positive(t, value.metadataOpens.Load())
				require.Positive(t, value.snapshotOpens.Load(), "retry must reach the requested output")
			}
		})
	}
}
