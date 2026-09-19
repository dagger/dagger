package dagql

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/dagql/call"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
)

// These tests are the batch's own behavioral controls for the generic codec:
// attached-null identity, exact numbers, declared list types, the reference
// visitor, envelope version cuts and second saves. They are separate from the
// ported pre-existing codec tests.

// persistCodecUnregistered implements the codec interfaces but is never
// registered as a family.
type persistCodecUnregistered struct{ Name string }

func (*persistCodecUnregistered) Type() *ast.Type {
	return &ast.Type{NamedType: "PersistCodecUnregistered", NonNull: true}
}

func (obj *persistCodecUnregistered) EncodePersistedObject(context.Context, *PersistEncodeContext) (PersistedObjectEncoding, error) {
	return PersistedObjectEncoding{JSON: json.RawMessage(`{}`)}, nil
}

func (*persistCodecUnregistered) DecodePersistedObject(context.Context, *PersistDecodeContext, json.RawMessage) (Typed, error) {
	return &persistCodecUnregistered{}, nil
}

// persistTestScalar is a module-defined scalar: only a server that installed
// it can decode its persisted value.
type persistTestScalar string

func (persistTestScalar) TypeName() string { return "PersistTestScalar" }

func (s persistTestScalar) Type() *ast.Type {
	return &ast.Type{NamedType: s.TypeName(), NonNull: true}
}

func (s persistTestScalar) TypeDefinition(call.View) *ast.Definition {
	return &ast.Definition{Kind: ast.Scalar, Name: s.TypeName()}
}

func (persistTestScalar) DecodeInput(val any) (Input, error) {
	str, ok := val.(string)
	if !ok {
		return nil, fmt.Errorf("cannot create PersistTestScalar from %T", val)
	}
	return persistTestScalar(str), nil
}

func (s persistTestScalar) Decoder() InputDecoder { return s }

func (s persistTestScalar) ToLiteral() call.Literal { return call.NewLiteralString(string(s)) }

func persistCodecFrame(field string, typ Typed) *ResultCall {
	return &ResultCall{Kind: ResultCallKindField, Field: field, Type: NewResultCallType(typ.Type())}
}

func persistCodecRoundTrip(t *testing.T, ctx context.Context, srv *Server, cache PersistedObjectCache, res AnyResult) (PersistedResultEncoding, AnyResult) {
	t.Helper()
	encoding, err := DefaultPersistedSelfCodec.EncodeResult(ctx, cache, res)
	require.NoError(t, err)
	var resultID uint64
	if cache != nil {
		if id, err := cache.PersistedResultID(res); err == nil {
			resultID = id
		}
	}
	decoded, err := DefaultPersistedSelfCodec.DecodeResult(ctx, srv, resultID, res.cacheSharedResult().loadResultCall().clone(), encoding.Envelope)
	require.NoError(t, err)
	return encoding, decoded
}

func TestPersistedAttachedNullKeepsRowIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	ctx, cache, srv := persistedListTestCache(t, path)

	absent := persistedListTestResult(t, ctx, cache, srv, "absent-int", DynamicNullable{Elem: Int(0)})
	absentID, err := cache.PersistedResultID(absent)
	require.NoError(t, err)
	child := persistedListTestResult(t, ctx, cache, srv, "present-int", Int(3))
	list := persistedListTestResult(t, ctx, cache, srv, "nullable-int-list", DynamicResultArrayOutput{
		Elem:   DynamicNullable{Elem: Int(0)},
		Values: []AnyResult{absent, child},
	})
	listID, err := cache.PersistedResultID(list)
	require.NoError(t, err)

	encoding, err := DefaultPersistedSelfCodec.EncodeResult(ctx, cache, absent)
	require.NoError(t, err)
	require.Equal(t, persistedResultKindNull, encoding.Envelope.Kind)
	require.Equal(t, absentID, encoding.Envelope.ResultID, "an attached absent value keeps its row identity")
	require.Equal(t, persistedResultEnvelopeVersion, encoding.Envelope.Version)
	require.Empty(t, encoding.Envelope.ScalarJSON, "absence is not a scalar null")

	fresh := DynamicNullable{Elem: Int(0)}
	require.NoError(t, cache.ReleaseSession(ctx, "test-session"))
	require.NoError(t, cache.Close(ctx))
	ctx, cache, srv = persistedListTestCache(t, path)

	restored, err := cache.LoadResultByResultID(ctx, "test-session", srv, absentID)
	require.NoError(t, err)
	require.Equal(t, absentID, uint64(restored.cacheSharedResult().id))
	require.Equal(t, fresh.Type().String(), restored.Type().String(), "restored type matches a fresh nullable")
	_, present := restored.DerefValue()
	require.False(t, present, "restored absence dereferences to nothing")
	require.Equal(t, "absent-int", restored.cacheSharedResult().loadResultCall().Field, "recorded call survives")

	restoredList, err := cache.LoadResultByResultID(ctx, "test-session", srv, listID)
	require.NoError(t, err)
	first, err := restoredList.NthValue(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, absentID, uint64(first.cacheSharedResult().id), "the list item is the attached absent row")
	_, present = first.DerefValue()
	require.False(t, present, "external item normalization reports absence")
	second, err := restoredList.NthValue(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, Typed(Int(3)), second.Unwrap())

	reencoded, err := DefaultPersistedSelfCodec.EncodeResult(ctx, cache, restored)
	require.NoError(t, err)
	require.Equal(t, encoding.Envelope, reencoded.Envelope, "re-encoding emits the same null with the same row ID")

	// Deliberate controls.
	t.Run("scalar null encoding fails to decode", func(t *testing.T) {
		env := PersistedResultEnvelope{Version: persistedResultEnvelopeVersion, Kind: persistedResultKindScalar, TypeName: "Int", ResultID: absentID, ScalarJSON: json.RawMessage(`null`)}
		_, err := DefaultPersistedSelfCodec.DecodeResult(ctx, srv, absentID, persistCodecFrame("absent-int", DynamicNullable{Elem: Int(0)}), env)
		require.Error(t, err)
	})
	t.Run("absence without a row ID decodes to nothing", func(t *testing.T) {
		env := PersistedResultEnvelope{Version: persistedResultEnvelopeVersion, Kind: persistedResultKindNull}
		decoded, err := DefaultPersistedSelfCodec.DecodeResult(ctx, srv, 0, persistCodecFrame("absent-int", DynamicNullable{Elem: Int(0)}), env)
		require.NoError(t, err)
		require.Nil(t, decoded)
	})
	t.Run("absence declared non-null is rejected", func(t *testing.T) {
		_, err := persistedAbsentValue(cacheTestIntCall("non-null-int"))
		require.ErrorContains(t, err, "declared non-null")
		env := PersistedResultEnvelope{Version: persistedResultEnvelopeVersion, Kind: persistedResultKindNull, ResultID: 77}
		_, err = DefaultPersistedSelfCodec.DecodeResult(ctx, srv, 77, cacheTestIntCall("non-null-int"), env)
		require.ErrorContains(t, err, "declared non-null")
	})
}

func TestPersistedScalarNumbersAreExact(t *testing.T) {
	ctx := setupPersistCodecTest(t)
	srv := CurrentDagqlServer(ctx)

	for _, tc := range []struct {
		name  string
		value Typed
	}{
		{"above float mantissa", Int(9007199254740993)},
		{"max int64", Int(math.MaxInt64)},
		{"min int64", Int(math.MinInt64)},
		{"ordinary float", Float(1.5)},
		{"large float", Float(1e300)},
		{"string", String("9007199254740993")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := NewResultForCall(tc.value, persistCodecFrame("number", tc.value))
			require.NoError(t, err)
			encoding, decoded := persistCodecRoundTrip(t, ctx, srv, nil, res)
			require.Equal(t, tc.value, decoded.Unwrap())
			again, err := DefaultPersistedSelfCodec.EncodeResult(ctx, nil, decoded)
			require.NoError(t, err)
			require.Equal(t, encoding.Envelope, again.Envelope, "second encoding is byte-identical")
		})
	}

	t.Run("untyped float decoding loses the large integer", func(t *testing.T) {
		// The previous decoder went through encoding/json into any, which
		// yields float64; this control shows that path cannot pass the
		// assertion above.
		var lossy any
		require.NoError(t, json.Unmarshal([]byte(`9007199254740993`), &lossy))
		rounded, err := Int(0).DecodeInput(lossy)
		require.NoError(t, err)
		require.NotEqual(t, Int(9007199254740993), rounded)
		exact, err := DecodeLosslessJSON([]byte(`9007199254740993`))
		require.NoError(t, err)
		kept, err := Int(0).DecodeInput(exact)
		require.NoError(t, err)
		require.Equal(t, Input(Int(9007199254740993)), kept)
	})
	t.Run("trailing data is an error", func(t *testing.T) {
		_, err := DecodeLosslessJSON([]byte(`7 8`))
		require.ErrorContains(t, err, "trailing data")
		env := PersistedResultEnvelope{Version: persistedResultEnvelopeVersion, Kind: persistedResultKindScalar, TypeName: "Int", ScalarJSON: json.RawMessage(`7 8`)}
		_, err = DefaultPersistedSelfCodec.DecodeResult(ctx, srv, 0, cacheTestIntCall("trailing"), env)
		require.ErrorContains(t, err, "trailing data")
	})
	t.Run("out of range stays an error", func(t *testing.T) {
		env := PersistedResultEnvelope{Version: persistedResultEnvelopeVersion, Kind: persistedResultKindScalar, TypeName: "Int", ScalarJSON: json.RawMessage(`92233720368547758070`)}
		_, err := DefaultPersistedSelfCodec.DecodeResult(ctx, srv, 0, cacheTestIntCall("range"), env)
		require.Error(t, err)
	})
}

func TestPersistedListsRebuildDeclaredTypes(t *testing.T) {
	ctx := setupPersistCodecTest(t)
	srv := CurrentDagqlServer(ctx)
	srv.InstallScalar(persistTestScalar(""))

	nullableInt := DynamicNullable{Elem: Int(0)}
	obj := &persistCodecObj{Name: "impl"}
	objRes, err := NewResultForCall(obj, persistCodecFrame("iface-impl", obj))
	require.NoError(t, err)
	// The declared element is an interface the concrete object implements;
	// the declaration, not the first item, must survive.
	iface := persistedTypedRef{name: "PersistIface"}

	for _, tc := range []struct {
		name  string
		value Typed
	}{
		{"empty nested non-null", DynamicResultArrayOutput{Elem: DynamicResultArrayOutput{Elem: Int(0)}}},
		{"all-null nested", DynamicResultArrayOutput{Elem: DynamicNullable{Elem: DynamicResultArrayOutput{Elem: Int(0)}}, Values: []AnyResult{nil, nil}}},
		{"optional elements", DynamicResultArrayOutput{Elem: nullableInt, Values: []AnyResult{nil}}},
		{"interface elements", DynamicResultArrayOutput{Elem: iface, Values: []AnyResult{objRes}}},
		{"raw nested arrays", Array[Array[Int]]{{1}, {}}},
		{"custom scalar elements", Array[persistTestScalar]{"a", "b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := NewResultForCall(tc.value, persistCodecFrame("declared", tc.value))
			require.NoError(t, err)
			encoding, decoded := persistCodecRoundTrip(t, ctx, srv, nil, res)
			require.Equal(t, res.Type().String(), decoded.Type().String(), "full declared type survives")
			original := res.Unwrap().(Enumerable)
			restored := decoded.Unwrap().(Enumerable)
			require.Equal(t, original.Element().Type().String(), restored.Element().Type().String(), "declared element type survives, not the first item's")
			require.Equal(t, original.Len(), restored.Len())
			frame := res.cacheSharedResult().loadResultCall()
			for i := 1; i <= original.Len(); i++ {
				want, err := original.NthValue(i, frame)
				require.NoError(t, err)
				got, err := restored.NthValue(i, frame)
				require.NoError(t, err)
				if want == nil {
					require.Nil(t, got)
					continue
				}
				require.NotNil(t, got)
				require.Equal(t, want.Type().String(), got.Type().String())
				if wantObj, ok := want.(AnyObjectResult); ok {
					require.Equal(t, wantObj.Unwrap().(*persistCodecObj).Name, got.Unwrap().(*persistCodecObj).Name)
				}
			}
			again, err := DefaultPersistedSelfCodec.EncodeResult(ctx, nil, decoded)
			require.NoError(t, err)
			require.Equal(t, encoding.Envelope, again.Envelope)
			require.Empty(t, encoding.Envelope.ObjectJSON)
		})
	}

	t.Run("nullable declaration is presented like a fresh nullable result", func(t *testing.T) {
		value := DynamicResultArrayOutput{Elem: Int(0), Values: []AnyResult{cacheTestIntResult(cacheTestIntCall("item"), 1)}}
		frame := &ResultCall{Kind: ResultCallKindField, Field: "nullable-list", Type: &ResultCallType{Elem: &ResultCallType{NamedType: "Int", NonNull: true}}}
		res, err := NewResultForCall(value, frame)
		require.NoError(t, err)
		freshView := res.NullableWrapped()
		encoding, err := DefaultPersistedSelfCodec.EncodeResult(ctx, nil, freshView)
		require.NoError(t, err)
		decoded, err := DefaultPersistedSelfCodec.DecodeResult(ctx, srv, 0, frame.clone(), encoding.Envelope)
		require.NoError(t, err)
		require.Equal(t, freshView.Type().String(), decoded.Type().String(), "before dereference the nullable declaration shows")
		freshInner, ok := freshView.DerefValue()
		require.True(t, ok)
		restoredInner, ok := decoded.DerefValue()
		require.True(t, ok)
		require.Equal(t, freshInner.Type().String(), restoredInner.Type().String(), "dereference exposes the non-null array")
		item, err := restoredInner.Unwrap().(Enumerable).NthValue(1, nil)
		require.NoError(t, err)
		require.Equal(t, Typed(Int(1)), item.Unwrap())
	})

	t.Run("a call without a declared list type is rejected", func(t *testing.T) {
		value := Array[Int]{1}
		res, err := NewResultForCall(value, cacheTestIntCall("not-a-list"))
		require.NoError(t, err)
		_, err = DefaultPersistedSelfCodec.EncodeResult(ctx, nil, res)
		require.ErrorContains(t, err, "does not declare a list")
		env := PersistedResultEnvelope{Version: persistedResultEnvelopeVersion, Kind: persistedResultKindList, Items: []PersistedResultEnvelope{
			{Version: persistedResultEnvelopeVersion, Kind: persistedResultKindScalar, TypeName: "Int", ScalarJSON: json.RawMessage(`1`)},
		}}
		_, err = DefaultPersistedSelfCodec.DecodeResult(ctx, srv, 0, cacheTestIntCall("not-a-list"), env)
		require.ErrorContains(t, err, "does not declare a list")
	})
}

func TestPersistedScalarDecodeResolvesDefiningServer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	ctx, cache, srv := persistedListTestCache(t, path)
	srv.InstallScalar(persistTestScalar(""))
	value := persistedListTestResult(t, ctx, cache, srv, "module-scalar", persistTestScalar("cold"))
	id, err := cache.PersistedResultID(value)
	require.NoError(t, err)
	require.NoError(t, cache.ReleaseSession(ctx, "test-session"))
	require.NoError(t, cache.Close(ctx))

	ctx, cache, srv = persistedListTestCache(t, path)
	// The reading server does not define the scalar; without a resolver the
	// row cannot decode.
	_, err = cache.LoadResultByResultID(ctx, "test-session", srv, id)
	require.ErrorContains(t, err, "unknown scalar type")
	// The failed read leaves the envelope untouched; the next save copies it.
	require.NoError(t, cache.Close(ctx))

	ctx, cache, srv = persistedListTestCache(t, path)
	defining := newDagqlServerForTest(t, &persistCodecRoot{})
	defining.InstallScalar(persistTestScalar(""))
	var resolved []string
	srv.SetResultServerForCall(func(_ context.Context, frame *ResultCall) (*Server, error) {
		resolved = append(resolved, frame.Field)
		return defining, nil
	})
	loaded, err := cache.LoadResultByResultID(ctx, "test-session", srv, id)
	require.NoError(t, err)
	require.Equal(t, []string{"module-scalar"}, resolved, "the defining schema is resolved from the recorded call")
	require.Equal(t, Typed(persistTestScalar("cold")), loaded.Unwrap())
}

func TestPersistedObjectFamilyDispatch(t *testing.T) {
	ctx := setupPersistCodecTest(t)
	srv := CurrentDagqlServer(ctx)

	t.Run("unregistered codec cannot encode", func(t *testing.T) {
		obj := &persistCodecUnregistered{Name: "x"}
		res, err := NewResultForCall(obj, persistCodecFrame("unregistered", obj))
		require.NoError(t, err)
		_, err = DefaultPersistedSelfCodec.EncodeResult(ctx, nil, res)
		require.ErrorContains(t, err, "no registered persisted object family")
	})

	var original AnyResult
	require.NoError(t, srv.Select(ctx, srv.root, &original, Selector{Field: "obj"}))
	encoding, err := DefaultPersistedSelfCodec.EncodeResult(ctx, nil, original)
	require.NoError(t, err)
	require.Equal(t, "dagql_test.PersistCodecObj", encoding.Envelope.ObjectCodec)
	require.Equal(t, "PersistCodecObj", encoding.Envelope.TypeName)
	frame := original.cacheSharedResult().loadResultCall().clone()

	t.Run("unknown family is rejected", func(t *testing.T) {
		env := encoding.Envelope
		env.ObjectCodec = "nope"
		_, err := DefaultPersistedSelfCodec.DecodeResult(ctx, srv, 0, frame, env)
		require.ErrorContains(t, err, `unknown object codec family "nope"`)
		_, err = VisitEncodedReferences(PersistedRecord{Envelope: env, Call: frame}, func(*PersistedRef) error { return nil })
		require.ErrorContains(t, err, `unknown object codec family "nope"`)
	})
	t.Run("family must match the decoding type", func(t *testing.T) {
		env := encoding.Envelope
		env.ObjectCodec = "dagql_test.PersistSnapshotValue"
		_, err := DefaultPersistedSelfCodec.DecodeResult(ctx, srv, 0, frame, env)
		require.ErrorContains(t, err, "decodes family")
	})
	t.Run("old envelope versions are rejected", func(t *testing.T) {
		env := encoding.Envelope
		env.Version = 2
		_, err := DefaultPersistedSelfCodec.DecodeResult(ctx, srv, 0, frame, env)
		require.ErrorContains(t, err, "unsupported version 2")
	})
	t.Run("every registered family names a distinct Go type", func(t *testing.T) {
		seen := map[string]struct{}{}
		for _, family := range PersistedObjectFamilies() {
			key := fmt.Sprintf("%T", family.Typed)
			_, dup := seen[key]
			require.False(t, dup, "family %q reuses %s", family.Name, key)
			seen[key] = struct{}{}
			require.NotNil(t, family.Visitor)
		}
	})
}

type persistVisitedRef struct {
	kind PersistedRefKind
	path string
	id   uint64
	role string
	key  string
}

func TestVisitEncodedReferencesRelocatesDeclaredPositions(t *testing.T) {
	sharedCall := &ResultCall{
		Kind:   ResultCallKindField,
		Field:  "shared",
		Type:   NewResultCallType(String("").Type()),
		Module: &ResultCallModule{Name: "mod", ResultRef: &ResultCallRef{ResultID: 9}},
	}
	frame := &ResultCall{
		Kind:     ResultCallKindField,
		Field:    "root",
		Type:     &ResultCallType{Elem: &ResultCallType{NamedType: "Int", NonNull: true}, NonNull: true},
		Receiver: &ResultCallRef{ResultID: 17},
		Args: []*ResultCallArg{
			{Name: "a", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindResultRef, ResultRef: &ResultCallRef{Call: sharedCall}}},
			{Name: "b", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindList, ListItems: []*ResultCallLiteral{
				{Kind: ResultCallLiteralKindResultRef, ResultRef: &ResultCallRef{ResultID: 5, Call: sharedCall}},
				{Kind: ResultCallLiteralKindInt, IntValue: 17},
				{Kind: ResultCallLiteralKindObject, ObjectFields: []*ResultCallArg{{Name: "inner", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindString, StringValue: "42"}}}},
			}}},
		},
		ImplicitInputs: []*ResultCallArg{{Name: "impl", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindResultRef, ResultRef: &ResultCallRef{ResultID: 42}}}},
	}
	env := PersistedResultEnvelope{
		Version:  persistedResultEnvelopeVersion,
		Kind:     persistedResultKindList,
		ResultID: 42,
		Items: []PersistedResultEnvelope{
			{Version: persistedResultEnvelopeVersion, Kind: persistedResultKindRef, ResultID: 17},
			{Version: persistedResultEnvelopeVersion, Kind: persistedResultKindScalar, TypeName: "Int", ScalarJSON: json.RawMessage(`17`)},
			{Version: persistedResultEnvelopeVersion, Kind: persistedResultKindObject, TypeName: "PersistCodecObj", ObjectCodec: "dagql_test.PersistCodecObj", ObjectJSON: json.RawMessage(`{"name":"17"}`)},
			{Version: persistedResultEnvelopeVersion, Kind: persistedResultKindNull},
		},
	}
	rec := PersistedRecord{ResultID: 42, Envelope: env, Call: frame}
	originalJSON, err := json.Marshal(rec)
	require.NoError(t, err)

	// Colliding namespaces: A's 17 becomes B's 5 and A's 5 becomes B's 17.
	mapping := map[uint64]uint64{42: 901, 17: 5, 5: 17, 9: 9}
	var visited []persistVisitedRef
	visit := func(ref *PersistedRef) error {
		visited = append(visited, persistVisitedRef{kind: ref.Kind, path: ref.Path.String(), id: ref.ResultID, role: ref.Role, key: ref.RefKey})
		if ref.ResultID == 0 {
			return nil
		}
		mapped, ok := mapping[ref.ResultID]
		if !ok {
			return fmt.Errorf("no mapping for row %d at %s", ref.ResultID, ref.Path)
		}
		ref.ResultID = mapped
		return nil
	}
	out, err := VisitEncodedReferences(rec, visit)
	require.NoError(t, err)
	require.Equal(t, []persistVisitedRef{
		{kind: PersistedRefSelf, path: "", id: 42},
		{kind: PersistedRefChild, path: "items[0]", id: 17},
		{kind: PersistedRefCall, path: "call.receiver", id: 17},
		{kind: PersistedRefCall, path: "call.args[0].module", id: 9},
		{kind: PersistedRefCall, path: "call.args[1][0]", id: 5},
		{kind: PersistedRefCall, path: "call.implicitInputs[0]", id: 42},
	}, visited, "every declared position is reported exactly once with its path; the shared frame is walked once and self once")

	require.Equal(t, uint64(901), out.ResultID)
	require.Equal(t, uint64(901), out.Envelope.ResultID)
	require.Equal(t, uint64(5), out.Envelope.Items[0].ResultID)
	require.Equal(t, json.RawMessage(`17`), out.Envelope.Items[1].ScalarJSON, "scalar leaves are not references")
	require.Equal(t, json.RawMessage(`{"name":"17"}`), out.Envelope.Items[2].ObjectJSON, "string leaves are not references")
	require.Equal(t, uint64(5), out.Call.Receiver.ResultID)
	require.Equal(t, uint64(17), out.Call.Args[1].Value.ListItems[0].ResultRef.ResultID)
	require.Equal(t, int64(17), out.Call.Args[1].Value.ListItems[1].IntValue, "ordinary numbers are untouched")
	require.Equal(t, "42", out.Call.Args[1].Value.ListItems[2].ObjectFields[0].Value.StringValue)
	require.Equal(t, uint64(901), out.Call.ImplicitInputs[0].Value.ResultRef.ResultID)
	require.Same(t, out.Call.Args[0].Value.ResultRef.Call, out.Call.Args[1].Value.ListItems[0].ResultRef.Call, "shared call subgraphs stay shared")
	require.NotSame(t, sharedCall, out.Call.Args[0].Value.ResultRef.Call, "frames are fresh")
	require.Equal(t, uint64(9), out.Call.Args[0].Value.ResultRef.Call.Module.ResultRef.ResultID)

	afterJSON, err := json.Marshal(rec)
	require.NoError(t, err)
	require.JSONEq(t, string(originalJSON), string(afterJSON), "the input record is untouched")
	require.Equal(t, uint64(17), frame.Receiver.ResultID)

	t.Run("a missing mapping fails without partial writes", func(t *testing.T) {
		partial := map[uint64]uint64{42: 901, 17: 5}
		_, err := VisitEncodedReferences(rec, func(ref *PersistedRef) error {
			if ref.ResultID == 0 {
				return nil
			}
			mapped, ok := partial[ref.ResultID]
			if !ok {
				return fmt.Errorf("no mapping for row %d", ref.ResultID)
			}
			ref.ResultID = mapped
			return nil
		})
		require.ErrorContains(t, err, "no mapping for row 9")
		afterJSON, err := json.Marshal(rec)
		require.NoError(t, err)
		require.JSONEq(t, string(originalJSON), string(afterJSON))
	})
	t.Run("a mismatched envelope identity is rejected", func(t *testing.T) {
		bad := rec
		bad.ResultID = 43
		_, err := VisitEncodedReferences(bad, visit)
		require.ErrorContains(t, err, "envelope names row 42 but record is row 43")
	})
	t.Run("a root reference envelope is rejected", func(t *testing.T) {
		bad := PersistedRecord{ResultID: 17, Envelope: PersistedResultEnvelope{Version: persistedResultEnvelopeVersion, Kind: persistedResultKindRef, ResultID: 17}}
		_, err := VisitEncodedReferences(bad, visit)
		require.ErrorContains(t, err, "root envelope cannot be a result reference")
	})
}

func TestVisitEncodedReferencesClassifiesStorageRoles(t *testing.T) {
	env := PersistedResultEnvelope{
		Version:     persistedResultEnvelopeVersion,
		Kind:        persistedResultKindObject,
		TypeName:    "PersistSnapshotValue",
		ObjectCodec: "dagql_test.PersistSnapshotValue",
		ResultID:    3,
		ObjectJSON:  json.RawMessage(`{"name":"snap"}`),
	}
	rec := PersistedRecord{ResultID: 3, Envelope: env, SnapshotLinks: []PersistedSnapshotRefLink{{RefKey: "snap-a", Role: "snapshot"}}}
	var visited []persistVisitedRef
	out, err := VisitEncodedReferences(rec, func(ref *PersistedRef) error {
		visited = append(visited, persistVisitedRef{kind: ref.Kind, path: ref.Path.String(), id: ref.ResultID, role: ref.Role, key: ref.RefKey})
		if ref.Kind == PersistedRefOutputRole {
			ref.RefKey = "snap-b"
		}
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, []persistVisitedRef{
		{kind: PersistedRefSelf, id: 3},
		{kind: PersistedRefOutputRole, path: "objectJSON.snapshotLinks[0]", role: "snapshot", key: "snap-a"},
	}, visited)
	require.Equal(t, "snap-b", out.SnapshotLinks[0].RefKey)
	require.Equal(t, "snap-a", rec.SnapshotLinks[0].RefKey, "the input links are untouched")
	require.Equal(t, json.RawMessage(`{"name":"snap"}`), out.Envelope.ObjectJSON)

	t.Run("a family declaring no references rejects storage links", func(t *testing.T) {
		bad := PersistedRecord{ResultID: 3, Envelope: PersistedResultEnvelope{
			Version: persistedResultEnvelopeVersion, Kind: persistedResultKindObject, TypeName: "PersistCodecObj",
			ObjectCodec: "dagql_test.PersistCodecObj", ResultID: 3, ObjectJSON: json.RawMessage(`{"name":"x"}`),
		}, SnapshotLinks: rec.SnapshotLinks}
		_, err := VisitEncodedReferences(bad, func(*PersistedRef) error { return nil })
		require.ErrorContains(t, err, "declares no references but has 1 snapshot links")
	})
}

func TestVisitPersistedCallIDTranslatesHandlesOnly(t *testing.T) {
	handle, err := call.NewEngineResultID(17, call.NewType(String("").Type())).Encode()
	require.NoError(t, err)
	recipe, err := call.New().Append(String("").Type(), "foo").Encode()
	require.NoError(t, err)
	visit := func(ref *PersistedRef) error {
		require.Equal(t, PersistedRefChild, ref.Kind)
		require.Equal(t, "field", ref.Path.String())
		require.Equal(t, uint64(17), ref.ResultID)
		ref.ResultID = 5
		return nil
	}

	translated := handle
	changed, err := VisitPersistedCallID(visit, PersistedRefChild, PersistedRefPath{}.Field("field"), &translated)
	require.NoError(t, err)
	require.True(t, changed)
	var decoded call.ID
	require.NoError(t, decoded.Decode(translated))
	require.True(t, decoded.IsHandle())
	require.Equal(t, uint64(5), decoded.EngineResultID())
	require.Equal(t, "String", decoded.Type().NamedType(), "the exact type wrapper survives translation")

	untouched := recipe
	changed, err = VisitPersistedCallID(func(*PersistedRef) error {
		t.Fatal("recipe IDs carry no row references")
		return nil
	}, PersistedRefChild, nil, &untouched)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, recipe, untouched)
}

func TestPersistedSecondSaveThroughRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	ctx, cache, srv := persistedListTestCache(t, path)
	srv.InstallObject(NewClass(srv, ClassOpts[*persistCodecObj]{}))
	attachedInt := persistedListTestResult(t, ctx, cache, srv, "attached-int", Int(9007199254740993))
	attachedIntID, err := cache.PersistedResultID(attachedInt)
	require.NoError(t, err)
	absent := persistedListTestResult(t, ctx, cache, srv, "attached-absent", DynamicNullable{Elem: Int(0)})
	absentID, err := cache.PersistedResultID(absent)
	require.NoError(t, err)
	obj := persistedListTestResult(t, ctx, cache, srv, "attached-obj", &persistCodecObj{Name: "child"})
	objID, err := cache.PersistedResultID(obj)
	require.NoError(t, err)
	list := persistedListTestResult(t, ctx, cache, srv, "mixed-list", DynamicResultArrayOutput{
		Elem:   DynamicNullable{Elem: Int(0)},
		Values: []AnyResult{attachedInt, absent, cacheTestIntResult(cacheTestIntCall("inline"), 2), nil},
	})
	listID, err := cache.PersistedResultID(list)
	require.NoError(t, err)
	firstEncoding := persistedListTestEncoding(t, ctx, cache, list)
	require.NoError(t, cache.ReleaseSession(ctx, "test-session"))
	require.NoError(t, cache.Close(ctx))

	check := func(t *testing.T, ctx context.Context, cache *Cache, srv *Server) {
		t.Helper()
		loaded, err := cache.LoadResultByResultID(ctx, "test-session", srv, listID)
		require.NoError(t, err)
		require.Equal(t, firstEncoding, persistedListTestEncoding(t, ctx, cache, loaded), "the same bytes come back; no identity drifts")
		items := loaded.Unwrap().(DynamicResultArrayOutput).Values
		require.Len(t, items, 4)
		require.Equal(t, attachedIntID, uint64(items[0].cacheSharedResult().id))
		require.Equal(t, Typed(Int(9007199254740993)), items[0].Unwrap(), "the attached large integer is exact")
		require.Equal(t, absentID, uint64(items[1].cacheSharedResult().id))
		_, present := items[1].DerefValue()
		require.False(t, present)
		// Publication attached the detached item as its own row.
		require.NotZero(t, items[2].cacheSharedResult().id)
		require.Equal(t, Typed(Int(2)), items[2].Unwrap())
		require.Nil(t, items[3], "an inline absent item stays nil")
		child, err := cache.LoadResultByResultID(ctx, "test-session", srv, objID)
		require.NoError(t, err)
		require.Equal(t, "child", child.Unwrap().(*persistCodecObj).Name)
	}

	// Typed-read middle process: decode before the second save.
	ctx, cache, srv = persistedListTestCache(t, path)
	srv.InstallObject(NewClass(srv, ClassOpts[*persistCodecObj]{}))
	check(t, ctx, cache, srv)
	require.NoError(t, cache.ReleaseSession(ctx, "test-session"))
	require.NoError(t, cache.Close(ctx))

	// Untouched middle process: the saved envelope is copied without a read.
	ctx, cache, _ = persistedListTestCache(t, path)
	state := cache.resultsByID[sharedResultID(listID)].loadPayloadState()
	require.False(t, state.hasValue)
	require.NotNil(t, state.persistedEnvelope)
	require.NoError(t, cache.Close(ctx))

	ctx, cache, srv = persistedListTestCache(t, path)
	srv.InstallObject(NewClass(srv, ClassOpts[*persistCodecObj]{}))
	check(t, ctx, cache, srv)
}

// persistTestRoleOmittingVisitor reports every declared role except the
// first; persistTestRoleDuplicatingVisitor reports the first role twice.
type persistTestRoleOmittingVisitor struct{}

func (persistTestRoleOmittingVisitor) VisitPersistedReferences(v PersistedPayloadVisit, visit PersistedRefVisitor) (json.RawMessage, error) {
	if len(v.SnapshotLinks) > 0 {
		if err := VisitPersistedSnapshotRoles(visit, PersistedRefOutputRole, v.Path, v.SnapshotLinks[1:]); err != nil {
			return nil, err
		}
	}
	return v.Payload, nil
}

type persistTestRoleDuplicatingVisitor struct{}

func (persistTestRoleDuplicatingVisitor) VisitPersistedReferences(v PersistedPayloadVisit, visit PersistedRefVisitor) (json.RawMessage, error) {
	if err := VisitPersistedSnapshotRoles(visit, PersistedRefOutputRole, v.Path, v.SnapshotLinks); err != nil {
		return nil, err
	}
	if len(v.SnapshotLinks) > 0 {
		if err := VisitPersistedSnapshotRoles(visit, PersistedRefOutputRole, v.Path, v.SnapshotLinks[:1]); err != nil {
			return nil, err
		}
	}
	return v.Payload, nil
}

type persistRoleOmittingObj struct{}
type persistRoleDuplicatingObj struct{}

func (*persistRoleOmittingObj) Type() *ast.Type {
	return &ast.Type{NamedType: "PersistRoleOmittingObj", NonNull: true}
}

func (*persistRoleDuplicatingObj) Type() *ast.Type {
	return &ast.Type{NamedType: "PersistRoleDuplicatingObj", NonNull: true}
}

func init() {
	RegisterPersistedObjectFamily(PersistedObjectFamily{Name: "dagql_test.RoleOmitting", Typed: (*persistRoleOmittingObj)(nil), Visitor: persistTestRoleOmittingVisitor{}})
	RegisterPersistedObjectFamily(PersistedObjectFamily{Name: "dagql_test.RoleDuplicating", Typed: (*persistRoleDuplicatingObj)(nil), Visitor: persistTestRoleDuplicatingVisitor{}})
}

func TestVisitEncodedReferencesRequiresEveryRoleOnce(t *testing.T) {
	links := []PersistedSnapshotRefLink{{RefKey: "fs-a", Role: "fs"}, {RefKey: "meta-a", Role: "meta"}}
	record := func(family string) PersistedRecord {
		return PersistedRecord{ResultID: 4, Envelope: PersistedResultEnvelope{
			Version: persistedResultEnvelopeVersion, Kind: persistedResultKindObject, TypeName: "X",
			ObjectCodec: family, ResultID: 4, ObjectJSON: json.RawMessage(`{}`),
		}, SnapshotLinks: links}
	}
	noop := func(*PersistedRef) error { return nil }

	_, err := VisitEncodedReferences(record("dagql_test.RoleOmitting"), noop)
	require.ErrorContains(t, err, `storage role "fs" classified 0 times`)

	_, err = VisitEncodedReferences(record("dagql_test.RoleDuplicating"), noop)
	require.ErrorContains(t, err, `storage role "fs" classified 2 times`)

	dup := record("dagql_test.PersistSnapshotValue")
	dup.SnapshotLinks = []PersistedSnapshotRefLink{{RefKey: "a", Role: "snapshot"}, {RefKey: "b", Role: "snapshot"}}
	_, err = VisitEncodedReferences(dup, noop)
	require.ErrorContains(t, err, `storage role "snapshot" declared twice`)

	undeclared := record("dagql_test.PersistSnapshotValue")
	undeclared.SnapshotLinks = nil
	_, err = VisitEncodedReferences(undeclared, noop)
	require.NoError(t, err, "a family with no links reports no roles")

	// Only an object payload's family visitor classifies storage roles. A
	// record whose root is a null, scalar or list while it still declares
	// links would otherwise pass through with those keys unclassified and
	// unrelocated. Capture pairs links only with an object self today; the
	// visitor is also driven from stored records, so it must say so rather
	// than skip them.
	for _, kind := range []struct {
		name string
		env  PersistedResultEnvelope
	}{
		{persistedResultKindNull, PersistedResultEnvelope{Version: persistedResultEnvelopeVersion, Kind: persistedResultKindNull, ResultID: 4}},
		{persistedResultKindScalar, PersistedResultEnvelope{Version: persistedResultEnvelopeVersion, Kind: persistedResultKindScalar, TypeName: "Int", ScalarJSON: json.RawMessage(`1`)}},
		{persistedResultKindList, PersistedResultEnvelope{Version: persistedResultEnvelopeVersion, Kind: persistedResultKindList, ResultID: 4}},
	} {
		t.Run("a "+kind.name+" root cannot declare storage roles", func(t *testing.T) {
			_, err := VisitEncodedReferences(PersistedRecord{ResultID: 4, Envelope: kind.env, SnapshotLinks: links}, noop)
			require.ErrorContains(t, err, "only an object payload classifies storage roles")
		})
	}
}

func TestPersistAbsentRowValidatedAtCapture(t *testing.T) {
	ctx, cache, _ := persistedListTestCache(t, "")

	nullable := &persistResultSnapshot{resultID: 7, frame: persistCodecFrame("nullable-int", DynamicNullable{Elem: Int(0)}), hasValue: true, self: nil}
	encoding, err := cache.persistResultEnvelope(ctx, nullable)
	require.NoError(t, err)
	require.Equal(t, persistedAbsentEnvelope(7, ""), encoding.Envelope)

	unvalued := &persistResultSnapshot{resultID: 8, frame: persistCodecFrame("nullable-int", DynamicNullable{Elem: Int(0)}), hasValue: false}
	encoding, err = cache.persistResultEnvelope(ctx, unvalued)
	require.NoError(t, err)
	require.Equal(t, persistedAbsentEnvelope(8, ""), encoding.Envelope)

	nonNull := &persistResultSnapshot{resultID: 9, frame: cacheTestIntCall("non-null-int"), hasValue: true, self: nil}
	_, err = cache.persistResultEnvelope(ctx, nonNull)
	require.ErrorContains(t, err, "persist absent value of result 9")
	require.ErrorContains(t, err, "declared non-null")

	untyped := &persistResultSnapshot{resultID: 10, frame: &ResultCall{Kind: ResultCallKindSynthetic, SyntheticOp: "no-type"}, hasValue: true, self: nil}
	_, err = cache.persistResultEnvelope(ctx, untyped)
	require.ErrorContains(t, err, "persist absent value of result 10")

	// The encode path reaches the same check for a dereferenced absence.
	absent, err := NewResultForCall(DynamicNullable{Elem: Int(0)}, cacheTestIntCall("declared-non-null"))
	require.NoError(t, err)
	absent.shared.id = 11
	_, err = DefaultPersistedSelfCodec.EncodeResult(ctx, cache, absent)
	require.ErrorContains(t, err, "persist absent value of result 11")
}
