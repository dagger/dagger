package dagger

import (
	"testing"
)

func TestValueFinalize(t *testing.T) {
	cc := &Compiler{}
	root, err := cc.Compile("test.cue",
		`
	#FetchContainer: {
		do: "fetch-container"
		ref: string
		tag: string | *"latest"
	}

	good: {
		do: "fetch-container"
		ref: "scratch"
	}

	missing: {
		do: "fetch-container"
		// missing ref
	}

	unfinished: {
		do: "fetch-container"
		ref: string // unfinished but present: should pass validation
	}

	forbidden: {
		do: "fetch-container"
		foo: "bar" // forbidden field
	}
	`)
	if err != nil {
		t.Fatal(err)
	}
	spec := root.Get("#FetchContainer")
	if _, err := root.Get("good").Finalize(spec); err != nil {
		// Should not fail
		t.Errorf("'good': validation should not fail. err=%q", err)
	}
	if _, err := root.Get("missing").Finalize(spec); err != nil {
		// SHOULD NOT fail
		// NOTE: this behavior may change in the future.
		t.Errorf("'missing': validation should fail")
	}
	if _, err := root.Get("forbidden").Finalize(spec); err == nil {
		// SHOULD fail
		t.Errorf("'forbidden': validation should fail")
	}
	if _, err := root.Get("unfinished").Finalize(spec); err != nil {
		// Should not fail
		t.Errorf("'unfinished': validation should not fail. err=%q", err)
	}
}

// Test that a non-existing field is detected correctly
func TestFieldNotExist(t *testing.T) {
	cc := &Compiler{}
	root, err := cc.Compile("test.cue", `foo: "bar"`)
	if err != nil {
		t.Fatal(err)
	}
	if v := root.Get("foo"); !v.Exists() {
		// value should exist
		t.Fatal(v)
	}
	if v := root.Get("bar"); v.Exists() {
		// value should NOT exist
		t.Fatal(v)
	}
}

// Test that a non-existing definition is detected correctly
func TestDefNotExist(t *testing.T) {
	cc := &Compiler{}
	root, err := cc.Compile("test.cue", `foo: #bla: "bar"`)
	if err != nil {
		t.Fatal(err)
	}
	if v := root.Get("foo.#bla"); !v.Exists() {
		// value should exist
		t.Fatal(v)
	}
	if v := root.Get("foo.#nope"); v.Exists() {
		// value should NOT exist
		t.Fatal(v)
	}
}

func TestSimple(t *testing.T) {
	cc := &Compiler{}
	_, err := cc.EmptyStruct()
	if err != nil {
		t.Fatal(err)
	}
}

func TestJSON(t *testing.T) {
	cc := &Compiler{}
	v, err := cc.Compile("", `foo: hello: "world"`)
	if err != nil {
		t.Fatal(err)
	}
	b1 := v.JSON()
	if string(b1) != `{"foo":{"hello":"world"}}` {
		t.Fatal(b1)
	}
	// Reproduce a bug where Value.Get().JSON() ignores Get()
	b2 := v.Get("foo").JSON()
	if string(b2) != `{"hello":"world"}` {
		t.Fatal(b2)
	}
}

func TestCompileSimpleScript(t *testing.T) {
	cc := &Compiler{}
	_, err := cc.CompileScript("simple.cue", `[{do: "local", dir: "."}]`)
	if err != nil {
		t.Fatal(err)
	}
}
