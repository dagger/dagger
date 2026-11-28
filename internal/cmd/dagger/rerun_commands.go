package daggercmd

import (
	"context"
	"path"
	"slices"
	"strings"
	"sync"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"github.com/dagger/dagger/core/artifact"
	"github.com/dagger/dagger/core/dagaddress"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/slog"
	telemetry "github.com/dagger/otel-go"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
)

// rerunCommands holds the command lines that re-run this run's failed checks
// and generators, for the final report (dagui.FrontendOpts.RerunCommand).
// The report renders after the engine session is gone, so they are worked
// out while it is still up, once the items have run.
var rerunCommands = &artifactRerunCommands{}

type artifactRerunCommands struct {
	mu sync.Mutex
	// tables holds a table per command: check or generate.
	tables map[string]*rerunTable
}

// reset forgets every table, so a run names only its own items.
func (c *artifactRerunCommands) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tables = nil
}

func (c *artifactRerunCommands) set(command string, table *rerunTable) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tables == nil {
		c.tables = map[string]*rerunTable{}
	}
	c.tables[command] = table
}

// lookup returns the command line that re-runs the item address names, or
// false when no table of command's knows it.
func (c *artifactRerunCommands) lookup(command, address string) (string, bool) {
	c.mu.Lock()
	table := c.tables[command]
	c.mu.Unlock()
	if table == nil {
		return "", false
	}
	entry := table.find(address)
	if entry == nil {
		return "", false
	}
	return entry.line, true
}

// rerunTable is a listing's items, each with the line `dagger <command> -l
// --all --format=cli` prints for it.
type rerunTable struct {
	definitions artifact.Dimensions
	entries     []rerunEntry
}

// rerunEntry is one listed item: its keys, in both the type-key form and the
// canonical form of an address, and its line.
type rerunEntry struct {
	module string
	// typ is the item's type dimension (e.g. type:Check), and path the full
	// path its type key holds.
	typ, path string
	// keys are the item's collection keys, by dimension identifier.
	keys map[string]string
	// coordinates are its canonical coordinates, its name among them, but
	// its module.
	coordinates map[string]string
	line        string
}

// newRerunTable lists items of selection as `dagger <cmd> -l --all
// --format=cli` lists them, with options replayed on each line: the items
// uris select, or with no uris, all of them. Naming an item's flags reads the
// workspace's other paths either way, so its line is the one the whole
// listing prints.
func newRerunTable(ctx context.Context, dag *dagger.Client, cmd *cobra.Command, selection *core.Artifacts, uris []string, options []string) (*rerunTable, error) {
	definitions, err := artifactDimensions(ctx, dag, selection)
	if err != nil {
		return nil, err
	}
	var output []listedArtifact
	if uris == nil {
		output, _, err = listedArtifactRows(ctx, dag, selection, cmd, definitions, false, true)
		if err != nil {
			return nil, err
		}
	} else {
		rows := make([][]listedArtifact, len(uris))
		jobs := errgroup.Group{}
		jobs.SetLimit(8)
		for i, uri := range uris {
			jobs.Go(func() error {
				// An item that can't be listed keeps its address.
				rows[i], _, _ = listedArtifactRows(ctx, dag, selection.FilterURI(uri), cmd, definitions, false, true)
				return nil
			})
		}
		_ = jobs.Wait()
		output = slices.Concat(rows...)
	}
	output = uniqueArtifactList(output)
	names, err := prepareArtifactOutput(ctx, dag, cmd, output, "cli")
	if err != nil {
		return nil, err
	}
	command := artifactCLICommand(cmd)
	table := &rerunTable{definitions: definitions}
	for _, item := range output {
		// A presence flag selects a whole collection, not one item.
		if len(item.Presence) > 0 {
			continue
		}
		args, err := artifactCLIArguments(item, names, options)
		if err != nil {
			continue
		}
		entry := rerunEntry{
			module:      item.ModuleName,
			keys:        map[string]string{},
			coordinates: map[string]string{},
			line:        strings.TrimSpace(command + " " + args),
		}
		for _, key := range item.DimensionKeys {
			switch {
			case key.Dimension == artifact.ModuleDimension:
				entry.module = key.Key
			case strings.HasPrefix(key.Dimension, "type:"):
				entry.typ, entry.path = key.Dimension, key.Key
			case !artifact.IsStaticDimension(key.Dimension):
				entry.keys[key.Dimension] = key.Key
			}
		}
		for _, key := range item.Coordinates {
			if key.Dimension == artifact.ModuleDimension {
				if entry.module == "" {
					entry.module = key.Key
				}
				continue
			}
			entry.coordinates[key.Dimension] = key.Key
		}
		if entry.path == "" {
			continue
		}
		table.entries = append(table.entries, entry)
	}
	return table, nil
}

// find returns the one entry address selects, or nil when it selects none,
// or more than one, or can't be read. An address names an item by its type
// key and collection keys, or by its canonical coordinates; either way it
// must give every collection key the item has, so that it selects just the
// item.
func (t *rerunTable) find(address string) *rerunEntry {
	addr, err := dagaddress.Parse(address)
	if err != nil || len(addr.Types) > 0 {
		return nil
	}
	pairs := slices.Clone(addr.Query)
	if err := bindArtifactDimensions(pairs, t.definitions); err != nil {
		return nil
	}
	var found *rerunEntry
	for i := range t.entries {
		if !t.entries[i].selectedBy(addr.Path, pairs) {
			continue
		}
		if found != nil {
			return nil
		}
		found = &t.entries[i]
	}
	return found
}

// selectedBy reports whether the address of addrPath and pairs (bound to
// dimension identifiers) selects just e.
func (e *rerunEntry) selectedBy(addrPath string, pairs []dagaddress.Pair) bool {
	named := addrPath != ""
	if named && !rerunPathMatches(e.path, addrPath) {
		return false
	}
	keys, coordinates := map[string]bool{}, map[string]bool{}
	for _, pair := range pairs {
		if pair.Absent {
			_, hasKey := e.keys[pair.Dimension]
			_, hasCoordinate := e.coordinates[pair.Dimension]
			if hasKey || hasCoordinate || pair.Dimension == artifact.ModuleDimension && e.module != "" {
				return false
			}
			continue
		}
		if !pair.HasKey {
			return false
		}
		switch {
		case pair.Dimension == artifact.ModuleDimension:
			if e.module != pair.Key {
				return false
			}
		case strings.HasPrefix(pair.Dimension, "type:"):
			if pair.Dimension != e.typ || !rerunPathMatches(e.path, pair.Key) {
				return false
			}
			named = true
		default:
			key, hasKey := e.keys[pair.Dimension]
			hasKey = hasKey && key == pair.Key
			coordinate, hasCoordinate := e.coordinates[pair.Dimension]
			hasCoordinate = hasCoordinate && coordinate == pair.Key
			if !hasKey && !hasCoordinate {
				return false
			}
			if hasKey {
				keys[pair.Dimension] = true
			}
			if hasCoordinate {
				coordinates[pair.Dimension] = true
				named = named || pair.Dimension == artifact.NameDimension
			}
		}
	}
	if !named {
		return false
	}
	// Every collection key, in one form or the other.
	complete := func(have map[string]string, given map[string]bool, skip string) bool {
		for id := range have {
			if id != skip && !given[id] {
				return false
			}
		}
		return true
	}
	return complete(e.keys, keys, "") || (coordinates[artifact.NameDimension] && complete(e.coordinates, coordinates, artifact.NameDimension))
}

// rerunPathMatches reports whether key, an address's path or type key,
// names full: the full path ("/" first, or as is), or a suffix of it at a
// "/", as a type key may be.
func rerunPathMatches(full, key string) bool {
	if rest, ok := strings.CutPrefix(key, "/"); ok {
		return full == rest
	}
	return full == key || strings.HasSuffix(full, "/"+key)
}

// prepareCheckRerunCommands works out, for a human's report, the commands
// that re-run the failed checks of a `dagger check` run, and the generators
// that fix its failed staleness checks. Any it can't work out keep their
// quoted address in the report.
func prepareCheckRerunCommands(ctx context.Context, dag *dagger.Client, ws *core.Workspace, checks *core.Artifacts, cmd *cobra.Command, results []artifactValueResult) {
	rerunCommands.reset()
	failed := failedArtifactURIs(results)
	if len(failed) == 0 || !reportNamesRerunCommands() {
		return
	}
	ctx, span := Tracer().Start(ctx, "rerun commands", telemetry.Internal(), telemetry.Encapsulate())
	defer span.End()
	options := artifactListReplayArgs(cmd)
	table, err := newRerunTable(ctx, dag, cmd, checks, failed, options)
	if err != nil {
		slog.Debug("failed to name the checks to re-run", "error", err)
		return
	}
	rerunCommands.set("check", table)

	// `dagger generate` has no -m: it selects from the workspace alone, so
	// it can't name a generator a one-off module provides.
	if artifactLoadModuleArg(cmd) != "" {
		return
	}
	// A failed staleness check is fixed by its generator, the artifact one
	// level up its path.
	stale, err := readArtifactListPaths(ctx, dag, checks.FilterParentTypes([]string{"Generator"}))
	if err != nil {
		slog.Debug("failed to name the generators to re-run", "error", err)
		return
	}
	stalePaths := map[string]bool{}
	for _, p := range stale {
		if addr, err := dagaddress.Parse(p.URI); err == nil {
			stalePaths[addr.ArtifactPath(addr.Types...)] = true
		}
	}
	var generatorPaths []string
	for _, uri := range failed {
		if entry := table.find(uri); entry != nil && stalePaths[entry.path] {
			if parent := path.Dir(entry.path); parent != "." && !slices.Contains(generatorPaths, parent) {
				generatorPaths = append(generatorPaths, parent)
			}
		}
	}
	if len(generatorPaths) == 0 {
		return
	}
	generators, err := commandArtifactTargets(ctx, dag, generateCmd, ws.Artifacts(core.WorkspaceArtifactsOpts{Include: generatorPaths}))
	if err != nil {
		slog.Debug("failed to name the generators to re-run", "error", err)
		return
	}
	generatorTable, err := newRerunTable(ctx, dag, generateCmd, generators, nil, artifactWorkspaceOptions(options))
	if err != nil {
		slog.Debug("failed to name the generators to re-run", "error", err)
		return
	}
	rerunCommands.set("generate", generatorTable)
}

// prepareGenerateRerunCommands works out, for a human's report, the
// commands that re-run the failed generators of a `dagger generate` run.
func prepareGenerateRerunCommands(ctx context.Context, dag *dagger.Client, generators *core.Artifacts, cmd *cobra.Command, results []artifactValueResult) {
	rerunCommands.reset()
	failed := failedArtifactURIs(results)
	if len(failed) == 0 || !reportNamesRerunCommands() {
		return
	}
	ctx, span := Tracer().Start(ctx, "rerun commands", telemetry.Internal(), telemetry.Encapsulate())
	defer span.End()
	table, err := newRerunTable(ctx, dag, cmd, generators, failed, artifactListReplayArgs(cmd))
	if err != nil {
		slog.Debug("failed to name the generators to re-run", "error", err)
		return
	}
	rerunCommands.set("generate", table)
}

// reportNamesRerunCommands reports whether this run's final report asks for
// rerun commands: one the progress frontend renders (tty, report, or dots,
// whose report it renders the same way), for a human, since a report for an
// agent quotes addresses.
func reportNamesRerunCommands() bool {
	switch progress {
	case "tty", "report", "dots":
		return !idtui.RunningInAgent()
	default:
		return false
	}
}

// failedArtifactURIs returns the addresses of the results that failed. Not
// of those cancelled (e.g. by --fail-fast) before they could: one that never
// started has no row in the report to name.
func failedArtifactURIs(results []artifactValueResult) []string {
	var uris []string
	for _, result := range results {
		if result.Error != nil && result.Error.Message != context.Canceled.Error() {
			uris = append(uris, result.Artifact.URI)
		}
	}
	return uris
}

// artifactWorkspaceOptions keeps the -W option of replayed options, the one
// every artifact command shares.
func artifactWorkspaceOptions(options []string) []string {
	for i := 0; i+1 < len(options); i++ {
		if options[i] == "-W" {
			return []string{"-W", options[i+1]}
		}
	}
	return nil
}
