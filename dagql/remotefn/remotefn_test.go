package remotefn

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

type Color string

func (c Color) EnumValues() []string {
	return []string{"Red", "Green", "Blue"}
}

type Nested struct {
	Foo string
	Bar int
}

type InputStruct struct {
	A   bool
	B   int
	C   float64
	D   string
	E   time.Time
	Ptr *int
	Arr []string
	Map map[string]int
	Nst Nested
	Col Color
}

func processFn(in InputStruct) (string, error) {
	return fmt.Sprintf("OK: %v, %v, %v, %v, T=%v, Ptr=%v, Arr=%v, Map=%v, Nested=%v, Col=%s",
		in.A, in.B, in.C, in.D, in.E.Format(time.RFC3339),
		valOrNil(in.Ptr),
		in.Arr, in.Map, in.Nst, in.Col), nil
}

func valOrNil(p *int) interface{} {
	if p == nil {
		return nil
	}
	return *p
}

func TestRemoteFn(t *testing.T) {
	schema, err := FnSchema(processFn)
	if err != nil {
		t.Fatalf("FnSchema error: %v", err)
	}
	t.Logf("Schema:\n%s\n", schema)

	ptrVal := 999
	in := InputStruct{
		A:   true,
		B:   42,
		C:   3.14,
		D:   "hello",
		E:   time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC),
		Ptr: &ptrVal,
		Arr: []string{"x", "y"},
		Map: map[string]int{"k1": 10, "k2": 20},
		Nst: Nested{Foo: "foo", Bar: 999},
		Col: "Green",
	}

	args, err := encodeJSON(in)
	if err != nil {
		t.Fatalf("JSON encode error: %v", err)
	}

	res, callErr := FnCall(context.Background(), processFn, args)
	if callErr != nil {
		t.Fatalf("FnCall error: %v", callErr)
	}
	t.Logf("Result: %s", res)
}

func encodeJSON(v interface{}) ([]byte, error) {
	return remotefnJSONMarshal(v)
}

// For demonstration, just use standard library
func remotefnJSONMarshal(v interface{}) ([]byte, error) {
	return json.Marshal(v)
}

// If needed, import "encoding/json" yourself.
