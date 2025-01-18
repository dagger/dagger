package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"dagger.io/dagger"
	"dagger.io/dagger/querybuilder"
	"mvdan.cc/sh/v3/interp"
)

const (
	// shellStatePrefix is the prefix that identifies a shell state in input/output
	shellStatePrefix = "DSH:"
)

// ShellState is an intermediate representation of a query
//
// The query builder serializes to a GraphQL query but not back from it so we
// use this data structure to keep track of the command chain in order to
// make it easy to create a querybuilder.Selection from it, when needed.
//
// We could alternatively encode this in the querybuilder itself, except that
// this state also includes key pieces of information from introspection that
// make it very easy to validate and get the next function's definition.
//
// This state is passed around from the stdout of an exec handler to then next
// one's stdin. Each handler in the chain should add a corresponding FunctionCall
// to the state and write it to stdout for the next handler to read.
type ShellState struct {
	// ModRef is the module reference for the current state
	//
	// If empty, it must fall back to the default context.
	// It matches a key in the modDefs map in the handler, which comes from
	// user input, not from the API.
	ModRef string `json:"modRef"`

	// Cmd is non-empty if next command comes from a builtin instead of an API object
	Cmd string `json:"cmd"`

	// Calls is the list of functions for building an API query
	Calls []FunctionCall `json:"calls,omitempty"`

	// Error is non-nil if the previous command failed
	Error *string `json:"error,omitempty"`
}

func (st ShellState) IsError() bool {
	return st.Error != nil
}

// IsEmpty returns true if there's no function calls in the chain
func (st ShellState) IsEmpty() bool {
	return len(st.Calls) == 0
}

func (st ShellState) IsCommandRoot() bool {
	return st.IsEmpty() && st.Cmd != ""
}

func (st ShellState) IsStdlib() bool {
	return st.Cmd == shellStdlibCmdName
}

func (st ShellState) IsCore() bool {
	return st.Cmd == shellCoreCmdName
}

func (st ShellState) IsDeps() bool {
	return st.Cmd == shellDepsCmdName
}

// FunctionCall represents a querybyilder.Selection
//
// The query builder only cares about the name of the function and its arguments,
// but we also keep track of its object's name and return type to make it easy
// to get the right definition from the introspection data.
type FunctionCall struct {
	Object       string         `json:"object"`
	Name         string         `json:"name"`
	Arguments    map[string]any `json:"arguments"`
	ReturnObject string         `json:"returnObject"`
}

// Write serializes the shell state to the current exec handler's stdout
func (st ShellState) Write(ctx context.Context) error {
	return st.WriteTo(interp.HandlerCtx(ctx).Stdout)
}

func (st ShellState) WriteTo(w io.Writer) error {
	var buf bytes.Buffer

	// Encode state in base64 to avoid issues with spaces being turned into
	// multiple arguments when the result of a command subsitution.
	bEnc := base64.NewEncoder(base64.StdEncoding, &buf)
	jEnc := json.NewEncoder(bEnc)

	if err := jEnc.Encode(st); err != nil {
		return err
	}
	if err := bEnc.Close(); err != nil {
		return err
	}

	w.Write([]byte(shellStatePrefix))
	w.Write(buf.Bytes())

	return nil
}

// Function returns the last function in the chain, if not empty
func (st ShellState) Function() FunctionCall {
	if st.IsEmpty() {
		// The first call is a field under Query.
		return FunctionCall{
			ReturnObject: "Query",
		}
	}
	return st.Calls[len(st.Calls)-1]
}

// WithCall returns a new state with the given function call added to the chain
func (st ShellState) WithCall(fn *modFunction, argValues map[string]any) *ShellState {
	prev := st.Function()
	return &ShellState{
		Cmd:    st.Cmd,
		ModRef: st.ModRef,
		Calls: append(st.Calls, FunctionCall{
			Object:       prev.ReturnObject,
			Name:         fn.Name,
			ReturnObject: fn.ReturnType.Name(),
			Arguments:    argValues,
		}),
	}
}

// QueryBuilder returns a querybuilder.Selection from the shell state
func (st ShellState) QueryBuilder(dag *dagger.Client) *querybuilder.Selection {
	q := querybuilder.Query().Client(dag.GraphQLClient())
	for _, call := range st.Calls {
		q = q.Select(call.Name)
		for n, v := range call.Arguments {
			q = q.Arg(n, v)
		}
	}
	return q
}

// GetTypeDef returns the introspection definition for the return type of the last function call
func (st *ShellState) GetTypeDef(modDef *moduleDef) (*modTypeDef, error) {
	fn, err := st.GetDef(modDef)
	return fn.ReturnType, err
}

// GetDef returns the introspection definition for the last function call
func (st *ShellState) GetDef(modDef *moduleDef) (*modFunction, error) {
	if st == nil || st.IsEmpty() {
		return modDef.MainObject.AsObject.Constructor, nil
	}
	return st.Function().GetDef(modDef)
}

// GetDef returns the introspection definition for this function call
func (f FunctionCall) GetDef(modDef *moduleDef) (*modFunction, error) {
	return modDef.GetObjectFunction(f.Object, cliName(f.Name))
}

// GetNextDef returns the introspection definition for the next function call, based on
// the current return type and name of the next function
func (f FunctionCall) GetNextDef(modDef *moduleDef, name string) (*modFunction, error) {
	if f.ReturnObject == "" {
		return nil, fmt.Errorf("cannot pipe %q after %q returning a non-object type", name, f.Name)
	}
	return modDef.GetObjectFunction(f.ReturnObject, name)
}

// readShellState deserializes shell state
//
// We use an hardcoded prefix when writing and reading state to make it easy
// to detect if a given input is a shell state or not. This way we can tell
// the difference between a serialized state that failed to unmarshal and
// non-state data.
func readShellState(r io.Reader) (*ShellState, []byte, error) {
	if r == nil {
		return nil, nil, nil
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, nil, err
	}
	p := []byte(shellStatePrefix)
	if !bytes.HasPrefix(b, p) {
		return nil, b, nil
	}
	encoded := bytes.TrimPrefix(b, p)
	decoder := base64.NewDecoder(base64.StdEncoding, bytes.NewReader(encoded))
	jsonDec := json.NewDecoder(decoder)
	jsonDec.UseNumber()

	var s ShellState
	if err := jsonDec.Decode(&s); err != nil {
		return nil, b, fmt.Errorf("decode state: %w", err)
	}
	if s.IsError() {
		return &s, nil, errors.New(*s.Error)
	}
	return &s, nil, nil
}

func shellState(ctx context.Context) (*ShellState, []byte, error) {
	return readShellState(interp.HandlerCtx(ctx).Stdin)
}

func (h *shellCallHandler) newModState(ref string) *ShellState {
	return &ShellState{
		ModRef: ref,
	}
}

func (h *shellCallHandler) newStdlibState() *ShellState {
	return &ShellState{
		Cmd: shellStdlibCmdName,
	}
}

func (h *shellCallHandler) newCoreState() *ShellState {
	return &ShellState{
		Cmd: shellCoreCmdName,
	}
}

func (h *shellCallHandler) newDepsState() *ShellState {
	return &ShellState{
		Cmd: shellDepsCmdName,
	}
}

func (h *shellCallHandler) newState() *ShellState {
	return &ShellState{}
}
