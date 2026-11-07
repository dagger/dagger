package core

import (
	"context"
	"crypto/rand"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/stretchr/testify/require"
)

// keyedInstallMod is a ctxInstallMod whose module set identity is key, so no
// other test's module set shares it.
type keyedInstallMod struct {
	ctxInstallMod
	key string
}

func (m *keyedInstallMod) SchemaASTCacheKey() string { return m.key }

// The counts say which schema builds repeat a module set: in the same session,
// and then why, in another session, or with no session to tell.
func TestSchemaBuildsCountRepeats(t *testing.T) {
	key := rand.Text()
	root := &Query{}
	deps := NewSchemaBuilder(root, []Mod{&keyedInstallMod{key: key}})
	client := func(session *SchemaBuilderMemo) context.Context {
		return ContextWithQuery(t.Context(), &Query{Server: &memoTestServer{mockServer: &mockServer{}, client: NewSchemaBuilderMemo(), session: session}})
	}
	session := NewSchemaBuilderMemo()
	build := func(ctx context.Context, deps *SchemaBuilder) *dagql.Server {
		srv, err := deps.Schema(ctx)
		require.NoError(t, err)
		return srv
	}
	delta := func(before SchemaBuildStats) SchemaBuildStats {
		after := SchemaBuilds()
		return SchemaBuildStats{
			Servers:                after.Servers - before.Servers,
			NoSession:              after.NoSession - before.NoSession,
			Unkeyed:                after.Unkeyed - before.Unkeyed,
			RepeatedInSession:      after.RepeatedInSession - before.RepeatedInSession,
			RepeatedOnOtherRoot:    after.RepeatedOnOtherRoot - before.RepeatedOnOtherRoot,
			RepeatedOnSameRoot:     after.RepeatedOnSameRoot - before.RepeatedOnSameRoot,
			RepeatedIdentical:      after.RepeatedIdentical - before.RepeatedIdentical,
			RepeatedAcrossSessions: after.RepeatedAcrossSessions - before.RepeatedAcrossSessions,
		}
	}

	before := SchemaBuilds()
	srv := build(client(session), deps)
	build(client(session), deps) // the session's memo has it: no build
	require.Equal(t, SchemaBuildStats{Servers: 1}, delta(before), "a first build")

	before = SchemaBuilds()
	build(client(session), deps.WithRoot(&Query{}))
	require.Equal(t, SchemaBuildStats{Servers: 1, RepeatedInSession: 1, RepeatedOnOtherRoot: 1}, delta(before),
		"the same modules on another root")

	before = SchemaBuilds()
	build(client(session), NewSchemaBuilder(root, []Mod{&keyedInstallMod{key: key}}))
	require.Equal(t, SchemaBuildStats{Servers: 1, RepeatedInSession: 1, RepeatedOnSameRoot: 1}, delta(before),
		"the same modules on the same root, under another builder identity")

	before = SchemaBuilds()
	session.Get(deps).loadSchemaFailed.Store(true)
	build(client(session), deps)
	require.Equal(t, SchemaBuildStats{Servers: 1, RepeatedInSession: 1, RepeatedOnSameRoot: 1, RepeatedIdentical: 1}, delta(before),
		"the same server again, its memoized builder having failed")

	// A main module's builder also builds the schema without it as main: the
	// server built before for the same modules.
	before = SchemaBuilds()
	mod := deps.entries[0].mod
	build(client(session), deps.With(mod, InstallOpts{Main: true}))
	require.Equal(t, SchemaBuildStats{Servers: 2, RepeatedInSession: 1, RepeatedOnSameRoot: 1, RepeatedIdentical: 1}, delta(before),
		"the same modules as main: the inner server again, the outer one new")

	before = SchemaBuilds()
	build(client(NewSchemaBuilderMemo()), deps)
	require.Equal(t, SchemaBuildStats{Servers: 1, RepeatedAcrossSessions: 1}, delta(before),
		"the same modules in another session")

	before = SchemaBuilds()
	build(t.Context(), deps)
	require.Equal(t, SchemaBuildStats{Servers: 1, NoSession: 1}, delta(before), "no session")

	// Once a session's record is full, builds it can't record go untracked,
	// not counted as first builds on their root.
	before = SchemaBuilds()
	session.builtServers = maxTrackedModuleSets
	build(client(session), deps.WithRoot(&Query{}))
	build(client(session), deps.WithRoot(&Query{}))
	after := SchemaBuilds()
	require.Equal(t, int64(2), after.Untracked-before.Untracked)
	require.Equal(t, SchemaBuildStats{Servers: 2}, delta(before))

	asts := dagql.SchemaASTBuilds()
	srv.Schema()
	srv.Schema()
	require.Equal(t, asts+1, dagql.SchemaASTBuilds(), "a server describes its schema once per view")
}

// A description built for a module set and view another session described
// counts as a repeat; servers of one session share theirs.
func TestSchemaDescriptionsCountRepeats(t *testing.T) {
	f := newModuleOwnershipTest(t)
	ctx := f.session("schema-description-counts")
	mods := []modInstall{{mod: NewUserMod(f.module(ctx, "fixture", moduleOwnershipModuleOpts{}))}}
	describe := func(memo *SchemaBuilderMemo, view call.View) {
		srv, err := dagql.NewServer(t.Context(), &Query{})
		require.NoError(t, err)
		srv.UseSchemaASTCache(memo.schemaASTCache(mods))
		srv.SchemaForView(view)
	}

	before := SchemaBuilds()
	session := NewSchemaBuilderMemo()
	describe(session, "")
	describe(session, "") // the session's other server shares it
	describe(NewSchemaBuilderMemo(), "")
	describe(NewSchemaBuilderMemo(), "v-other")
	after := SchemaBuilds()
	require.Equal(t, int64(3), after.Descriptions-before.Descriptions)
	require.Equal(t, int64(1), after.DescriptionsRepeated-before.DescriptionsRepeated)
}
