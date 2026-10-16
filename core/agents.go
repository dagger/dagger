package core

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/engine/slog"
)

// WarnToolNameCollisions tells people reading the trace which toolsets are
// served under namespaced names. The warning is human-only: it would reach a
// model as ANSI-coloured log noise in the result of a tool returning this
// conversation, which gets a plain-text note instead (see
// summarizeToolNameCollisions).
func (llm *LLM) WarnToolNameCollisions(ctx context.Context) {
	if llm == nil || llm.mcp == nil {
		return
	}
	collisions, err := llm.mcp.ToolNameCollisions(ctx)
	if err != nil || len(collisions) == 0 {
		return
	}
	warnToolNameCollisions(ctx, collisions)
}

func warnToolNameCollisions(ctx context.Context, collisions map[string][]string) {
	logger := slog.SpanLogger(ctx, InstrumentationLibrary, log.Bool(telemetryattrs.LogHumanOnlyAttr, true))
	for _, name := range slices.Sorted(maps.Keys(collisions)) {
		contributors := collisions[name]
		served := make([]string, len(contributors))
		for i, typeName := range contributors {
			served[i] = namespacedToolName(typeName, name)
		}
		logger.Warn("agent tool name collision: serving the conflicting toolsets under namespaced names",
			"tool", name, "contributors", contributors, "served", served)
	}
}

// Expertise retains a conversation-transforming function without evaluating it.
// The conversation and, when requested, its owning Agent are supplied at run time.
type Expertise struct{ Artifact *Artifact }

func (*Expertise) Type() *ast.Type         { return &ast.Type{NamedType: "Expertise", NonNull: true} }
func (*Expertise) TypeDescription() string { return "A function that can modify a conversation." }

func expertiseFunction(artifact *Artifact) *Function {
	if artifact.Node != nil && artifact.Node.Parent != nil {
		if obj := artifact.Node.Parent.ObjectType(); obj != nil {
			if fn, ok := obj.FunctionByName(artifact.Node.Name); ok {
				return fn
			}
		}
	}
	return nil
}

// ConversationArgument returns the name of the LLM! argument of an artifact
// that transforms a conversation (whether or not it is nominated with @agent),
// or "" for any other artifact. Such an artifact is discoverable without
// arguments because composition supplies the conversation.
func ConversationArgument(artifact *Artifact) string {
	fn := expertiseFunction(artifact)
	if fn == nil || !isExpertiseFunction(fn) {
		return ""
	}
	for _, arg := range fn.Args {
		if isCoreLLMArg(arg.Self()) {
			return arg.Self().Name
		}
	}
	return ""
}

func NewExpertise(artifact *Artifact) (*Expertise, error) {
	fn := expertiseFunction(artifact)
	if fn == nil || !isExpertiseFunction(fn) || artifact.Node.OriginalModule.Self() == nil {
		uri, err := artifact.URI(ArtifactURIOpts{DimensionKeys: true})
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("%s is not a source of expertise: expected one LLM! argument, only auto-filled or optional additional arguments, and an LLM! return", uri)
	}
	return &Expertise{Artifact: artifact.Clone()}, nil
}
func (a *Expertise) Clone() *Expertise       { return &Expertise{Artifact: a.Artifact.Clone()} }
func (a *Expertise) Name() string            { return a.Artifact.Node.CommandName() }
func (a *Expertise) Description() string     { return a.Artifact.Node.Description }
func (a *Expertise) Path() []string          { return a.Artifact.Node.Path() }
func (a *Expertise) OriginalModule() *Module { return a.Artifact.Node.OriginalModule.Self() }

// ModuleName is the composition owner of the entry's own contributions: the
// name its module is installed under, which is also the current module of the
// entry's function call (see CompositionOwner). Refreshing an entry clears
// and reruns its whole module.
func (a *Expertise) ModuleName() string { return CompositionOwner(a.OriginalModule()) }

// CompositionOwner is the owner recorded on the system prompts, tool bindings
// and skills that mod's code contributes to a conversation, and the key that
// composition clears to refresh mod's entries. It is the name mod is installed
// under, so an aliased install owns its contributions under its alias. Empty
// (unowned) without a module, e.g. for the main client.
func CompositionOwner(mod *Module) string {
	if mod == nil {
		return ""
	}
	return mod.Name()
}

// Identity intentionally excludes the implementation and workspace revision.
func (a *Expertise) Identity() (string, error) {
	return a.Artifact.URI(ArtifactURIOpts{DimensionKeys: true})
}

// requiresAgent reports whether the entry has a required Agent argument.
// Composed into a conversation without an owning agent, such an entry is
// skipped.
func (a *Expertise) requiresAgent() bool {
	fn := expertiseFunction(a.Artifact)
	if fn == nil {
		return false
	}
	for _, arg := range fn.Args {
		if isCoreAgentArg(arg.Self()) && !arg.Self().TypeDef.Self().Optional {
			return true
		}
	}
	return false
}

// skipsUnowned reports whether entry is skipped when composed into base: it
// requires an Agent and base has no owning agent to supply.
func skipsUnowned(base dagql.ObjectResult[*LLM], entry dagql.ObjectResult[*Expertise]) bool {
	return base.Self().OwnerAgentHandle == "" && entry.Self().requiresAgent()
}

// expertiseSkippedAttr lists, on the composition's span, the entries skipped
// because they require an Agent and the conversation has no owning agent.
// Skipping is expected (e.g. installing an agent's expertise into a plain
// conversation), so it is recorded as span state rather than a warning.
const expertiseSkippedAttr = "dagger.io/expertise.skipped"

func recordSkippedExpertise(ctx context.Context, skipped []string) {
	if len(skipped) == 0 {
		return
	}
	trace.SpanFromContext(ctx).SetAttributes(attribute.StringSlice(expertiseSkippedAttr, skipped))
}

func (a *Expertise) Run(ctx context.Context, base dagql.ObjectResult[*LLM]) (dagql.ObjectResult[*LLM], error) {
	var result dagql.ObjectResult[*LLM]
	ctx, err := a.Artifact.WorkspaceContext(ctx)
	if err != nil {
		return result, err
	}
	id, err := base.ID()
	if err != nil {
		return result, err
	}
	fn := expertiseFunction(a.Artifact)
	if fn == nil || !isExpertiseFunction(fn) {
		return result, fmt.Errorf("invalid expertise %q", a.Name())
	}
	var inputs []dagql.NamedInput
	for _, arg := range fn.Args {
		if isCoreLLMArg(arg.Self()) {
			inputs = append(inputs, dagql.NamedInput{Name: arg.Self().Name, Value: dagql.NewID[*LLM](id)})
		}
		if isCoreAgentArg(arg.Self()) {
			// Every Agent argument receives the agent that owns the
			// conversation, never the caller. A handle-only reference avoids
			// embedding the seed in its own recipe.
			optional := arg.Self().TypeDef.Self().Optional
			if base.Self().OwnerAgentHandle == "" {
				if optional {
					continue
				}
				// Composition skips such entries before running them.
				return result, fmt.Errorf("expertise %q requires an owning agent for argument %q", a.Name(), arg.Self().Name)
			}
			owner, err := AgentReference(ctx, base.Self().OwnerAgentHandle)
			if err != nil {
				return result, fmt.Errorf("expertise %q owning agent: %w", a.Name(), err)
			}
			ownerID, err := owner.ID()
			if err != nil {
				return result, err
			}
			var value dagql.Input = dagql.NewID[*Agent](ownerID)
			if optional {
				value = dagql.Opt(dagql.NewID[*Agent](ownerID))
			}
			inputs = append(inputs, dagql.NamedInput{Name: arg.Self().Name, Value: value})
		}
	}
	if err := a.Artifact.Evaluate(ctx, &result, inputs...); err != nil {
		return result, err
	}
	// An entry may build a fresh conversation. Keep the owner before the next
	// entry runs, not just when spawn finalizes the seed.
	return WithLLMOwnerAgent(ctx, result, base.Self().OwnerAgentHandle)
}

func (a *Expertise) EncodePersistedObject(ctx context.Context, enc *dagql.PersistEncodeContext) (dagql.PersistedObjectEncoding, error) {
	return a.Artifact.EncodePersistedObject(ctx, enc)
}
func (*Expertise) DecodePersistedObject(ctx context.Context, dec *dagql.PersistDecodeContext, raw json.RawMessage) (dagql.Typed, error) {
	artifact, err := (*Artifact)(nil).DecodePersistedObject(ctx, dec, raw)
	if err != nil {
		return nil, err
	}
	return &Expertise{Artifact: artifact.(*Artifact)}, nil
}
func (a *Expertise) AttachDependencyResults(ctx context.Context, owner dagql.AnyResult, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	return a.Artifact.AttachDependencyResults(ctx, owner, attach)
}

// ComposeExpertise runs the given entries and records them on the
// conversation. Contributions are owned by the module whose code makes them
// (see CompositionOwner), so an entry is refreshed with its whole module:
//
//   - A module is refreshed when any of its given entries is already recorded
//     under the same identity. Its contributions are cleared once, before any
//     entry runs; then all of its recorded entries run again in record order,
//     a given entry replacing its recorded one in place. Compatible tool state
//     is carried over (see preserveRecomposedTools).
//   - Entries not yet recorded then run in the given order and are appended to
//     the record. A new entry alone does not refresh its module.
//   - Other recorded entries and their contributions are untouched.
//
// A dependency's system prompts and skill directories, made inside a function
// of another module the entry calls, are re-stamped as the entry's module's
// once the entry returns (see claimDependencyContributions), so a refresh
// replaces them rather than adding them again. Tool bindings keep their
// dependency owner: they are kept one per type, so a refresh rebinds rather
// than duplicates them, and a binding shared by several modules' entries must
// not change hands. Contributions of entries composed by a nested composition
// stay theirs.
//
// An entry requiring an Agent is skipped on a conversation without an owning
// agent: a new one neither runs nor is recorded. A module with such a recorded
// entry cannot run again in full, so it is not refreshed: its record and
// contributions are kept, and only its new, runnable entries run.
//
// While its entries run, the conversation is marked with the identities being
// composed (LLM.ComposingExpertise). A composition nested in a running entry
// sees the mark and never clears or refreshes anything: it runs only the given
// entries that are neither recorded nor being composed, and records them, so
// the outer composition keeps them instead of running them again. An entry
// composing entries of its own module therefore cannot refresh that module
// recursively.
//
// No entries is a no-op.
func ComposeExpertise(ctx context.Context, base dagql.ObjectResult[*LLM], expertise dagql.ObjectResultArray[*Expertise]) (dagql.ObjectResult[*LLM], error) {
	if len(expertise) == 0 {
		return base, nil
	}
	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return base, err
	}
	plan, err := planComposition(base, expertise)
	defer func() { recordSkippedExpertise(ctx, plan.skipped) }()
	if err != nil {
		return base, err
	}
	if len(plan.run) == 0 {
		return base, nil
	}
	record, composing := plan.record, plan.composing
	outer := base.Self().ComposingExpertise

	// Entries are recorded once they have run, not while they run: an entry
	// that spawns from its base must not hand the child a record of
	// contributions the child does not have.
	acc, err := recordExpertise(ctx, base, base.Self().Expertise, composing)
	if err != nil {
		return base, err
	}
	for _, mod := range plan.refresh {
		if acc, err = withoutComposition(ctx, srv, acc, mod); err != nil {
			return base, err
		}
	}
	for _, entry := range plan.run {
		next, err := entry.Self().Run(ctx, acc)
		if err != nil {
			return base, fmt.Errorf("compose agent %q: %w", entry.Self().Name(), err)
		}
		// A dependency's contributions, made while the entry ran, are the
		// entry's module's to refresh.
		if next, err = claimDependencyContributions(ctx, srv, acc, next, entry.Self()); err != nil {
			return base, fmt.Errorf("compose agent %q: %w", entry.Self().Name(), err)
		}
		// Keep the entries a nested composition recorded.
		if record, err = mergeExpertiseRecord(record, next.Self().Expertise); err != nil {
			return base, err
		}
		acc = next
		if !slices.Equal(acc.Self().ComposingExpertise, composing) {
			// The entry returned a conversation without the mark, e.g. one it
			// built itself: mark it again for the entries that follow.
			if acc, err = recordExpertise(ctx, acc, acc.Self().Expertise, composing); err != nil {
				return base, err
			}
		}
	}
	if len(plan.refresh) > 0 {
		if acc, err = preserveRecomposedTools(ctx, srv, base, acc); err != nil {
			return base, err
		}
	}
	// A nested composition leaves the outer composition's mark in place.
	if acc, err = recordExpertise(ctx, acc, record, outer); err != nil {
		return base, err
	}
	acc.Self().WarnToolNameCollisions(ctx)
	return acc, nil
}

// compositionPlan is what ComposeExpertise does, decided before any entry
// runs.
type compositionPlan struct {
	record    dagql.ObjectResultArray[*Expertise] // the record once the entries have run
	run       dagql.ObjectResultArray[*Expertise] // entries to run, in order
	refresh   []string                            // modules to clear before running
	composing []string                            // the mark while entries run
	skipped   []string                            // given entries skipped for want of an owner
}

// planComposition decides which entries ComposeExpertise runs and which
// modules it refreshes (see ComposeExpertise).
func planComposition(base dagql.ObjectResult[*LLM], expertise dagql.ObjectResultArray[*Expertise]) (compositionPlan, error) {
	var plan compositionPlan
	outer := base.Self().ComposingExpertise

	record := slices.Clone(base.Self().Expertise)
	keys := make([]string, len(record))
	position := map[string]int{}
	for i, entry := range record {
		key, err := entry.Self().Identity()
		if err != nil {
			return plan, err
		}
		keys[i] = key
		position[key] = i
	}
	// Each identity is considered once, at its first position in the list, as
	// the last entry given for it.
	var order []string
	given := map[string]dagql.ObjectResult[*Expertise]{}
	for _, entry := range expertise {
		key, err := entry.Self().Identity()
		if err != nil {
			return plan, err
		}
		if _, seen := given[key]; !seen {
			order = append(order, key)
		}
		given[key] = entry
		if skipsUnowned(base, entry) && !slices.Contains(plan.skipped, key) {
			plan.skipped = append(plan.skipped, key)
		}
	}

	if len(outer) == 0 {
		// A nested composition never refreshes.
		plan.refresh = refreshedModules(base, record, keys, given)
	}
	for i, entry := range record {
		if !slices.Contains(plan.refresh, entry.Self().ModuleName()) {
			continue
		}
		if next, ok := given[keys[i]]; ok {
			record[i] = next
		}
		plan.run = append(plan.run, record[i])
	}
	for _, key := range order {
		entry := given[key]
		if _, recorded := position[key]; recorded || slices.Contains(outer, key) || skipsUnowned(base, entry) {
			// A recorded entry was refreshed with its module above, or is kept.
			// One being composed is run by the outer composition.
			continue
		}
		position[key] = len(record)
		record = append(record, entry)
		plan.run = append(plan.run, entry)
	}
	plan.record = record
	plan.composing = slices.Clone(outer)
	for _, entry := range plan.run {
		key, err := entry.Self().Identity()
		if err != nil {
			return plan, err
		}
		plan.composing = append(plan.composing, key)
	}
	return plan, nil
}

// refreshedModules returns, in record order, the modules a top-level
// composition refreshes: those with a given entry already recorded, unless a
// recorded entry of theirs (as replaced by a given one) would be skipped.
func refreshedModules(base dagql.ObjectResult[*LLM], record dagql.ObjectResultArray[*Expertise], keys []string, given map[string]dagql.ObjectResult[*Expertise]) []string {
	candidate := map[string]bool{}
	for i := range record {
		if _, ok := given[keys[i]]; ok {
			candidate[record[i].Self().ModuleName()] = true
		}
	}
	blocked := map[string]bool{}
	for i, entry := range record {
		if next, ok := given[keys[i]]; ok {
			entry = next
		}
		if skipsUnowned(base, entry) {
			blocked[record[i].Self().ModuleName()] = true
		}
	}
	var refresh []string
	for _, entry := range record {
		mod := entry.Self().ModuleName()
		if candidate[mod] && !blocked[mod] && !slices.Contains(refresh, mod) {
			refresh = append(refresh, mod)
		}
	}
	return refresh
}

// claimDependencyContributions re-stamps, as entry's module's own, the system
// prompts and skill directories entry added with another owner: those a
// dependency's function made while the entry ran. Without this, refreshing the
// entry's module would clear only its own contributions and rerun the entry,
// adding the dependency's contributions again beside the old ones.
//
// Only additions are claimed: what after holds beyond a prefix equal to
// before. An entry that rewrote what it was given (or built a new
// conversation) claims nothing. Contributions owned by the module of an entry a
// nested composition recorded while this one ran are that entry's, which its
// own module refreshes. Tool bindings are never claimed (see
// ComposeExpertise).
//
// The claim is a recorded selector, so replay reproduces it.
func claimDependencyContributions(ctx context.Context, srv *dagql.Server, before, after dagql.ObjectResult[*LLM], entry *Expertise) (dagql.ObjectResult[*LLM], error) {
	owner := entry.ModuleName()
	if owner == "" || before.Self() == nil || after.Self() == nil {
		return after, nil
	}
	recorded := map[string]bool{}
	for _, e := range before.Self().Expertise {
		key, err := e.Self().Identity()
		if err != nil {
			return after, err
		}
		recorded[key] = true
	}
	nested := map[string]bool{}
	for _, e := range after.Self().Expertise {
		key, err := e.Self().Identity()
		if err != nil {
			return after, err
		}
		if !recorded[key] {
			nested[e.Self().ModuleName()] = true
		}
	}
	claims := func(o string) bool { return o != owner && !nested[o] }

	prev, next := before.Self(), after.Self()
	var messages dagql.ArrayInput[dagql.Int]
	if len(next.Messages) > len(prev.Messages) && slices.EqualFunc(prev.Messages, next.Messages[:len(prev.Messages)], sameContribution) {
		for i := len(prev.Messages); i < len(next.Messages); i++ {
			if msg := next.Messages[i]; msg.Role == LLMMessageRoleSystem && claims(msg.CompositionOwner) {
				messages = append(messages, dagql.Int(i))
			}
		}
	}
	var skills dagql.ArrayInput[dagql.Int]
	if prev.mcp != nil && next.mcp != nil {
		prevDirs, nextDirs := prev.mcp.skillDirs, next.mcp.skillDirs
		if len(nextDirs) > len(prevDirs) && slices.EqualFunc(prevDirs, nextDirs[:len(prevDirs)], sameSkillDirectory) {
			for i := len(prevDirs); i < len(nextDirs); i++ {
				if claims(nextDirs[i].Owner) {
					skills = append(skills, dagql.Int(i))
				}
			}
		}
	}
	if len(messages) == 0 && len(skills) == 0 {
		return after, nil
	}
	args := []dagql.NamedInput{{Name: "owner", Value: dagql.String(owner)}}
	if len(messages) > 0 {
		args = append(args, dagql.NamedInput{Name: "messages", Value: messages})
	}
	if len(skills) > 0 {
		args = append(args, dagql.NamedInput{Name: "skills", Value: skills})
	}
	err := srv.Select(ctx, after, &after, dagql.Selector{Field: "__withContributionOwner", Args: args})
	return after, err
}

// sameContribution compares messages by value, including their owner.
func sameContribution(a, b *LLMMessage) bool {
	return messagesEqual(a, b) && (a == nil || a.CompositionOwner == b.CompositionOwner)
}

func sameSkillDirectory(a, b ownedSkillDirectory) bool {
	if a.Owner != b.Owner {
		return false
	}
	aID, aErr := a.Directory.ID()
	bID, bErr := b.Directory.ID()
	return aErr == nil && bErr == nil && stableIDDigest(aID) == stableIDDigest(bID)
}

// mergeExpertiseRecord appends the entries of from not already in record.
func mergeExpertiseRecord(record, from dagql.ObjectResultArray[*Expertise]) (dagql.ObjectResultArray[*Expertise], error) {
	if len(from) == 0 {
		return record, nil
	}
	seen := map[string]bool{}
	for _, entry := range record {
		key, err := entry.Self().Identity()
		if err != nil {
			return record, err
		}
		seen[key] = true
	}
	for _, entry := range from {
		key, err := entry.Self().Identity()
		if err != nil {
			return record, err
		}
		if !seen[key] {
			seen[key] = true
			record = append(record, entry)
		}
	}
	return record, nil
}

// recordExpertise sets the conversation's record and the identities of the
// entries being composed on it (none outside a composition), as one selector
// so checkpoint and portable replay retain both, even when an entry returned
// a separately built conversation.
func recordExpertise(ctx context.Context, llm dagql.ObjectResult[*LLM], entries dagql.ObjectResultArray[*Expertise], composing []string) (dagql.ObjectResult[*LLM], error) {
	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return llm, err
	}
	ids := make(dagql.ArrayInput[dagql.ID[*Expertise]], len(entries))
	for i, entry := range entries {
		id, err := entry.ID()
		if err != nil {
			return llm, err
		}
		ids[i] = dagql.NewID[*Expertise](id)
	}
	args := []dagql.NamedInput{{Name: "expertise", Value: ids}}
	if len(composing) > 0 {
		args = append(args, dagql.NamedInput{Name: "composing", Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(composing...))})
	}
	err = srv.Select(ctx, llm, &llm, dagql.Selector{Field: "__withExpertise", Args: args})
	return llm, err
}

// WithoutComposingMark returns llm without the mark of a running composition
// (see ComposeExpertise), for a conversation that starts anew from it, e.g. a
// spawned agent's: its own composition is not nested in the one running.
func WithoutComposingMark(ctx context.Context, llm dagql.ObjectResult[*LLM]) (dagql.ObjectResult[*LLM], error) {
	if len(llm.Self().ComposingExpertise) == 0 {
		return llm, nil
	}
	return recordExpertise(ctx, llm, llm.Self().Expertise, nil)
}
