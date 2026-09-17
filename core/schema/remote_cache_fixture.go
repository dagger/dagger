package schema

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/fixturetransport"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
	"github.com/vektah/gqlparser/v2/ast"
)

const remoteCacheFixtureGate = core.RemoteCacheFixtureRootEnv

type remoteCacheFixtureArgs struct {
	Operation string
	Path      string
	IDs       dagql.ArrayInput[dagql.AnyID]
	OutputIDs dagql.ArrayInput[dagql.AnyID] `name:"outputIDs"`
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
	// Transport is what the in-process dispatcher saw of requests to the
	// fixture's own hosts, and how many other requests it delegated.
	Transport *fixturetransport.Report `json:"transport,omitempty"`
	// Storage is read from the engine's real stores; absent where the
	// fixture runs without an engine server.
	Storage *core.RemoteCacheFixtureStorage `json:"storage,omitempty"`
	// Renewal is what the fixture's consumer loop did; absent likewise.
	Renewal *core.RemoteCacheFixtureRenewals `json:"renewal,omitempty"`
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
	}).Args(dagql.Arg("path").Default(dagql.String("")), dagql.Arg("ids").Default(dagql.ArrayInput[dagql.AnyID]{}), dagql.Arg("outputIDs").Default(dagql.ArrayInput[dagql.AnyID]{})).View(AllVersion).DoNotCache("Test fixture reads and mutates external state")}.Install(srv)
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

// ImportValues returns roots, after reserving one contiguous ID interval for
// the closure and relocating ordinal n to firstID+n-1. The gated fixture also
// reports dependency rows so observations can name exact imported operations.
// Keep roots first for existing fixture callers that select the first root.
func fixtureImportedMappings(bundle dagql.ValueBundle, roots []dagql.ImportedValue, rows []dagql.TransferFixtureRow) ([]remoteCacheFixtureMapping, error) {
	if len(roots) == 0 || roots[0].ResultID < uint64(roots[0].Ordinal) {
		return nil, fmt.Errorf("missing fixture import allocation")
	}
	base := roots[0].ResultID - uint64(roots[0].Ordinal)
	seen := map[dagql.TransferOrdinal]bool{}
	values := append([]dagql.ImportedValue(nil), roots...)
	for _, root := range roots {
		if root.ResultID != base+uint64(root.Ordinal) {
			return nil, fmt.Errorf("inconsistent fixture import allocation")
		}
		seen[root.Ordinal] = true
	}
	byID := make(map[uint64]dagql.TransferFixtureRow, len(rows))
	for _, row := range rows {
		byID[row.ResultID] = row
	}
	hasNonRoot, validatedNonRoot := false, false
	for _, value := range bundle.Values {
		if !seen[value.Ordinal] {
			hasNonRoot = true
			id := base + uint64(value.Ordinal)
			if row, ok := byID[id]; ok {
				if !row.Imported || row.Call == nil || value.Record.Call == nil || row.Call.Field != value.Record.Call.Field || !reflect.DeepEqual(row.Call.Type, value.Record.Call.Type) {
					return nil, fmt.Errorf("fixture non-root allocation mismatch at ordinal %d", value.Ordinal)
				}
				if ref := value.Record.Call.Receiver; ref != nil && ref.ResultID != 0 {
					if row.Call.Receiver == nil || row.Call.Receiver.ResultID != base+ref.ResultID {
						return nil, fmt.Errorf("fixture non-root receiver mismatch at ordinal %d", value.Ordinal)
					}
				}
				validatedNonRoot = true
			}
			values = append(values, dagql.ImportedValue{Ordinal: value.Ordinal, ResultID: id})
		}
	}
	if hasNonRoot && !validatedNonRoot {
		return nil, fmt.Errorf("fixture import allocation lacks a reported non-root row")
	}
	return fixtureMappings(bundle, values)
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
	if err := validateFixtureOperation(args); err != nil {
		return nil, err
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
	cache.EnableTransferFixtureParts()
	cache.SetPartContentSource(fixturePartContentSource{path: path})
	outputIDs := make([]*call.ID, len(args.OutputIDs))
	for i, arg := range args.OutputIDs {
		outputIDs[i], err = arg.ID()
		if err != nil {
			return nil, err
		}
	}
	var response any
	switch args.Operation {
	case "export":
		err = cache.WithTransferFixtureRoots(ctx, md.SessionID, ids, func(roots []dagql.AnyResult) error {
			return cache.WithTransferFixtureRoots(ctx, md.SessionID, outputIDs, func(outputs []dagql.AnyResult) error {
				selection := dagql.ValueSelection{Roots: roots}
				for _, output := range outputs {
					typ := output.Type().Name()
					part := dagql.PartKey("snapshot")
					switch typ {
					case "Container":
						part = core.ContainerPartFS
					case "Directory", "File":
					default:
						return fmt.Errorf("selected fixture output must be Directory, File or Container")
					}
					selection.Outputs = append(selection.Outputs, dagql.SelectedValueOutput{Result: output, Address: dagql.PersistedPartAddress{Part: part}})
				}
				return cache.WithExportedValues(ctx, selection, config.RefConfig{Compression: compression.New(compression.Uncompressed)}, func(ctx context.Context, values *dagql.ExportedValues) error {
					if err := writeFixtureChains(ctx, path, values.Chains); err != nil {
						return err
					}
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
			var report dagql.TransferFixtureReport
			report, err = cache.TransferFixtureSnapshot(ctx, md.SessionID, nil)
			if err == nil {
				response, err = fixtureImportedMappings(bundle, values, report.Rows)
			}
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
		if dispatcher := fixturetransport.Current(); dispatcher != nil && err == nil {
			transport := dispatcher.Report()
			if transport.Overflowed {
				err = fmt.Errorf("%w: transport", dagql.ErrTransferFixtureOverflow)
			}
			report.Transport = &transport
		}
		if controls, controlsErr := fixtureControls(q); controlsErr == nil && err == nil {
			var storage core.RemoteCacheFixtureStorage
			if storage, err = controls.RemoteCacheFixtureStorage(ctx); err == nil {
				report.Storage = &storage
			}
			if renewals, renewalErr := controls.RemoteCacheFixtureRenewals(); renewalErr == nil {
				report.Renewal = &renewals
			}
		}
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
	default:
		response, err = runFixtureControl(ctx, q, cache, md.SessionID, path, args, ids)
	}
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(response)
	return core.JSON(raw), err
}
