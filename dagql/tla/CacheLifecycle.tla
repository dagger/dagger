-------------------------- MODULE CacheLifecycle --------------------------
(***************************************************************************)
(* A TLA+ model of the dagql cache's result lifecycle and concurrency      *)
(* kernel, as implemented in dagql/cache.go and its sibling files.         *)
(*                                                                         *)
(* WHAT THIS MODELS                                                        *)
(* One GetOrInitCall from lookup to return, and everything that can        *)
(* interleave with it:                                                     *)
(*   - cache lookup, hit and miss                                          *)
(*   - in-flight call deduplication (the Cache.ongoingCalls singleflight)      *)
(*   - result publication (initCompletedResult, both branches)             *)
(*   - ownership counting and the release cascade                          *)
(*   - the dependency-attachment barrier on the read path                  *)
(*   - session release and persisted-edge pruning                          *)
(*   - lazy evaluation (Cache.Evaluate; the ModelLazy constant)            *)
(*   - persisted decode, import, flush, restart (ModelPersistence)         *)
(*   - calls issued from inside a call executor, which the server's drain  *)
(*     neither counts nor rejects (ModelNestedCalls)                       *)
(*   - per-session operation accounting: admission against the release     *)
(*     tombstone, late refusal at the return boundary, deferred release    *)
(*     cleanup by the last exiting operation, lazy callback attempt        *)
(*     tokens, and shutdown quiescence (Cache.Close)                       *)
(*                                                                         *)
(* Configurations select scenarios: which machinery is active and which    *)
(* external events and failures can happen. They never select alternative  *)
(* implementations.                                                        *)
(*                                                                         *)
(* GRANULARITY                                                             *)
(* One atomic model action represents one Go critical section or one       *)
(* lock-free atomic transition. Races live between those actions.          *)
(*                                                                         *)
(* ABSTRACTIONS                                                            *)
(*   - Equivalence is a static partition of call identities (ClassOf).     *)
(*     The e-graph's runtime class merging (the Teach* helpers) is not     *)
(*     yet modeled; modeling it means making ClassOf mutable state. Take   *)
(*     it up when an effort changes equivalence teaching.                  *)
(*   - Lookup may miss even when a candidate exists. This over-            *)
(*     approximates candidate selection (the lowest-ID pick, TTL,          *)
(*     e-graph routing) even though session-resource filtering is now      *)
(*     modeled, and it exercises the engine's accepted                     *)
(*     duplicate-execution window (getOrInitCallInner, cache.go:4522).     *)
(*   - MODEL AXIOM, assumed and never verified here: results the cache     *)
(*     considers equivalent are interchangeable.                           *)
(*   - Session-resource gating IS modeled (the Handles constant):          *)
(*     each result carries handle (mirrors sessionResourceHandle)          *)
(*     and required (the STORED requiredSessionResources set,              *)
(*     maintained where the code maintains it: publication and             *)
(*     attachment recomputes, and the import's dependency-first            *)
(*     recompute); each session carries a bound handle                     *)
(*     set; the lookup filter is required subset-of bound.                 *)
(*     TrueRequired, RequiredExact and ReturnedGated encode intent -       *)
(*     see the PROPERTIES header for their contract provenance.            *)
(*   - Not yet modeled, each for a stated reason and none forbidden:       *)
(*     TTL/expiry and DoNotCache (candidate-selection refinements folded   *)
(*     into the lookup-miss over-approximation above; model them when an   *)
(*     effort changes their behavior); recipe-replay taint (excluded by    *)
(*     ruling G31: not an input to this effort); the arbitrary-value       *)
(*     cache (acquireSessionArbitraryLocked - the same atomic              *)
(*     record-and-count claim as the modeled result claim, under callsMu   *)
(*     with sessionMu nested; it follows the same operation-accounting     *)
(*     and deferred-release contract; Go tests carry its coverage).        *)
(*   - Not yet modeled: the lock-time registration guards in the e-graph   *)
(*     mutation helpers (TeachCallEquivalentToResult, TeachContentDigest,  *)
(*     AddExplicitDependency, WithSessionResourceHandle). Numeric result   *)
(*     IDs are engine-lifetime unique, so those guards only refuse         *)
(*     mutations for already-collected results; Go tests carry them.       *)
(*   - Every modeled cache operation has session metadata. The             *)
(*     implementation also uses a cache-wide operation count when metadata *)
(*     is absent; shutdown treats both counts identically.                 *)
(*   - ReleaseSession can fire while cache operations are active. The      *)
(*     server drain controls only whether handler-origin calls are still   *)
(*     admitted to release; executor-nested calls remain outside it. Cache *)
(*     operation accounting covers both origins.                           *)
(*                                                                         *)
(* PROPERTY-ONLY STATE                                                     *)
(* Some definitions and record fields exist only so that properties can    *)
(* judge a state; no action guard reads them. Each encodes a documented    *)
(* contract rather than code:                                              *)
(*   - DerivedOwn, the ownership recount (OwnershipExact)                  *)
(*   - the invocation ret* flags, return-time evidence (ReturnedLive,      *)
(*     ReturnedOwned, NoHalfAttachedRead, NoLaunderedServe)                *)
(*   - lookupBarrierAtSelection and refusedEpoch                           *)
(*     (NoErroredLookupSelection, RefusedOnlyAfterRelease)                 *)
(*   - res.laundered and the flushed-row verdicts dirty and ownClean       *)
(*     (NoLaunderedServe, FlushCleanCapture)                               *)
(*   - evals foreignCancel (NoStaleCancelError)                            *)
(*   - the invocation ownCancel flag, set only by the actions that model   *)
(*     that invocation's own ctx.Done arm (CancelOnlyOwn)                  *)
(*   - TrueRequired, the transitive session-resource recount, and the      *)
(*     invocation retGated flag (RequiredExact, ReturnedGated); their      *)
(*     contract is in the DerivedOwn-adjacent block and session_resources  *)
(*     .md. res.handle, res.required and each session's bound handle set   *)
(*     are real state the lookup filter reads, not property-only.          *)
(***************************************************************************)
EXTENDS Naturals, Sequences, FiniteSets, TLC

CONSTANTS
    \* --- scope of one checking run ---------------------------------------
    Sessions,           \* opaque session IDs, e.g. {s1, s2}
    Calls,              \* opaque call identities (recipe digests)
    ClassOf,            \* lookup table: which equivalence class each call
                        \* is in; results of same-class calls are
                        \* interchangeable for cache hits
    MaxInvocations,     \* how many GetOrInitCalls may be issued in total
    MaxResults,         \* bound on allocated sharedResult records
    Handles,            \* session-resource handles a run may involve, a set
                        \* of opaque values (mirrors SessionResourceHandle).
                        \* With Handles = {} the machinery is inert:
                        \* BindResource never fires, every result's handle
                        \* stays "none" and required stays {}, the lookup
                        \* filter is vacuous, and each gating field is
                        \* constant, so a configuration's distinct-state
                        \* count is unchanged by it. Handles = {h1} costs
                        \* roughly six to ten times the states; enable it
                        \* wherever the budget allows.

    \* --- external events -------------------------------------------------
    AllowRelease,       \* enable session release; off in configs that
                        \* isolate a question from release races
    DrainOnRelease,     \* model the server's handler drain. TRUE delays
                        \* release until handler-origin calls are terminal;
                        \* FALSE allows release without that courtesy.
                        \* Executor-nested calls are outside the drain in
                        \* either case. The cache's own operation count is
                        \* load-bearing for safety in both cases.
    AllowPruneCut,      \* enable the PruneCut action; off in configs that
                        \* isolate a question from prune races

    \* --- optional machinery -----------------------------------------------
    \* These scope a configuration to the machinery its question needs.
    \* Turning one off removes those actions from the state space entirely,
    \* which keeps configurations that ask unrelated questions small.
    ModelLazy,          \* lazy evaluation (Cache.Evaluate): evaluators can
                        \* spawn and the lazy actions can fire.
    MaxEvals,           \* how many Cache.Evaluate callers may be issued
    ModelPersistence,   \* persistence: imported starting graphs, undecoded
                        \* payloads, the decode arm of the read barrier, and
                        \* one flush+restart cycle. Off: the model starts
                        \* empty, never flushes, never restarts.
    ImportInit,         \* the model may START from a small imported graph
                        \* instead of an empty cache - results retained by
                        \* persisted and dependency edges, no session edges,
                        \* payloads possibly still encoded ("envelope") -
                        \* the result-level shape importPersistedState
                        \* reconstructs after a clean restart.
    ModelNestedCalls,   \* calls issued from inside a detached call executor.
                        \* They carry the same session ID and may reach the
                        \* cache after the handler drain has completed. New
                        \* operations are refused after the cache tombstone;
                        \* operations admitted earlier keep release deferred.
    ModelLateDeps,      \* explicit dependency edges added to results that
                        \* are already published (Cache.AddExplicitDependency;
                        \* the one caller today attaches generated module
                        \* types to a published module container,
                        \* core/sdk/module_typedefs.go). Off in
                        \* configurations that ask no late-dependency
                        \* question.
    \* --- failure and cancellation injection --------------------------------
    \* Each enables one nondeterministic event the environment can inject.
    \* Configurations keep injections off unless their question needs them,
    \* so that a violation always points at the mechanism under test.
    FnCanFail,          \* the executed fn may return an error
    AttachCanFail,      \* attachDependencyResults may fail
    LeaseCanFail,       \* withOperationLease may fail. Its error branch
                        \* discharges the local cancel and returns without
                        \* publishing shared state.
    ReaderCanCancel,    \* a caller's context may cancel while it waits at
                        \* the read barrier (ensurePersistedHitValueLoaded's
                        \* ctx.Done arms, cache_persistence_import.go:595,
                        \* :603). Go's select picks nondeterministically
                        \* even when the barrier is already closed, so a
                        \* canceled caller can fail on a healthy result.
                        \* Also enables the persisted-decode arms: a parked
                        \* decode joiner's own ctx.Done (DecodeJoinCancel)
                        \* and the decode leader's own context, which the
                        \* decode itself runs on (DecodeLeadCancel).
    LazyCanFail,        \* the lazy callback may fail. Failure must leave
                        \* the result retryable (lazyEvalComplete is set
                        \* only on success, evaluateOne, cache.go:3693, 3798).
    DecodeCanFail       \* decoding a persisted payload may fail. Failure
                        \* must leave the entry retryable: the decode wait
                        \* channel is cleared at finish, so the next
                        \* demand leads a fresh attempt
                        \* (finishPersistDecode in ensurePersistedHitValueLoaded,
                        \* cache_persistence_import.go:648-655). Two
                        \* failure sites: DecodeResult itself (payload
                        \* stays encoded, retried on the next demand) and
                        \* the lease sync after the decoded value was
                        \* installed (payload decoded, error latched,
                        \* persistSyncPending set so the next demand
                        \* retries just the sync; see DecodeLeadFinish).

\* Config sanity check, evaluated once at startup: the ClassOf table must
\* assign a class to every call and to nothing else.
ASSUME /\ DOMAIN ClassOf = Calls
       /\ Calls # {}

\* Convenience value for configs: every call in one equivalence class.
OneClass == [c \in Calls |-> "k1"]
DistinctClasses == [c \in Calls |-> c]
EquivalenceClasses == {ClassOf[c] : c \in Calls}

\* Sessions are interchangeable with each other, and so are calls: the spec
\* never treats any one specially. Declaring that symmetry (SYMMETRY Symm
\* in a cfg) lets TLC skip states that differ only by renaming them.
\* Safety configs only: TLC's symmetry reduction can give wrong answers
\* for liveness properties.
Symm == Permutations(Sessions) \cup Permutations(Calls)

VARIABLES
    invocations,        \* one record per issued GetOrInitCall; its phase
                        \* field says where it is between critical sections
    res,                \* one record per allocated sharedResult, indexed
                        \* by sharedResultID; IDs are never reused
                        \* (Cache.nextSharedResultID is monotonic for the
                        \* engine lifetime, surviving even a full e-graph
                        \* reset in maybeResetEgraphLocked)
    ongoingCalls,       \* one record per ongoingCall struct ever created.
                        \* Records stay here even after leaving the index,
                        \* because waiters still reference them - exactly
                        \* like Go, where the struct outlives its map entry
    ongoingCallIndex,   \* which ongoingCall is CURRENTLY registered for
                        \* each (call, session) key, or 0 for none. This is
                        \* the mirror of the Cache.ongoingCalls map. The
                        \* key includes the session because the default
                        \* concurrency key is the session ID
                        \* (preselect, dagql/objects.go:607)
    sessionEdges,       \* session->result pairs RECORDED in the session's
                        \* map (Cache.sessionResultIDsBySession)
    countedEdges,       \* the session edges whose ownership units are still
                        \* present. Claims add both sets atomically. The sets
                        \* differ only while release has removed records and
                        \* has not yet removed their snapshotted units.
    sessionRelease,     \* per-session lifecycle. active counts admitted
                        \* cache operations. phase is live, marking,
                        \* deferred, collecting, deleting, or released.
                        \* snap is the release plan's result-edge snapshot;
                        \* exitingLazy is 1 when one or more callback tokens
                        \* are between done close and goroutine exit.
    evals,              \* one record per issued Cache.Evaluate caller
                        \* (empty unless ModelLazy)
    epoch,              \* 1 before the modeled restart, 2 after. One
                        \* graceful-shutdown/restart cycle per run.
    flushed             \* shutdown state and, once complete, the snapshot:
                        \* closing refuses admission; rows contains one
                        \* record per result captured by persistence.

vars == <<invocations, res, ongoingCalls, ongoingCallIndex, sessionEdges, countedEdges,
          sessionRelease, evals, epoch, flushed>>


\* The currently-allocated ID ranges (sequences are 1-indexed).
ResultIds == 1..Len(res)
EvalIds   == 1..Len(evals)
InvocationIds    == 1..Len(invocations)
OngoingCallIds     == 1..Len(ongoingCalls)

\* Callback completion first latches an outcome and retires the published
\* attempt under lazyMu. Closing that attempt's done channel is the following
\* lock-free region. Existing waiters retain the attempt-local outcome while a
\* new attempt may already be current on the result.
EvalLatchedPhases == {"latchedDone", "latchedFail", "latchedCancel"}
EvalWakePhases == {"wakeDone", "wakeFail", "wakeCancel"}
EvalPendingPhases == {"demand", "waiting"} \cup EvalLatchedPhases \cup EvalWakePhases
EvalExitPhases == {"returnDone", "returnFailed", "returnAbandoned", "returnRefused"}
EvalTerminalPhases == {"done", "failedCallback", "abandoned", "refused"}
EvalPhaseDomain == EvalPendingPhases \cup EvalExitPhases \cup EvalTerminalPhases

\* phase values in which an invocation has finished, one way or another.
TerminalPhases == {"done", "failed", "canceled", "refused"}
InvocationExitPhases == {"returning", "failing", "canceling", "refusing"}

(***************************************************************************)
(* Recounting ownership from scratch.                                      *)
(*                                                                         *)
(* The Go code maintains sharedResult.incomingOwnershipCount               *)
(* incrementally, with ++/-- scattered across many paths. The model        *)
(* mirrors that counter as res[r].own. The four operators below instead    *)
(* RECOUNT the edges of each kind directly from the state. The             *)
(* OwnershipExact invariant demands counter = recount in every reachable   *)
(* state, so any missed or doubled increment anywhere shows up             *)
(* immediately, with a trace.                                              *)
(***************************************************************************)

\* How many sessions currently hold a counted ownership edge on result r.
SessEdgeCount(r) ==
    Cardinality({s \in Sessions : <<s, r>> \in countedEdges})

\* How many registered results list r as a dependency (live parents
\* retaining r).
DepParentCount(r) ==
    Cardinality({p \in ResultIds : res[p].registered /\ r \in res[p].deps})

\* How many in-flight publications currently pin r with a handoff hold.
HoldCount(r) ==
    Cardinality({o \in OngoingCallIds : ongoingCalls[o].hold /\ ongoingCalls[o].resId = r})

\* 1 if r has a persisted edge, else 0.
PersistedCount(r) == IF res[r].persisted THEN 1 ELSE 0

\* Every ownership edge pointing at r, recounted from scratch.
DerivedOwn(r) ==
    SessEdgeCount(r) + DepParentCount(r)
      + HoldCount(r) + PersistedCount(r)

(***************************************************************************)
(* Recomputing session-resource requirements from scratch.                 *)
(*                                                                         *)
(* res[r].required is the STORED requiredSessionResources set, maintained  *)
(* incrementally by recomputeRequiredSessionResourcesLocked (cache.go:556) *)
(* one level deep off each dep's own stored set. TrueRequired instead      *)
(* RECOUNTS the true transitive requirement: the own handle of every       *)
(* handle leaf reachable from r through dependency edges, r included.       *)
(* RequiredExact demands stored = TrueRequired in every reachable state,   *)
(* the same relationship OwnershipExact has to DerivedOwn.                  *)
(*                                                                         *)
(* TrueRequired is intent-encoding, used only in properties and in the     *)
(* retGated ghost. Its contract: the field comment calls                   *)
(* requiredSessionResources "the flattened transitive set" (cache.go, the  *)
(* sharedResult field block near "sessionResourceHandle is set when this   *)
(* result is itself"), and internal-docs/session_resources.md says the     *)
(* cache "recomputes transitive requiredSessionResources by unioning       *)
(* dependency requirements" so that a container depending on a secret       *)
(* propagates the handle transitively.                                     *)
(***************************************************************************)

\* All results reachable from r by following dependency edges, r included.
DepClosureFrom(r) ==
    LET RECURSIVE Cl(_, _)
        Cl(frontier, seen) ==
            LET next == UNION {res[p].deps : p \in frontier} \ seen
            IN IF next = {} THEN seen ELSE Cl(next, seen \cup next)
    IN Cl({r}, {r})

\* The true transitive requirement of r: every handle leaf's own handle in
\* r's dependency closure.
TrueRequired(r) ==
    {res[x].handle : x \in {y \in DepClosureFrom(r) : res[y].handle # "none"}}

\* All registered results whose semantic call is in equivalence class k.
LiveInClass(k) ==
    {r \in ResultIds : res[r].registered /\ ClassOf[res[r].call] = k}

\* Lookup and canonical selection exclude an entry after dependency
\* attachment closes with an error. An open barrier remains eligible because
\* a reader may wait for its eventual outcome.
LookupEligibleInClass(k) ==
    {r \in LiveInClass(k) : res[r].barrier # "closedErr"}

\* "The caller got a result it can actually keep": the session's atomic edge
\* claim is still counted when the result returns.
ProtectedReturn(s, r) ==
    /\ r # 0
    /\ <<s, r>> \in countedEdges

(***************************************************************************)
(* The release cascade: collect every registered result whose count is     *)
(* zero, drop its dependency edges, decrement the dependencies, repeat.    *)
(*                                                                         *)
(* Mirrors collectUnownedResultsLocked (cache.go:1360-1406), which runs    *)
(* inside the same egraphMu critical section as whatever decrement         *)
(* triggered it. released=TRUE conservatively means cleanup hooks are now  *)
(* eligible to have run; Go invokes them just after dropping egraphMu.     *)
(* Making the result dead at collection is the earlier, dangerous side of  *)
(* that small interval for every returned-live safety check.               *)
(***************************************************************************)
RECURSIVE Cascade(_)
Cascade(rf) ==
    LET dead == {r \in DOMAIN rf : rf[r].registered /\ rf[r].own = 0}
    IN IF dead = {} THEN rf
       ELSE Cascade(
         [r \in DOMAIN rf |->
            IF r \in dead
            THEN [rf[r] EXCEPT !.registered = FALSE,
                               !.released = TRUE,
                               !.deps = {}]
            ELSE [rf[r] EXCEPT !.own = @ -
                    Cardinality({p \in dead : r \in rf[p].deps})]])

\* Drop one ownership edge from r, then run the cascade.
DecAndCascade(rf, r) == Cascade([rf EXCEPT ![r].own = @ - 1])

---------------------------------------------------------------------------
\* The empty cache: no results, no calls in flight, nothing owned.
(***************************************************************************)
(* IMPORTED INITIAL STATES. After a clean restart,                         *)
(* importPersistedState (cache_persistence_import.go:15) rebuilds the      *)
(* retained graph: results whose ownership comes entirely from persisted   *)
(* edges and dependency edges - no session edges, no holds - with some     *)
(* payloads decoded eagerly and others left as encoded envelopes for the   *)
(* read barrier to decode on first use. The model can start from any       *)
(* such graph of up to two results: every result must be retained          *)
(* (persisted itself, or the dependency of a retained result), deps point  *)
(* only at earlier record slots, and ownership counts are computed from    *)
(* the edges exactly as import's increments do.                            *)
(***************************************************************************)

ImportedResult(c, persistedFlag, depsSet, ownVal, payloadVal, launderedFlag,
               handleVal, requiredVal) ==
    [call |-> c, registered |-> TRUE, released |-> FALSE,
     own |-> ownVal, deps |-> depsSet,
     persisted |-> persistedFlag, barrier |-> "none",
     payload |-> payloadVal, decodePhase |-> "idle", decodeErr |-> "none",
     decodeGen |-> 0, persistSyncPending |-> FALSE,
     laundered |-> launderedFlag,
     imported |-> TRUE,
     \* session-resource gating: handle mirrors sessionResourceHandle set by
     \* the import row (env.SessionResourceHandle), required is the STORED set
     \* from the import's dependency-first recompute.
     handle |-> handleVal, required |-> requiredVal,
     lazyCb |-> "none", lazyComplete |-> FALSE, lazyPhase |-> "idle",
     lazyCancel |-> FALSE, lazySyncPending |-> FALSE,
     lazyWaiters |-> 0, lazyRunning |-> 0, lazyTokenSession |-> 0]

\* A collected result's ID is never reused, so after a restart the slots of
\* results that were not in the snapshot stay as dead husks - present in
\* the sequence, invisible to every action (nothing matches an unregistered
\* result). This keeps IDs stable across the restart, as the Go import does.
DeadHusk ==
    [call |-> CHOOSE c \in Calls : TRUE, registered |-> FALSE,
     released |-> TRUE, own |-> 0, deps |-> {},
     persisted |-> FALSE, barrier |-> "none",
     payload |-> "decoded", decodePhase |-> "idle", decodeErr |-> "none",
     decodeGen |-> 0, persistSyncPending |-> FALSE,
     laundered |-> FALSE,
     imported |-> TRUE,
     handle |-> "none", required |-> {},
     lazyCb |-> "none", lazyComplete |-> FALSE, lazyPhase |-> "idle",
     lazyCancel |-> FALSE, lazySyncPending |-> FALSE,
     lazyWaiters |-> 0, lazyRunning |-> 0, lazyTokenSession |-> 0]

\* The candidate import rows for position pos: any call, persisted or not,
\* at most one dependency and only on an earlier row, payload decoded or
\* still an envelope, and an own handle drawn from Handles or none. The own
\* handle mirrors the row's env.SessionResourceHandle.
ImportRowChoices(pos) ==
    [call : Calls, persisted : BOOLEAN,
     deps : {{}} \cup {{d} : d \in 1..(pos-1)},
     payload : {"decoded", "envelope"},
     handle : {"none"} \cup Handles]

\* Own-handle contribution of a row: {handle} unless the row has none.
OwnHandleReq(h) == IF h = "none" THEN {} ELSE {h}

\* The stored required set each row ends the import with. The import's
\* recompute walks dependencies first (importPersistedState's memoized DFS
\* over c.resultsByID), so every row reads its deps' FINAL stored sets and
\* the result is the transitive requirement, independent of map iteration
\* order. The decode installs (eager import decode and the hit path in
\* ensurePersistedHitValueLoaded) leave the session-resource fields alone.
\* The recursion is well-founded because the import graph is acyclic.
ImportRequiredFinal(D, handleOf, depsOf) ==
    LET RECURSIVE reqOf(_)
        reqOf(x) == OwnHandleReq(handleOf[x])
                      \cup UNION {reqOf(d) : d \in depsOf[x]}
    IN [x \in D |-> reqOf(x)]

\* Every row must be retained: a persisted root, or a dependency of some
\* row. (A flushed store contains only the retained graph.)
ImportGraphRetained(g) ==
    \A x \in 1..Len(g) :
        g[x].persisted \/ \E y \in 1..Len(g) : x \in g[y].deps

ImportOwn(g, x) ==
    (IF g[x].persisted THEN 1 ELSE 0)
      + Cardinality({y \in 1..Len(g) : x \in g[y].deps})

ImportGraphState(g) ==
    LET n        == Len(g)
        handleOf == [x \in 1..n |-> g[x].handle]
        depsOf   == [x \in 1..n |-> g[x].deps]
        required == ImportRequiredFinal(1..n, handleOf, depsOf)
    IN [x \in 1..n |->
        ImportedResult(g[x].call, g[x].persisted, g[x].deps,
                       ImportOwn(g, x), g[x].payload, FALSE,
                       g[x].handle, required[x])]

InitialResStates ==
    IF ModelPersistence /\ ImportInit
    THEN {ImportGraphState(g) :
             g \in {h \in {<<>>}
                        \cup {<<a>> : a \in ImportRowChoices(1)}
                        \cup {<<a, b>> : a \in ImportRowChoices(1),
                                         b \in ImportRowChoices(2)} :
                    ImportGraphRetained(h)}}
    ELSE {<<>>}

RegisteredResultIds == {r \in ResultIds : res[r].registered}

Init ==
    /\ invocations = <<>>
    /\ res \in InitialResStates
    /\ ongoingCalls = <<>>
    /\ ongoingCallIndex = [k \in Calls \X Sessions |-> 0]
    /\ sessionEdges = {}
    /\ countedEdges = {}
    /\ sessionRelease = [s \in Sessions |->
         [phase |-> "live", snap |-> {}, active |-> 0, exitingLazy |-> 0,
          handles |-> {}]]
    /\ evals = <<>>
    /\ epoch = 1
    /\ flushed = [closing |-> FALSE, done |-> FALSE, rows |-> <<>>]

(***************************************************************************)
(* Spawn: a client issues GetOrInitCall through a serveQuery handler. The  *)
(* cache atomically refuses a released session or increments its active    *)
(* operation count before entering lookup. The server drain additionally   *)
(* prevents new handler calls after teardown begins.                       *)
(*                                                                         *)
(* SpawnNested: a call issued from INSIDE a call executor - a resolver's   *)
(* nested Select running on the detached fn goroutine. It carries the same *)
(* session ID and is not counted by the server drain. It still enters at   *)
(* the cache boundary, where the tombstone refuses it after release.        *)
(***************************************************************************)
NewInvocation(s, c, p, o, admitted) ==
    [sess |-> s, call |-> c, persistable |-> p,
     phase |-> IF admitted THEN "lookup" ELSE "refused",
     origin |-> o,
     opActive |-> admitted,
     oc |-> 0, resId |-> 0, path |-> "none",
     \* which decode attempt this invocation is parked on (DecodeJoin): its
     \* channel is closed once res.decodeGen has moved past this value
     joinedGen |-> 0,
     \* Invocations survive a modeled restart for property checking even though
     \* their real goroutines and the cache's in-memory tombstones do not, so this
     \* field ties a refusal to the epoch whose lifecycle state produced it.
     refusedEpoch |-> IF admitted THEN 0 ELSE epoch,
     lookupBarrierAtSelection |-> "none",
     \* Property-only ghost: TRUE once an action modeling THIS invocation's
     \* own ctx.Done arm has fired. See CancelOnlyOwn.
     ownCancel |-> FALSE,
     retLive |-> TRUE, retOwned |-> TRUE,
     retBarrierOK |-> TRUE, retClean |-> TRUE, retGated |-> TRUE]

Spawn ==
    /\ Len(invocations) < MaxInvocations
    /\ ~flushed.closing
    /\ \E s \in Sessions, c \in Calls, p \in BOOLEAN :
        /\ DrainOnRelease => sessionRelease[s].phase = "live"
        /\ LET admitted == sessionRelease[s].phase = "live" IN
           /\ invocations' = Append(invocations,
                NewInvocation(s, c, p, "handler", admitted))
           /\ sessionRelease' = IF admitted
                THEN [sessionRelease EXCEPT ![s].active = @ + 1]
                ELSE sessionRelease
    /\ UNCHANGED <<res, ongoingCalls, ongoingCallIndex, sessionEdges, countedEdges,
                   evals, epoch, flushed>>

SpawnNested ==
    /\ ModelNestedCalls
    /\ Len(invocations) < MaxInvocations
    /\ ~flushed.closing
    /\ \E s \in Sessions, c \in Calls, p \in BOOLEAN :
        /\ \E o \in OngoingCallIds :
             /\ ongoingCalls[o].sess = s
             /\ ongoingCalls[o].fnState \in {"running", "canceled"}
        /\ LET admitted == sessionRelease[s].phase = "live" IN
           /\ invocations' = Append(invocations,
                NewInvocation(s, c, p, "nested", admitted))
           /\ sessionRelease' = IF admitted
                THEN [sessionRelease EXCEPT ![s].active = @ + 1]
                ELSE sessionRelease
    /\ UNCHANGED <<res, ongoingCalls, ongoingCallIndex, sessionEdges, countedEdges,
                   evals, epoch, flushed>>

(***************************************************************************)
(* LookupHit: the lookup finds a live equivalent result whose attachment   *)
(* has not failed. The egraphMu section selects the result, then nests      *)
(* sessionMu to claim the session edge. A released-session tombstone       *)
(* refuses the claim without adding an edge. Persistable hits commit their *)
(* edge only after the read barrier and payload load succeed.               *)
(*                                                                         *)
(* Go: lookupCacheForRequest, cache_egraph.go:954-1007. Inside the one     *)
(* lock hold:                                                              *)
(*   - a candidate in the request's equivalence class is selected          *)
(*   - the session record and ownership unit are added together             *)
(* After the lock, an accepted hit passes the read barrier before return.  *)
(***************************************************************************)
LookupHit(i) ==
    /\ invocations[i].phase = "lookup"
    /\ \E r \in LookupEligibleInClass(ClassOf[invocations[i].call]) :
        \* Session-resource filter (selectLookupCandidateForSessionLocked,
        \* cache_egraph.go:685 via sessionSatisfiesResourceRequirementsLocked,
        \* :671): a candidate is eligible only if its STORED required set is a
        \* subset of the session's bound handles. The direction is
        \* required subset-of bound (available.Subset(required),
        \* required subset-of available), confirmed by the extra-handle-still-
        \* hits case in dagql/cache_test.go. The check reads the stored set,
        \* not TrueRequired; any satisfying candidate may be picked, not
        \* necessarily the lowest ID - that is the selection over-approximation
        \* the model already makes for LookupHit.
        /\ res[r].required \subseteq sessionRelease[invocations[i].sess].handles
        /\ LET s == invocations[i].sess
               haveEdge == <<s, r>> \in sessionEdges
           IN IF sessionRelease[s].phase = "live"
           THEN /\ res' = IF haveEdge THEN res
                           ELSE [res EXCEPT ![r].own = @ + 1]
                /\ sessionEdges' = sessionEdges \cup {<<s, r>>}
                /\ countedEdges' = countedEdges \cup {<<s, r>>}
                /\ invocations' = [invocations EXCEPT ![i].phase = "readBarrier",
                                        ![i].resId = r,
                                        ![i].path = "hit",
                                        ![i].lookupBarrierAtSelection = res[r].barrier]
           ELSE /\ UNCHANGED res
                /\ invocations' = [invocations EXCEPT ![i].phase = "refusing",
                                        ![i].resId = r,
                                        ![i].path = "hit",
                                        ![i].lookupBarrierAtSelection = res[r].barrier,
                                        ![i].refusedEpoch = epoch]
                /\ UNCHANGED <<sessionEdges, countedEdges>>
    /\ UNCHANGED <<ongoingCalls, ongoingCallIndex, sessionRelease, evals, epoch, flushed>>

\* LookupMiss: the lookup finds nothing usable; fall through to the
\* singleflight. A miss is allowed even when a candidate exists - that
\* over-approximates candidate selection (the lowest-ID pick, TTL, e-graph
\* routing), which is not modeled even though the session-resource filter
\* is, and exercises the engine's accepted duplicate-execution window
\* (getOrInitCallInner, cache.go:4522).
LookupMiss(i) ==
    /\ invocations[i].phase = "lookup"
    /\ invocations' = [invocations EXCEPT ![i].phase = "join"]
    /\ UNCHANGED <<res, ongoingCalls, ongoingCallIndex, sessionEdges, countedEdges,
                   sessionRelease, evals, epoch, flushed>>

(***************************************************************************)
(* Join: an ongoing call for this (call, session) already exists - become  *)
(* one of its waiters instead of executing again.                          *)
(*                                                                         *)
(* Go: getOrInitCallInner, cache.go:4502-4517, one callsMu critical        *)
(* section: bump oc.waiters, and stamp the persistence intent (the         *)
(* atomic.Bool oc.isPersistable) onto the shared ongoingCall.              *)
(*                                                                         *)
(* The Once tail snapshots aggregate intent when it closes admission (see  *)
(* PubUnregister), and the final handoff release commits the edge (see      *)
(* PersistThenDropHold). The guarantee is that the edge exists by the       *)
(* FINAL handoff release - not by each persistable waiter's own return; a  *)
(* non-final late joiner can return before the edge lands. That is what    *)
(* PersistableIntentDurable checks.                                        *)
(***************************************************************************)
Join(i) ==
    /\ invocations[i].phase = "join"
    /\ LET k == <<invocations[i].call, invocations[i].sess>>
           o == ongoingCallIndex[k]
       IN /\ o # 0
          /\ ongoingCalls' = [ongoingCalls EXCEPT
                ![o].waiters = @ + 1,
                ![o].isPersistable = @ \/ invocations[i].persistable]
          /\ invocations' = [invocations EXCEPT ![i].phase = "waiting", ![i].oc = o,
                                  ![i].path = "wait"]
    /\ UNCHANGED <<res, ongoingCallIndex, sessionEdges, countedEdges,
                   sessionRelease, evals, epoch, flushed>>

(***************************************************************************)
(* CreateOc: no ongoing call exists - create one and start executing.      *)
(*                                                                         *)
(* Go: getOrInitCallInner miss path, cache.go:4519-4601, still the same    *)
(* callsMu hold:                                                           *)
(*   - the WithCancelCause cancel is created (:4476)                       *)
(*   - the operation lease is acquired (:4496)                             *)
(*   - the ongoingCall is registered (:4530-4531; always, because the      *)
(*     default concurrency key - the session ID - is never empty)          *)
(*   - the fn goroutine starts (:4534)                                     *)
(***************************************************************************)
CreateOc(i) ==
    /\ invocations[i].phase = "join"
    /\ LET k == <<invocations[i].call, invocations[i].sess>> IN
       /\ ongoingCallIndex[k] = 0
       /\ ongoingCalls' = Append(ongoingCalls,
            [call |-> invocations[i].call, sess |-> invocations[i].sess,
             waiters |-> 1, fnState |-> "running", fnErr |-> FALSE,
             outcome |-> "none", reuseFrom |-> 0,
             isPersistable |-> invocations[i].persistable,
             \* final-handoff persistence bookkeeping; see PubUnregister:
             needsPersistedEdge |-> FALSE,
             pubState |-> "none", pubBy |-> 0, hold |-> FALSE, resId |-> 0,
             inIndex |-> TRUE])
       /\ ongoingCallIndex' = [ongoingCallIndex EXCEPT ![k] = Len(ongoingCalls) + 1]
       /\ invocations' = [invocations EXCEPT ![i].phase = "waiting", ![i].oc = Len(ongoingCalls) + 1,
                               ![i].path = "wait"]
    /\ UNCHANGED <<res, sessionEdges, countedEdges, sessionRelease,
                   evals, epoch, flushed>>

\* CreateOcLeaseFail: the operation lease cannot be acquired. The call
\* discharges its newly-created cancel and fails before publishing any
\* ongoing call, result, or ownership edge.
CreateOcLeaseFail(i) ==
    /\ LeaseCanFail
    /\ invocations[i].phase = "join"
    /\ ongoingCallIndex[<<invocations[i].call, invocations[i].sess>>] = 0
    /\ invocations' = [invocations EXCEPT ![i].phase = "failing",
                                           ![i].path = "leaseFailure"]
    /\ UNCHANGED <<res, ongoingCalls, ongoingCallIndex, sessionEdges, countedEdges,
                   sessionRelease, evals, epoch, flushed>>

(***************************************************************************)
(* FnComplete: the executing fn finishes (the goroutine started at         *)
(* cache.go:4588). Three possible outcomes:                                *)
(*   - "fresh": fn computed a new detached value                           *)
(*   - "reuse": fn returned an already-attached result (for example, an    *)
(*     inner call hit cache). This drives publication's canonical-         *)
(*     adoption branch. The model picks any cleanly attached result in     *)
(*     the call's class - the static-partition stand-in for output-class   *)
(*     merging. Clean attachment is required because a resolver cannot     *)
(*     obtain an attachment-errored result through any current             *)
(*     result-returning cache entry point: each such path either waits at  *)
(*     the attach barrier and returns errors instead of values, or hands   *)
(*     back a value whose clean attachment was already established.        *)
(*   - error: only when FnCanFail is on                                    *)
(***************************************************************************)
FnComplete(o) ==
    /\ ongoingCalls[o].fnState = "running"
    /\ \/ ongoingCalls' = [ongoingCalls EXCEPT ![o].fnState = "done", ![o].outcome = "fresh"]
       \/ \E r \in LiveInClass(ClassOf[ongoingCalls[o].call]) :
            /\ res[r].barrier \in {"none", "closedOk"}
            \* The reused result was obtained by the fn's resolver through a
            \* filtered, OWNED acquisition - every result-returning entry
            \* point (request lookup, digest lookup, gated result-ID load,
            \* wait return) records the session edge and its ownership unit
            \* atomically - so the session holds a counted edge to it and
            \* release cannot collect it out from under the fn. Ownership
            \* proves the result passed the filter WHEN OBTAINED; its
            \* requirement may have grown since (the gated-growth finding),
            \* and the code re-checks nothing at reuse, so no
            \* current-satisfaction conjunct belongs here.
            /\ <<ongoingCalls[o].sess, r>> \in countedEdges
            /\ ongoingCalls' = [ongoingCalls EXCEPT ![o].fnState = "done",
                                  ![o].outcome = "reuse", ![o].reuseFrom = r]
       \/ /\ FnCanFail
          /\ ongoingCalls' = [ongoingCalls EXCEPT ![o].fnState = "done", ![o].fnErr = TRUE]
    /\ UNCHANGED <<invocations, res, ongoingCallIndex, sessionEdges, countedEdges,
                   sessionRelease, evals, epoch, flushed>>

(***************************************************************************)
(* WaiterCancel: a waiter's own context is canceled while the fn is still  *)
(* running.                                                                *)
(*                                                                         *)
(* Go: wait's !completed branch, cache.go:4771-4787. Notes:                *)
(*   - the LAST canceling waiter removes the entry from the Cache.ongoingCalls index and   *)
(*     cancels the fn's context. Cancellation is COOPERATIVE: the fn       *)
(*     goroutine keeps executing until it notices - fnState "canceled"     *)
(*     means "cancel requested, executor still winding down", and with     *)
(*     ModelNestedCalls the wind-down window admits nested calls until     *)
(*     FnWindDown fires                                                    *)
(*   - the handoff hold cannot be active here (publication has not         *)
(*     started), so there is no hold to release                            *)
(*   - this action covers the select taking ctx.Done while the fn runs.    *)
(*     Go's select is nondeterministic even when BOTH channels are ready,  *)
(*     so a waiter can also take ctx.Done after the fn (and even           *)
(*     publication) completed - that arm is WaiterCancelLate below         *)
(***************************************************************************)
WaiterCancel(i) ==
    /\ invocations[i].phase = "waiting"
    /\ LET o == invocations[i].oc
           last == ongoingCalls[o].waiters = 1
       IN /\ ongoingCalls[o].fnState = "running"
          /\ ongoingCalls' = [ongoingCalls EXCEPT
                ![o].waiters = @ - 1,
                ![o].fnState = IF last THEN "canceled" ELSE "running",
                ![o].inIndex = @ /\ ~last]
          /\ ongoingCallIndex' = IF last /\ ongoingCalls[o].inIndex
                        THEN [ongoingCallIndex EXCEPT
                                ![<<ongoingCalls[o].call, ongoingCalls[o].sess>>] = 0]
                        ELSE ongoingCallIndex
          /\ invocations' = [invocations EXCEPT ![i].phase = "canceling",
                                                 ![i].ownCancel = TRUE]
    /\ UNCHANGED <<res, sessionEdges, countedEdges, sessionRelease,
                   evals, epoch, flushed>>

\* FnWindDown: the cancel-requested executor finally exits. Until it does,
\* its resolver can still issue nested cache calls (SpawnNested) - that
\* window is the drain escape. Gated by ModelNestedCalls so unrelated
\* scenarios do not pay for nested-executor states.
FnWindDown(o) ==
    /\ ModelNestedCalls
    /\ ongoingCalls[o].fnState = "canceled"
    /\ ongoingCalls' = [ongoingCalls EXCEPT ![o].fnState = "exited"]
    /\ UNCHANGED <<invocations, res, ongoingCallIndex, sessionEdges, countedEdges,
                   sessionRelease, evals, epoch, flushed>>

\* Commit aggregate persistence intent and release the publication handoff
\* hold in one egraphMu critical section. The registration check prevents a
\* stale result pointer from recreating an edge after collection.
PersistThenDropHold(o) ==
    LET r == ongoingCalls[o].resId
        persisted == IF ongoingCalls[o].needsPersistedEdge
                           /\ res[r].registered
                           /\ ~res[r].persisted
                    THEN [res EXCEPT ![r].persisted = TRUE, ![r].own = @ + 1]
                    ELSE res
    IN DecAndCascade(persisted, r)

(***************************************************************************)
(* WaiterCancelLate: the select takes ctx.Done even though waitCh is       *)
(* already closed - Go picks among ready select arms nondeterministically, *)
(* so this is an ordinary code path, not an anomaly. The waiter departs    *)
(* through the same !completed branch (cache.go:4771-4787): waiters--,     *)
(* and the LAST departing waiter removes a still-indexed entry,            *)
(* discharges the fn cancel, and then releases the handoff hold - with     *)
(* the final persistence commit - in a separate egraphMu section (see      *)
(* WaiterDropHoldCanceled).                                                *)
(*                                                                         *)
(* Guard notes:                                                            *)
(*   - the publisher is a waiter that already left the select, and it      *)
(*     stays counted in oc.waiters until after its Once completes; so      *)
(*     while publication is in flight (pubState in progress, or done but   *)
(*     still inIndex - i.e. mid-Once), a canceling waiter can never be     *)
(*     the last one. The guard encodes that invariant instead of           *)
(*     tracking which waiter publishes.                                    *)
(*   - fnState stays "done": the executor already exited, so there is no   *)
(*     wind-down window here.                                              *)
(***************************************************************************)
WaiterCancelLate(i) ==
    /\ invocations[i].phase = "waiting"
    /\ LET o == invocations[i].oc
           last == ongoingCalls[o].waiters = 1
           postOnce == /\ ongoingCalls[o].pubState \in {"done", "failed"}
                       /\ ~ongoingCalls[o].inIndex
       IN /\ ongoingCalls[o].fnState = "done"
          \* the publisher already left its select to enter the Once; only
          \* a waiter still parked at the select can take ctx.Done
          /\ i # ongoingCalls[o].pubBy
          /\ \/ ongoingCalls[o].waiters >= 2
             \/ ongoingCalls[o].pubState = "none"
             \/ postOnce
          /\ ongoingCalls' = [ongoingCalls EXCEPT
                ![o].waiters = @ - 1,
                ![o].inIndex = @ /\ ~last]
          /\ ongoingCallIndex' = IF last /\ ongoingCalls[o].inIndex
                        THEN [ongoingCallIndex EXCEPT
                                ![<<ongoingCalls[o].call, ongoingCalls[o].sess>>] = 0]
                        ELSE ongoingCallIndex
          /\ invocations' = [invocations EXCEPT ![i].phase =
                IF last /\ postOnce /\ ongoingCalls[o].hold
                THEN "cancelDropHold" ELSE "canceling",
                ![i].ownCancel = TRUE]
    /\ UNCHANGED <<res, sessionEdges, countedEdges, sessionRelease,
                   evals, epoch, flushed>>

\* WaiterDropHoldCanceled: the canceled final waiter drops the handoff
\* hold in its own egraphMu section. Aggregate persistence intent is
\* committed first, so late intent survives even when the final waiter
\* departs through cancellation.
WaiterDropHoldCanceled(i) ==
    /\ invocations[i].phase = "cancelDropHold"
    /\ LET o == invocations[i].oc IN
       /\ ongoingCalls' = [ongoingCalls EXCEPT ![o].hold = FALSE]
       /\ res' = PersistThenDropHold(o)
       /\ invocations' = [invocations EXCEPT ![i].phase = "canceling"]
    /\ UNCHANGED <<ongoingCallIndex, sessionEdges, countedEdges, sessionRelease,
                   evals, epoch, flushed>>

\* WaiterObserveFnErr: the fn failed; each waiter observes the error and
\* returns it. The last waiter removes the Cache.ongoingCalls index entry.
\* Go: wait's completionErr path, cache.go:4789-4799.
WaiterObserveFnErr(i) ==
    /\ invocations[i].phase = "waiting"
    /\ LET o == invocations[i].oc
           last == ongoingCalls[o].waiters = 1
       IN /\ ongoingCalls[o].fnState = "done" /\ ongoingCalls[o].fnErr
          /\ ongoingCalls' = [ongoingCalls EXCEPT ![o].waiters = @ - 1,
                                ![o].inIndex = @ /\ ~last]
          /\ ongoingCallIndex' = IF last /\ ongoingCalls[o].inIndex
                        THEN [ongoingCallIndex EXCEPT
                                ![<<ongoingCalls[o].call, ongoingCalls[o].sess>>] = 0]
                        ELSE ongoingCallIndex
          /\ invocations' = [invocations EXCEPT ![i].phase = "failing"]
    /\ UNCHANGED <<res, sessionEdges, countedEdges, sessionRelease,
                   evals, epoch, flushed>>

(***************************************************************************)
(* PUBLICATION - initCompletedResult, entered through the sync.Once in     *)
(* wait (cache.go:4801). The next several actions form its state machine,  *)
(* driven by oc.pubState. The publishing waiter runs it to completion      *)
(* regardless of its own caller's context (the publication context is      *)
(* WithoutCancel, cache.go:4821).                                     *)
(*                                                                         *)
(* PubBegin: entering the Once, plus the lock-free prologue - oc.res is    *)
(* set to a fresh empty &sharedResult{} at cache.go:4945 before any lock   *)
(* is taken. sync.Once.Do blocks every other waiter until publication      *)
(* returns, and that blocking is what makes the later                      *)
(* initCompletedResultErr reads safe.                                      *)
(***************************************************************************)
PubBegin(o) ==
    /\ ongoingCalls[o].fnState = "done" /\ ~ongoingCalls[o].fnErr
    /\ ongoingCalls[o].pubState = "none"
    /\ \E w \in InvocationIds :
        /\ invocations[w].phase = "waiting" /\ invocations[w].oc = o
        \* w's select took the completed branch and w entered the Once: it
        \* is the publisher. A waiter past its select can never take the
        \* ctx.Done arm again, so pubBy is excluded from WaiterCancelLate.
        /\ ongoingCalls' = [ongoingCalls EXCEPT ![o].pubState = "begun",
                                                ![o].pubBy = w]
    /\ UNCHANGED <<invocations, res, ongoingCallIndex, sessionEdges, countedEdges,
                   sessionRelease, evals, epoch, flushed>>

(***************************************************************************)
(* PubIndexFresh: publish a freshly computed value. One egraphMu critical  *)
(* section (ending at cache.go:5247) that does, in order:                  *)
(*   1. allocate and register the sharedResult - it is lookup-visible      *)
(*      from this instant, before attachment completes                     *)
(*   2. add exact dependency edges from the result-call refs, each         *)
(*      incrementing its dependency                                        *)
(*   3. take the publication handoff hold                                  *)
(*   4. arm the dependency-attachment barrier                              *)
(***************************************************************************)
PubIndexFresh(o) ==
    /\ ongoingCalls[o].pubState = "begun"
    /\ ongoingCalls[o].outcome = "fresh"
    /\ Len(res) < MaxResults
    \* A fresh result may carry deferred lazy work (a value implementing
    \* HasLazyEvaluation, whose callback registerLazyEvaluation stores on
    \* the sharedResult at cache.go:3466). Which results are lazy is the
    \* producer's business, so the model picks nondeterministically.
    /\ \E lazyCb \in IF ModelLazy THEN {"none", "armed"} ELSE {"none"} :
       \* The publishing session's own handle for this result: "none", or a
       \* handle the session has already bound. A handle leaf's resolver calls
       \* BindSessionResource before returning the leaf, so at publication the
       \* handle is present; PubIndexFresh mirrors initCompletedResult's
       \* recompute. A handle the session never bound is not publishable as
       \* this result's own.
       \E handleChoice \in {"none"} \cup
             {h \in Handles : h \in sessionRelease[ongoingCalls[o].sess].handles} :
       \* Structural deps are results this session obtained with ownership
       \* (every acquisition path records the session edge and its unit
       \* atomically) - without that restriction the model would reach a
       \* session structurally depending on a leaf it never obtained, which
       \* the code cannot produce. Ownership proves each dep passed the
       \* filter when obtained; its requirement may have grown since, and
       \* the code re-checks nothing here. A held dep's attachment is also
       \* settled: an attached result was handed out only past its read
       \* barrier, and attachResult completes a detached child before the
       \* edge is recorded.
       \E deps \in {{}} \cup {{d} : d \in {r \in ResultIds :
                       /\ res[r].registered
                       /\ res[r].barrier \in {"none", "closedOk"}
                       /\ <<ongoingCalls[o].sess, r>> \in countedEdges}} :
        LET withDeps == [r \in DOMAIN res |->
                IF r \in deps THEN [res[r] EXCEPT !.own = @ + 1]
                ELSE res[r]]
            newRes == [call |-> ongoingCalls[o].call, registered |-> TRUE,
                       released |-> FALSE,
                       own |-> 1,
                       deps |-> deps,
                       persisted |-> FALSE,
                       barrier |-> "open",
                       \* fresh results have their typed payload in memory;
                       \* only imported entries carry encoded envelopes
                       payload |-> "decoded",
                       decodePhase |-> "idle", decodeErr |-> "none",
                       decodeGen |-> 0, persistSyncPending |-> FALSE,
                       \* session-resource gating: own handle chosen above;
                       \* required is {own handle} union the deps' STORED
                       \* required sets (initCompletedResult's recompute, one
                       \* level deep off each dep's stored set).
                       handle |-> handleChoice,
                       required |-> OwnHandleReq(handleChoice)
                                      \cup UNION {res[d].required : d \in deps},
                       laundered |-> FALSE,
                       \* lazy-evaluation state, mirroring the lazyMu block
                       \* on sharedResult (cache.go:2032-2041):
                       imported |-> FALSE,       \* fresh, not from the store
                       lazyCb |-> lazyCb,        \* lazyEval callback stored?
                       lazyComplete |-> FALSE,   \* lazyEvalComplete
                       lazyPhase |-> "idle",     \* published attempt lifecycle
                       lazyCancel |-> FALSE,     \* attempt cancel requested
                       lazySyncPending |-> FALSE, \* lazySyncPending
                       lazyWaiters |-> 0,        \* current attempt's waiters
                       lazyRunning |-> 0,        \* callbacks actually running
                       lazyTokenSession |-> 0]   \* active callback token owner
        IN /\ res' = Append(withDeps, newRes)
           /\ ongoingCalls' = [ongoingCalls EXCEPT ![o].pubState = "attaching",
                                 ![o].hold = TRUE,
                                 ![o].resId = Len(res) + 1]
    /\ UNCHANGED <<invocations, ongoingCallIndex, sessionEdges, countedEdges,
                   sessionRelease, evals, epoch, flushed>>

(***************************************************************************)
(* ADOPTION BRANCH: the fn returned an already-attached result, so         *)
(* publication adopts the canonical equivalent instead of indexing a new   *)
(* sharedResult.                                                           *)
(***************************************************************************)

\* The canonical equivalent Go picks during adoption: the lowest-ID cleanly
\* attached result in the returned result's class
\* (canonicalEquivalentSharedResultLocked with requireCleanAttachment).
\* Candidates come from the returned result's own index entries, so a
\* collected result has none and Go falls back to the returned result
\* itself; the indexing step then refuses to re-register it (the
\* PubIndexReuse failure branch). A registered returned result is always
\* its own candidate - it stays cleanly attached once published - so the
\* pick over a registered result is never empty.
CanonicalPick(o) ==
    LET rf == ongoingCalls[o].reuseFrom
        s  == ongoingCalls[o].sess
    IN IF ~res[rf].registered
       THEN rf
       \* canonicalEquivalentSharedResultLocked (cache.go:2400) gathers clean
       \* candidates, applies the session filter, and returns the lowest-ID
       \* survivor, else falls back to the returned result rf when none passes.
       ELSE LET live == {r \in LiveInClass(ClassOf[res[rf].call]) :
                            /\ res[r].barrier \in {"none", "closedOk"}
                            /\ res[r].required \subseteq sessionRelease[s].handles}
            IN IF live = {} THEN rf
               ELSE CHOOSE r \in live : \A q \in live : r <= q

\* PubAdopt: one egraphMu critical section both picks the canonical
\* equivalent AND takes the handoff hold (initCompletedResult's adoption
\* swap), so nothing can collect the adopted result before publication
\* finishes. Taking the hold in the same section as the pick is what makes
\* adoption safe against a concurrent session release; the regression test
\* dagql/cache_canonical_race_test.go pins that property in Go. When the
\* returned result was already collected, the pick falls back to it and the
\* hold lands on the dead slot, mirroring Go's unconditional hold
\* increment; PubIndexReuse then fails the publication.
PubAdopt(o) ==
    /\ ongoingCalls[o].pubState = "begun"
    /\ ongoingCalls[o].outcome = "reuse"
    /\ LET r == CanonicalPick(o) IN
        /\ res' = [res EXCEPT ![r].own = @ + 1]
        /\ ongoingCalls' = [ongoingCalls EXCEPT ![o].pubState = "adopted",
                              ![o].hold = TRUE, ![o].resId = r]
    /\ UNCHANGED <<invocations, ongoingCallIndex, sessionEdges, countedEdges,
                   sessionRelease, evals, epoch, flushed>>

(***************************************************************************)
(* PubIndexReuse: finish publication for an adopted result - the egraphMu  *)
(* section containing indexWaitResultInEgraphLocked. Behavior:             *)
(*   - if the returned result was collected before the adoption pick, the  *)
(*     pick fell back to it, and indexWaitResultInEgraphLocked refuses to  *)
(*     re-register it: publication fails rather than serve a collected     *)
(*     result                                                              *)
(*   - no attach barrier: the adoption pick only selects cleanly attached  *)
(*     results (resWasCacheBacked skips the barrier arm)                   *)
(***************************************************************************)
PubIndexReuse(o) ==
    /\ ongoingCalls[o].pubState = "adopted"
    /\ LET r == ongoingCalls[o].resId IN
       IF ~res[r].registered
       THEN /\ ongoingCalls' = [ongoingCalls EXCEPT ![o].pubState = "failed"]
            /\ UNCHANGED res
       ELSE /\ UNCHANGED res
            /\ ongoingCalls' = [ongoingCalls EXCEPT ![o].pubState = "done"]
    /\ UNCHANGED <<invocations, ongoingCallIndex, sessionEdges, countedEdges,
                   sessionRelease, evals, epoch, flushed>>

(***************************************************************************)
(* ATTACHMENT PHASE for fresh results. attachDependencyResults runs        *)
(* OUTSIDE egraphMu (cache.go:5249) while the result is already            *)
(* lookup-visible - that is why the barrier exists. Steps:                 *)
(*   - PubAttachAddDep: attachment discovers an embedded child result and  *)
(*     records the dependency edge; each such AddExplicitDependency is     *)
(*     its own egraphMu critical section (cache.go:2650)              *)
(*   - PubFinishOk: attachment succeeded; close the barrier clean          *)
(*   - PubAttachFailDropHold: attachment failed; release the hold and      *)
(*     cascade in one egraphMu critical section                            *)
(*   - PubAttachFailCloseBarrier: close the barrier with the error in the  *)
(*     following attachDepsMu critical section                             *)
(*                                                                         *)
(* STATED ASSUMPTION: dependency edges never form a cycle. The Go cache    *)
(* does not enforce this - addExplicitDependencyLocked (cache.go:2673)     *)
(* rejects only self-edges - but cycles cannot arise in practice:          *)
(* structural (call-ref) deps always point from a newer result ID to       *)
(* older ones (a fresh result gets the highest ID after its refs already   *)
(* have theirs), and explicit attachment edges mirror the value's          *)
(* embedded-object graph, which the core types build as a DAG. A cycle     *)
(* would need two values each embedding the other, which no producer       *)
(* constructs. If one ever formed, both results would hold each other's    *)
(* ownership count above zero forever: silently permanently                *)
(* uncollectable, with no error anywhere. The NoCycle guard below bakes    *)
(* the assumption into the model so that collection-liveness properties    *)
(* stay meaningful; without it, the model could form cycles the real       *)
(* producers never build, and those model-only cycles would falsely        *)
(* defeat any "everything is eventually collected" check.                  *)
(***************************************************************************)

\* Is target reachable from start by following dependency edges? Used by
\* the NoCycle guard: adding parent -> d is allowed only if parent is not
\* already reachable from d.
DepReachable(rf, start, target) ==
    LET RECURSIVE Reach(_, _)
        Reach(frontier, seen) ==
            IF target \in frontier THEN TRUE
            ELSE LET next == UNION {rf[p].deps : p \in frontier} \ seen
                 IN IF next = {} THEN FALSE
                    ELSE Reach(next, seen \cup next)
    IN Reach({start}, {start})

\* p together with every registered result that transitively depends on it.
AncestorClosure(rf, p) ==
    LET RECURSIVE up(_)
        up(S) ==
            LET next == S \cup {q \in DOMAIN rf :
                                  rf[q].registered /\ rf[q].deps \cap S # {}}
            IN IF next = S THEN S ELSE up(next)
    IN up({p})

\* The stored required sets after addExplicitDependencyLocked adds the
\* edge making p's deps newDepsOfP: the parent is recomputed and the
\* change cascades upward through depParents until the sets stop changing
\* (the worklist in addExplicitDependencyLocked). At the fixpoint every
\* result in p's ancestor closure reads its deps' final stored sets, and
\* everything outside the closure keeps its stored set - which is why a
\* drifted set elsewhere would NOT be repaired by the cascade. The
\* recursion is well-founded because dependency edges are acyclic.
CascadeRequired(rf, p, newDepsOfP) ==
    LET A == AncestorClosure(rf, p)
        depsOf(x) == IF x = p THEN newDepsOfP ELSE rf[x].deps
        RECURSIVE reqOf(_)
        reqOf(x) == IF x \notin A THEN rf[x].required
                    ELSE OwnHandleReq(rf[x].handle)
                           \cup UNION {reqOf(dd) : dd \in depsOf(x)}
    IN [x \in DOMAIN rf |-> reqOf(x)]

PubAttachAddDep(o) ==
    /\ ongoingCalls[o].pubState = "attaching"
    /\ \E d \in ResultIds :
        /\ res[d].registered
        \* the dep's own attachment is settled, as in PubIndexFresh: the
        \* value's embedded children were resolver-held (past their read
        \* barrier) or attached to completion by attachResult first
        /\ res[d].barrier \in {"none", "closedOk"}
        /\ d # ongoingCalls[o].resId
        /\ d \notin res[ongoingCalls[o].resId].deps
        \* the stated no-cycle assumption (see the comment block above)
        /\ ~DepReachable(res, d, ongoingCalls[o].resId)
        \* attachment deps are the value's embedded children, which
        \* attachResult claims for the session (trackSessionResult) before
        \* the edge is recorded - ownership, as in PubIndexFresh; the
        \* filter held at acquisition and is not re-checked here.
        /\ <<ongoingCalls[o].sess, d>> \in countedEdges
        /\ LET p == ongoingCalls[o].resId
               newDeps == res[p].deps \cup {d}
               \* addExplicitDependencyLocked recomputes the parent after
               \* adding the edge and cascades the change upward through
               \* depParents (the worklist in cache.go), so an ancestor that
               \* already depends on p learns about requirements the new
               \* dep introduces.
               newReq == CascadeRequired(res, p, newDeps)
           IN res' = [x \in DOMAIN res |->
                IF x = p THEN [res[x] EXCEPT !.deps = newDeps,
                                             !.required = newReq[x]]
                ELSE IF x = d THEN [res[x] EXCEPT !.own = @ + 1,
                                                  !.required = newReq[x]]
                ELSE [res[x] EXCEPT !.required = newReq[x]]]
    /\ UNCHANGED <<invocations, ongoingCalls, ongoingCallIndex, sessionEdges, countedEdges,
                   sessionRelease, evals, epoch, flushed>>

PubFinishOk(o) ==
    /\ ongoingCalls[o].pubState = "attaching"
    /\ res' = [res EXCEPT ![ongoingCalls[o].resId].barrier = "closedOk"]
    /\ ongoingCalls' = [ongoingCalls EXCEPT ![o].pubState = "done"]
    /\ UNCHANGED <<invocations, ongoingCallIndex, sessionEdges, countedEdges,
                   sessionRelease, evals, epoch, flushed>>

PubAttachFailDropHold(o) ==
    /\ AttachCanFail
    /\ ongoingCalls[o].pubState = "attaching"
    /\ res' = DecAndCascade(res, ongoingCalls[o].resId)
    /\ ongoingCalls' = [ongoingCalls EXCEPT
         ![o].pubState = "attachFailClosing", ![o].hold = FALSE]
    /\ UNCHANGED <<invocations, ongoingCallIndex, sessionEdges, countedEdges,
                   sessionRelease, evals, epoch, flushed>>

PubAttachFailCloseBarrier(o) ==
    /\ ongoingCalls[o].pubState = "attachFailClosing"
    /\ res' = [res EXCEPT ![ongoingCalls[o].resId].barrier = "closedErr"]
    /\ ongoingCalls' = [ongoingCalls EXCEPT ![o].pubState = "failed"]
    /\ UNCHANGED <<invocations, ongoingCallIndex, sessionEdges, countedEdges,
                   sessionRelease, evals, epoch, flushed>>

\* PubUnregister: the entry leaves the Cache.ongoingCalls index - the tail of the Once
\* (wait, cache.go:4830-4835), in its own callsMu critical section AFTER
\* publication finished. Joining stays possible until this fires. This is
\* also where admission closes, so final persistence intent is snapshotted:
\* publication must have succeeded, a result must exist, and some admitted
\* request must have asked for persistence.
PubUnregister(o) ==
    /\ ongoingCalls[o].pubState \in {"done", "failed"}
    /\ ongoingCalls[o].inIndex
    /\ ongoingCallIndex' = [ongoingCallIndex EXCEPT ![<<ongoingCalls[o].call, ongoingCalls[o].sess>>] = 0]
    /\ ongoingCalls' = [ongoingCalls EXCEPT ![o].inIndex = FALSE,
          ![o].needsPersistedEdge =
              /\ ongoingCalls[o].pubState = "done"
              /\ ongoingCalls[o].resId # 0
              /\ ongoingCalls[o].isPersistable]
    /\ UNCHANGED <<invocations, res, sessionEdges, countedEdges, sessionRelease,
                   evals, epoch, flushed>>

\* WaiterObservePubErr: publication failed; each waiter observes the error
\* and returns it, decrementing the waiter count under callsMu. If the
\* departing waiter is the last one and the handoff hold is still active
\* (the failed-adoption path keeps it held), it goes on to release the
\* hold in a separate egraphMu section - see WaiterDropHoldPubErr.
\* Go: wait's initCompletedResultErr path, cache.go:4837-4848.
WaiterObservePubErr(i) ==
    /\ invocations[i].phase = "waiting"
    /\ LET o == invocations[i].oc
           last == ongoingCalls[o].waiters = 1
           dropHold == last /\ ongoingCalls[o].hold
       IN /\ ongoingCalls[o].pubState = "failed"
          /\ ~ongoingCalls[o].inIndex   \* Once-completion ordering; see
                               \* WaiterClaim
          /\ ongoingCalls' = [ongoingCalls EXCEPT ![o].waiters = @ - 1]
          /\ invocations' = [invocations EXCEPT
               ![i].phase = IF dropHold THEN "pubErrDropHold" ELSE "failing"]
    /\ UNCHANGED <<res, ongoingCallIndex, sessionEdges, countedEdges, sessionRelease,
                   evals, epoch, flushed>>

\* WaiterDropHoldPubErr: the last waiter of a failed publication drops the
\* handoff hold in its own egraphMu section (releaseOngoingCallHandoff,
\* cache.go:4901-4919). The persistence arm is vacuous here because
\* needsPersistedEdge requires a successful publication.
WaiterDropHoldPubErr(i) ==
    /\ invocations[i].phase = "pubErrDropHold"
    /\ LET o == invocations[i].oc IN
       /\ ongoingCalls' = [ongoingCalls EXCEPT ![o].hold = FALSE]
       /\ res' = PersistThenDropHold(o)
       /\ invocations' = [invocations EXCEPT ![i].phase = "failing"]
    /\ UNCHANGED <<ongoingCallIndex, sessionEdges, countedEdges, sessionRelease,
                   evals, epoch, flushed>>

(***************************************************************************)
(* WAITER SUCCESS PATH. In code order (wait, cache.go:4863-4895):          *)
(*   1. claim the session edge while the handoff                           *)
(*      hold still pins the result                                         *)
(*   2. waiters--                                                          *)
(*   3. the last waiter releases the handoff hold                          *)
(*   4. the read barrier (ensurePersistedHitValueLoaded)                   *)
(*                                                                         *)
(* The claim is one egraphMu critical section with sessionMu nested inside *)
(* it. A released tombstone refuses the claim. The waiter still departs,   *)
(* and the final waiter still commits persistence before dropping the hold. *)
(***************************************************************************)

WaiterClaim(i) ==
    /\ invocations[i].phase = "waiting"
    /\ ongoingCalls[invocations[i].oc].pubState = "done"
    \* Ordering note: every waiter passes through the COMPLETED sync.Once,
    \* whose tail already deleted the Cache.ongoingCalls index entry (see
    \* PubUnregister). So no Join can interleave with waiter claims.
    /\ ~ongoingCalls[invocations[i].oc].inIndex
    /\ LET r == ongoingCalls[invocations[i].oc].resId
           s == invocations[i].sess
       IN IF sessionRelease[s].phase = "live"
          THEN /\ res' = IF <<s, r>> \in sessionEdges
                          THEN res ELSE [res EXCEPT ![r].own = @ + 1]
               /\ sessionEdges' = sessionEdges \cup {<<s, r>>}
               /\ countedEdges' = countedEdges \cup {<<s, r>>}
               /\ invocations' = [invocations EXCEPT ![i].phase = "depart",
                                        ![i].resId = r]
          ELSE /\ invocations' = [invocations EXCEPT ![i].phase = "refusedDepart",
                                        ![i].resId = r,
                                        ![i].refusedEpoch = epoch]
               /\ UNCHANGED <<res, sessionEdges, countedEdges>>
    /\ UNCHANGED <<ongoingCalls, ongoingCallIndex, sessionRelease,
                   evals, epoch, flushed>>

\* WaiterDepart: waiters--, under callsMu. The last waiter goes on to
\* release the handoff hold. Go: wait, cache.go:4873-4876.
WaiterDepart(i) ==
    /\ invocations[i].phase \in {"depart", "refusedDepart"}
    /\ LET o == invocations[i].oc
           last == ongoingCalls[o].waiters = 1
           refused == invocations[i].phase = "refusedDepart"
       IN /\ ongoingCalls' = [ongoingCalls EXCEPT ![o].waiters = @ - 1]
          /\ invocations' = [invocations EXCEPT
               ![i].phase = IF last /\ ongoingCalls[o].hold
                         THEN IF refused THEN "refusedReleaseHold" ELSE "releaseHold"
                         ELSE IF refused THEN "refusing" ELSE "readBarrier"]
    /\ UNCHANGED <<res, ongoingCallIndex, sessionEdges, countedEdges, sessionRelease,
                   evals, epoch, flushed>>

\* WaiterReleaseHold: the last waiter drops the publication handoff hold,
\* in its own egraphMu section - releaseOngoingCallHandoff, which first
\* commits the admission-close persistence intent. From here on, only real
\* edges (session, dependency, persisted) keep the result alive.
\* Go: wait, cache.go:4878-4881 -> releaseOngoingCallHandoff.
WaiterReleaseHold(i) ==
    /\ invocations[i].phase \in {"releaseHold", "refusedReleaseHold"}
    /\ LET o == invocations[i].oc
           refused == invocations[i].phase = "refusedReleaseHold"
       IN
       /\ ongoingCalls' = [ongoingCalls EXCEPT ![o].hold = FALSE]
       /\ res' = PersistThenDropHold(o)
       /\ invocations' = [invocations EXCEPT ![i].phase = IF refused THEN "refusing" ELSE "readBarrier"]
    /\ UNCHANGED <<ongoingCallIndex, sessionEdges, countedEdges, sessionRelease,
                   evals, epoch, flushed>>

(***************************************************************************)
(* THE READ BARRIER: every return path waits for the result's dependency   *)
(* attachment to finish before handing the result out.                     *)
(*                                                                         *)
(* Go: ensurePersistedHitValueLoaded, cache_persistence_import.go:578-604. *)
(* Outcomes: a clean barrier returns the result; an error returns the      *)
(* error. In either case the claimed session edge remains until session    *)
(* release.                                                               *)
(*                                                                         *)
(* Completion is also where each invocation records its return-time        *)
(* evidence (the ret* flags) for the properties to inspect.                *)
(***************************************************************************)
ReadBarrierOk(i) ==
    /\ invocations[i].phase = "readBarrier"
    /\ LET r == invocations[i].resId
       IN /\ res[r].barrier \in {"none", "closedOk"}
          \* An encoded payload must be decoded before the result can be
          \* returned; the decode actions below handle that arm and loop
          \* back here once the payload is in memory. An installed payload
          \* whose owner-lease sync is still pending (persistSyncPending,
          \* Go persistLeaseSyncPending) is not served either: the caller
          \* joins or leads a sync-only attempt first.
          /\ res[r].payload = "decoded"
          /\ ~res[r].persistSyncPending
          /\ invocations' = [invocations EXCEPT
               ![i].phase = IF invocations[i].path = "hit"
                                  /\ invocations[i].persistable
                             THEN "persistHit" ELSE "returning"]
          /\ UNCHANGED res
    /\ UNCHANGED <<ongoingCalls, ongoingCallIndex, sessionEdges, countedEdges,
                   sessionRelease, evals, epoch, flushed>>

\* A persistable cache hit takes one additional egraphMu critical section
\* after attachment and payload loading succeed. The upsert keeps the expiry
\* computed during lookup; TTL values are abstracted from this model. The
\* registration guard prevents resurrection if the result was collected in
\* the interval before this lock acquisition.
PersistHit(i) ==
    /\ invocations[i].phase = "persistHit"
    /\ LET r == invocations[i].resId
           persisted == IF res[r].registered /\ ~res[r].persisted
                        THEN [res EXCEPT ![r].persisted = TRUE,
                                         ![r].own = @ + 1]
                        ELSE res
       IN /\ res' = persisted
          /\ invocations' = [invocations EXCEPT
               ![i].phase = "returning"]
    /\ UNCHANGED <<ongoingCalls, ongoingCallIndex, sessionEdges, countedEdges,
                   sessionRelease, evals, epoch, flushed>>

ReadBarrierErrHit(i) ==
    /\ invocations[i].phase = "readBarrier"
    /\ invocations[i].path = "hit"
    /\ res[invocations[i].resId].barrier = "closedErr"
    /\ invocations' = [invocations EXCEPT ![i].phase = "failing"]
    /\ UNCHANGED <<res, ongoingCalls, ongoingCallIndex, sessionEdges, countedEdges,
                   sessionRelease, evals, epoch, flushed>>

ReadBarrierErrWait(i) ==
    /\ invocations[i].phase = "readBarrier"
    /\ invocations[i].path = "wait"
    /\ res[invocations[i].resId].barrier = "closedErr"
    /\ invocations' = [invocations EXCEPT ![i].phase = "failing"]
    /\ UNCHANGED <<res, ongoingCalls, ongoingCallIndex, sessionEdges, countedEdges,
                   sessionRelease, evals, epoch, flushed>>

(***************************************************************************)
(* READER CANCELLATION (ReaderCanCancel). ensurePersistedHitValueLoaded    *)
(* waits with a select in two places: on the attach barrier               *)
(* (cache_persistence_import.go:593-596) and on another caller's decode    *)
(* (:601-604). Each has a ctx.Done arm, and Go's select picks among ready  *)
(* arms nondeterministically, so a canceled caller can fail here even on   *)
(* a healthy result whose barrier is already closed. Cancellation leaves   *)
(* the session edge in place for release.                                  *)
(* The two actions here are the attach-barrier arm, which also covers a    *)
(* reader canceling before it joined or led a decode. A reader already     *)
(* parked on a decode, or leading one, cancels through DecodeJoinCancel    *)
(* and DecodeLeadCancel below.                                             *)
(* The select exists only when attachDepsWaitCh is non-nil. Fresh results  *)
(* get the channel at publication (initCompletedResult) and keep it,       *)
(* closed, for the rest of their life (finishAttachDeps closes it and      *)
(* never clears it), so both arms stay ready for them long after the       *)
(* barrier closed. Imported rows never get the channel, so a reader of a   *)
(* result whose barrier is "none" has no ctx.Done arm here at all; hence   *)
(* the barrier # "none" guard.                                             *)
(***************************************************************************)
ReadBarrierCancelHit(i) ==
    /\ ReaderCanCancel
    /\ invocations[i].phase = "readBarrier"
    /\ invocations[i].path = "hit"
    /\ res[invocations[i].resId].barrier # "none"
    /\ invocations' = [invocations EXCEPT ![i].phase = "canceling",
                                           ![i].ownCancel = TRUE]
    /\ UNCHANGED <<res, ongoingCalls, ongoingCallIndex, sessionEdges, countedEdges,
                   sessionRelease, evals, epoch, flushed>>

\* The WAIT-path twin: the error propagates and the claimed session edge
\* simply remains (wait, cache.go:4889-4894) - same as ReadBarrierErrWait.
ReadBarrierCancelWait(i) ==
    /\ ReaderCanCancel
    /\ invocations[i].phase = "readBarrier"
    /\ invocations[i].path = "wait"
    /\ res[invocations[i].resId].barrier # "none"
    /\ invocations' = [invocations EXCEPT ![i].phase = "canceling",
                                           ![i].ownCancel = TRUE]
    /\ UNCHANGED <<res, ongoingCalls, ongoingCallIndex, sessionEdges, countedEdges,
                   sessionRelease, evals, epoch, flushed>>

(***************************************************************************)
(* PERSISTED-PAYLOAD DECODE. An imported result can exist                  *)
(* with its payload still encoded as a persisted envelope; the read        *)
(* barrier decodes it on first use (ensurePersistedHitValueLoaded's        *)
(* decode loop, cache_persistence_import.go:606-728). Per result:          *)
(*   - one caller becomes the decode leader (publishes                     *)
(*     persistDecodeWaitCh, :616) and performs the decode itself:          *)
(*     DecodeResult, then the install of the decoded value under           *)
(*     payloadMu (DecodeInstall), then syncResultSnapshotLeases            *)
(*   - later callers wait on that channel                                  *)
(*   - the finish (:620-627) latches the error, CLEARS the channel, and    *)
(*     closes it - so unlike the lazy singleflight there is no lingering   *)
(*     published channel: after any finish, the next demand leads a        *)
(*     fresh attempt. A failure before the install leaves the payload      *)
(*     encoded, so the next attempt decodes afresh; a failure after it     *)
(*     leaves the payload decoded and sets persistSyncPending, so the      *)
(*     next attempt retries only the lease sync (sync-only attempt).       *)
(* Channels are modeled as generations: res.decodeGen counts finishes      *)
(* and cancellations of attempts on the result, a joiner records the       *)
(* generation it joined, and its channel is closed once decodeGen has      *)
(* moved past that. A woken waiter re-reads the CURRENT latched error,     *)
(* which a newer leader may have already reset to none - in that case      *)
(* the waiter just loops: re-checks the payload and either returns it,     *)
(* rejoins the running attempt, or leads a new one. That loop is the       *)
(* "continue" in the Go, and it is why a joiner of a canceled attempt      *)
(* can still end successfully: a newer leader may have installed the       *)
(* value before the old channel closed.                                    *)
(*                                                                         *)
(* A decode failure returns an error and leaves the claimed session edge   *)
(* in place for session release.                                           *)
(*                                                                         *)
(* The decode runs on the LEADER'S OWN request context: DecodeResult is    *)
(* called on ContextWithCall(ctx, call) and syncResultSnapshotLeases on    *)
(* ctx, both the leader's ctx with no WithoutCancel. A leader canceled     *)
(* mid-decode gets a context error back and returns it as its own; the     *)
(* finish latches the error together with a retry classification           *)
(* (persistDecodeRetry, computed by lazyEvalErrorCausedByContext against   *)
(* the leader's ctx). A woken joiner reads both: a genuine failure is      *)
(* returned as the joiner's own error, while a departed leader's           *)
(* cancellation sends a healthy joiner back around the loop to lead or     *)
(* join afresh - the same shape waitForLazyEvaluation gives the lazy       *)
(* singleflight. Where the cancellation landed decides the rest: inside    *)
(* DecodeResult the payload stays encoded and the next attempt decodes     *)
(* afresh; inside the lease sync, after the install, persistSyncPending    *)
(* routes every later reader through a sync-only attempt, so the           *)
(* lease set cannot be left incompletely synchronized behind a served      *)
(* value (syncResultSnapshotLeases stores the reconciled link set only on  *)
(* full success, so a retry re-diffs from the previous stored state).      *)
(* DecodeLeadCancel, DecodeLeadFinish, and DecodeWake model exactly that;  *)
(* the decode_cancel configuration holds NoSpuriousErrors green over it.   *)
(***************************************************************************)

\* DecodeLead: a reader finds no attempt published and leads one. Two
\* shapes, decided by the payload: an encoded envelope needs the full
\* decode (DecodeResult, install, lease sync), while an installed payload
\* with persistSyncPending set needs only the lease sync - the leader
\* skips the decode and reconciles the leases (sync-only attempt).
DecodeLead(i) ==
    /\ invocations[i].phase = "readBarrier"
    /\ LET r == invocations[i].resId IN
       /\ res[r].barrier \in {"none", "closedOk"}
       /\ \/ res[r].payload = "envelope"
          \/ res[r].payload = "decoded" /\ res[r].persistSyncPending
       /\ res[r].decodePhase = "idle"
       /\ res' = [res EXCEPT ![r].decodePhase = "running",
                             ![r].decodeErr = "none"]
       /\ invocations' = [invocations EXCEPT ![i].phase = "decoding"]
    /\ UNCHANGED <<ongoingCalls, ongoingCallIndex, sessionEdges, countedEdges,
                   sessionRelease, evals, epoch, flushed>>

\* DecodeInstall: DecodeResult returned a value and the leader installs it
\* under payloadMu (the block after DecodeResult in
\* ensurePersistedHitValueLoaded): res.self is set, hasValue becomes true,
\* the envelope is dropped. The attempt is still running - the channel is
\* still published and the lease sync has not run - and from this instant
\* a reader reaching the top of the loop takes the fast path and returns
\* the value without joining (ReadBarrierOk): persistSyncPending is still
\* FALSE while the attempt runs. Only a post-install FAILURE of this
\* attempt sets the flag and routes later readers through a sync-only
\* attempt.
DecodeInstall(i) ==
    /\ invocations[i].phase = "decoding"
    /\ LET r == invocations[i].resId IN
       /\ res[r].payload = "envelope"
       \* The install leaves the session-resource fields alone: the decoded
       \* shell only knows the row's own handle (the same envelope value the
       \* import row was built from), and requiredSessionResources belongs
       \* to import and publication and is read under egraphMu. (The install
       \* used to copy both fields from the shell, which reduced a non-leaf's
       \* stored set to its own handle and raced the lookup filter's read.)
       /\ res' = [res EXCEPT ![r].payload = "decoded"]
    /\ UNCHANGED <<invocations, ongoingCalls, ongoingCallIndex, sessionEdges,
                   countedEdges, sessionRelease, evals, epoch, flushed>>

\* DecodeLeadFinish: the leader's attempt ends and finishPersistDecode
\* latches the outcome, clears the channel, and closes it: decodeGen moves
\* on, which is what wakes the joiners. Success requires the install to
\* have happened; the leader then loops back to the top of the read
\* barrier, where the normal return path applies. Under DecodeCanFail a
\* failure has two shapes, told apart by whether the install happened:
\*   - "failEnvelope": DecodeResult (or the resolver and call lookups
\*     before it) failed. The payload stays encoded, and the next demand
\*     leads a fresh attempt.
\*   - "failDecoded": the value was installed and syncResultSnapshotLeases
\*     then failed (AttachLease can fail). The payload stays decoded and
\*     persistSyncPending is set, so later readers do not take the fast
\*     path: the next demand leads a sync-only attempt that retries the
\*     lease reconciliation. Joiners already parked read the latched
\*     error and fail (a genuine failure is not classified retryable).
\* The finish computes persistSyncPending exactly as the Go finish does:
\* installed and failed. "ok" therefore also clears the flag after a
\* successful sync-only attempt.
DecodeLeadFinish(i) ==
    /\ invocations[i].phase = "decoding"
    /\ LET r == invocations[i].resId
           installed == res[r].payload = "decoded"
       IN
       \E outcome \in {"ok"} \cup
             (IF DecodeCanFail THEN {"failEnvelope", "failDecoded"} ELSE {}) :
          /\ outcome = "ok" => installed
          /\ outcome = "failEnvelope" => ~installed
          /\ outcome = "failDecoded" => installed
          /\ res' = [res EXCEPT ![r].decodePhase = "idle",
                                ![r].decodeErr = IF outcome = "ok"
                                                 THEN "none" ELSE "fail",
                                ![r].decodeGen = @ + 1,
                                ![r].persistSyncPending =
                                     outcome # "ok" /\ installed]
          /\ invocations' = [invocations EXCEPT ![i].phase =
               IF outcome = "ok" THEN "readBarrier" ELSE "decodeErr"]
    /\ UNCHANGED <<ongoingCalls, ongoingCallIndex, sessionEdges, countedEdges,
                   sessionRelease, evals, epoch, flushed>>

\* DecodeJoin: a reader finds an attempt published and parks on its
\* channel. joinedGen records which attempt; the channel closes when
\* decodeGen moves past it. A reader only reaches the singleflight while
\* the payload is encoded or its lease sync is pending; during a fresh
\* attempt's post-install window (payload decoded, flag still FALSE) it
\* takes the fast path instead and never joins.
DecodeJoin(i) ==
    /\ invocations[i].phase = "readBarrier"
    /\ LET r == invocations[i].resId IN
       /\ res[r].barrier \in {"none", "closedOk"}
       /\ \/ res[r].payload = "envelope"
          \/ res[r].payload = "decoded" /\ res[r].persistSyncPending
       /\ res[r].decodePhase = "running"
       /\ invocations' = [invocations EXCEPT ![i].phase = "decodeJoined",
                                              ![i].joinedGen = res[r].decodeGen]
    /\ UNCHANGED <<res, ongoingCalls, ongoingCallIndex, sessionEdges, countedEdges,
                   sessionRelease, evals, epoch, flushed>>

\* DecodeWake: the joiner's select took its closed channel - its attempt's
\* finish or cancellation moved decodeGen past joinedGen - and it re-reads
\* the CURRENT latched error and its retry classification
\* (persistDecodeErr and persistDecodeRetry): a genuine failure ("fail")
\* is returned as the joiner's own failure; a departed leader's
\* cancellation ("cancel", classified retryable by
\* lazyEvalErrorCausedByContext against the leader's ctx) sends the
\* joiner back to the top of the read barrier, exactly like "none" (the
\* attempt succeeded or a newer leader reset the error). There it returns
\* an installed value (ReadBarrierOk), rejoins the running attempt
\* (DecodeJoin), or leads (DecodeLead). Nothing here depends on
\* decodePhase: a newer attempt may well be running. Go's retrying joiner
\* first checks its own context and returns its own cause if it is dead;
\* that check folds into the cancel actions reachable from the phases the
\* retry passes through (DecodeJoinCancel, DecodeLeadCancel), which stay
\* enabled for it, so the outcome is the same.
DecodeWake(i) ==
    /\ invocations[i].phase = "decodeJoined"
    /\ LET r == invocations[i].resId IN
       /\ res[r].decodeGen > invocations[i].joinedGen
       /\ invocations' = [invocations EXCEPT ![i].phase =
            IF res[r].decodeErr = "fail"
            THEN "decodeErr" ELSE "readBarrier"]
    /\ UNCHANGED <<res, ongoingCalls, ongoingCallIndex, sessionEdges, countedEdges,
                   sessionRelease, evals, epoch, flushed>>

(***************************************************************************)
(* DECODE CANCELLATION (ReaderCanCancel).                                  *)
(*                                                                         *)
(* DecodeLeadCancel: the leader's own context is canceled while it         *)
(* decodes. Go: the leader branch of ensurePersistedHitValueLoaded. The    *)
(* decode runs on the leader's request context, so a ctx-aware step        *)
(* returns the context error, finishPersistDecode latches it, clears the   *)
(* channel, and closes it (decodeGen moves on), and the leader returns the *)
(* error as its own. Two sites, told apart by whether the install          *)
(* (DecodeInstall) has already happened; the action is the same:           *)
(*   - before install: inside DecodeResult, or in the resolver lookup      *)
(*     before it - a snapshot reopen                                       *)
(*     (loadPersistedImmutableSnapshotByResultID), a referenced-result     *)
(*     load (LoadResultByResultID), resultServerForCall. The payload stays *)
(*     encoded and the next demand leads a fresh attempt.                  *)
(*   - after install: syncResultSnapshotLeases, still on the leader's ctx, *)
(*     returned the context error. The payload stays decoded and           *)
(*     persistSyncPending is set (the finish computes it as installed and  *)
(*     failed), so later readers lead or join a sync-only attempt that     *)
(*     retries the lease reconciliation instead of serving the value       *)
(*     over an incompletely synchronized lease set. This also covers a     *)
(*     canceled sync-only leader: the flag simply stays set.               *)
(* The latch, retry classification, clear, and close are one action. What  *)
(* each joiner then sees is decided by the generation mechanism in         *)
(* DecodeWake, including the case where a newer leader published a fresh   *)
(* channel, and possibly installed the value, before this attempt's        *)
(* joiners woke; a joiner that reads this attempt's latched "cancel"       *)
(* retries rather than failing. A canceled leader whose decode has no      *)
(* ctx-aware step still succeeds, because nothing re-checks ctx after the  *)
(* decode: DecodeInstall and DecodeLeadFinish stay enabled for it. This    *)
(* is the leader's own cancellation, so ownCancel is set.                  *)
(***************************************************************************)
DecodeLeadCancel(i) ==
    /\ ReaderCanCancel
    /\ invocations[i].phase = "decoding"
    /\ LET r == invocations[i].resId IN
       /\ res' = [res EXCEPT ![r].decodePhase = "idle",
                             ![r].decodeErr = "cancel",
                             ![r].decodeGen = @ + 1,
                             ![r].persistSyncPending =
                                  res[r].payload = "decoded"]
       /\ invocations' = [invocations EXCEPT ![i].phase = "canceling",
                                              ![i].ownCancel = TRUE]
    /\ UNCHANGED <<ongoingCalls, ongoingCallIndex, sessionEdges, countedEdges,
                   sessionRelease, evals, epoch, flushed>>

\* DecodeJoinCancel: a parked joiner's select takes its own ctx.Done arm
\* (the joiner branch of ensurePersistedHitValueLoaded, which returns
\* context.Cause(ctx)). Go's select picks among ready arms
\* nondeterministically, so this fires whether or not the leader has
\* already finished. Nothing shared changes; the session edge remains.
DecodeJoinCancel(i) ==
    /\ ReaderCanCancel
    /\ invocations[i].phase = "decodeJoined"
    /\ invocations' = [invocations EXCEPT ![i].phase = "canceling",
                                           ![i].ownCancel = TRUE]
    /\ UNCHANGED <<res, ongoingCalls, ongoingCallIndex, sessionEdges, countedEdges,
                   sessionRelease, evals, epoch, flushed>>

\* Decode failed for a HIT-path caller.
DecodeFailHit(i) ==
    /\ invocations[i].phase = "decodeErr"
    /\ invocations[i].path = "hit"
    /\ invocations' = [invocations EXCEPT ![i].phase = "failing"]
    /\ UNCHANGED <<res, ongoingCalls, ongoingCallIndex, sessionEdges, countedEdges, sessionRelease,
                   evals, epoch, flushed>>

\* Decode failed for a WAIT-path caller: the error just propagates; the
\* session edge claimed after publication remains (wait, cache.go:4878-4881).
DecodeFailWait(i) ==
    /\ invocations[i].phase = "decodeErr"
    /\ invocations[i].path = "wait"
    /\ invocations' = [invocations EXCEPT ![i].phase = "failing"]
    /\ UNCHANGED <<res, ongoingCalls, ongoingCallIndex, sessionEdges, countedEdges,
                   sessionRelease, evals, epoch, flushed>>

\* Decrement an admitted operation. When this is the last operation after a
\* release plan was published, cleanup becomes irrevocably assigned to this
\* operation before its goroutine returns.
FinishSessionOperation(sr, s) ==
    LET old == sr[s]
        remaining == old.active - 1
        nextPhase == IF remaining = 0 /\ old.phase = "deferred"
                     THEN "collecting" ELSE old.phase
    IN [sr EXCEPT ![s] = [old EXCEPT !.active = remaining,
                                      !.phase = nextPhase]]

\* Operation exit is the return boundary. A successful value is changed to a
\* released-session refusal when release won before this atomic decrement.
\* Cleanup cannot run before the decrement because the active count is still
\* positive; if this is the last operation, the same transition assigns it.
InvocationOperationExit(i) ==
    /\ invocations[i].phase \in InvocationExitPhases
    /\ invocations[i].opActive
    /\ LET s == invocations[i].sess
           r == invocations[i].resId
           success == invocations[i].phase = "returning"
           lateRefusal == success /\ sessionRelease[s].phase # "live"
           terminal == CASE lateRefusal -> "refused"
                         [] invocations[i].phase = "returning" -> "done"
                         [] invocations[i].phase = "failing" -> "failed"
                         [] invocations[i].phase = "canceling" -> "canceled"
                         [] OTHER -> "refused"
       IN /\ sessionRelease[s].active > 0
          /\ sessionRelease' = FinishSessionOperation(sessionRelease, s)
          /\ invocations' = [invocations EXCEPT
               ![i].phase = terminal,
               ![i].opActive = FALSE,
               ![i].refusedEpoch = IF lateRefusal THEN epoch ELSE @,
               ![i].retLive = IF success
                    THEN res[r].registered /\ ~res[r].released ELSE @,
               ![i].retOwned = IF success
                    THEN ProtectedReturn(s, r) ELSE @,
               ![i].retBarrierOK = IF success
                    THEN res[r].barrier \in {"none", "closedOk"} ELSE @,
               ![i].retClean = IF success THEN ~res[r].laundered ELSE @,
               \* the true transitive requirement of the returned result is a
               \* subset of the session's bound handles: the session was never
               \* handed a result depending on a handle leaf it never bound.
               ![i].retGated = IF success
                    THEN TrueRequired(r) \subseteq sessionRelease[s].handles ELSE @]
    /\ UNCHANGED <<res, ongoingCalls, ongoingCallIndex, sessionEdges,
                   countedEdges, evals, epoch, flushed>>

(***************************************************************************)
(* Release first sets the tombstone with an atomic compare-and-swap. It    *)
(* then snapshots session edges while holding the session mutex and        *)
(* publishes an immutable cleanup plan. If operations remain, release      *)
(* returns with phase deferred. The last operation exit moves the plan to  *)
(* collecting. Cleanup removes ownership under the e-graph mutex, runs     *)
(* release hooks, and finally deletes session records under the session    *)
(* mutex. Release can begin at any time permitted by DrainOnRelease.       *)
(***************************************************************************)

\* Atomic lifecycle-word update: new operation admission and tombstoning are
\* totally ordered even though neither takes the cache-wide session mutex.
ReleaseSessionMark(s) ==
    /\ AllowRelease
    /\ sessionRelease[s].phase = "live"
    /\ DrainOnRelease =>
         \A i \in InvocationIds :
             (invocations[i].sess = s /\ invocations[i].origin = "handler")
                 => invocations[i].phase \in TerminalPhases
    /\ sessionRelease' = [sessionRelease EXCEPT ![s].phase = "marking"]
    /\ UNCHANGED <<invocations, res, ongoingCalls, ongoingCallIndex,
                   sessionEdges, countedEdges, evals, epoch, flushed>>

\* The session-mutex section snapshots records. The release mutex prevents a
\* last-operation cleanup from consuming the plan before it is published.
ReleaseSessionSnapshot(s) ==
    /\ sessionRelease[s].phase = "marking"
    /\ LET snap == {r \in ResultIds : <<s, r>> \in sessionEdges}
           old == sessionRelease[s]
       IN /\ sessionRelease' = [sessionRelease EXCEPT ![s] =
                [old EXCEPT !.phase = IF old.active = 0
                                     THEN "collecting" ELSE "deferred",
                            !.snap = snap]]
    /\ UNCHANGED <<invocations, res, ongoingCalls, ongoingCallIndex,
                   sessionEdges, countedEdges, evals, epoch, flushed>>

\* The egraphMu section: remove one unit per snapshotted, still-registered
\* result, then collect. Session records remain until hooks and arbitrary
\* value cleanup complete.
ReleaseSessionCollect(s) ==
    /\ sessionRelease[s].phase = "collecting"
    /\ sessionRelease[s].active = 0
    /\ LET snap == sessionRelease[s].snap
           live == {r \in snap : res[r].registered}
           rf0 == [r \in DOMAIN res |->
                     IF r \in live
                     THEN [res[r] EXCEPT !.own = @ - 1]
                     ELSE res[r]]
       IN /\ res' = Cascade(rf0)
          /\ countedEdges' = countedEdges \ {<<s, r>> : r \in snap}
          /\ sessionRelease' = [sessionRelease EXCEPT ![s].phase = "deleting"]
    /\ UNCHANGED <<invocations, ongoingCalls, ongoingCallIndex,
                   sessionEdges, evals, epoch, flushed>>

\* Release hooks and arbitrary-value cleanup run before this final
\* session-mutex section deletes every per-session record.
ReleaseSessionDelete(s) ==
    /\ sessionRelease[s].phase = "deleting"
    /\ sessionEdges' = {e \in sessionEdges : e[1] # s}
    /\ sessionRelease' = [sessionRelease EXCEPT ![s] =
         [sessionRelease[s] EXCEPT !.phase = "released", !.snap = {},
                                   !.handles = {}]]
    /\ UNCHANGED <<invocations, res, ongoingCalls, ongoingCallIndex,
                   countedEdges, evals, epoch, flushed>>

(***************************************************************************)
(* BindResource: a session binds a session-resource handle. Go:            *)
(* BindSessionResource (cache.go:588) inserts the handle into              *)
(* sessionHandlesBySession under sessionMu and refuses a released session  *)
(* (the released tombstone bit). Binding is not a counted cache operation. *)
(* A handle leaf's resolver calls this before returning the leaf (for      *)
(* example core/schema/secret.go: WithSessionResourceHandle then           *)
(* BindSessionResource, both before return), which is why PubIndexFresh    *)
(* may publish a leaf whose own handle the session has already bound. The   *)
(* per-session bound set is stored in the sessionRelease record's handles  *)
(* field, cleared when the record is deleted (ReleaseSessionDelete).       *)
(* Disabled for lack of a handle when Handles = {}.                        *)
(***************************************************************************)
BindResource ==
    /\ \E s \in Sessions, h \in Handles :
        /\ sessionRelease[s].phase = "live"
        /\ h \notin sessionRelease[s].handles
        /\ sessionRelease' = [sessionRelease EXCEPT ![s].handles = @ \cup {h}]
    /\ UNCHANGED <<invocations, res, ongoingCalls, ongoingCallIndex,
                   sessionEdges, countedEdges, evals, epoch, flushed>>

(***************************************************************************)
(* AddDepLate: an explicit dependency edge is added to a result that was   *)
(* already published. Go: Cache.AddExplicitDependency ->                   *)
(* addExplicitDependencyLocked, one egraphMu critical section; the one     *)
(* caller today attaches generated module types to a published module      *)
(* container (core/sdk/module_typedefs.go). The caller holds both          *)
(* results, so both barriers are settled. The edge must not form a cycle   *)
(* (the stated no-cycle assumption), and the stored required sets cascade  *)
(* upward exactly as in PubAttachAddDep.                                   *)
(***************************************************************************)
AddDepLate ==
    /\ ModelLateDeps
    /\ \E p \in ResultIds, d \in ResultIds :
        /\ p # d
        /\ res[p].registered
        /\ res[d].registered
        /\ res[p].barrier \in {"none", "closedOk"}
        /\ res[d].barrier \in {"none", "closedOk"}
        /\ d \notin res[p].deps
        /\ ~DepReachable(res, d, p)
        \* The caller possesses both results: the one production caller
        \* loads the parent and the dep through its own session context
        \* before calling AddExplicitDependency, so some session holds a
        \* counted edge to each. Ownership proves each passed the filter
        \* when obtained; requirements may have grown since, and the code
        \* checks nothing further at this call. Without possession the
        \* edge could appear with no modeled caller able to produce it.
        /\ \E s \in Sessions :
            /\ <<s, p>> \in countedEdges
            /\ <<s, d>> \in countedEdges
        /\ LET newDeps == res[p].deps \cup {d}
               newReq  == CascadeRequired(res, p, newDeps)
           IN res' = [x \in DOMAIN res |->
                IF x = p THEN [res[x] EXCEPT !.deps = newDeps,
                                             !.required = newReq[x]]
                ELSE IF x = d THEN [res[x] EXCEPT !.own = @ + 1,
                                                  !.required = newReq[x]]
                ELSE [res[x] EXCEPT !.required = newReq[x]]]
    /\ UNCHANGED <<invocations, ongoingCalls, ongoingCallIndex, sessionEdges,
                   countedEdges, sessionRelease, evals, epoch, flushed>>

(***************************************************************************)
(* PruneCut: cut one persisted root edge and let the normal cascade        *)
(* collect whatever that leaves unowned. Fireable at any time.             *)
(*                                                                         *)
(* Go: removePersistedEdge, cache.go:1294-1321. This one action is the     *)
(* only way either prune mode (disk-policy or structural) touches the      *)
(* live kernel; all prune planning happens outside the lock and is not     *)
(* modeled.                                                                *)
(***************************************************************************)
PruneCut(r) ==
    /\ AllowPruneCut
    /\ r \in ResultIds
    /\ res[r].persisted
    /\ res' = DecAndCascade([res EXCEPT ![r].persisted = FALSE], r)
    /\ UNCHANGED <<invocations, ongoingCalls, ongoingCallIndex, sessionEdges, countedEdges,
                   sessionRelease, evals, epoch, flushed>>

(***************************************************************************)
(* FLUSH AND RESTART. Graceful shutdown calls ReleaseSession for every     *)
(* session before Cache.Close begins. A returned release may still have a  *)
(* deferred cleanup plan. Close atomically refuses new operations, waits   *)
(* until all session and cache-wide operations and deferred cleanups are    *)
(* quiescent, and only then snapshots the retained graph. If its context    *)
(* expires first, no Flush action occurs and the store remains dirty.       *)
(*                                                                         *)
(* Snapshot selection starts at persisted roots. A root is kept only when  *)
(* its entire transitive dependency closure is registered and cleanly      *)
(* attached; every kept row is reachable from such a root. If any node is  *)
(* open, errored, or missing, that node and every root depending on it are  *)
(* rejected.                                                               *)
(*                                                                         *)
(* Each candidate row also records two verdicts used only by properties:   *)
(*   dirty    - the attach barrier was not cleanly closed at capture       *)
(*              (still open, or closed with an error). The Go writes no    *)
(*              such marker: attachDeps state lives only in memory, which  *)
(*              is why the snapshot must reject them rather than rely on   *)
(*              restart to remember the failure.                           *)
(*   ownClean - ownership at capture was fully explained by persisted +    *)
(*              dependency edges, nothing transient.                       *)
(***************************************************************************)

CleanAttachedResult(r) ==
    res[r].registered /\ res[r].barrier \in {"none", "closedOk"}

CleanPersistedRoot(r) ==
    /\ CleanAttachedResult(r)
    /\ res[r].persisted
    /\ \A d \in ResultIds :
         DepReachable(res, r, d) => CleanAttachedResult(d)

KeptByPersistedRoot(r) ==
    \E root \in ResultIds :
        CleanPersistedRoot(root) /\ DepReachable(res, root, r)

\* GracefulStop has called every ReleaseSession to its non-blocking return
\* point. This atomic closing flag rejects every later cache admission.
BeginClose ==
    /\ ModelPersistence
    /\ epoch = 1
    /\ ~flushed.done
    /\ ~flushed.closing
    /\ \A s \in Sessions :
         sessionRelease[s].phase \in {"deferred", "collecting", "deleting", "released"}
    /\ flushed' = [flushed EXCEPT !.closing = TRUE]
    /\ UNCHANGED <<invocations, res, ongoingCalls, ongoingCallIndex,
                   sessionEdges, countedEdges, sessionRelease, evals, epoch>>

Flush ==
    /\ ModelPersistence
    /\ epoch = 1
    /\ ~flushed.done
    /\ flushed.closing
    \* Quiescence includes the operation counters and completed deferred
    \* cleanup, not merely ReleaseSession's return.
    /\ \A s \in Sessions : sessionRelease[s].active = 0
    /\ \A s \in Sessions : sessionRelease[s].phase = "released"
    /\ flushed' = [flushed EXCEPT !.done = TRUE,
         !.rows = [r \in 1..Len(res) |->
         [keep      |-> KeptByPersistedRoot(r),
          call      |-> res[r].call,
          persisted |-> res[r].persisted,
          deps      |-> res[r].deps,
          \* the row's own session-resource handle survives the flush
          \* (env.SessionResourceHandle); required is recomputed at import.
          handle    |-> res[r].handle,
          dirty     |-> res[r].barrier \in {"open", "closedErr"},
          ownClean  |-> res[r].own =
              PersistedCount(r) + DepParentCount(r)]]]
    /\ UNCHANGED <<invocations, res, ongoingCalls, ongoingCallIndex,
                   sessionEdges, countedEdges, sessionRelease,
                   evals, epoch>>

(***************************************************************************)
(* Restart: the process dies and comes back, importing the snapshot.       *)
(* Everything in-memory-only is gone: in-flight calls, ongoing-call        *)
(* bookkeeping, evaluators, session edges, attach-barrier state, decode    *)
(* state, latched errors - and, crucially, the knowledge that a dirty      *)
(* row was dirty. Imported results come back with ownership recomputed     *)
(* from their persisted + dependency edges (importPersistedState's         *)
(* increments), payloads nondeterministically decoded-eagerly or left as   *)
(* envelopes, and numeric IDs rebuilt from retained row IDs. Omitted       *)
(* record slots stay as dead husks for property bookkeeping.               *)
(* The laundered flag carries each row's dirty verdict forward so the      *)
(* NoLaunderedServe property can see what the restarted engine cannot.     *)
(***************************************************************************)

Restart ==
    /\ ModelPersistence
    /\ epoch = 1
    /\ flushed.done
    /\ epoch' = 2
    /\ \E pm \in [1..Len(flushed.rows) -> {"decoded", "envelope"}] :
       \* required is recomputed at import exactly as InitialResStates does:
       \* the dependency-first walk over the kept rows, giving every row its
       \* transitive requirement regardless of map iteration order.
         LET kept     == {x \in 1..Len(flushed.rows) : flushed.rows[x].keep}
             handleOf == [x \in kept |-> flushed.rows[x].handle]
             depsOf   == [x \in kept |-> flushed.rows[x].deps]
             required == ImportRequiredFinal(kept, handleOf, depsOf)
         IN res' = [x \in 1..Len(flushed.rows) |->
             IF flushed.rows[x].keep
             THEN ImportedResult(
                    flushed.rows[x].call, flushed.rows[x].persisted, flushed.rows[x].deps,
                    (IF flushed.rows[x].persisted THEN 1 ELSE 0)
                      + Cardinality({y \in 1..Len(flushed.rows) :
                            flushed.rows[y].keep /\ x \in flushed.rows[y].deps}),
                    pm[x],
                    flushed.rows[x].dirty,
                    flushed.rows[x].handle,
                    required[x])
             ELSE DeadHusk]
    /\ invocations' = invocations
    /\ ongoingCalls' = [o \in OngoingCallIds |->
         \* "exited", not "canceled": a restart kills the process, so no
         \* executor is winding down and no nested call can spawn from it
         [ongoingCalls[o] EXCEPT !.fnState = "exited", !.pubState = "none",
                                 !.pubBy = 0,
                                 !.hold = FALSE, !.inIndex = FALSE,
                                 !.needsPersistedEdge = FALSE,
                                 !.waiters = 0]]
    /\ ongoingCallIndex' = [k \in Calls \X Sessions |-> 0]
    \* A restart kills the process: evaluator goroutines die with it, so no
    \* evaluator record survives, terminal or not. (Invocations differ:
    \* they are retained across restart with epoch bookkeeping because the
    \* refusal properties must still judge pre-restart refusals; no
    \* evaluator property needs that, and a retained "done" evaluator would
    \* pair with a rebuilt incomplete result and falsely trip
    \* EvalDoneComplete.) Clearing also resets the MaxEvals budget: a new
    \* process gets new callers.
    /\ evals' = <<>>
    /\ sessionEdges' = {}
    /\ countedEdges' = {}
    /\ sessionRelease' = [s \in Sessions |->
         [phase |-> "live", snap |-> {}, active |-> 0, exitingLazy |-> 0,
          handles |-> {}]]
    /\ flushed' = [flushed EXCEPT !.closing = FALSE]

---------------------------------------------------------------------------
(***************************************************************************)
(* LAZY EVALUATION. A resolver can return a result whose                   *)
(* expensive materialization is deferred: the value carries a callback,    *)
(* stored on the sharedResult by registerLazyEvaluation.                   *)
(* Anyone later needing the materialized value calls Cache.Evaluate,       *)
(* which coordinates all callers per result in evaluateOne:                *)
(*   - if evaluation already completed, return immediately                 *)
(*   - if an attempt is in flight, join it as a waiter                     *)
(*   - otherwise start the callback in a goroutine and wait               *)
(* Success is permanent: lazyEvalComplete is set and the callback is       *)
(* cleared. Failure leaves the callback in place so a later Evaluate can   *)
(* retry. Each waiter can abandon independently; the LAST waiter to leave  *)
(* a running attempt cancels that attempt's callback context.              *)
(*                                                                         *)
(* Attempt state is separate from permanent result state. The result       *)
(* publishes one current attempt while its callback runs. Callback finish  *)
(* latches the outcome on that attempt and unpublishes it under lazyMu;     *)
(* closing the attempt's done channel is the following lock-free region.   *)
(* Existing waiters retain the retired attempt record, so they cannot read *)
(* or decrement a retry's state. A new attempt can start after callback    *)
(* finish even before old waiters consume their outcome.                   *)
(*                                                                         *)
(* A healthy waiter may have joined an attempt after every earlier waiter  *)
(* abandoned and requested cancellation. If that callback returns its     *)
(* shared context's cancellation, the healthy waiter treats it as an       *)
(* intermediate retry outcome, not a returned error. Its own cancellation  *)
(* remains terminal, and a genuine callback failure still propagates.      *)
(*                                                                         *)
(* Each Evaluate caller holds one session operation token. Starting a      *)
(* callback takes a second token for the callback goroutine. Callback       *)
(* completion retires the attempt and closes its done channel before that   *)
(* second token exits, so exitingLazy represents the small but real tail.   *)
(* It is a saturating 0/1 abstraction: several overlapping tails still     *)
(* retain one release hold, and their final exit clears it.                 *)
(*                                                                         *)
(* One attempt has two stages, and evaluateOne consults them before any    *)
(* object-side lazy state: callback bodies (Directory/File/Container)      *)
(* clear their object-side callback pointer while the attempt is still     *)
(* running its cache-side bookkeeping (snapshot-lease sync, lease          *)
(* release), so object-side state is only trustworthy when no attempt is   *)
(* published. Model phases of one attempt (res[r].lazyPhase):              *)
(*   "idle"     no attempt is published                                    *)
(*   "running"  the callback body is running (lazyCb still armed)          *)
(*   "syncing"  the body succeeded and consumed the object-side callback   *)
(*              (lazyCb now "none"); the same goroutine is running the     *)
(*              cache-side bookkeeping                                     *)
(* res[r].lazyCancel means every waiter abandoned and the last one         *)
(* invoked the attempt's cancel; the attempt keeps running and remains     *)
(* joinable. res[r].lazySyncPending means a previous attempt's body        *)
(* succeeded but its bookkeeping did not: the next attempt starts in       *)
(* "syncing" with no body, retrying only the bookkeeping.                  *)
(*                                                                         *)
(* Evaluator latched phases mean callback finish stored an attempt-local   *)
(* outcome and retired the attempt, but that attempt's done-channel close  *)
(* has not yet become observable to this waiter. Wake phases mean the      *)
(* close is observable and the select can consume either completion or the *)
(* waiter's own cancellation.                                              *)
(***************************************************************************)

LatchEvalOutcomes(r, outcome) ==
    [e \in DOMAIN evals |->
        IF evals[e].phase = "waiting" /\ evals[e].target = r
        THEN [evals[e] EXCEPT !.phase = outcome,
                                  !.foreignCancel = (outcome = "latchedCancel")]
        ELSE evals[e]]

\* A new Evaluate caller appears, demanding some registered result its
\* session owns. A caller can only hold a result whose attachment barrier
\* is not open and not errored: every result-returning API waits at the
\* barrier and returns errors instead of values. Lazy callbacks are
\* registered at attachment completion (registerLazyEvaluation,
\* immediately before the barrier closes) and again when a persisted hit's
\* value is loaded; both sites run after the barrier settles. "none"
\* covers results that never armed a fresh barrier, such as imported rows
\* and adopted cache-backed values. Admission is atomic with the session
\* tombstone check: a caller reaching the cache after release is refused
\* without increasing the count.
EvalSpawn ==
    /\ ModelLazy
    /\ Len(evals) < MaxEvals
    /\ ~flushed.closing
    /\ \E r \in ResultIds, s \in Sessions :
        /\ res[r].registered
        /\ res[r].barrier \notin {"open", "closedErr"}
        /\ <<s, r>> \in sessionEdges
        /\ LET admitted == sessionRelease[s].phase = "live" IN
           /\ evals' = Append(evals,
                [target |-> r, sess |-> s,
                 phase |-> IF admitted THEN "demand" ELSE "refused",
                 opActive |-> admitted, foreignCancel |-> FALSE])
           /\ sessionRelease' = IF admitted
                THEN [sessionRelease EXCEPT ![s].active = @ + 1]
                ELSE sessionRelease
    /\ UNCHANGED <<invocations, res, ongoingCalls, ongoingCallIndex,
                   sessionEdges, countedEdges, epoch, flushed>>

\* A hit loads an imported result's value and registers its lazy work:
\* ensurePersistedHitValueLoaded calls registerLazyEvaluation after the
\* value (possibly just decoded) is in memory, and persisted payloads can
\* carry deferred work - a lazy container persists its recipe
\* (core/container.go, persistedContainerFormLazy). Whether a given row
\* carries lazy work is payload data the model does not track, so arming
\* is optional: rows without lazy work are the paths where this never
\* fires. Registration reads object-side state under lazyMu only with no
\* attempt published, and is a no-op once a callback is stored, evaluation
\* completed, or bookkeeping is pending, hence the guards. Fresh results
\* arm at publication (PubIndexFresh), never here. After a restart every
\* kept row is imported again and may re-arm, matching a store row whose
\* payload still carries its recipe.
ImportedLazyArm(r) ==
    /\ ModelLazy
    /\ r \in ResultIds
    /\ res[r].registered
    /\ res[r].imported
    /\ res[r].payload = "decoded"
    /\ res[r].lazyCb = "none"
    /\ ~res[r].lazyComplete
    /\ ~res[r].lazySyncPending
    /\ res[r].lazyPhase = "idle"
    /\ res' = [res EXCEPT ![r].lazyCb = "armed"]
    /\ UNCHANGED <<invocations, ongoingCalls, ongoingCallIndex, sessionEdges,
                   countedEdges, sessionRelease, evals, epoch, flushed>>

\* Fast path under lazyMu: evaluation already completed, or nothing
\* deferred remains anywhere - no published attempt, no object-side
\* callback, no pending bookkeeping. Go latches lazyEvalComplete on that
\* second shape. Requiring the attempt and bookkeeping checks is the fix
\* for the bypass bug: trusting the consumed object-side callback while
\* its attempt was still running bookkeeping reported success early.
EvalNoWork(e) ==
    /\ evals[e].phase = "demand"
    /\ LET r == evals[e].target IN
       /\ \/ res[r].lazyComplete
          \/ /\ res[r].lazyPhase = "idle"
             /\ res[r].lazyCb = "none"
             /\ ~res[r].lazySyncPending
       /\ res' = [res EXCEPT ![r].lazyComplete = TRUE]
    /\ evals' = [evals EXCEPT ![e].phase = "returnDone",
                              ![e].foreignCancel = FALSE]
    /\ UNCHANGED <<invocations, ongoingCalls, ongoingCallIndex,
                   sessionEdges, countedEdges, sessionRelease,
                   epoch, flushed>>

\* Start an attempt: no attempt is published, so this caller becomes the
\* leader. One lazyMu critical section publishes a fresh attempt record whose
\* wait targets, done channel, cancel function, and waiter count are already
\* initialized. An armed callback leads a full attempt ("running"); pending
\* bookkeeping leads a bookkeeping-only attempt that starts directly in
\* "syncing". Here an armed callback and pending bookkeeping cannot coexist
\* (body success consumes lazyCb before bookkeeping can fail); Go's
\* evaluateOne enforces the same precedence directly by not re-reading the
\* value's callback while lazySyncPending is set, so the correspondence does
\* not depend on implementations clearing their callback.
EvalStartAttempt(e) ==
    /\ evals[e].phase = "demand"
    /\ LET r == evals[e].target
           s == evals[e].sess
       IN
       /\ ~res[r].lazyComplete
       /\ res[r].lazyPhase = "idle"
       /\ res[r].lazyCb = "armed" \/ res[r].lazySyncPending
       /\ sessionRelease[s].phase = "live"
       /\ res' = [res EXCEPT
            ![r].lazyPhase = IF res[r].lazyCb = "armed"
                             THEN "running" ELSE "syncing",
            ![r].lazyCancel = FALSE,
            ![r].lazyWaiters = @ + 1,
            ![r].lazyRunning = @ + 1,
            ![r].lazyTokenSession = s]
       /\ evals' = [evals EXCEPT ![e].phase = "waiting",
                                 ![e].foreignCancel = FALSE]
       /\ sessionRelease' = [sessionRelease EXCEPT ![s].active = @ + 1]
    /\ UNCHANGED <<invocations, ongoingCalls, ongoingCallIndex,
                   sessionEdges, countedEdges, epoch, flushed>>

\* Release may win after the Evaluate waiter entered but before it tries to
\* create an attempt. Callback-token admission then refuses the start.
EvalStartAttemptRefused(e) ==
    /\ evals[e].phase = "demand"
    /\ LET r == evals[e].target
           s == evals[e].sess
       IN /\ res[r].lazyCb = "armed" \/ res[r].lazySyncPending
          /\ ~res[r].lazyComplete
          /\ res[r].lazyPhase = "idle"
          /\ sessionRelease[s].phase # "live"
          /\ evals' = [evals EXCEPT ![e].phase = "returnRefused",
                                    ![e].foreignCancel = FALSE]
    /\ UNCHANGED <<invocations, res, ongoingCalls, ongoingCallIndex,
                   sessionEdges, countedEdges, sessionRelease,
                   epoch, flushed>>

\* Join a published attempt in either stage, canceled or not: Go's join
\* checks only that an attempt is published, and cancellation is
\* cooperative. The joiner retains this attempt record and retries if its
\* outcome is foreign cancellation.
EvalJoin(e) ==
    /\ evals[e].phase = "demand"
    /\ LET r == evals[e].target IN
       /\ ~res[r].lazyComplete
       /\ res[r].lazyPhase \in {"running", "syncing"}
       /\ res' = [res EXCEPT ![r].lazyWaiters = @ + 1]
       /\ evals' = [evals EXCEPT ![e].phase = "waiting",
                                 ![e].foreignCancel = FALSE]
    /\ UNCHANGED <<invocations, ongoingCalls, ongoingCallIndex,
                   sessionEdges, countedEdges, sessionRelease,
                   epoch, flushed>>

\* The callback body finishes while the attempt keeps running. Success
\* consumes the object-side callback (Directory, File, and Container clear
\* their Lazy pointer inside the body) and the same goroutine proceeds to
\* the cache-side bookkeeping stage. Failure ends the whole attempt: the
\* outcome is latched on the retained waiters and the attempt is retired,
\* with the body left armed for a later attempt. The retry decision is
\* per-error (lazyEvalErrorCausedByContext), not per-stage: a failure
\* attributable to the attempt's canceled callback context latches as
\* foreign cancellation, which healthy waiters retry, while a genuine body
\* failure (LazyCanFail) propagates even when cancellation was requested.
EvalBodyFinish(r) ==
    /\ r \in ResultIds
    /\ res[r].lazyPhase = "running"
    /\ res[r].lazyRunning > 0
    /\ res[r].lazyTokenSession \in Sessions
    /\ LET s == res[r].lazyTokenSession IN
       \E outcome \in ({"bodyOk"}
            \cup (IF LazyCanFail THEN {"bodyFail"} ELSE {})
            \cup (IF res[r].lazyCancel THEN {"bodyCancel"} ELSE {})) :
        IF outcome = "bodyOk"
        THEN /\ res' = [res EXCEPT ![r].lazyCb = "none",
                                   ![r].lazyPhase = "syncing"]
             \* The same goroutine continues into bookkeeping, so the
             \* callback token stays held and no exit is staged.
             /\ UNCHANGED <<evals, sessionRelease>>
        ELSE /\ res' = [res EXCEPT
                  ![r].lazyRunning = @ - 1,
                  ![r].lazyPhase = "idle",
                  ![r].lazyCancel = FALSE,
                  ![r].lazyWaiters = 0,
                  ![r].lazyTokenSession = 0]
             /\ evals' = LatchEvalOutcomes(r,
                  IF outcome = "bodyFail"
                  THEN "latchedFail" ELSE "latchedCancel")
             /\ sessionRelease' = IF sessionRelease[s].exitingLazy = 0
                  THEN [sessionRelease EXCEPT ![s].exitingLazy = 1]
                  ELSE FinishSessionOperation(sessionRelease, s)
    /\ UNCHANGED <<invocations, ongoingCalls, ongoingCallIndex,
                   sessionEdges, countedEdges, epoch, flushed>>

\* The cache-side bookkeeping finishes and the attempt is retired under
\* lazyMu. Success marks evaluation complete. Any failure here records
\* lazySyncPending: the body's object-side state is already consumed, so
\* only the bookkeeping remains retryable and the next attempt starts in
\* "syncing" with no body. LazyCanFail doubles as the fault switch for
\* bookkeeping failure (in Go it is environment failure, such as the
\* snapshot manager, rather than the callback's own work); a
\* cancellation-shaped bookkeeping failure latches as foreign cancellation
\* exactly like a canceled body.
EvalSyncFinish(r) ==
    /\ r \in ResultIds
    /\ res[r].lazyPhase = "syncing"
    /\ res[r].lazyRunning > 0
    /\ res[r].lazyTokenSession \in Sessions
    /\ LET s == res[r].lazyTokenSession IN
       \E outcome \in ({"syncOk"}
            \cup (IF LazyCanFail THEN {"syncFail"} ELSE {})
            \cup (IF res[r].lazyCancel THEN {"syncCancel"} ELSE {})) :
        /\ res' = [res EXCEPT
             ![r].lazyRunning = @ - 1,
             ![r].lazyPhase = "idle",
             ![r].lazyCancel = FALSE,
             ![r].lazyWaiters = 0,
             ![r].lazyComplete = @ \/ outcome = "syncOk",
             ![r].lazySyncPending = outcome # "syncOk",
             ![r].lazyTokenSession = 0]
        /\ evals' = LatchEvalOutcomes(r,
             CASE outcome = "syncOk" -> "latchedDone"
               [] outcome = "syncFail" -> "latchedFail"
               [] OTHER -> "latchedCancel")
        /\ sessionRelease' = IF sessionRelease[s].exitingLazy = 0
             THEN [sessionRelease EXCEPT ![s].exitingLazy = 1]
             ELSE FinishSessionOperation(sessionRelease, s)
    /\ UNCHANGED <<invocations, ongoingCalls, ongoingCallIndex,
                   sessionEdges, countedEdges, epoch, flushed>>

\* The callback goroutine's deferred token release occurs after done closes.
\* Multiple completed attempts may be in this tail at once. The saturating
\* flag represents their shared release hold; this action is their final exit.
EvalCallbackTokenExit(s) ==
    /\ sessionRelease[s].exitingLazy > 0
    /\ sessionRelease[s].active > 0
    /\ LET finished == FinishSessionOperation(sessionRelease, s) IN
       sessionRelease' = [finished EXCEPT ![s].exitingLazy = @ - 1]
    /\ UNCHANGED <<invocations, res, ongoingCalls, ongoingCallIndex,
                   sessionEdges, countedEdges, evals, epoch, flushed>>

\* Closing the retired attempt's done channel is the lock-free region after
\* callback finish. Each transition below represents that broadcast becoming
\* observable to one retained waiter; the waiter still has not selected
\* between completion and its own context cancellation.
EvalCallbackClose(e) ==
    /\ evals[e].phase \in EvalLatchedPhases
    /\ evals' = [evals EXCEPT ![e].phase =
         CASE @ = "latchedDone" -> "wakeDone"
           [] @ = "latchedFail" -> "wakeFail"
           [] OTHER -> "wakeCancel"]
    /\ UNCHANGED <<invocations, res, ongoingCalls, ongoingCallIndex,
                   sessionEdges, countedEdges, sessionRelease,
                   epoch, flushed>>

\* The completion arm consumes one retired attempt's outcome. Success and
\* genuine callback failure terminate. Foreign cancellation returns the
\* healthy evaluator to demand so it can re-check completion, join a current
\* attempt, or lead a fresh one.
EvalWake(e) ==
    /\ evals[e].phase \in EvalWakePhases
    /\ evals' = [evals EXCEPT
         ![e].phase = CASE @ = "wakeDone" -> "returnDone"
                           [] @ = "wakeFail" -> "returnFailed"
                           [] OTHER -> "demand",
         ![e].foreignCancel = (evals[e].phase = "wakeCancel")]
    /\ UNCHANGED <<invocations, res, ongoingCalls, ongoingCallIndex,
                   sessionEdges, countedEdges, sessionRelease,
                   epoch, flushed>>

\* A waiter gives up because its own context was canceled. While the attempt
\* is current, it decrements that attempt's waiter count and the last waiter
\* requests callback cancellation. Once callback finish retired the attempt,
\* abandonment touches no result-level state, even if a new attempt is current.
EvalAbandon(e) ==
    /\ evals[e].phase \in {"waiting"} \cup EvalLatchedPhases \cup EvalWakePhases
    /\ LET r == evals[e].target
           current == evals[e].phase = "waiting"
           last == current /\ res[r].lazyWaiters = 1
       IN /\ ~current \/ res[r].lazyPhase \in {"running", "syncing"}
          /\ res' = IF current
                    THEN [res EXCEPT
                         ![r].lazyWaiters = @ - 1,
                         ![r].lazyCancel = @ \/ last]
                    ELSE res
          /\ evals' = [evals EXCEPT ![e].phase = "returnAbandoned",
                                     ![e].foreignCancel = FALSE]
    /\ UNCHANGED <<invocations, ongoingCalls, ongoingCallIndex,
                   sessionEdges, countedEdges, sessionRelease,
                   epoch, flushed>>

\* Evaluate's outer operation exits after its final return decision. A
\* successful Evaluate is changed to a released-session refusal if release
\* began while it was active.
EvalOperationExit(e) ==
    /\ evals[e].phase \in EvalExitPhases
    /\ evals[e].opActive
    /\ LET s == evals[e].sess
           success == evals[e].phase = "returnDone"
           lateRefusal == success /\ sessionRelease[s].phase # "live"
           terminal == CASE lateRefusal -> "refused"
                         [] evals[e].phase = "returnDone" -> "done"
                         [] evals[e].phase = "returnFailed" -> "failedCallback"
                         [] evals[e].phase = "returnAbandoned" -> "abandoned"
                         [] OTHER -> "refused"
       IN /\ sessionRelease[s].active > 0
          /\ sessionRelease' = FinishSessionOperation(sessionRelease, s)
          /\ evals' = [evals EXCEPT ![e].phase = terminal,
                                    ![e].opActive = FALSE]
    /\ UNCHANGED <<invocations, res, ongoingCalls, ongoingCallIndex,
                   sessionEdges, countedEdges, epoch, flushed>>

---------------------------------------------------------------------------
\* Everything that can happen, from any state: some invocation takes its
\* next step, some ongoing call's fn or publication advances, a session is
\* released, or a persisted edge is pruned.
Next ==
    \/ Spawn \/ SpawnNested
    \/ \E i \in InvocationIds :
         \/ LookupHit(i) \/ LookupMiss(i)
         \/ Join(i) \/ CreateOc(i) \/ CreateOcLeaseFail(i)
         \/ WaiterCancel(i) \/ WaiterCancelLate(i) \/ WaiterObserveFnErr(i)
         \/ WaiterObservePubErr(i) \/ WaiterDropHoldPubErr(i)
         \/ WaiterDropHoldCanceled(i)
         \/ WaiterClaim(i)
         \/ WaiterDepart(i) \/ WaiterReleaseHold(i)
         \/ ReadBarrierOk(i) \/ PersistHit(i)
         \/ ReadBarrierErrHit(i) \/ ReadBarrierErrWait(i)
         \/ ReadBarrierCancelHit(i) \/ ReadBarrierCancelWait(i)
         \/ DecodeLead(i) \/ DecodeInstall(i) \/ DecodeLeadFinish(i)
         \/ DecodeJoin(i) \/ DecodeWake(i)
         \/ DecodeFailHit(i) \/ DecodeFailWait(i)
         \/ DecodeLeadCancel(i) \/ DecodeJoinCancel(i)
         \/ InvocationOperationExit(i)
    \/ \E o \in OngoingCallIds :
         \/ FnComplete(o) \/ PubBegin(o)
         \/ PubIndexFresh(o) \/ PubAdopt(o) \/ PubIndexReuse(o)
         \/ PubAttachAddDep(o) \/ PubFinishOk(o)
         \/ PubAttachFailDropHold(o) \/ PubAttachFailCloseBarrier(o)
         \/ PubUnregister(o) \/ FnWindDown(o)
    \/ \E s \in Sessions :
         ReleaseSessionMark(s) \/ ReleaseSessionSnapshot(s)
           \/ ReleaseSessionCollect(s) \/ ReleaseSessionDelete(s)
    \/ \E r \in 1..Len(res) : PruneCut(r)
    \/ BindResource
    \/ AddDepLate
    \/ EvalSpawn
    \/ \E e \in EvalIds :
         \/ EvalNoWork(e) \/ EvalStartAttempt(e) \/ EvalStartAttemptRefused(e)
         \/ EvalJoin(e) \/ EvalCallbackClose(e) \/ EvalWake(e)
         \/ EvalAbandon(e) \/ EvalOperationExit(e)
    \/ \E r \in 1..Len(res) :
         EvalBodyFinish(r) \/ EvalSyncFinish(r) \/ ImportedLazyArm(r)
    \/ \E s \in Sessions : EvalCallbackTokenExit(s)
    \/ BeginClose
    \/ Flush
    \/ Restart

Spec == Init /\ [][Next]_vars

(***************************************************************************)
(* FAIRNESS - which actions must eventually run if they stay enabled.      *)
(* Needed only for the liveness property; safety checking ignores this.    *)
(*                                                                         *)
(* Weak fairness on SYSTEM progress only:                                  *)
(*   - fn completion, the publication chain, unregistration                *)
(*   - each waiter's own forward steps                                     *)
(* These correspond to goroutines the engine runs to completion. Without   *)
(* fairness on them, TLC would report fake wedges that really mean "the    *)
(* scheduler never ran that goroutine".                                    *)
(*                                                                         *)
(* NO fairness on:                                                         *)
(*   - Spawn and SpawnNested: clients may stop arriving                    *)
(*   - WaiterCancel, WaiterCancelLate, ReadBarrierCancelHit/Wait,          *)
(*     DecodeLeadCancel, DecodeJoinCancel, and every failure-injection     *)
(*     branch: possibilities, not obligations                              *)
(*   - ReleaseSession and PruneCut: external events                        *)
(*   - FnWindDown: how long a canceled executor unwinds is external. The   *)
(*     deferred-release property is conditional on the cache operation     *)
(*     count, so it does not assume a canceled executor itself unwinds.     *)
(*                                                                         *)
(* Two placement subtleties:                                               *)
(*   - FnComplete's fairness guarantees SOME outcome, not a successful     *)
(*     one (the outcome choice inside stays nondeterministic)              *)
(*   - fairness sits on the disjunction of the attach outcomes             *)
(*     (PubFinishOk or PubAttachFailDropHold), never on the success arm -  *)
(*     fairness on the success arm would wrongly forbid persistent         *)
(*     failure                                                             *)
(***************************************************************************)
SystemProgress(o) ==
    \/ FnComplete(o) \/ PubBegin(o)
    \/ PubIndexFresh(o) \/ PubAdopt(o) \/ PubIndexReuse(o)
    \/ PubFinishOk(o) \/ PubAttachFailDropHold(o)
    \/ PubAttachFailCloseBarrier(o)
    \/ PubUnregister(o)

WaiterProgress(i) ==
    \/ LookupHit(i) \/ LookupMiss(i) \/ Join(i) \/ CreateOc(i)
    \/ WaiterObserveFnErr(i) \/ WaiterObservePubErr(i)
    \/ WaiterDropHoldPubErr(i) \/ WaiterDropHoldCanceled(i)
    \/ WaiterClaim(i)
    \/ WaiterDepart(i) \/ WaiterReleaseHold(i)
    \/ ReadBarrierOk(i) \/ PersistHit(i)
    \/ ReadBarrierErrHit(i) \/ ReadBarrierErrWait(i)
    \/ DecodeLead(i) \/ DecodeInstall(i) \/ DecodeLeadFinish(i)
    \/ DecodeJoin(i) \/ DecodeWake(i)
    \/ DecodeFailHit(i) \/ DecodeFailWait(i)
    \/ InvocationOperationExit(i)

\* An evaluator's own forward steps. Abandoning is deliberately NOT here:
\* giving up is a caller's choice, never an obligation.
EvalProgress(e) ==
    \/ EvalNoWork(e) \/ EvalStartAttempt(e) \/ EvalStartAttemptRefused(e)
    \/ EvalJoin(e) \/ EvalWake(e) \/ EvalOperationExit(e)

\* Callback completion always proceeds to close the retired attempt's done
\* channel. This fairness covers the lock-free close becoming observable to
\* each waiter that retained that attempt.
LazyCallbackCloseProgress(e) == EvalCallbackClose(e)

\* The lazy callback goroutine always eventually finishes, canceled or
\* not. Fairness sits on the disjunction of both finish shapes for the
\* same reason it does for attachment: weak fairness on the success arm
\* alone would wrongly forbid persistent failure.
LazyCallbackProgress(r) ==
    EvalBodyFinish(r) \/ EvalSyncFinish(r)

\* Publishing a release plan and consuming assigned cleanup are system
\* progress. ReleaseSessionMark itself remains an external event.
ReleaseProgress(s) ==
    ReleaseSessionSnapshot(s) \/ ReleaseSessionCollect(s) \/ ReleaseSessionDelete(s)

\* The callback goroutine releases its attempt token after closing done.
LazyCallbackTokenProgress(s) == EvalCallbackTokenExit(s)

LiveSpec ==
    /\ Spec
    /\ \A o \in 1..MaxInvocations :
         WF_vars(o \in OngoingCallIds /\ SystemProgress(o))
    /\ \A i \in 1..MaxInvocations :
         WF_vars(i \in InvocationIds /\ WaiterProgress(i))
    \* Vacuous when MaxEvals = 0, so liveness runs without lazy
    \* evaluation are untouched by the lazy machinery.
    /\ \A e \in 1..MaxEvals :
         WF_vars(e \in EvalIds /\ EvalProgress(e))
    /\ \A e \in 1..MaxEvals :
         WF_vars(e \in EvalIds /\ LazyCallbackCloseProgress(e))
    /\ \A r \in 1..MaxResults :
         WF_vars(r \in ResultIds /\ LazyCallbackProgress(r))
    /\ \A s \in Sessions : WF_vars(ReleaseProgress(s))
    /\ \A s \in Sessions : WF_vars(LazyCallbackTokenProgress(s))

---------------------------------------------------------------------------
(***************************************************************************)
(* PROPERTIES. The invariants below are checked in every reachable state.  *)
(* EvalEventuallyTerminal, DeferredReleaseEventuallyCompletes, and         *)
(* EventuallyTerminal are liveness properties checked against LiveSpec.    *)
(* Each .cfg selects only the subset                                       *)
(* that isolates its question - checking every property in every           *)
(* configuration would conflate independent questions: a scenario that     *)
(* exercises one failure could drown the property under test in violations *)
(* of unrelated properties.                                                *)
(*                                                                         *)
(* GUARANTEE (session-resource gating). Every path that hands a session a  *)
(* result VALUE filters on the session's bound set: request and digest     *)
(* lookups (LookupHit), publication-adoption picks (CanonicalPick /        *)
(* FnComplete's reuse), waits for a result the session itself produced,    *)
(* and result-ID value loads, which refuse the session when neither a      *)
(* canonical candidate nor the exact result satisfies it                   *)
(* (sharedResultLookupCanonicalEquivalentGated,                            *)
(* cache_persistence_resolver.go; before that check existed, the exact     *)
(* fallback bypassed the filter). Two kinds of guard follow from it.       *)
(* SELECTION-TIME checks test current satisfaction, exactly as the code    *)
(* does at candidate selection: LookupHit and CanonicalPick's candidate    *)
(* set (selectLookupCandidateForSessionLocked). HELD-RESULT guards use     *)
(* ownership, never current satisfaction: FnComplete's reuse,              *)
(* PubIndexFresh's structural deps, PubAttachAddDep's attachment deps,     *)
(* AddDepLate's possession, and CanonicalPick's returned-result fallback   *)
(* (the publisher owns what it just produced), because ownership proves    *)
(* only that the result satisfied the session WHEN OBTAINED -              *)
(* requirement growth after the filter (the gated-growth finding) can      *)
(* invalidate it, and the code re-checks nothing on held results.          *)
(* A gated ID load needs no action of its own: refusal returns nothing     *)
(* and serving is behaviorally a LookupHit. Not yet modeled on this path:  *)
(* call-frame loads (ResultCallByResultID) serve the recipe ungated, and   *)
(* the canonical pick for ID loads admits open-barrier siblings            *)
(* (requireCleanAttachment=false) whose attachment error an ID load can    *)
(* then surface; both are modelable if an effort takes them up.            *)
(***************************************************************************)

DerivedSessionActive(s) ==
    Cardinality({i \in InvocationIds :
        invocations[i].sess = s /\ invocations[i].opActive})
      + Cardinality({e \in EvalIds :
        evals[e].sess = s /\ evals[e].opActive})
      + Cardinality({r \in ResultIds : res[r].lazyTokenSession = s})
      + sessionRelease[s].exitingLazy

\* Basic shape sanity: bounds respected, counts non-negative, and no
\* counted edge without its record.
TypeOK ==
    /\ Len(invocations) <= MaxInvocations
    /\ Len(res) <= MaxResults
    /\ Len(evals) <= MaxEvals
    /\ countedEdges \subseteq sessionEdges
    /\ epoch \in {1, 2}
    /\ flushed.closing \in BOOLEAN
    /\ \A s \in Sessions :
         /\ sessionRelease[s].phase \in
              {"live", "marking", "deferred", "collecting", "deleting", "released"}
         /\ sessionRelease[s].active \in Nat
         /\ sessionRelease[s].exitingLazy \in 0..1
         /\ sessionRelease[s].exitingLazy <= sessionRelease[s].active
         /\ sessionRelease[s].active = DerivedSessionActive(s)
         /\ sessionRelease[s].phase = "deferred" => sessionRelease[s].active > 0
         /\ sessionRelease[s].phase \in {"collecting", "deleting", "released"}
              => sessionRelease[s].active = 0
         /\ sessionRelease[s].phase = "released"
              => sessionRelease[s].snap = {}
    /\ \A o \in OngoingCallIds : ongoingCalls[o].waiters >= 0
    /\ \A r \in ResultIds :
         /\ res[r].lazyWaiters >= 0 /\ res[r].lazyRunning >= 0
         /\ res[r].imported \in BOOLEAN
         /\ res[r].lazyPhase \in {"idle", "running", "syncing"}
         /\ res[r].lazyCb \in {"none", "armed"}
         /\ res[r].lazyCancel \in BOOLEAN
         /\ res[r].lazySyncPending \in BOOLEAN
         /\ res[r].lazyTokenSession \in Sessions \cup {0}
         /\ (res[r].lazyRunning = 0) = (res[r].lazyTokenSession = 0)
         /\ res[r].decodePhase \in {"idle", "running"}
         /\ res[r].decodeErr \in {"none", "fail", "cancel"}
         \* every finish or cancellation is by a distinct leader, and an
         \* invocation leads at most once
         /\ res[r].decodeGen \in 0..MaxInvocations
         /\ res[r].persistSyncPending \in BOOLEAN
         \* the pending flag exists only for an installed payload: it is
         \* set by a post-install finish and the install never reverts
         /\ res[r].persistSyncPending => res[r].payload = "decoded"
         /\ res[r].handle \in Handles \cup {"none"}
         /\ res[r].required \subseteq Handles
         /\ res[r].lazyWaiters = Cardinality(
              {e \in EvalIds :
                  evals[e].target = r /\ evals[e].phase = "waiting"})
    /\ \A e \in EvalIds :
         /\ evals[e].phase \in EvalPhaseDomain
         /\ evals[e].foreignCancel \in BOOLEAN
         /\ evals[e].sess \in Sessions
         /\ evals[e].opActive \in BOOLEAN
    /\ \A i \in InvocationIds :
         /\ invocations[i].origin \in {"handler", "nested"}
         /\ invocations[i].opActive \in BOOLEAN
         /\ invocations[i].refusedEpoch \in 0..epoch
         /\ invocations[i].lookupBarrierAtSelection
              \in {"none", "open", "closedOk", "closedErr"}
         /\ invocations[i].ownCancel \in BOOLEAN
         /\ invocations[i].joinedGen \in 0..MaxInvocations
         /\ invocations[i].retGated \in BOOLEAN
    /\ \A s \in Sessions : sessionRelease[s].handles \subseteq Handles
\* Ownership accounting is exact: for every registered result, the
\* incrementally-maintained counter equals the recount of its edges
\* (counted session edges + dependency parents + handoff holds +
\* persisted edge).
OwnershipExact ==
    \A r \in ResultIds :
        res[r].registered => res[r].own = DerivedOwn(r)

\* Session-resource accounting is exact: for every registered result, the
\* STORED required set equals the transitive recount. The same relationship
\* OwnershipExact has to DerivedOwn. It held only outside persistence until
\* the import recompute became dependency-first and the decode installs
\* stopped overwriting the stored set; it is a regression gate on both.
RequiredExact ==
    \A r \in ResultIds :
        res[r].registered => res[r].required = TrueRequired(r)

\* No ownership count ever goes below zero.
NoUnderflow == \A r \in ResultIds : res[r].own >= 0

\* A collected result - one whose OnRelease hooks ran - is never
\* re-registered into the cache.
NoResurrection ==
    \A r \in ResultIds : res[r].registered => ~res[r].released

\* No invocation completes holding a result that was already collected.
ReturnedLive ==
    \A i \in InvocationIds : invocations[i].phase = "done" => invocations[i].retLive

\* At the instant GetOrInitCall returns a result, the calling session's
\* edge is recorded and the result is pinned (counted edge or active
\* handoff hold) - it cannot vanish out from under the caller.
ReturnedOwned ==
    \A i \in InvocationIds : invocations[i].phase = "done" => invocations[i].retOwned

\* No session is ever handed a result that depends, directly or transitively,
\* on a handle leaf it never bound: every returned invocation satisfies the
\* transitive requirement of its result. This is the session-resource gate's
\* purpose. It fails where a result reaches a session whose bound set does
\* not cover the result's TrueRequired; with exact stored accounting
\* (RequiredExact) the lookup filter enforces exactly that, so the two
\* properties gate the accounting and the harm separately.
ReturnedGated ==
    \A i \in InvocationIds : invocations[i].phase = "done" => invocations[i].retGated

\* No reader is handed a result whose dependency attachment has not
\* finished cleanly.
NoHalfAttachedRead ==
    \A i \in InvocationIds : invocations[i].phase = "done" => invocations[i].retBarrierOK

\* Persistence intent is never lost: once a successful call's admission has
\* closed and its handoff hold has been released, an admitted persistable
\* request implies the persisted edge exists. This includes a refused or
\* canceled final waiter because final handoff commits the edge first.
PersistableIntentDurable ==
    \A o \in OngoingCallIds :
        (/\ ongoingCalls[o].pubState = "done"
         /\ ~ongoingCalls[o].inIndex
         /\ ongoingCalls[o].waiters = 0
         /\ ~ongoingCalls[o].hold
         /\ ongoingCalls[o].isPersistable
         /\ ongoingCalls[o].resId # 0
         /\ res[ongoingCalls[o].resId].registered)
            => res[ongoingCalls[o].resId].persisted

\* Operation-lease failure terminates before an ongoing call, result, or
\* ownership edge is published. The Go branch also discharges its local
\* cancel, which has no persistent model state after the action completes.
LeaseFailureClean ==
    \A i \in InvocationIds :
        invocations[i].path = "leaseFailure" =>
            /\ invocations[i].phase \in {"failing", "failed"}
            /\ invocations[i].oc = 0
            /\ invocations[i].resId = 0

\* Racing alone never manufactures an execution failure: if no failure
\* injection is enabled, no invocation ends in "failed". Cancellation and a
\* released-session refusal have their own terminal phases. This holds
\* with release enabled too: ongoing calls are keyed by (call, session),
\* so no invocation waits on another session's singleflight entry, and
\* every result a session reuses is one it acquired with ownership, so a
\* release cannot fail anyone's call. (An earlier note that release
\* trips this invariant traced to a model-only reuse without ownership,
\* removed by FnComplete's counted-edge guard.)
NoSpuriousErrors ==
    (~FnCanFail /\ ~AttachCanFail /\ ~LeaseCanFail /\ ~DecodeCanFail) =>
        \A i \in InvocationIds : invocations[i].phase # "failed"

\* An invocation reports cancellation only for its own context. Contract:
\* the ctx.Done arms in wait and ensurePersistedHitValueLoaded return
\* context.Cause of the caller's OWN ctx; a cancellation that originated
\* with another caller reaches a waiter as an error (or, in the lazy
\* singleflight, is retried by waitForLazyEvaluation), never relabeled as
\* the waiter's own. ownCancel is a property-only ghost set by exactly the
\* actions that model an invocation's own ctx.Done arm: WaiterCancel,
\* WaiterCancelLate, ReadBarrierCancelHit/Wait, DecodeLeadCancel,
\* DecodeJoinCancel. The pre-exit phases are included so that a
\* DecodeWake that sent a joiner to "canceling" on a latched foreign
\* "cancel" trips this at once: the retry the code performs on a departed
\* leader's cancellation must loop the joiner, never relabel the foreign
\* cancellation as the joiner's own.
CancelOnlyOwn ==
    \A i \in InvocationIds :
        invocations[i].phase \in {"cancelDropHold", "canceling", "canceled"}
            => invocations[i].ownCancel

\* Once every session is released and all activity has settled, no session
\* ownership may remain.
NoOrphanEdgesAtQuiescence ==
    (/\ \A s \in Sessions : sessionRelease[s].phase = "released"
     /\ Len(invocations) = MaxInvocations
     /\ \A i \in InvocationIds : invocations[i].phase \in TerminalPhases
     /\ \A o \in OngoingCallIds : ongoingCalls[o].pubState \in {"none", "done", "failed"}
                          /\ ~ongoingCalls[o].hold)
    => countedEdges = {}

\* A refused claim is explained by the session tombstone. Sessions do not
\* reopen within one engine lifetime in this model.
\* The earlier-epoch arm preserves a refusal already validated before restart;
\* otherwise resetting every session to live would make it look spurious.
RefusedOnlyAfterRelease ==
    \A i \in InvocationIds :
        invocations[i].phase = "refused" =>
            \/ invocations[i].refusedEpoch < epoch
            \/ /\ invocations[i].refusedEpoch = epoch
               /\ sessionRelease[invocations[i].sess].phase # "live"

\* Attachment failure never gains a persisted edge. A concurrent hit may
\* have claimed a session edge while the barrier was open, so the errored
\* result can remain registered until that session releases it.
NoRetainedPoisonedEntry ==
    \A r \in ResultIds :
        (res[r].registered /\ res[r].barrier = "closedErr") =>
            ~res[r].persisted

\* The selection-time marker distinguishes a forbidden lookup made after an
\* attachment error from a legitimate lookup that selected an open barrier
\* before the attachment later failed.
NoErroredLookupSelection ==
    \A i \in InvocationIds :
        invocations[i].lookupBarrierAtSelection # "closedErr"

(***************************************************************************)
(* LAZY-EVALUATION PROPERTIES. Each names what the code's lazyMu           *)
(* coordination guarantees.                                                *)
(***************************************************************************)

\* At most one lazy callback runs per result at any moment - the whole
\* point of the per-result singleflight in evaluateOne.
LazyMutualExclusion ==
    \A r \in ResultIds : res[r].lazyRunning <= 1

\* A successful Evaluate return implies the result's evaluation is
\* complete. The pre-fix fast path violated this: it trusted the consumed
\* object-side callback while that callback's attempt was still running
\* its cache-side bookkeeping, and reported success early.
EvalDoneComplete ==
    \A e \in EvalIds :
        evals[e].phase = "done" => res[evals[e].target].lazyComplete

\* Completion is only ever recorded with every stage settled: the
\* object-side callback consumed, no bookkeeping pending, no attempt in
\* flight. The pre-fix preflight violated this by converting a consumed
\* object-side callback into lazyEvalComplete while bookkeeping had
\* failed or was still running.
LazyCompleteSettled ==
    \A r \in ResultIds :
        res[r].lazyComplete =>
            /\ res[r].lazyCb = "none"
            /\ ~res[r].lazySyncPending
            /\ res[r].lazyPhase = "idle"

\* Success is permanent: once a result's evaluation completed, no callback
\* for it is running or can start again (the callback is cleared and every
\* start path checks lazyEvalComplete first).
LazySuccessPermanent ==
    \A r \in ResultIds : res[r].lazyComplete => res[r].lazyRunning = 0

\* A running callback or its post-close token tail keeps its owner session
\* out of collection. This is the attempt-lifetime guarantee: waiter exit
\* alone cannot make release eligible while the callback goroutine remains.
LazyAttemptDefersCollection ==
    \A s \in Sessions :
        (\/ sessionRelease[s].exitingLazy > 0
         \/ \E r \in ResultIds : res[r].lazyTokenSession = s)
            => sessionRelease[s].phase \notin {"collecting", "deleting", "released"}

\* An Evaluate caller never returns a cancellation error caused by another
\* waiter. foreignCancel becomes true when the canceled callback's outcome is
\* latched on its retained waiters and remains true as a healthy waiter returns
\* to demand, so the stale-cancellation configuration reaches states that
\* exercise this predicate. Starting or joining the retry clears it. Reaching
\* any terminal phase while it is true would mean the foreign cancellation
\* escaped Evaluate instead of being retried.
NoStaleCancelError ==
    \A e \in EvalIds :
        evals[e].foreignCancel => evals[e].phase \notin EvalTerminalPhases

(***************************************************************************)
(* IMPORT/FLUSH PROPERTIES.                                                *)
(***************************************************************************)

\* At most one decode runs per result - the persistDecodeWaitCh
\* singleflight. (Ownership-exactness from imported starts and after
\* restart needs no property of its own: OwnershipExact is checked in
\* every state, and both import constructions compute counts from edges.)
DecodeMutualExclusion ==
    \A r \in ResultIds :
        Cardinality({i \in InvocationIds :
            invocations[i].phase = "decoding"
              /\ invocations[i].resId = r}) <= 1

\* The graceful-shutdown snapshot captures only clean, fully-retained state:
\* every kept row had a cleanly-closed (or never-armed) attach barrier and
\* ownership fully explained by persisted and dependency edges. Flush is
\* enabled only after operation and deferred-cleanup quiescence.
FlushCleanCapture ==
    flushed.done =>
        \A r \in DOMAIN flushed.rows :
            flushed.rows[r].keep =>
                (~flushed.rows[r].dirty /\ flushed.rows[r].ownClean)

\* Every written result belongs to a complete clean dependency closure rooted
\* at a written persisted edge. The Go closure walk
\* (snapshotPersistedRootClosureLocked) provides this by construction, and
\* import's explicit reference checks reject dangling rows at restart.
FlushReferentialIntegrity ==
    flushed.done =>
        \A r \in DOMAIN flushed.rows :
            flushed.rows[r].keep =>
                /\ ~flushed.rows[r].dirty
                /\ \A d \in flushed.rows[r].deps : flushed.rows[d].keep
                /\ \E root \in DOMAIN flushed.rows :
                     /\ flushed.rows[root].keep
                     /\ flushed.rows[root].persisted
                     /\ DepReachable(flushed.rows, root, r)

\* A result that was open or attachment-errored at snapshot time is never
\* imported and served. Flush rejects every persisted root whose dependency
\* closure contains such a result.
NoLaunderedServe ==
    \A i \in InvocationIds :
        invocations[i].phase = "done" => invocations[i].retClean

\* Liveness (against LiveSpec): every Evaluate caller eventually
\* terminates - served, failed, or abandoned by its own choice, never
\* wedged forever.
EvalEventuallyTerminal ==
    \A e \in 1..MaxEvals :
        (e \in EvalIds) ~>
            (e \in EvalIds /\ evals[e].phase \in EvalTerminalPhases)

\* Once a release has a quiescent operation count, fair system progress
\* eventually consumes its cleanup plan and deletes the session records.
\* This is deliberately conditional: a genuinely wedged operation keeps
\* active nonzero and causes shutdown-context failure instead of a snapshot.
DeferredReleaseEventuallyCompletes ==
    \A s \in Sessions :
        (sessionRelease[s].phase # "live" /\ sessionRelease[s].active = 0)
          ~> (sessionRelease[s].phase = "released")

\* Liveness (against LiveSpec): every issued call eventually terminates -
\* served, failed, or canceled, never wedged forever.
EventuallyTerminal ==
    \A i \in 1..MaxInvocations :
        (i \in InvocationIds) ~> (i \in InvocationIds /\ invocations[i].phase \in TerminalPhases)

=============================================================================
