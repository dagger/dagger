package core

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
)

// A receiver memo owns one current plan, not a history of call imports. Large
// receivers use normal import rather than retaining unbounded plan metadata.
const maxSDKPreparedInputs = 16 * 1024
const maxSDKPreparedDependencies = 64 * 1024
const maxSDKPreparedStorage = 64 * 1024 * 1024

// Storage charges include encoded data plus conservative decoded/frame and
// map overhead. This bounds cached plans, not temporary or process heap use.
type sdkInputPlanBudget struct {
	mu   sync.Mutex
	used int
}

func (b *sdkInputPlanBudget) replace(previous, next int) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if next > maxSDKPreparedStorage-(b.used-previous) {
		return false
	}
	b.used += next - previous
	return true
}

type workspacePreparedInputs struct {
	values  map[uint64]*dagql.ResultCallRef
	summary workspaceInputSummary
}

type sdkInputPreparation struct {
	mu             sync.Mutex
	plan           *sdkInputPlan
	refusedCache   *dagql.Cache
	refusedSession string
}

type sdkInputPlan struct {
	cache       *dagql.Cache
	session     string
	base        *workspacePreparedInputs
	residual    []dagql.AnyResult
	tokens      []dagql.ResultFactsToken
	generations map[dagql.ResultFactsToken]*sdkInputFactGeneration
	storage     int
	budget      *sdkInputPlanBudget
}

type sdkInputFactGeneration struct {
	snapshot dagql.ResultFactsSnapshot
	frame    *dagql.ResultCall
	facts    []workspaceInputFact
	decoded  bool
	storage  int
}

func (p *sdkInputPreparation) importInputs(ctx context.Context, values []dagql.AnyResult) {
	reads := currentWorkspaceInputReads(ctx)
	if reads == nil || len(values) == 0 {
		return
	}
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		importSDKInputs(ctx, values)
		return
	}
	md, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		importSDKInputs(ctx, values)
		return
	}
	p.mu.Lock()
	if p.refusedCache == cache && p.refusedSession == md.SessionID {
		p.mu.Unlock()
		importSDKInputs(ctx, values)
		return
	}
	// Each cache/session gets its own admission chance. The preparation itself
	// already belongs to one immutable receiver and artifact view.
	p.refusedCache, p.refusedSession = nil, ""
	plan := p.plan
	valid := false
	if plan != nil && plan.cache == cache && plan.session == md.SessionID {
		valid, err = cache.ValidateResultFactsSnapshots(context.WithoutCancel(ctx), md.SessionID, plan.tokens)
	}
	if !valid || err != nil {
		previous := plan
		var refused bool
		plan, refused = buildSDKInputPlan(ctx, cache, md.SessionID, values, previous)
		if plan != nil {
			budgetKey := fmt.Sprintf("module-sdk-input-plan-budget:%q", md.SessionID)
			res, budgetErr := cache.GetOrInitArbitrary(ctx, md.SessionID, budgetKey, func(context.Context) (any, error) { return &sdkInputPlanBudget{}, nil })
			if budgetErr != nil {
				plan = nil
			} else {
				budget := res.Value().(*sdkInputPlanBudget)
				oldCharge := 0
				if previous != nil && previous.budget == budget {
					oldCharge = previous.storage
				}
				if budget.replace(oldCharge, plan.storage) {
					plan.budget = budget
				} else {
					plan = nil
					refused = true
				}
			}
		}
		if previous != nil && previous.budget != nil && (plan == nil || plan.budget != previous.budget) {
			previous.budget.replace(previous.storage, 0)
		}
		p.plan = plan
		if refused {
			// Deliberate admission refusal lasts for this memo's session even
			// if space later becomes available. Normal import stays current.
			p.refusedCache, p.refusedSession = cache, md.SessionID
		}
	}
	p.mu.Unlock()
	if plan == nil {
		importSDKInputs(ctx, values)
		return
	}
	if len(plan.base.values) != 0 {
		reads.addPrepared(plan.base)
	}
	// Context-sensitive inputs keep their current binding and error semantics.
	importSDKInputs(ctx, plan.residual)
}

type sdkInputPlanBuilder struct {
	ctx         context.Context
	cache       *dagql.Cache
	session     string
	previous    map[dagql.ResultFactsToken]*sdkInputFactGeneration
	byID        map[uint64]*sdkInputFactGeneration
	tokens      map[dagql.ResultFactsToken]bool
	generations map[dagql.ResultFactsToken]*sdkInputFactGeneration
	seen        map[uint64]bool
	used        map[dagql.ResultFactsToken]bool
	retained    map[dagql.ResultFactsToken]bool
	overflow    bool
	retry       bool
}

func buildSDKInputPlan(ctx context.Context, cache *dagql.Cache, session string, values []dagql.AnyResult, previous *sdkInputPlan) (*sdkInputPlan, bool) {
	if len(values) > maxSDKPreparedInputs {
		return nil, true
	}
	b := &sdkInputPlanBuilder{ctx: context.WithoutCancel(ctx), cache: cache, session: session,
		byID: map[uint64]*sdkInputFactGeneration{}, tokens: map[dagql.ResultFactsToken]bool{}, generations: map[dagql.ResultFactsToken]*sdkInputFactGeneration{}, seen: map[uint64]bool{}, retained: map[dagql.ResultFactsToken]bool{}}
	if previous != nil && previous.cache == cache && previous.session == session {
		b.previous = previous.generations
	}
	plan := &sdkInputPlan{cache: cache, session: session, base: &workspacePreparedInputs{values: map[uint64]*dagql.ResultCallRef{}}}
	for _, value := range values {
		if value == nil || value.Unwrap() == nil {
			continue
		}
		id, err := value.ID()
		if err != nil || id == nil || id.EngineResultID() == 0 {
			plan.residual = append(plan.residual, value)
			continue
		}
		ref := &dagql.ResultCallRef{ResultID: id.EngineResultID()}
		current := &workspaceInputCollector{ctx: b.ctx, cache: cache, session: session}
		visited := map[uint64]bool{}
		b.used = map[dagql.ResultFactsToken]bool{}
		if !b.closed(ref, current, visited) {
			plan.residual = append(plan.residual, value)
			continue
		}
		for id := range visited {
			b.seen[id] = true
		}
		for token := range b.used {
			b.retained[token] = true
		}
		plan.base.values[ref.ResultID] = ref
		plan.base.summary.merge(current.result)
	}
	if b.overflow {
		return nil, true
	}
	if b.retry {
		return nil, false
	}
	plan.base.summary = plan.base.summary.normalized()
	if len(plan.base.summary.reads) != 0 || len(plan.base.summary.findUps) != 0 || plan.base.summary.contextInputs != nil {
		return nil, false
	}
	plan.generations = map[dagql.ResultFactsToken]*sdkInputFactGeneration{}
	for token := range b.retained {
		if generation := b.generations[token]; generation != nil {
			plan.generations[token] = generation
		}
	}
	for token := range b.tokens {
		plan.tokens = append(plan.tokens, token)
	}
	encoded, err := json.Marshal(plan.base.summary)
	if err != nil {
		return nil, false
	}
	plan.storage = 16*len(encoded) + 256*(len(plan.base.values)+len(plan.tokens)+len(plan.residual))
	for _, generation := range plan.generations {
		plan.storage += generation.storage
	}
	if plan.storage > maxSDKPreparedStorage {
		return nil, true
	}
	// A fact or frame can change while the plan is collected. First use has the
	// same complete dependency validation as later uses.
	valid, err := cache.ValidateResultFactsSnapshots(b.ctx, session, plan.tokens)
	if err != nil || !valid {
		return nil, false
	}
	return plan, false
}

func (b *sdkInputPlanBuilder) load(ref *dagql.ResultCallRef) (*sdkInputFactGeneration, bool) {
	if ref == nil || ref.ResultID == 0 || ref.Call != nil {
		return nil, false
	}
	if generation := b.byID[ref.ResultID]; generation != nil {
		b.used[generation.snapshot.Token()] = true
		return generation, true
	}
	if len(b.byID) >= maxSDKPreparedDependencies {
		b.overflow = true
		return nil, false
	}
	snapshot, err := b.cache.ResultFactsSnapshot(b.ctx, b.session, ref, workspaceInputsNamespace)
	if err != nil || !snapshot.Available() {
		b.retry = true
		return nil, false
	}
	token := snapshot.Token()
	generation := b.previous[token]
	if generation == nil {
		frame := snapshot.Call()
		encoded, err := json.Marshal(frame)
		if err != nil {
			return nil, false
		}
		generation = &sdkInputFactGeneration{snapshot: snapshot, frame: frame, storage: 4096 + 16*len(encoded)}
	}
	if generation.frame == nil {
		b.retry = true
		return nil, false
	}
	b.byID[ref.ResultID] = generation
	b.tokens[token] = true
	b.generations[token] = generation
	b.used[token] = true
	return generation, true
}

func (g *sdkInputFactGeneration) decode() bool {
	if g.decoded {
		return true
	}
	facts := make([]workspaceInputFact, g.snapshot.Len())
	for i := range facts {
		if err := g.snapshot.Decode(i, &facts[i]); err != nil {
			return false
		}
	}
	g.facts = facts
	encoded, err := json.Marshal(facts)
	if err != nil {
		return false
	}
	g.storage += 4096 + 16*len(encoded)
	g.decoded = true
	return true
}

func (b *sdkInputPlanBuilder) noLegacy(ref *dagql.ResultCallRef) bool {
	for _, namespace := range legacyWorkspaceInputsNamespaces {
		snapshot, err := b.cache.ResultFactsSnapshot(b.ctx, b.session, ref, namespace)
		if err != nil || !snapshot.Available() {
			b.retry = true
			return false
		}
		b.tokens[snapshot.Token()] = true
		// Existing legacy diagnostics stay on the normal path.
		if snapshot.Len() != 0 {
			return false
		}
	}
	return true
}

func (b *sdkInputPlanBuilder) operation(frame *dagql.ResultCall) (string, bool) {
	var receiver string
	if frame.Receiver != nil {
		parent, ok := b.load(frame.Receiver)
		if !ok {
			return "", false
		}
		if parent.frame.Type != nil {
			receiver = parent.frame.Type.NamedType
		}
	}
	return workspaceInputOperationForReceiver(frame, receiver), true
}

// Only this closed subset of collector.ref is reusable. It never loads a
// payload, follows producer ancestry, inspects a native capability, or binds a
// read to the caller. Unsupported inputs are imported normally beside it.
func (b *sdkInputPlanBuilder) closed(ref *dagql.ResultCallRef, c *workspaceInputCollector, visited map[uint64]bool) bool {
	if ref == nil || ref.ResultID == 0 || ref.Call != nil {
		return false
	}
	if b.seen[ref.ResultID] || visited[ref.ResultID] {
		return true
	}
	visited[ref.ResultID] = true
	generation, ok := b.load(ref)
	if !ok {
		return false
	}
	frame := generation.frame
	if frame.Nth > 0 && frame.Receiver != nil {
		parent, ok := b.load(frame.Receiver)
		if !ok {
			return false
		}
		list := parent.frame
		if list.Type != nil && list.Type.Elem != nil && frame.Field == list.Field && frame.Kind == list.Kind && frame.SyntheticOp == list.SyntheticOp && reflect.DeepEqual(frame.Type, list.Type.Elem) {
			if !generation.decode() || !b.noLegacy(ref) {
				return false
			}
			if !generation.snapshot.Complete() {
				operation, ok := b.operation(frame)
				if !ok {
					return false
				}
				c.issue("list item has incomplete source origins for %s", operation)
			}
			// Same diagnostics as listItemDiagnostics, without treating copied list
			// input paths as bindings against this item.
			for _, fact := range generation.facts {
				c.listItemFactDiagnostics(fact)
			}
			return b.closed(frame.Receiver, c, visited)
		}
	}
	if frame.Module == nil && workspaceInputTransport(frame.Type) {
		return true
	}
	typeName := ""
	if frame.Type != nil {
		typeName = frame.Type.NamedType
	}
	if typeName == "Query" || typeName == "Host" {
		return true
	}
	// Native Module handling precedes source facts and can inspect ambient
	// source/client state. Never replace that handling with a cached summary.
	if typeName == "Module" {
		return false
	}
	operation, ok := b.operation(frame)
	if !ok || !generation.decode() || !b.noLegacy(ref) {
		return false
	}
	owned, captured := false, false
	for _, fact := range generation.facts {
		if fact.Operation != operation {
			continue
		}
		if len(fact.Reads) != 0 {
			return false
		}
		owned = true
		captured = captured || fact.Complete
		// With no Reads this method depends only on immutable fact data. Stored
		// FindUps and fixed ContextInputs keep their producer-owned bindings.
		c.result.merge(c.materializeReads(fact, frame))
	}
	if !owned {
		return false
	}
	if !generation.snapshot.Complete() {
		c.issue("%s input has incomplete source origins for %s", typeName, operation)
	}
	if !captured {
		c.issue("%s input has incomplete source query capture for %s", typeName, operation)
	}
	return true
}
