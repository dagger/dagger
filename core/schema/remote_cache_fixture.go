package schema

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/vektah/gqlparser/v2/ast"
)

const remoteCacheFixtureGate = core.RemoteCacheFixtureRootEnv

type remoteCacheFixtureArgs struct {
	Operation string
	Path      string
	IDs       dagql.ArrayInput[dagql.AnyID]
}
type remoteCacheFixtureMapping struct {
	Ordinal  dagql.TransferOrdinal `json:"ordinal"`
	ResultID uint64                `json:"resultID"`
	Type     *dagql.ResultCallType `json:"type"`
	Handle   string                `json:"handle"`
}
type remoteCacheBodyEntry struct {
	Parent   string `json:"parent"`
	Function string `json:"function"`
	Receiver uint64 `json:"receiver"`
	Client   string `json:"client"`
}
type remoteCacheBodyCount struct {
	remoteCacheBodyEntry
	Count uint64 `json:"count"`
}
type remoteCacheFixtureReport struct {
	dagql.TransferFixtureReport
	Bodies      []remoteCacheBodyCount             `json:"bodies"`
	Persistence core.RemoteCacheFixturePersistence `json:"persistence"`
}

func installRemoteCacheFixture(srv *dagql.Server) error {
	if query, ok := srv.ObjectType("Query"); ok {
		if _, installed := query.FieldSpec("_remoteCacheFixture", srv.View); installed {
			return nil
		}
	}
	path := os.Getenv(remoteCacheFixtureGate)
	if path == "" {
		return nil
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("%s must be an existing absolute directory", remoteCacheFixtureGate)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("%s: %w", remoteCacheFixtureGate, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", remoteCacheFixtureGate)
	}
	dagql.Fields[*core.Query]{dagql.Func("_remoteCacheFixture", func(ctx context.Context, q *core.Query, args remoteCacheFixtureArgs) (core.JSON, error) {
		return runRemoteCacheFixture(ctx, q, path, args)
	}).Args(dagql.Arg("path").Default(dagql.String("")), dagql.Arg("ids").Default(dagql.ArrayInput[dagql.AnyID]{})).View(AllVersion).DoNotCache("Test fixture reads and mutates external state")}.Install(srv)
	return nil
}

func fixtureBundlePath(path string) error {
	if path == "" || filepath.IsAbs(path) || strings.ContainsRune(path, 0) {
		return fmt.Errorf("invalid fixture bundle path")
	}
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == ".." || part == "." || part == "" {
			return fmt.Errorf("invalid fixture bundle path component")
		}
	}
	return nil
}
func fixtureRoot(path, subdir string) (*os.Root, error) {
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if err := root.MkdirAll(subdir, 0700); err != nil {
		return nil, err
	}
	return root.OpenRoot(subdir)
}
func writeFixtureJSON(ctx context.Context, root *os.Root, path string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err := context.Cause(ctx); err != nil {
		return err
	}
	if err := root.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(path), ".tmp-"+identity.NewID())
	file, err := root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(tmp)
	_, writeErr := file.Write(raw)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := context.Cause(ctx); err != nil {
		return err
	}
	return root.Rename(tmp, path)
}
func readFixtureJSON(root *os.Root, path string, value any) error {
	file, err := root.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	dec := json.NewDecoder(file)
	dec.DisallowUnknownFields()
	if err := dec.Decode(value); err != nil {
		return err
	}
	if err := dec.Decode(new(json.RawMessage)); err != io.EOF {
		return fmt.Errorf("fixture file has trailing content")
	}
	return nil
}
func fixtureType(typ *dagql.ResultCallType, depth int) (*ast.Type, error) {
	if typ == nil || depth > 128 || (typ.Elem == nil) == (typ.NamedType == "") {
		return nil, fmt.Errorf("invalid fixture handle type")
	}
	out := &ast.Type{NamedType: typ.NamedType, NonNull: typ.NonNull}
	if typ.Elem != nil {
		var err error
		out.Elem, err = fixtureType(typ.Elem, depth+1)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
func fixtureMappings(bundle dagql.ValueBundle, values []dagql.ImportedValue) ([]remoteCacheFixtureMapping, error) {
	types := make(map[dagql.TransferOrdinal]*dagql.ResultCallType, len(bundle.Values))
	for _, value := range bundle.Values {
		if value.Record.Call == nil {
			return nil, fmt.Errorf("missing fixture frame")
		}
		types[value.Ordinal] = value.Record.Call.Type
	}
	out := make([]remoteCacheFixtureMapping, 0, len(values))
	for _, value := range values {
		typ, err := fixtureType(types[value.Ordinal], 0)
		if err != nil {
			return nil, err
		}
		handle, err := call.NewEngineResultID(value.ResultID, call.NewType(typ)).Encode()
		if err != nil {
			return nil, err
		}
		out = append(out, remoteCacheFixtureMapping{Ordinal: value.Ordinal, ResultID: value.ResultID, Type: types[value.Ordinal], Handle: handle})
	}
	return out, nil
}
func readFixtureBodies(root *os.Root) ([]remoteCacheBodyCount, error) {
	dir, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	counts := map[remoteCacheBodyEntry]uint64{}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".tmp-") {
			continue
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			return nil, fmt.Errorf("invalid committed body record %q", entry.Name())
		}
		var body remoteCacheBodyEntry
		if err := readFixtureJSON(root, entry.Name(), &body); err != nil {
			return nil, err
		}
		if body.Parent == "" || body.Function == "" || body.Client == "" {
			return nil, fmt.Errorf("invalid committed body entry")
		}
		counts[body]++
	}
	out := make([]remoteCacheBodyCount, 0, len(counts))
	for entry, count := range counts {
		out = append(out, remoteCacheBodyCount{entry, count})
	}
	slices.SortFunc(out, func(a, b remoteCacheBodyCount) int {
		aa, _ := json.Marshal(a)
		bb, _ := json.Marshal(b)
		return bytes.Compare(aa, bb)
	})
	return out, nil
}

//nolint:gocyclo // one phase per fixture scenario kind; splitting hides the order of the phases
func runRemoteCacheFixture(ctx context.Context, q *core.Query, path string, args remoteCacheFixtureArgs) (core.JSON, error) {
	switch args.Operation {
	case "export":
		if len(args.IDs) == 0 {
			return nil, fmt.Errorf("export requires handles")
		}
		if err := fixtureBundlePath(args.Path); err != nil {
			return nil, err
		}
	case "import":
		if len(args.IDs) != 0 {
			return nil, fmt.Errorf("import does not accept IDs")
		}
		if err := fixtureBundlePath(args.Path); err != nil {
			return nil, err
		}
	case "report":
		if args.Path != "" {
			return nil, fmt.Errorf("report does not accept a path")
		}
	case "recordBody":
		if args.Path != "" || len(args.IDs) != 0 {
			return nil, fmt.Errorf("recordBody does not accept path or IDs")
		}
	default:
		return nil, fmt.Errorf("unknown fixture operation %q", args.Operation)
	}
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return nil, err
	}
	md, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]*call.ID, len(args.IDs))
	for i, arg := range args.IDs {
		ids[i], err = arg.ID()
		if err != nil {
			return nil, err
		}
	}
	var response any
	switch args.Operation {
	case "export":
		err = cache.WithTransferFixtureRoots(ctx, md.SessionID, ids, func(roots []dagql.AnyResult) error {
			return cache.WithExportedValues(ctx, dagql.ValueSelection{Roots: roots}, config.RefConfig{}, func(ctx context.Context, values *dagql.ExportedValues) error {
				mapping, err := fixtureMappings(values.Bundle, values.Sources)
				if err != nil {
					return err
				}
				root, err := fixtureRoot(path, "bundles")
				if err != nil {
					return err
				}
				defer root.Close()
				if err := writeFixtureJSON(ctx, root, args.Path, values.Bundle); err != nil {
					return err
				}
				response = mapping
				return nil
			})
		})
	case "import":
		root, openErr := fixtureRoot(path, "bundles")
		if openErr != nil {
			return nil, openErr
		}
		defer root.Close()
		var bundle dagql.ValueBundle
		if err := readFixtureJSON(root, args.Path, &bundle); err != nil {
			return nil, err
		}
		// Prepare recursive types before import's commit point.
		for _, value := range bundle.Values {
			if value.Record.Call == nil {
				return nil, fmt.Errorf("missing fixture frame")
			}
			if _, err := fixtureType(value.Record.Call.Type, 0); err != nil {
				return nil, err
			}
		}
		var values []dagql.ImportedValue
		values, err = cache.ImportValues(ctx, bundle)
		if err == nil {
			response, err = fixtureMappings(bundle, values)
		}
	case "report":
		var report remoteCacheFixtureReport
		report.Persistence.PersistenceResetReason = cache.PersistenceResetReason()
		fixture, openErr := os.OpenRoot(path)
		if openErr != nil {
			return nil, openErr
		}
		defer fixture.Close()
		if err := readFixtureJSON(fixture, "persistence.json", &report.Persistence); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		report.TransferFixtureReport, err = cache.TransferFixtureSnapshot(ctx, md.SessionID, ids)
		if err != nil {
			return nil, err
		}
		root, openErr := fixtureRoot(path, "body-entries")
		if openErr != nil {
			return nil, openErr
		}
		defer root.Close()
		report.Bodies, err = readFixtureBodies(root)
		response = report
	case "recordBody":
		fn, callErr := q.CurrentFunctionCall(ctx)
		if callErr != nil {
			return nil, callErr
		}
		if fn == nil || fn.Name == "" || fn.ParentName == "" {
			return nil, fmt.Errorf("recordBody requires a current function call")
		}
		entry := remoteCacheBodyEntry{Parent: fn.ParentName, Function: fn.Name, Client: md.ClientID}
		if parent := fn.ParentTyped(); parent != nil {
			entry.Receiver, err = cache.PersistedResultID(parent)
			if err != nil {
				return nil, err
			}
		}
		root, openErr := fixtureRoot(path, "body-entries")
		if openErr != nil {
			return nil, openErr
		}
		defer root.Close()
		err = writeFixtureJSON(ctx, root, identity.NewID()+".json", entry)
		response = entry
	}
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(response)
	return core.JSON(raw), err
}
