package core

import (
	"fmt"
	"sync"

	"github.com/dagger/dagger/dagql/call"
)

// SchemaBuildStats counts this engine's module schema builds since it
// started, to tell how many repeat work already done: in the same session, or
// in another session. A module set is the modules a schema installs (by
// result) with their install options, the identity the description cache
// shares descriptions by (schemaEntriesKey).
type SchemaBuildStats struct {
	// Servers are schema servers built (buildSchema).
	Servers int64
	// NoSession are servers built with no session memo in the context.
	NoSession int64
	// Unkeyed are servers whose module set has no stable identity, so a
	// repeat cannot be told from a first build.
	Unkeyed int64
	// RepeatedInSession are servers for a module set this session had built a
	// server for. A server is bound to one client's root: RepeatedOnOtherRoot
	// are those on a root that had no server for the set, RepeatedOnSameRoot
	// those on a root that had one. RepeatedIdentical, a part of
	// RepeatedOnSameRoot, also had the same module instances and install
	// options as that earlier build: the same server, built again.
	RepeatedInSession   int64
	RepeatedOnOtherRoot int64
	RepeatedOnSameRoot  int64
	RepeatedIdentical   int64
	// RepeatedAcrossSessions are servers for a module set another session
	// had built, but this one had not.
	RepeatedAcrossSessions int64
	// Descriptions are the GraphQL descriptions (a schema's syntax tree and
	// digest, for one view) built through a shared description cache.
	// DescriptionsRepeated are those for a module set and view one had been
	// built for before.
	Descriptions         int64
	DescriptionsRepeated int64
	// ModuleSets are the distinct module sets seen. Untracked counts builds
	// not classified because a record reached its limit.
	ModuleSets int
	Untracked  int64
}

// maxTrackedModuleSets bounds each record schemaBuilds and a session keep:
// one entry per module set (and view, or root), a few hundred bytes each.
const maxTrackedModuleSets = 20000

var schemaBuilds = struct {
	sync.Mutex
	stats SchemaBuildStats
	// built holds module sets some session built a server for; described,
	// module sets and views some session built a description for.
	built     map[string]struct{}
	described map[string]struct{}
}{
	built:     map[string]struct{}{},
	described: map[string]struct{}{},
}

// SchemaBuilds returns the engine's schema build counts so far.
func SchemaBuilds() SchemaBuildStats {
	schemaBuilds.Lock()
	defer schemaBuilds.Unlock()
	stats := schemaBuilds.stats
	stats.ModuleSets = len(schemaBuilds.built)
	return stats
}

// sessionBuilds is what one session built for one module set: the roots it
// built servers on, and each server's root and module instances.
type sessionBuilds struct {
	roots   map[string]struct{}
	servers map[builtOn]struct{}
}

type builtOn struct {
	root    string
	modules string
}

// countSchemaBuild classifies one buildSchema of mods on root for the session
// whose memo is m.
func (m *SchemaBuilderMemo) countSchemaBuild(root *Query, mods []modInstall) {
	key, keyed := modInstallsKey(mods)
	var inSession, sameRoot, identical, untracked bool
	if m != nil && keyed {
		entries := make([]modDepEntry, len(mods))
		for i, entry := range mods {
			entries[i] = modDepEntry(entry)
		}
		modules, _ := schemaEntriesKey(entries, false)
		this := builtOn{root: fmt.Sprintf("%p", root), modules: modules}
		m.mu.Lock()
		built := m.builtModuleSets[key]
		inSession = built != nil
		if built != nil {
			_, sameRoot = built.roots[this.root]
			_, identical = built.servers[this]
		}
		switch {
		case identical:
		case m.builtServers >= maxTrackedModuleSets:
			// Not recorded, so a later build can't be told a repeat of it.
			untracked = true
		default:
			if built == nil {
				if m.builtModuleSets == nil {
					m.builtModuleSets = map[string]*sessionBuilds{}
				}
				built = &sessionBuilds{roots: map[string]struct{}{}, servers: map[builtOn]struct{}{}}
				m.builtModuleSets[key] = built
			}
			built.roots[this.root] = struct{}{}
			built.servers[this] = struct{}{}
			m.builtServers++
		}
		m.mu.Unlock()
	}

	schemaBuilds.Lock()
	defer schemaBuilds.Unlock()
	stats := &schemaBuilds.stats
	stats.Servers++
	switch {
	case m == nil:
		stats.NoSession++
	case !keyed:
		stats.Unkeyed++
	case untracked:
		stats.Untracked++
	case inSession:
		stats.RepeatedInSession++
		switch {
		case !sameRoot:
			stats.RepeatedOnOtherRoot++
		case identical:
			stats.RepeatedOnSameRoot++
			stats.RepeatedIdentical++
		default:
			stats.RepeatedOnSameRoot++
		}
	default:
		if _, ok := schemaBuilds.built[key]; ok {
			stats.RepeatedAcrossSessions++
		} else if len(schemaBuilds.built) >= maxTrackedModuleSets {
			stats.Untracked++
		} else {
			schemaBuilds.built[key] = struct{}{}
		}
	}
}

// countDescription counts a description cache for the module set key building
// its description for view.
func countDescription(key string, view call.View) {
	described := fmt.Sprintf("%d:%s%s", len(key), key, view)
	schemaBuilds.Lock()
	defer schemaBuilds.Unlock()
	schemaBuilds.stats.Descriptions++
	if _, ok := schemaBuilds.described[described]; ok {
		schemaBuilds.stats.DescriptionsRepeated++
	} else if len(schemaBuilds.described) < maxTrackedModuleSets {
		schemaBuilds.described[described] = struct{}{}
	} else {
		schemaBuilds.stats.Untracked++
	}
}

func modInstallsKey(mods []modInstall) (string, bool) {
	entries := make([]modDepEntry, len(mods))
	for i, entry := range mods {
		entries[i] = modDepEntry(entry)
	}
	return schemaEntriesKey(entries, true)
}
