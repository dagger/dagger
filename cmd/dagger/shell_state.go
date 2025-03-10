package main

import (
	"context"
	"fmt"
	"iter"
	"maps"
	"regexp"
	"slices"
	"strings"
	"sync"

	"dagger.io/dagger"
	"dagger.io/dagger/querybuilder"
	"github.com/dagger/dagger/core/rand"
	"golang.org/x/sync/errgroup"
	"mvdan.cc/sh/v3/interp"
)

// shellStatePattern is a regular expression to match state tokens.
var shellStatePattern = regexp.MustCompile(`\{DSH:([A-Z2-7]+)\}`)

// newStateToken returns a new state token for the given key.
func newStateToken(key string) string {
	return "{DSH:" + key + "}"
}

// HasState returns true if the input string contains a state token.
func HasState(s string) bool {
	return shellStatePattern.MatchString(s)
}

// FindStateTokens returns all state tokens in the input string, if any.
func FindStateTokens(s string) []string {
	return shellStatePattern.FindAllString(s, -1)
}

// FindStateKeys returns an iterator over all state keys in the input string.
func FindStateKeys(s string) iter.Seq[string] {
	return func(yield func(string) bool) {
		for _, m := range shellStatePattern.FindAllStringSubmatch(s, -1) {
			if !yield(m[1]) {
				return
			}
		}
	}
}

// GetStateKey returns the state key from a token.
//
// If input is not exactly a token, returns an empty string.
func GetStateKey(in string) string {
	m := shellStatePattern.FindAllStringSubmatch(in, -1)
	if len(m) != 1 {
		return ""
	}
	token, key := m[0][0], m[0][1]
	if token != in {
		return ""
	}
	return key
}

// ShellStateStore manages state instances in memory concurrently.
type ShellStateStore struct {
	data   map[string]ShellState
	mu     sync.RWMutex
	runner *interp.Runner
}

func NewStateStore(runner *interp.Runner) *ShellStateStore {
	return &ShellStateStore{
		data:   make(map[string]ShellState),
		runner: runner,
	}
}

// Store saves a state instance and returns its key.
//
// This always generates a new key for immutability.
func (s *ShellStateStore) Store(st ShellState) string {
	st.Key = rand.Text()
	s.mu.Lock()
	s.data[st.Key] = st
	s.mu.Unlock()
	return st.Key
}

// Get returns a state instance by key.
func (s *ShellStateStore) Get(key string) (ShellState, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st, exists := s.data[key]
	return st, exists
}

// Delete removes a state instance by key.
//
// The state won't be deleted if in use by a variable.
func (s *ShellStateStore) Delete(key string) {
	if s.isUsed(key) {
		return
	}
	s.mu.Lock()
	delete(s.data, key)
	s.mu.Unlock()
}

// Load is like [Get] but returns an error if the key is not found or if
// the state represents an error.
func (s *ShellStateStore) Load(key string) (*ShellState, error) {
	if key == "" {
		return nil, nil
	}
	st, exists := s.Get(key)
	if !exists {
		return nil, fmt.Errorf("tried to access non-existent state %q", key)
	}
	return &st, st.Error
}

// Extract is like [Load] but also deletes the state from memory.
//
// This is expected to be used when the state is being resolved rather than
// just passed around.
func (s *ShellStateStore) Extract(key string) (*ShellState, error) {
	defer s.Delete(key)
	return s.Load(key)
}

// isUsed returns true if a state key is being used in a variable.
func (s *ShellStateStore) isUsed(key string) bool {
	if s.runner == nil {
		return false
	}
	for _, v := range s.runner.Vars {
		if strings.Contains(v.String(), newStateToken(key)) {
			return true
		}
	}
	return false
}

// Prune removes all state instances that are not in use by a variable.
//
// This can be used at the end of a run to avoid
func (s *ShellStateStore) Prune() int {
	used := make(map[string]struct{})

	if s.runner != nil {
		for _, v := range s.runner.Vars {
			for key := range FindStateKeys(v.String()) {
				used[key] = struct{}{}
			}
		}
	}

	count := 0
	s.mu.Lock()
	for key := range s.data {
		if _, exists := used[key]; !exists {
			count++
			delete(s.data, key)
		}
	}
	s.mu.Unlock()
	return count
}

func (s *ShellStateStore) debug(ctx context.Context) {
	s.mu.RLock()
	shellDebug(ctx, "State dump", slices.Collect(maps.Values(s.data)))
	s.mu.RUnlock()
}

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
	// Key is the state store key
	Key string `json:"key"`

	// ModDigest is the module source digest for the current state
	//
	// If empty, it must fall back to the default context.
	// It matches a key in the modDefs map in the handler.
	ModDigest string `json:"digest"`

	// Cmd is non-empty if next command comes from a builtin instead of an API object
	Cmd string `json:"cmd"`

	// Calls is the list of functions for building an API query
	Calls []FunctionCall `json:"calls,omitempty"`

	// Error is non-nil if the previous command failed
	Error error `json:"error,omitempty"`
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

// Save the shell state in the state store so it can be retrieved by the
// next call in the chain
func (h *shellCallHandler) Save(ctx context.Context, st ShellState) error {
	if st.Key != "" {
		// Replace instead of mutate otherwise it's harder to manage
		// when it's saved in a var.
		defer h.state.Delete(st.Key)
	}
	nkey := h.state.Store(st)
	w := interp.HandlerCtx(ctx).Stdout
	_, err := w.Write([]byte(newStateToken(nkey)))
	return err
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
func (st ShellState) WithCall(fn *modFunction, argValues map[string]any) ShellState {
	prev := st.Function()
	next := st
	next.Calls = append(next.Calls, FunctionCall{
		Object:       prev.ReturnObject,
		Name:         fn.Name,
		ReturnObject: fn.ReturnType.Name(),
		Arguments:    argValues,
	})
	return next
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

func (h *shellCallHandler) stateResolver(ctx context.Context) func([]byte) ([]byte, error) {
	return func(b []byte) ([]byte, error) {
		s := string(b)
		if !HasState(s) {
			return b, nil
		}
		r, err := h.resolveResult(ctx, s)
		if err != nil {
			return nil, err
		}
		return []byte(r), nil
	}
}

// resolveResults replaces state keys with their resolved values in any of the
// input arguments
func (h *shellCallHandler) resolveResults(ctx context.Context, args []string) ([]string, error) {
	var mu sync.Mutex

	eg, gctx := errgroup.WithContext(ctx)

	results := make([]string, len(args))

	for i, arg := range args {
		if !HasState(arg) {
			mu.Lock()
			results[i] = arg
			mu.Unlock()
			continue
		}
		eg.Go(func() error {
			r, err := h.resolveResult(gctx, arg)
			if err != nil {
				return err
			}
			mu.Lock()
			results[i] = r
			mu.Unlock()
			return nil
		})
	}

	err := eg.Wait()

	if h.debug {
		shellDebug(ctx, "resolve results", args, results)
	}

	return results, err
}

// resolveResult replaces state keys with their resolved values in the input string
func (h *shellCallHandler) resolveResult(ctx context.Context, in string) (res string, rerr error) {
	matches := shellStatePattern.FindAllStringSubmatch(in, -1)
	if len(matches) == 0 {
		return in, nil
	}

	var mu sync.Mutex

	eg, gctx := errgroup.WithContext(ctx)
	for i, match := range matches {
		eg.Go(func() error {
			r, err := h.resolveState(gctx, match[1])
			if err != nil {
				return err
			}
			mu.Lock()
			matches[i][1] = r
			mu.Unlock()

			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return in, err
	}

	return strings.NewReplacer(slices.Concat(matches...)...).Replace(in), nil
}

// resolveState returns the resolved value of a state instance given its key
func (h *shellCallHandler) resolveState(ctx context.Context, key string) (string, error) {
	st, err := h.state.Extract(key)
	if err != nil {
		return "", err
	}
	r, err := h.StateResult(ctx, st)
	if err != nil {
		return "", err
	}
	return r.String()
}

func (h *shellCallHandler) newModState(dig string) ShellState {
	return ShellState{
		ModDigest: dig,
	}
}

func (h *shellCallHandler) NewState() ShellState {
	return ShellState{}
}

func (h *shellCallHandler) NewStdlibState() ShellState {
	return ShellState{
		Cmd: shellStdlibCmdName,
	}
}

func (h *shellCallHandler) NewCoreState() ShellState {
	return ShellState{
		Cmd: shellCoreCmdName,
	}
}

func (h *shellCallHandler) NewDepsState() ShellState {
	return ShellState{
		Cmd: shellDepsCmdName,
	}
}
