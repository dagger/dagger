package core

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/koron-go/prefixw"
	"github.com/opencontainers/go-digest"
)

// LLMObjects maps between dagql object IDs and LLM-friendly references
// like "Container#3".
type LLMObjects struct {
	byLLMID  map[string]*call.ID                   // "Foo#1" -> ID
	byType   map[string]map[digest.Digest]*call.ID // "Foo" -> {digest: ID}
	byDigest map[digest.Digest]string              // digest -> "Foo#1"
	mu       sync.Mutex
}

func NewLLMObjects() *LLMObjects {
	return &LLMObjects{
		byLLMID:  map[string]*call.ID{},
		byType:   map[string]map[digest.Digest]*call.ID{},
		byDigest: map[digest.Digest]string{},
	}
}

// Track adds an object, returning its LLM-friendly ID.
func (r *LLMObjects) Track(obj dagql.AnyObjectResult, desc string) string {
	id := obj.ID()
	if id == nil {
		return ""
	}
	hash := id.Digest()
	return r.TrackByDigest(id, desc, hash)
}

// TrackByDigest adds an object ID keyed by a specific digest.
func (r *LLMObjects) TrackByDigest(id *call.ID, desc string, hash digest.Digest) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	typeName := id.Type().NamedType()
	llmID, ok := r.byDigest[hash]
	if !ok {
		num := r.trackObjectLocked(id)
		llmID = fmt.Sprintf("%s#%d", typeName, num)
		if desc == "" {
			desc = r.describeLocked(id)
		}
		r.byDigest[hash] = llmID
		r.byLLMID[llmID] = id
	}
	return llmID
}

func (r *LLMObjects) trackObjectLocked(id *call.ID) int {
	typeName := id.Type().NamedType()
	objs, ok := r.byType[typeName]
	if !ok {
		objs = map[digest.Digest]*call.ID{}
		r.byType[typeName] = objs
	}
	objs[id.Digest()] = id
	return len(objs)
}

// Lookup resolves an LLM-friendly key (e.g. "Container#3") to a dagql object.
func (r *LLMObjects) Lookup(ctx context.Context, srv *dagql.Server, key string, expectedType string) (dagql.AnyObjectResult, error) {
	r.mu.Lock()
	id, exists := r.byLLMID[key]
	r.mu.Unlock()
	if !exists {
		return nil, fmt.Errorf("unknown object %q", key)
	}
	res, err := srv.Load(ctx, id)
	if err != nil {
		return nil, err
	}
	if obj, ok := dagql.UnwrapAs[dagql.AnyObjectResult](res); ok {
		objType := obj.Type().Name()
		if expectedType != "" && objType != expectedType {
			return nil, fmt.Errorf("type error for %q: expected %q, got %q", key, expectedType, objType)
		}
		return obj, nil
	}
	return nil, fmt.Errorf("type error: %q exists but is not an object", key)
}

// LookupBinding resolves an LLM-friendly key to a Binding.
func (r *LLMObjects) LookupBinding(ctx context.Context, srv *dagql.Server, key string) (*Binding, bool, error) {
	r.mu.Lock()
	id, exists := r.byLLMID[key]
	r.mu.Unlock()
	if !exists {
		return nil, false, nil
	}
	res, err := srv.Load(ctx, id)
	if err != nil {
		return nil, false, err
	}
	return &Binding{
		Key:          key,
		Value:        res,
		Description:  r.DescribeID(id),
		ExpectedType: res.Type().Name(),
	}, true, nil
}

// DescribeID returns a human-readable description of the given ID.
func (r *LLMObjects) DescribeID(id *call.ID) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.describeLocked(id)
}

func (r *LLMObjects) describeLocked(id *call.ID) string {
	str := new(strings.Builder)
	if recv := id.Receiver(); recv != nil {
		if llmID, ok := r.byDigest[recv.Digest()]; ok {
			str.WriteString(llmID)
		} else {
			str.WriteString(recv.Digest().String())
		}
		str.WriteString(".")
	}
	str.WriteString(id.Field())
	if args := id.Args(); len(args) > 0 {
		str.WriteString("(")
		for i, arg := range args {
			if i > 0 {
				str.WriteString(", ")
			}
			str.WriteString(arg.Name())
			str.WriteString(": ")
			str.WriteString(r.displayLitLocked(arg.Value()))
		}
		str.WriteString(")")
	}
	return str.String()
}

func (r *LLMObjects) displayLitLocked(lit call.Literal) string {
	switch x := lit.(type) {
	case *call.LiteralID:
		if llmID, ok := r.byDigest[x.Value().Digest()]; ok {
			return llmID
		}
		return x.Value().Type().NamedType()
	case *call.LiteralList:
		list := "["
		for i, value := range x.Values() {
			if i > 0 {
				list += ","
			}
			list += r.displayLitLocked(value)
		}
		list += "]"
		return list
	case *call.LiteralObject:
		obj := "{"
		for i, arg := range x.Args() {
			if i > 0 {
				obj += ","
			}
			obj += arg.Name() + ": " + r.displayLitLocked(arg.Value())
		}
		obj += "}"
		return obj
	default:
		return lit.Display()
	}
}

// LatestOfType returns the LLM ID of the latest object of a given type.
func (r *LLMObjects) LatestOfType(typeName string) (string, bool) {
	counts := r.TypeCounts()
	count, ok := counts[typeName]
	if !ok || count == 0 {
		return "", false
	}
	return fmt.Sprintf("%s#%d", typeName, count), true
}

// TypeCounts returns a map of type name to number of tracked objects.
func (r *LLMObjects) TypeCounts() map[string]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	counts := make(map[string]int, len(r.byType))
	for k, v := range r.byType {
		counts[k] = len(v)
	}
	return counts
}

// Types returns the names of all types with tracked objects.
func (r *LLMObjects) Types() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	types := make([]string, 0, len(r.byType))
	for k := range r.byType {
		types = append(types, k)
	}
	return types
}

// Snapshot returns a copy of the current byLLMID map for later diffing.
func (r *LLMObjects) Snapshot() map[string]*call.ID {
	r.mu.Lock()
	defer r.mu.Unlock()
	return maps.Clone(r.byLLMID)
}

// NewObjects returns the LLM IDs added since a prior snapshot.
func (r *LLMObjects) NewObjects(before map[string]*call.ID) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var newObjs []string
	for obj := range r.byLLMID {
		if _, exists := before[obj]; !exists {
			newObjs = append(newObjs, obj)
		}
	}
	return newObjs
}

// IDForLLMID returns the call.ID for a given LLM-friendly ID.
func (r *LLMObjects) IDForLLMID(llmID string) (*call.ID, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id, ok := r.byLLMID[llmID]
	return id, ok
}

// HasType returns whether there are any tracked objects of the given type.
func (r *LLMObjects) HasType(typeName string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.byType[typeName]
	return ok
}

// Clone creates a deep copy.
func (r *LLMObjects) Clone() *LLMObjects {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := &LLMObjects{
		byLLMID:  maps.Clone(r.byLLMID),
		byDigest: maps.Clone(r.byDigest),
		byType:   maps.Clone(r.byType),
	}
	for t, objs := range cp.byType {
		cp.byType[t] = maps.Clone(objs)
	}
	return cp
}

// WithObject adds an object under a specific LLM ID.
func (r *LLMObjects) WithObject(llmID string, id dagql.AnyID) *LLMObjects {
	cp := r.Clone()
	cp.mu.Lock()
	defer cp.mu.Unlock()
	hash := id.ID().Digest()
	cp.trackObjectLocked(id.ID())
	cp.byDigest[hash] = llmID
	cp.byLLMID[llmID] = id.ID()
	return cp
}

// DumpState writes debug state to stderr.
func (r *LLMObjects) DumpState(tag string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	objs := map[string]string{}
	for id, obj := range r.byLLMID {
		objs[id] = obj.DisplayShort()
	}
	enc := json.NewEncoder(prefixw.New(os.Stderr, "!!! "+tag))
	enc.SetIndent("", "  ")
	enc.Encode(map[string]any{
		"by-id": objs,
	})
}

// idRegex matches LLM-friendly object IDs like "Container#3".
var idRegex = regexp.MustCompile(`^(?P<type>[A-Z]\w*)#(?P<nth>\d+)$`)

// NormalizeObjectRef takes a key and optional expected type, and normalizes
// numeric-only IDs to "Type#N" format.
func NormalizeObjectRef(key, expectedType string) string {
	if expectedType != "" {
		if onlyNum, err := strconv.Atoi(key); err == nil {
			return fmt.Sprintf("%s#%d", expectedType, onlyNum)
		}
	}
	return key
}
