package cachefact

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func sampleFacts() []Fact {
	term := Term{Self: "xxh3:self", Inputs: []TermInput{
		{Digest: "xxh3:in0", Provenance: ProvenanceResult},
		{Digest: "xxh3:in1", Provenance: ProvenanceDigest},
	}}
	return []Fact{
		{Seq: 1, Body: EngineStart{EngineVersion: "v0.99.0", EngineName: "engine-a", Boot: BootRestored, RestoredResults: 3, CloudEngineID: "ce-1"}},
		{Seq: 2, Body: Class{Digests: []Digest{{Digest: "xxh3:a", Label: ""}, {Digest: "sha256:b", Label: "content"}}}},
		{Seq: 3, Body: TermFact{Self: "xxh3:self", Inputs: term.Inputs, Output: "xxh3:a"}},
		{Seq: 4, Body: Result{ID: 7, Origin: OriginRestored, Field: "withExec", TypeName: "Container", RecordType: "container", Digests: []Digest{{Digest: "xxh3:a"}}, Terms: []Term{}, CreatedAtUnixNano: 11, ExpiresAtUnix: 12, Deps: []uint64{3, 5}, Retained: true, RetentionExpiresAtUnix: 13, Unpruneable: true}},
		{Seq: 5, Body: Result{ID: 58, Origin: OriginComputed, Field: "withExec", TypeName: "Container", RecordType: "container", Digests: []Digest{{Digest: "xxh3:r", Label: LabelRecipe}, {Digest: "sha256:c", Label: "remote-cache"}}, Terms: []Term{term}, CreatedAtUnixNano: 21}},
		{Seq: 6, Body: Deps{ID: 58, Deps: []uint64{57, 12}, Complete: true}},
		{Seq: 7, Body: Identity{ID: 58, Digests: []Digest{{Digest: "sha256:d", Label: "content"}}, Term: term, TermUse: TermUseAssociated, ExpiresAtUnix: 99}},
		{Seq: 8, Body: Retention{ID: 58, Retained: true, ExpiresAtUnix: 100}},
		{Seq: 9, Body: Retention{ID: 58, Retained: false}},
		{Seq: 10, Body: Part{ID: 58, OutputPath: "", Part: "fs", State: PartStateCompleted}},
		{Seq: 11, Body: Removed{IDs: []uint64{58, 57}, Reason: RemovedSessionRelease}},
		{Seq: 12, Body: EngineAlive{DroppedFacts: 4}},
		{Seq: 13, Body: EngineStop{PersistedResults: 9, Clean: true}},
	}
}

func TestFactRoundTripEveryKind(t *testing.T) {
	t.Parallel()
	seen := map[Kind]bool{}
	for _, want := range sampleFacts() {
		data, err := json.Marshal(want)
		if err != nil {
			t.Fatalf("marshal %s: %v", want.Kind(), err)
		}
		got, err := Decode(data)
		if err != nil {
			t.Fatalf("decode %s: %v", want.Kind(), err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("round trip %s:\n got %#v\nwant %#v\njson %s", want.Kind(), got, want, data)
		}
		seen[want.Kind()] = true
	}
	for _, kind := range []Kind{KindEngineStart, KindEngineAlive, KindEngineStop, KindResult, KindClass, KindTerm, KindDeps, KindIdentity, KindRetention, KindPart, KindRemoved} {
		if !seen[kind] {
			t.Errorf("kind %s not covered", kind)
		}
	}
}

// The wire form is part of the contract with the Cloud service: a flat object
// with seq and kind first, then the body's fields under their documented names.
func TestFactWireForm(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		fact Fact
		want string
	}{
		{
			fact: Fact{Seq: 812, Body: Result{ID: 58, Origin: OriginComputed, Field: "withExec", TypeName: "Container", RecordType: "container", Digests: []Digest{{Digest: "xxh3:r", Label: LabelRecipe}}, Terms: []Term{{Self: "xxh3:s", Inputs: []TermInput{{Digest: "xxh3:i", Provenance: ProvenanceResult}}}}, CreatedAtUnixNano: 5}},
			want: `{"seq":812,"kind":"result","id":58,"origin":"computed","field":"withExec","typeName":"Container","recordType":"container","digests":[{"digest":"xxh3:r","label":"recipe"}],"terms":[{"self":"xxh3:s","inputs":[{"digest":"xxh3:i","provenance":"result"}]}],"createdAtUnixNano":5,"expiresAtUnix":0}`,
		},
		{
			fact: Fact{Seq: 813, Body: Deps{ID: 58, Deps: []uint64{57, 12}, Complete: true}},
			want: `{"seq":813,"kind":"deps","id":58,"deps":[57,12],"complete":true}`,
		},
		{
			fact: Fact{Seq: 814, Body: Retention{ID: 58, Retained: false}},
			want: `{"seq":814,"kind":"retention","id":58,"retained":false}`,
		},
		{
			fact: Fact{Seq: 815, Body: Removed{IDs: []uint64{58}, Reason: RemovedReleased}},
			want: `{"seq":815,"kind":"removed","ids":[58],"reason":"released"}`,
		},
		{
			fact: Fact{Seq: 1, Body: EngineStart{EngineVersion: "v1", EngineName: "e", Boot: BootFresh}},
			want: `{"seq":1,"kind":"engine.start","engineVersion":"v1","engineName":"e","boot":"fresh","restoredResults":0}`,
		},
	} {
		got, err := json.Marshal(tc.fact)
		if err != nil {
			t.Fatalf("marshal %s: %v", tc.fact.Kind(), err)
		}
		if string(got) != tc.want {
			t.Errorf("wire form of %s:\n got %s\nwant %s", tc.fact.Kind(), got, tc.want)
		}
	}
}

// A body field named seq or kind would collide with the header in the flat
// wire form.
func TestBodyFieldsDoNotShadowHeader(t *testing.T) {
	t.Parallel()
	for _, f := range sampleFacts() {
		typ := reflect.TypeOf(f.Body)
		for i := range typ.NumField() {
			name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
			if name == "seq" || name == "kind" {
				t.Errorf("%s field %s uses reserved JSON name %q", typ.Name(), typ.Field(i).Name, name)
			}
		}
	}
}

func TestDecodeRejectsUnknownKindAndMalformedInput(t *testing.T) {
	t.Parallel()
	if _, err := Decode([]byte(`{"seq":1,"kind":"future.kind","x":1}`)); !errors.Is(err, ErrUnknownKind) {
		t.Fatalf("unknown kind: got %v, want ErrUnknownKind", err)
	}
	if _, err := Decode([]byte(`{"seq":1}`)); !errors.Is(err, ErrUnknownKind) {
		t.Fatalf("missing kind: got %v, want ErrUnknownKind", err)
	}
	if _, err := Decode([]byte(`{"seq":1,"kind":"deps","id":"not a number"}`)); err == nil {
		t.Fatal("malformed body: got nil error")
	}
	if _, err := Decode([]byte(`not json`)); err == nil {
		t.Fatal("not json: got nil error")
	}
	if _, err := json.Marshal(Fact{Seq: 1}); err == nil {
		t.Fatal("marshal without body: got nil error")
	}
}
