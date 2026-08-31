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
(*                                                                         *)
(* The model reflects the code as it is. Configurations select scenarios   *)
(* (which machinery is active, which external events and failures can      *)
(* happen), never alternative code shapes. Where the current code has a    *)
(* known deficiency, a configuration reproduces it as an expected          *)
(* invariant violation, so the deficiency cannot silently change shape.    *)
(*                                                                         *)
(* GRANULARITY                                                             *)
(* One atomic model action per Go critical section: one hold of egraphMu,  *)
(* callsMu, sessionMu, or lazyMu, or one lock-free region between holds.  *)
(* Races live BETWEEN critical sections; the model explores exactly those  *)
(* interleavings and no finer-grained fake ones. Every action's comment    *)
(* names the Go function and lines it abstracts.                           *)
(*                                                                         *)
(* ABSTRACTIONS                                                            *)
(*   - Equivalence is a static partition of call identities (ClassOf).     *)
(*     The e-graph's merging machinery is not modeled. Lookup returns      *)
(*     some live result in the request's class, or misses.                 *)
(*   - Lookup may miss even when a candidate exists. This over-            *)
(*     approximates the candidate/session filtering that is not modeled,   *)
(*     and it exercises the engine's accepted duplicate-execution window   *)
(*     (cache.go:3889-3893).                                               *)
(*   - MODEL AXIOM, assumed and never verified here: results the cache     *)
(*     considers equivalent are interchangeable.                           *)
(*   - Not modeled: session resources, TTL/expiry, DoNotCache,             *)
(*     recipe-replay taint, and the arbitrary-value cache                  *)
(*     (acquireSessionArbitraryLocked, cache.go:751 - the same atomic      *)
(*     record-and-count claim as the modeled result claim, under callsMu   *)
(*     with sessionMu nested; Go tests carry its coverage).                *)
(*   - ReleaseSession can fire at ANY time, including while the session    *)
(*     has calls in flight. The server's drain (dagqlInFlight,             *)
(*     engine/server/session.go:603-608) narrows but does not close that   *)
(*     window: it counts only serveQuery handler goroutines, and the       *)
(*     detached call executor (cache.go:3954) keeps issuing nested cache   *)
(*     calls with the same session ID while it unwinds from cancellation.  *)
(*     DrainOnRelease models the drain as implemented (handler calls       *)
(*     only); ModelNestedCalls adds the executor-nested calls that         *)
(*     escape it.                                                          *)
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
    MaxResults,         \* bound on allocated sharedResult IDs

    \* --- external events -------------------------------------------------
    AllowRelease,       \* enable session release; off in configs that
                        \* isolate a question from release races
    DrainOnRelease,     \* model the server's politeness around release, AS
                        \* IMPLEMENTED - it covers handler-origin calls only:
                        \*   - TRUE: ReleaseSession waits until the
                        \*     session's HANDLER-origin calls are terminal
                        \*     (the dagqlInFlight drain, session.go:603-608,
                        \*     counts serveQuery handlers only) and released
                        \*     sessions get no new handler calls (the
                        \*     dagqlClosing reject, session.go:1719-1725).
                        \*     Executor-NESTED calls (ModelNestedCalls) are
                        \*     not counted and not rejected - exactly as in
                        \*     the engine.
                        \*   - FALSE: the cache on its own, no politeness
                        \* TRUE asks "does the drain, as implemented,
                        \* prevent this?"; FALSE asks what the cache
                        \* guarantees by itself.
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
                        \* payloads possibly still encoded ("envelope"),
                        \* exactly what importPersistedState reconstructs
                        \* after a clean restart.
    ModelNestedCalls,   \* calls issued from inside a call executor.
                        \* GetOrInitCall runs fn on a detached goroutine
                        \* (cache.go:3954) whose context survives caller
                        \* cancellation (WithCancelCause of WithoutCancel,
                        \* cache.go:3897), and resolvers make nested cache
                        \* calls carrying the SAME session ID
                        \* (dagql/objects.go:684; context values survive
                        \* WithoutCancel). Nothing counts that goroutine:
                        \* the drain waits only for serveQuery handlers, so
                        \* after a cancellation, teardown can pass the drain
                        \* and run ReleaseSession while the executor is
                        \* still unwinding and still claiming session
                        \* edges. Nested spawns are allowed while any of
                        \* the session's executors is running or winding
                        \* down, and are never blocked by DrainOnRelease.

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
                        \* ctx.Done arms, cache_persistence_import.go:562,
                        \* :598). Go's select picks nondeterministically
                        \* even when the barrier is already closed, so a
                        \* canceled caller can fail on a healthy result.
    LazyCanFail,        \* the lazy callback may fail. Failure must leave
                        \* the result retryable (lazyEvalComplete is set
                        \* only on success, cache.go:3187-3191).
    DecodeCanFail       \* decoding a persisted payload may fail. Failure
                        \* must leave the entry retryable: the decode wait
                        \* channel is cleared at finish, so the next
                        \* demand leads a fresh attempt
                        \* (cache_persistence_import.go:611-619).

\* Config sanity check, evaluated once at startup: the ClassOf table must
\* assign a class to every call and to nothing else.
ASSUME DOMAIN ClassOf = Calls

\* Convenience value for configs: every call in one equivalence class.
OneClass == [c \in Calls |-> "k1"]

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
                        \* (dagql/objects.go:607)
    sessionEdges,       \* session->result pairs RECORDED in the session's
                        \* map (Cache.sessionResultIDsBySession)
    countedEdges,       \* the session edges whose ownership units are still
                        \* present. Claims add both sets atomically. The sets
                        \* differ only while release has removed records and
                        \* has not yet removed their snapshotted units.
    sessionRelease,     \* per-session release progress, mirroring
                        \* ReleaseSession's two critical sections
                        \* (cache.go:760-834):
                        \*   phase "live":       not released
                        \*   phase "collecting": the sessionMu section ran -
                        \*     records snapshotted into snap and wiped -
                        \*     but the egraphMu decrements have not
                        \*   phase "released":   both sections ran
                        \* snap holds the record snapshot the decrement
                        \* section will consume.
    evals,              \* one record per issued Cache.Evaluate caller
                        \* (empty unless ModelLazy)
    epoch,              \* 1 before the modeled restart, 2 after. One
                        \* graceful-shutdown/restart cycle per run.
    flushed             \* "none", or the snapshot the graceful shutdown
                        \* captured: one row per then-registered result.
                        \* Mirrors what snapshotPersistState writes
                        \* (cache_persistence_worker.go:27) minus the
                        \* e-graph detail this model abstracts away.

vars == <<invocations, res, ongoingCalls, ongoingCallIndex, sessionEdges, countedEdges,
          sessionRelease, evals, epoch, flushed>>


\* The currently-allocated ID ranges (sequences are 1-indexed).
ResultIds == 1..Len(res)
EvalIds   == 1..Len(evals)
InvocationIds    == 1..Len(invocations)
OngoingCallIds     == 1..Len(ongoingCalls)

\* phase values in which an invocation has finished, one way or another.
TerminalPhases == {"done", "failed", "canceled", "refused"}

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

\* All registered results whose call is in equivalence class k.
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
(* Mirrors collectUnownedResultsLocked (cache.go:979-1026), which runs     *)
(* inside the same egraphMu critical section as whatever decrement         *)
(* triggered it. released=TRUE here stands in for "the OnRelease hooks     *)
(* have run" (in the code they run just after the lock drops).             *)
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
(* only at earlier IDs, and ownership counts are computed from the edges   *)
(* exactly as import's increments do.                                      *)
(***************************************************************************)

ImportedResult(c, persistedFlag, depsSet, ownVal, payloadVal, launderedFlag) ==
    [call |-> c, registered |-> TRUE, released |-> FALSE,
     own |-> ownVal, deps |-> depsSet,
     persisted |-> persistedFlag, barrier |-> "none",
     payload |-> payloadVal, decodePhase |-> "idle", decodeErr |-> "none",
     laundered |-> launderedFlag,
     lazyCb |-> "none", lazyComplete |-> FALSE, lazyPhase |-> "idle",
     lazyWaiters |-> 0, lazyRunning |-> 0, lazyErr |-> "none"]

\* A collected result's ID is never reused, so after a restart the slots of
\* results that were not in the snapshot stay as dead husks - present in
\* the sequence, invisible to every action (nothing matches an unregistered
\* result). This keeps IDs stable across the restart, as the Go import does.
DeadHusk ==
    [call |-> CHOOSE c \in Calls : TRUE, registered |-> FALSE,
     released |-> TRUE, own |-> 0, deps |-> {},
     persisted |-> FALSE, barrier |-> "none",
     payload |-> "decoded", decodePhase |-> "idle", decodeErr |-> "none",
     laundered |-> FALSE,
     lazyCb |-> "none", lazyComplete |-> FALSE, lazyPhase |-> "idle",
     lazyWaiters |-> 0, lazyRunning |-> 0, lazyErr |-> "none"]

\* The candidate import rows for position pos: any call, persisted or not,
\* at most one dependency and only on an earlier row, payload decoded or
\* still an envelope.
ImportRowChoices(pos) ==
    [call : Calls, persisted : BOOLEAN,
     deps : {{}} \cup {{d} : d \in 1..(pos-1)},
     payload : {"decoded", "envelope"}]

\* Every row must be retained: a persisted root, or a dependency of some
\* row. (A flushed store contains only the retained graph.)
ImportGraphRetained(g) ==
    \A x \in 1..Len(g) :
        g[x].persisted \/ \E y \in 1..Len(g) : x \in g[y].deps

ImportOwn(g, x) ==
    (IF g[x].persisted THEN 1 ELSE 0)
      + Cardinality({y \in 1..Len(g) : x \in g[y].deps})

ImportGraphState(g) ==
    [x \in 1..Len(g) |->
        ImportedResult(g[x].call, g[x].persisted, g[x].deps,
                       ImportOwn(g, x), g[x].payload, FALSE)]

InitialResStates ==
    IF ModelPersistence /\ ImportInit
    THEN {ImportGraphState(g) :
            g \in {h \in {<<>>}
                        \cup {<<a>> : a \in ImportRowChoices(1)}
                        \cup {<<a, b>> : a \in ImportRowChoices(1),
                                         b \in ImportRowChoices(2)} :
                    ImportGraphRetained(h)}}
    ELSE {<<>>}

Init ==
    /\ invocations = <<>>
    /\ res \in InitialResStates
    /\ ongoingCalls = <<>>
    /\ ongoingCallIndex = [k \in Calls \X Sessions |-> 0]
    /\ sessionEdges = {}
    /\ countedEdges = {}
    /\ sessionRelease = [s \in Sessions |-> [phase |-> "live", snap |-> {}]]
    /\ evals = <<>>
    /\ epoch = 1
    /\ flushed = [done |-> FALSE, rows |-> <<>>]

(***************************************************************************)
(* Spawn: a client issues GetOrInitCall(session, call, persistable)        *)
(* through a serveQuery handler. Under DrainOnRelease, released sessions   *)
(* get no new handler calls (the dagqlClosing reject,                      *)
(* session.go:1719-1725); without it, nothing in the cache itself          *)
(* prevents a released session from issuing calls.                         *)
(*                                                                         *)
(* SpawnNested: a call issued from INSIDE a call executor - a resolver's   *)
(* nested Select (dagql/objects.go:684) running on the detached fn         *)
(* goroutine (cache.go:3954). It carries the same session ID, it is not    *)
(* counted by the drain, and it is not rejected after release; its only    *)
(* precondition is that some executor of the session is still running or   *)
(* winding down from cancellation. The model does not bind the nested      *)
(* call to a specific executor - any live executor of the session          *)
(* suffices.                                                               *)
(***************************************************************************)
NewInvocation(s, c, p, o) ==
    [sess |-> s, call |-> c, persistable |-> p, phase |-> "lookup",
     origin |-> o,
     oc |-> 0, resId |-> 0, path |-> "none",
     \* Invocations survive a modeled restart for property checking even though
     \* their real goroutines and the cache's in-memory tombstones do not, so this
     \* field ties a refusal to the epoch whose lifecycle state produced it.
     refusedEpoch |-> 0,
     lookupBarrierAtSelection |-> "none",
     retLive |-> TRUE, retOwned |-> TRUE,
     retBarrierOK |-> TRUE, retClean |-> TRUE]

Spawn ==
    /\ Len(invocations) < MaxInvocations
    /\ \E s \in Sessions, c \in Calls, p \in BOOLEAN :
        /\ DrainOnRelease => sessionRelease[s].phase = "live"
        /\ invocations' = Append(invocations, NewInvocation(s, c, p, "handler"))
    /\ UNCHANGED <<res, ongoingCalls, ongoingCallIndex, sessionEdges, countedEdges,
                   sessionRelease, evals, epoch, flushed>>

SpawnNested ==
    /\ ModelNestedCalls
    /\ Len(invocations) < MaxInvocations
    /\ \E s \in Sessions, c \in Calls, p \in BOOLEAN :
        /\ \E o \in OngoingCallIds :
             /\ ongoingCalls[o].sess = s
             /\ ongoingCalls[o].fnState \in {"running", "canceled"}
        /\ invocations' = Append(invocations, NewInvocation(s, c, p, "nested"))
    /\ UNCHANGED <<res, ongoingCalls, ongoingCallIndex, sessionEdges, countedEdges,
                   sessionRelease, evals, epoch, flushed>>

(***************************************************************************)
(* LookupHit: the lookup finds a live equivalent result whose attachment   *)
(* has not failed. The egraphMu section selects the result, then nests      *)
(* sessionMu to claim the session edge. A released-session tombstone       *)
(* refuses the claim without adding an edge. Persistable hits commit their *)
(* edge only after the read barrier and payload load succeed.               *)
(*                                                                         *)
(* Go: lookupCacheForRequest, cache_egraph.go:950-1001. Inside the one     *)
(* lock hold:                                                              *)
(*   - a candidate in the request's equivalence class is selected          *)
(*   - the session record and ownership unit are added together             *)
(* After the lock, an accepted hit passes the read barrier before return.  *)
(***************************************************************************)
LookupHit(i) ==
    /\ invocations[i].phase = "lookup"
    /\ \E r \in LookupEligibleInClass(ClassOf[invocations[i].call]) :
        LET s == invocations[i].sess
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
                /\ invocations' = [invocations EXCEPT ![i].phase = "refused",
                                        ![i].resId = r,
                                        ![i].path = "hit",
                                        ![i].lookupBarrierAtSelection = res[r].barrier,
                                        ![i].refusedEpoch = epoch]
                /\ UNCHANGED <<sessionEdges, countedEdges>>
    /\ UNCHANGED <<ongoingCalls, ongoingCallIndex, sessionRelease, evals, epoch, flushed>>

\* LookupMiss: the lookup finds nothing usable; fall through to the
\* singleflight. A miss is allowed even when a candidate exists - that
\* over-approximates the filtering this model leaves out, and exercises
\* the engine's accepted duplicate-execution window (cache.go:3889-3893).
LookupMiss(i) ==
    /\ invocations[i].phase = "lookup"
    /\ invocations' = [invocations EXCEPT ![i].phase = "join"]
    /\ UNCHANGED <<res, ongoingCalls, ongoingCallIndex, sessionEdges, countedEdges,
                   sessionRelease, evals, epoch, flushed>>

(***************************************************************************)
(* Join: an ongoing call for this (call, session) already exists - become  *)
(* one of its waiters instead of executing again.                          *)
(*                                                                         *)
(* Go: getOrInitCallInner, cache.go:3864-3887, one callsMu critical        *)
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
(* Go: getOrInitCallInner miss path, cache.go:3895-3958, still the same    *)
(* callsMu hold:                                                           *)
(*   - the WithCancelCause cancel is created (:3897)                       *)
(*   - the operation lease is acquired (:3917)                             *)
(*   - the ongoingCall is registered (:3950-3952; always, because the      *)
(*     default concurrency key - the session ID - is never empty)          *)
(*   - the fn goroutine starts (:3954)                                     *)
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
    /\ invocations' = [invocations EXCEPT ![i].phase = "failed",
                                           ![i].path = "leaseFailure"]
    /\ UNCHANGED <<res, ongoingCalls, ongoingCallIndex, sessionEdges, countedEdges,
                   sessionRelease, evals, epoch, flushed>>

(***************************************************************************)
(* FnComplete: the executing fn finishes (the goroutine started at         *)
(* cache.go:3954). Three possible outcomes:                                *)
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
(* Go: wait's !completed branch, cache.go:4164-4182. Notes:                *)
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
          /\ invocations' = [invocations EXCEPT ![i].phase = "canceled"]
    /\ UNCHANGED <<res, sessionEdges, countedEdges, sessionRelease,
                   evals, epoch, flushed>>

\* FnWindDown: the cancel-requested executor finally exits. Until it does,
\* its resolver can still issue nested cache calls (SpawnNested) - that
\* window is the drain escape. Gated by ModelNestedCalls so earlier
\* configurations keep their exact state spaces.
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
(* through the same !completed branch (cache.go:4164-4182): waiters--,     *)
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
                THEN "cancelDropHold" ELSE "canceled"]
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
       /\ invocations' = [invocations EXCEPT ![i].phase = "canceled"]
    /\ UNCHANGED <<ongoingCallIndex, sessionEdges, countedEdges, sessionRelease,
                   evals, epoch, flushed>>

\* WaiterObserveFnErr: the fn failed; each waiter observes the error and
\* returns it. The last waiter removes the Cache.ongoingCalls index entry.
\* Go: wait's completionErr path, cache.go:4183-4193.
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
          /\ invocations' = [invocations EXCEPT ![i].phase = "failed"]
    /\ UNCHANGED <<res, sessionEdges, countedEdges, sessionRelease,
                   evals, epoch, flushed>>

(***************************************************************************)
(* PUBLICATION - initCompletedResult, entered through the sync.Once in     *)
(* wait (cache.go:4196). The next several actions form its state machine,  *)
(* driven by oc.pubState. The publishing waiter runs it to completion      *)
(* regardless of its own caller's context (the publication context is      *)
(* WithoutCancel, cache.go:4216).                                     *)
(*                                                                         *)
(* PubBegin: entering the Once, plus the lock-free prologue - oc.res is    *)
(* set to a fresh empty &sharedResult{} at cache.go:4333 before any lock   *)
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
(* section (ending at cache.go:4633) that does, in order:                  *)
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
    \* the sharedResult at cache.go:2878). Which results are lazy is the
    \* producer's business, so the model picks nondeterministically.
    /\ \E lazyCb \in IF ModelLazy THEN {"none", "armed"} ELSE {"none"} :
       \E deps \in {{}} \cup {{d} : d \in {r \in ResultIds : res[r].registered}} :
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
                       laundered |-> FALSE,
                       \* lazy-evaluation state, mirroring the lazyMu block
                       \* on sharedResult (cache.go:1584-1597):
                       lazyCb |-> lazyCb,        \* lazyEval callback stored?
                       lazyComplete |-> FALSE,   \* lazyEvalComplete
                       lazyPhase |-> "idle",     \* lazyEvalWaitCh lifecycle
                       lazyWaiters |-> 0,        \* lazyEvalWaiters
                       lazyRunning |-> 0,        \* callbacks actually running
                       lazyErr |-> "none"]       \* lazyEvalErr, latched
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
    IN IF ~res[rf].registered
       THEN rf
       ELSE LET live == {r \in LiveInClass(ClassOf[res[rf].call]) :
                            res[r].barrier \in {"none", "closedOk"}}
            IN CHOOSE r \in live : \A q \in live : r <= q

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
(* OUTSIDE egraphMu (cache.go:4635) while the result is already            *)
(* lookup-visible - that is why the barrier exists. Steps:                 *)
(*   - PubAttachAddDep: attachment discovers an embedded child result and  *)
(*     records the dependency edge; each such AddExplicitDependency is     *)
(*     its own egraphMu critical section (cache.go:2137)              *)
(*   - PubFinishOk: attachment succeeded; close the barrier clean          *)
(*   - PubAttachFailDropHold: attachment failed; release the hold and      *)
(*     cascade in one egraphMu critical section                            *)
(*   - PubAttachFailCloseBarrier: close the barrier with the error in the  *)
(*     following attachDepsMu critical section                             *)
(*                                                                         *)
(* STATED ASSUMPTION: dependency edges never form a cycle. The Go cache    *)
(* does not enforce this - addExplicitDependencyLocked (cache.go:2168)     *)
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

PubAttachAddDep(o) ==
    /\ ongoingCalls[o].pubState = "attaching"
    /\ \E d \in ResultIds :
        /\ res[d].registered
        /\ d # ongoingCalls[o].resId
        /\ d \notin res[ongoingCalls[o].resId].deps
        \* the stated no-cycle assumption (see the comment block above)
        /\ ~DepReachable(res, d, ongoingCalls[o].resId)
        /\ res' = [res EXCEPT
             ![ongoingCalls[o].resId].deps = @ \cup {d},
             ![d].own = @ + 1]
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
\* (wait, cache.go:4225-4231), in its own callsMu critical section AFTER
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
\* Go: wait's initCompletedResultErr path, cache.go:4232-4245.
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
               ![i].phase = IF dropHold THEN "pubErrDropHold" ELSE "failed"]
    /\ UNCHANGED <<res, ongoingCallIndex, sessionEdges, countedEdges, sessionRelease,
                   evals, epoch, flushed>>

\* WaiterDropHoldPubErr: the last waiter of a failed publication drops the
\* handoff hold in its own egraphMu section (releaseOngoingCallHandoff,
\* cache.go:4289-4306). The persistence arm is vacuous here because
\* needsPersistedEdge requires a successful publication.
WaiterDropHoldPubErr(i) ==
    /\ invocations[i].phase = "pubErrDropHold"
    /\ LET o == invocations[i].oc IN
       /\ ongoingCalls' = [ongoingCalls EXCEPT ![o].hold = FALSE]
       /\ res' = PersistThenDropHold(o)
       /\ invocations' = [invocations EXCEPT ![i].phase = "failed"]
    /\ UNCHANGED <<ongoingCallIndex, sessionEdges, countedEdges, sessionRelease,
                   evals, epoch, flushed>>

(***************************************************************************)
(* WAITER SUCCESS PATH. In code order (wait, cache.go:4259-4281):          *)
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
\* release the handoff hold. Go: wait, cache.go:4267-4270.
WaiterDepart(i) ==
    /\ invocations[i].phase \in {"depart", "refusedDepart"}
    /\ LET o == invocations[i].oc
           last == ongoingCalls[o].waiters = 1
           refused == invocations[i].phase = "refusedDepart"
       IN /\ ongoingCalls' = [ongoingCalls EXCEPT ![o].waiters = @ - 1]
          /\ invocations' = [invocations EXCEPT
               ![i].phase = IF last /\ ongoingCalls[o].hold
                         THEN IF refused THEN "refusedReleaseHold" ELSE "releaseHold"
                         ELSE IF refused THEN "refused" ELSE "readBarrier"]
    /\ UNCHANGED <<res, ongoingCallIndex, sessionEdges, countedEdges, sessionRelease,
                   evals, epoch, flushed>>

\* WaiterReleaseHold: the last waiter drops the publication handoff hold,
\* in its own egraphMu section - releaseOngoingCallHandoff, which first
\* commits the admission-close persistence intent. From here on, only real
\* edges (session, dependency, persisted) keep the result alive.
\* Go: wait, cache.go:4271-4275 -> releaseOngoingCallHandoff.
WaiterReleaseHold(i) ==
    /\ invocations[i].phase \in {"releaseHold", "refusedReleaseHold"}
    /\ LET o == invocations[i].oc
           refused == invocations[i].phase = "refusedReleaseHold"
       IN
       /\ ongoingCalls' = [ongoingCalls EXCEPT ![o].hold = FALSE]
       /\ res' = PersistThenDropHold(o)
       /\ invocations' = [invocations EXCEPT ![i].phase = IF refused THEN "refused" ELSE "readBarrier"]
    /\ UNCHANGED <<ongoingCallIndex, sessionEdges, countedEdges, sessionRelease,
                   evals, epoch, flushed>>

(***************************************************************************)
(* THE READ BARRIER: every return path waits for the result's dependency   *)
(* attachment to finish before handing the result out.                     *)
(*                                                                         *)
(* Go: ensurePersistedHitValueLoaded, cache_persistence_import.go:545-571. *)
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
          \* back here once the payload is in memory.
          /\ res[r].payload = "decoded"
          /\ invocations' = [invocations EXCEPT
               ![i].phase = IF invocations[i].path = "hit"
                                  /\ invocations[i].persistable
                             THEN "persistHit" ELSE "done",
               ![i].retLive = res[r].registered /\ ~res[r].released,
               ![i].retOwned = ProtectedReturn(invocations[i].sess, r),
               ![i].retBarrierOK = res[r].barrier \in {"none", "closedOk"},
               ![i].retClean = ~res[r].laundered]
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
               ![i].phase = "done",
               ![i].retLive = res[r].registered /\ ~res[r].released,
               ![i].retOwned = ProtectedReturn(invocations[i].sess, r),
               ![i].retBarrierOK = res[r].barrier \in {"none", "closedOk"},
               ![i].retClean = ~res[r].laundered]
    /\ UNCHANGED <<ongoingCalls, ongoingCallIndex, sessionEdges, countedEdges,
                   sessionRelease, evals, epoch, flushed>>

ReadBarrierErrHit(i) ==
    /\ invocations[i].phase = "readBarrier"
    /\ invocations[i].path = "hit"
    /\ res[invocations[i].resId].barrier = "closedErr"
    /\ invocations' = [invocations EXCEPT ![i].phase = "failed"]
    /\ UNCHANGED <<res, ongoingCalls, ongoingCallIndex, sessionEdges, countedEdges,
                   sessionRelease, evals, epoch, flushed>>

ReadBarrierErrWait(i) ==
    /\ invocations[i].phase = "readBarrier"
    /\ invocations[i].path = "wait"
    /\ res[invocations[i].resId].barrier = "closedErr"
    /\ invocations' = [invocations EXCEPT ![i].phase = "failed"]
    /\ UNCHANGED <<res, ongoingCalls, ongoingCallIndex, sessionEdges, countedEdges,
                   sessionRelease, evals, epoch, flushed>>

(***************************************************************************)
(* READER CANCELLATION (ReaderCanCancel). ensurePersistedHitValueLoaded    *)
(* waits with a select in two places: on the attach barrier               *)
(* (cache_persistence_import.go:560-563) and on another caller's decode    *)
(* (:596-599). Each has a ctx.Done arm, and Go's select picks among ready  *)
(* arms nondeterministically, so a canceled caller can fail here even on   *)
(* a healthy result whose barrier is already closed. Cancellation leaves   *)
(* the session edge in place for release.                                  *)
(***************************************************************************)
ReadBarrierCancelHit(i) ==
    /\ ReaderCanCancel
    /\ invocations[i].phase = "readBarrier"
    /\ invocations[i].path = "hit"
    /\ invocations' = [invocations EXCEPT ![i].phase = "canceled"]
    /\ UNCHANGED <<res, ongoingCalls, ongoingCallIndex, sessionEdges, countedEdges,
                   sessionRelease, evals, epoch, flushed>>

\* The WAIT-path twin: the error propagates and the claimed session edge
\* simply remains (wait, cache.go:4277-4281) - same as ReadBarrierErrWait.
ReadBarrierCancelWait(i) ==
    /\ ReaderCanCancel
    /\ invocations[i].phase = "readBarrier"
    /\ invocations[i].path = "wait"
    /\ invocations' = [invocations EXCEPT ![i].phase = "canceled"]
    /\ UNCHANGED <<res, ongoingCalls, ongoingCallIndex, sessionEdges, countedEdges,
                   sessionRelease, evals, epoch, flushed>>

(***************************************************************************)
(* PERSISTED-PAYLOAD DECODE. An imported result can exist                  *)
(* with its payload still encoded as a persisted envelope; the read        *)
(* barrier decodes it on first use (ensurePersistedHitValueLoaded's        *)
(* decode loop, cache_persistence_import.go:573-701). Per result:          *)
(*   - one caller becomes the decode leader (publishes                     *)
(*     persistDecodeWaitCh, :611-612) and performs the decode itself       *)
(*   - later callers wait on that channel                                  *)
(*   - the finish (:615-621) latches the error, CLEARS the channel, and    *)
(*     closes it - so unlike the lazy singleflight there is no lingering   *)
(*     published channel: after any finish, the next demand leads a        *)
(*     fresh attempt. Failure is therefore always retryable; success       *)
(*     installs the payload permanently.                                   *)
(* A woken waiter re-reads the CURRENT latched error, which a newer        *)
(* leader may have already reset to none - in that case the waiter just    *)
(* loops: re-checks the payload and either returns it, rejoins a running   *)
(* attempt, or leads a new one. That loop is the "continue" in the Go.     *)
(*                                                                         *)
(* A decode failure returns an error and leaves the claimed session edge   *)
(* in place for session release.                                           *)
(***************************************************************************)

DecodeLead(i) ==
    /\ invocations[i].phase = "readBarrier"
    /\ LET r == invocations[i].resId IN
       /\ res[r].barrier \in {"none", "closedOk"}
       /\ res[r].payload = "envelope"
       /\ res[r].decodePhase = "idle"
       /\ res' = [res EXCEPT ![r].decodePhase = "running",
                             ![r].decodeErr = "none"]
       /\ invocations' = [invocations EXCEPT ![i].phase = "decoding"]
    /\ UNCHANGED <<ongoingCalls, ongoingCallIndex, sessionEdges, countedEdges,
                   sessionRelease, evals, epoch, flushed>>

DecodeLeadFinish(i) ==
    /\ invocations[i].phase = "decoding"
    /\ LET r == invocations[i].resId IN
       \E ok \in IF DecodeCanFail THEN {TRUE, FALSE} ELSE {TRUE} :
          IF ok
          THEN /\ res' = [res EXCEPT ![r].payload = "decoded",
                                     ![r].decodePhase = "idle",
                                     ![r].decodeErr = "none"]
               \* loop back to the top of the read barrier: the payload is
               \* in memory now, so the normal return path applies
               /\ invocations' = [invocations EXCEPT ![i].phase = "readBarrier"]
          ELSE /\ res' = [res EXCEPT ![r].decodePhase = "idle",
                                     ![r].decodeErr = "fail"]
               /\ invocations' = [invocations EXCEPT ![i].phase = "decodeErr"]
    /\ UNCHANGED <<ongoingCalls, ongoingCallIndex, sessionEdges, countedEdges,
                   sessionRelease, evals, epoch, flushed>>

DecodeJoin(i) ==
    /\ invocations[i].phase = "readBarrier"
    /\ LET r == invocations[i].resId IN
       /\ res[r].barrier \in {"none", "closedOk"}
       /\ res[r].payload = "envelope"
       /\ res[r].decodePhase = "running"
       /\ invocations' = [invocations EXCEPT ![i].phase = "decodeJoined"]
    /\ UNCHANGED <<res, ongoingCalls, ongoingCallIndex, sessionEdges, countedEdges,
                   sessionRelease, evals, epoch, flushed>>

DecodeWake(i) ==
    /\ invocations[i].phase = "decodeJoined"
    /\ LET r == invocations[i].resId IN
       /\ res[r].decodePhase = "idle"
       /\ invocations' = [invocations EXCEPT ![i].phase =
            IF res[r].decodeErr = "fail" THEN "decodeErr" ELSE "readBarrier"]
    /\ UNCHANGED <<res, ongoingCalls, ongoingCallIndex, sessionEdges, countedEdges,
                   sessionRelease, evals, epoch, flushed>>

\* Decode failed for a HIT-path caller.
DecodeFailHit(i) ==
    /\ invocations[i].phase = "decodeErr"
    /\ invocations[i].path = "hit"
    /\ invocations' = [invocations EXCEPT ![i].phase = "failed"]
    /\ UNCHANGED <<res, ongoingCalls, ongoingCallIndex, sessionEdges, countedEdges, sessionRelease,
                   evals, epoch, flushed>>

\* Decode failed for a WAIT-path caller: the error just propagates; the
\* session edge claimed after publication remains (wait, cache.go:4271-4275).
DecodeFailWait(i) ==
    /\ invocations[i].phase = "decodeErr"
    /\ invocations[i].path = "wait"
    /\ invocations' = [invocations EXCEPT ![i].phase = "failed"]
    /\ UNCHANGED <<res, ongoingCalls, ongoingCallIndex, sessionEdges, countedEdges,
                   sessionRelease, evals, epoch, flushed>>

(***************************************************************************)
(* ReleaseSession, in its two real critical sections.                      *)
(*                                                                         *)
(* Go: Cache.ReleaseSession. First a sessionMu section marks the session   *)
(* released, snapshots its complete atomic edges, and deletes its records. *)
(* Then an egraphMu section removes one ownership unit per registered      *)
(* snapshotted result and collects whatever reaches zero.                  *)
(*                                                                         *)
(* Release can begin at ANY time unless DrainOnRelease is on - and the     *)
(* drain, as implemented, only waits out HANDLER-origin calls              *)
(* (dagqlInFlight counts serveQuery handlers, session.go:603-608);         *)
(* executor-nested calls keep running through it. Once per session.        *)
(***************************************************************************)

\* The sessionMu section: set the released tombstone, snapshot the session's
\* records, and delete them. Counts are untouched here. A session with no records skips the
\* decrement section entirely - the Go still takes egraphMu for an empty
\* loop, but that hold changes nothing observable.
ReleaseSessionRecord(s) ==
    /\ AllowRelease
    /\ sessionRelease[s].phase = "live"
    /\ DrainOnRelease =>
         \A i \in InvocationIds :
             (invocations[i].sess = s /\ invocations[i].origin = "handler")
                 => invocations[i].phase \in TerminalPhases
    /\ LET snap == {r \in ResultIds : <<s, r>> \in sessionEdges}
       IN /\ sessionRelease' = [sessionRelease EXCEPT ![s] =
                [phase |-> IF snap = {} THEN "released" ELSE "collecting",
                 snap |-> snap]]
          /\ sessionEdges' = {e \in sessionEdges : e[1] # s}
    /\ UNCHANGED <<invocations, res, ongoingCalls, ongoingCallIndex,
                   countedEdges, evals, epoch, flushed>>

\* The egraphMu section: remove one unit per snapshotted, still-registered
\* result, then collect.
ReleaseSessionCollect(s) ==
    /\ sessionRelease[s].phase = "collecting"
    /\ LET snap == sessionRelease[s].snap
           live == {r \in snap : res[r].registered}
           rf0 == [r \in DOMAIN res |->
                     IF r \in live
                     THEN [res[r] EXCEPT !.own = @ - 1]
                     ELSE res[r]]
       IN /\ res' = Cascade(rf0)
          /\ countedEdges' = countedEdges \ {<<s, r>> : r \in snap}
          /\ sessionRelease' = [sessionRelease EXCEPT ![s] =
                [phase |-> "released", snap |-> {}]]
    /\ UNCHANGED <<invocations, ongoingCalls, ongoingCallIndex,
                   sessionEdges, evals, epoch, flushed>>

(***************************************************************************)
(* PruneCut: cut one persisted root edge and let the normal cascade        *)
(* collect whatever that leaves unowned. Fireable at any time.             *)
(*                                                                         *)
(* Go: removePersistedEdge, cache.go:913-942. This one action is the only  *)
(* way either prune mode (disk-policy or structural) touches the live      *)
(* kernel; all prune planning happens outside the lock and is not          *)
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
(* FLUSH AND RESTART. Graceful shutdown removes every                      *)
(* session (each release preceded by the server's drain when the server    *)
(* behaves), then Cache.Close snapshots the retained graph in one          *)
(* egraphMu hold and writes it out (persistCurrentState ->                 *)
(* snapshotPersistState, cache_persistence_worker.go:14/:27; ordering in   *)
(* GracefulStop, engine/server/server.go:799). The model requires only     *)
(* what GracefulStop's structure guarantees - all sessions released -      *)
(* NOT that in-flight work has finished: the cache does not enforce        *)
(* that, so a snapshot racing an unfinished publication is a reachable    *)
(* capture and the FlushCleanCapture property judges it.                   *)
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

Flush ==
    /\ ModelPersistence
    /\ epoch = 1
    /\ ~flushed.done
    \* GracefulStop runs each session's release to completion (both
    \* critical sections) before Cache.Close snapshots.
    /\ \A s \in Sessions : sessionRelease[s].phase = "released"
    /\ flushed' = [done |-> TRUE, rows |-> [r \in 1..Len(res) |->
         [keep      |-> KeptByPersistedRoot(r),
          call      |-> res[r].call,
          persisted |-> res[r].persisted,
          deps      |-> res[r].deps,
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
(* envelopes, and IDs preserved (collected slots stay as dead husks).      *)
(* The laundered flag carries each row's dirty verdict forward so the      *)
(* NoLaunderedServe property can see what the restarted engine cannot.     *)
(***************************************************************************)

Restart ==
    /\ ModelPersistence
    /\ epoch = 1
    /\ flushed.done
    /\ epoch' = 2
    /\ \E pm \in [1..Len(flushed.rows) -> {"decoded", "envelope"}] :
         res' = [x \in 1..Len(flushed.rows) |->
             IF flushed.rows[x].keep
             THEN ImportedResult(
                    flushed.rows[x].call, flushed.rows[x].persisted, flushed.rows[x].deps,
                    (IF flushed.rows[x].persisted THEN 1 ELSE 0)
                      + Cardinality({y \in 1..Len(flushed.rows) :
                            flushed.rows[y].keep /\ x \in flushed.rows[y].deps}),
                    pm[x],
                    flushed.rows[x].dirty)
             ELSE DeadHusk]
    /\ invocations' = [i \in InvocationIds |->
         IF invocations[i].phase \in TerminalPhases THEN invocations[i]
         ELSE [invocations[i] EXCEPT !.phase = "canceled"]]
    /\ ongoingCalls' = [o \in OngoingCallIds |->
         \* "exited", not "canceled": a restart kills the process, so no
         \* executor is winding down and no nested call can spawn from it
         [ongoingCalls[o] EXCEPT !.fnState = "exited", !.pubState = "none",
                                 !.pubBy = 0,
                                 !.hold = FALSE, !.inIndex = FALSE,
                                 !.needsPersistedEdge = FALSE,
                                 !.waiters = 0]]
    /\ ongoingCallIndex' = [k \in Calls \X Sessions |-> 0]
    /\ evals' = [e \in EvalIds |->
         IF evals[e].phase \in {"demand", "waiting"}
         THEN [evals[e] EXCEPT !.phase = "abandoned"]
         ELSE evals[e]]
    /\ sessionEdges' = {}
    /\ countedEdges' = {}
    /\ sessionRelease' = [s \in Sessions |-> [phase |-> "live", snap |-> {}]]
    /\ UNCHANGED flushed

---------------------------------------------------------------------------
(***************************************************************************)
(* LAZY EVALUATION. A resolver can return a result whose                   *)
(* expensive materialization is deferred: the value carries a callback,    *)
(* stored on the sharedResult (registerLazyEvaluation, cache.go:2878).     *)
(* Anyone later needing the materialized value calls Cache.Evaluate,       *)
(* which coordinates all callers per result (evaluateOne,                  *)
(* cache.go:3000):                                                         *)
(*   - if evaluation already completed, return immediately                 *)
(*   - if an attempt is in flight, join it as a waiter                     *)
(*   - otherwise start the callback in a goroutine and wait               *)
(* Success is permanent (lazyEvalComplete set, callback cleared,           *)
(* cache.go:3187-3191). Failure leaves the callback in place so a later    *)
(* Evaluate retries. Each waiter can abandon its wait independently        *)
(* (waitForLazyEvaluation's ctx.Done arm, cache.go:2945-2954); the LAST    *)
(* waiter to abandon cancels the running callback.                         *)
(*                                                                         *)
(* One window deserves attention, and gets a property below: after the     *)
(* last waiter abandons and cancels, the attempt's wait channel stays      *)
(* published until the dying callback actually finishes. A brand-new       *)
(* Evaluate arriving in that window JOINS the dying attempt               *)
(* (evaluateOne's join arm checks only lazyEvalWaitCh != nil) and is       *)
(* handed the earlier caller's cancellation error - it fails though        *)
(* nothing is wrong with the result and it never asked to cancel.          *)
(*                                                                         *)
(* Evaluators here are modeled independently of the GetOrInitCall          *)
(* invocations: in the engine, Evaluate is called by code already          *)
(* holding the result, at any later time. Configurations that enable       *)
(* lazy evaluation keep ReleaseSession and PruneCut off: what happens      *)
(* when a result is collected while its callback runs belongs with the     *)
(* release-during-in-flight investigations, not here.                      *)
(*                                                                         *)
(* Model phases of one attempt (res[r].lazyPhase):                         *)
(*   "idle"            no attempt in flight (lazyEvalWaitCh == nil)        *)
(*   "running"         callback running, waiters waiting                   *)
(*   "cancelRequested" every waiter abandoned; the last one invoked the    *)
(*                     cancel; the callback has not finished yet. The      *)
(*                     wait channel is still published - the stale-error   *)
(*                     join window                                         *)
(*   "done"            callback finished and the error is latched, but    *)
(*                     waiters have not all drained; the last waiter to    *)
(*                     drain resets the state to "idle"                    *)
(***************************************************************************)

\* A new Evaluate caller appears, demanding some registered result.
EvalSpawn ==
    /\ ModelLazy
    /\ Len(evals) < MaxEvals
    /\ \E r \in ResultIds :
        /\ res[r].registered
        /\ evals' = Append(evals, [target |-> r, phase |-> "demand"])
    /\ UNCHANGED <<invocations, res, ongoingCalls, ongoingCallIndex,
                   sessionEdges, countedEdges, sessionRelease,
                   epoch, flushed>>

\* Fast path: nothing to do - evaluation already completed, or the value
\* carries no callback (evaluateOne, cache.go:3023-3039).
EvalNoWork(e) ==
    /\ evals[e].phase = "demand"
    /\ LET r == evals[e].target IN
       res[r].lazyComplete \/ res[r].lazyCb = "none"
    /\ evals' = [evals EXCEPT ![e].phase = "done"]
    /\ UNCHANGED <<invocations, res, ongoingCalls, ongoingCallIndex,
                   sessionEdges, countedEdges, sessionRelease,
                   epoch, flushed>>

\* Start the callback: no attempt is in flight, so this caller becomes the
\* leader - one lazyMu critical section publishes the wait channel and the
\* cancel, then the callback goroutine starts (evaluateOne,
\* cache.go:3145-3184).
EvalStartAttempt(e) ==
    /\ evals[e].phase = "demand"
    /\ LET r == evals[e].target IN
       /\ res[r].lazyCb = "armed"
       /\ ~res[r].lazyComplete
       /\ res[r].lazyPhase = "idle"
       /\ res' = [res EXCEPT
            ![r].lazyPhase = "running",
            ![r].lazyWaiters = @ + 1,
            ![r].lazyRunning = @ + 1,
            ![r].lazyErr = "none"]
       /\ evals' = [evals EXCEPT ![e].phase = "waiting"]
    /\ UNCHANGED <<invocations, ongoingCalls, ongoingCallIndex,
                   sessionEdges, countedEdges, sessionRelease,
                   epoch, flushed>>

\* Join an attempt already in flight (evaluateOne's join arm,
\* cache.go:3040-3063). The guard is only "a wait channel is published" -
\* which includes an attempt that every previous waiter has already
\* abandoned ("cancelRequested") and one that has finished but not
\* drained ("done"). Joining the first of those is the stale-error window.
EvalJoin(e) ==
    /\ evals[e].phase = "demand"
    /\ LET r == evals[e].target IN
       /\ res[r].lazyCb = "armed"
       /\ ~res[r].lazyComplete
       /\ res[r].lazyPhase \in {"running", "cancelRequested", "done"}
       /\ res' = [res EXCEPT ![r].lazyWaiters = @ + 1]
       /\ evals' = [evals EXCEPT ![e].phase = "waiting"]
    /\ UNCHANGED <<invocations, ongoingCalls, ongoingCallIndex,
                   sessionEdges, countedEdges, sessionRelease,
                   epoch, flushed>>

\* The callback finishes on its own (the goroutine tail, cache.go:3184-
\* 3202): latch the outcome; success also marks completion and clears the
\* callback. If no waiters remain, the goroutine itself resets the state
\* to idle; otherwise the state stays "done" until the last waiter drains.
\* The Go goroutine latches lazyEvalErr unconditionally. That is safe -
\* and this model needs no cross-attempt-overwrite action - only because
\* no second attempt can start while an old goroutine is alive: the state
\* resets exclusively here or in the last waiter's wake, both of which
\* happen after this latch.
EvalCallbackFinish(r) ==
    /\ r \in ResultIds
    /\ res[r].lazyPhase = "running"
    /\ res[r].lazyRunning > 0
    /\ \E ok \in IF LazyCanFail THEN {TRUE, FALSE} ELSE {TRUE} :
        LET drained == res[r].lazyWaiters = 0
        IN res' = [res EXCEPT
             ![r].lazyRunning = @ - 1,
             ![r].lazyComplete = @ \/ ok,
             ![r].lazyCb = IF ok THEN "none" ELSE @,
             ![r].lazyPhase = IF drained THEN "idle" ELSE "done",
             ![r].lazyErr = IF drained THEN "none"
                            ELSE IF ok THEN "none" ELSE "fail"]
    /\ UNCHANGED <<invocations, ongoingCalls, ongoingCallIndex, evals,
                   sessionEdges, countedEdges, sessionRelease,
                   epoch, flushed>>

\* The canceled callback winds down. It may still succeed (the callback
\* returns nil despite its context being canceled) or finish with the
\* cancellation error - which is then what any joiner who arrived during
\* the wind-down is handed.
EvalCallbackFinishCanceled(r) ==
    /\ r \in ResultIds
    /\ res[r].lazyPhase = "cancelRequested"
    /\ res[r].lazyRunning > 0
    /\ \E ok \in {TRUE, FALSE} :
        LET drained == res[r].lazyWaiters = 0
        IN res' = [res EXCEPT
             ![r].lazyRunning = @ - 1,
             ![r].lazyComplete = @ \/ ok,
             ![r].lazyCb = IF ok THEN "none" ELSE @,
             ![r].lazyPhase = IF drained THEN "idle" ELSE "done",
             ![r].lazyErr = IF drained THEN "none"
                            ELSE IF ok THEN "none" ELSE "cancel"]
    /\ UNCHANGED <<invocations, ongoingCalls, ongoingCallIndex, evals,
                   sessionEdges, countedEdges, sessionRelease,
                   epoch, flushed>>

\* A waiter wakes after the callback finished (waitForLazyEvaluation's
\* wait-channel arm, cache.go:2925-2943): it reads the latched error,
\* drops its waiter count, and the last waiter out resets the state to
\* idle. A "cancel" error here means this caller is failing with an
\* abandonment error some OTHER caller caused - the stale-cancel case.
EvalWake(e) ==
    /\ evals[e].phase = "waiting"
    /\ LET r == evals[e].target
           last == res[r].lazyWaiters = 1
       IN /\ res[r].lazyPhase = "done"
          /\ res' = [res EXCEPT
               ![r].lazyWaiters = @ - 1,
               ![r].lazyPhase = IF last THEN "idle" ELSE "done",
               ![r].lazyErr = IF last THEN "none" ELSE @]
          /\ evals' = [evals EXCEPT ![e].phase =
               CASE res[r].lazyErr = "none" -> "done"
                 [] res[r].lazyErr = "fail" -> "failedCallback"
                 [] OTHER -> "failedStale"]
    /\ UNCHANGED <<invocations, ongoingCalls, ongoingCallIndex,
                   sessionEdges, countedEdges, sessionRelease,
                   epoch, flushed>>

\* A waiter gives up (its own context canceled - waitForLazyEvaluation's
\* ctx.Done arm, cache.go:2945-2954). Only the LAST waiter to leave
\* invokes the attempt's cancel; earlier leavers just walk away. Note
\* what is NOT cleared: the wait channel stays published until the dying
\* callback finishes.
EvalAbandon(e) ==
    /\ evals[e].phase = "waiting"
    /\ LET r == evals[e].target
           last == res[r].lazyWaiters = 1
       IN \* Abandoning is possible in "done" too: when the callback has
          \* finished AND the waiter's own context is canceled, both arms
          \* of the Go select are ready and the runtime picks either. A
          \* last waiter leaving through ctx.Done does NOT clear the
          \* attempt state (that arm clears nothing), so the latched error
          \* stays published with zero waiters - the next Evaluate joins
          \* it and drains the stale error. Invoking the cancel on an
          \* already-finished attempt is a harmless no-op.
          /\ res[r].lazyPhase \in {"running", "cancelRequested", "done"}
          /\ res' = [res EXCEPT
               ![r].lazyWaiters = @ - 1,
               ![r].lazyPhase = IF last /\ res[r].lazyPhase = "running"
                                THEN "cancelRequested" ELSE @]
          /\ evals' = [evals EXCEPT ![e].phase = "abandoned"]
    /\ UNCHANGED <<invocations, ongoingCalls, ongoingCallIndex,
                   sessionEdges, countedEdges, sessionRelease,
                   epoch, flushed>>

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
         \/ DecodeLead(i) \/ DecodeLeadFinish(i) \/ DecodeJoin(i)
         \/ DecodeWake(i) \/ DecodeFailHit(i) \/ DecodeFailWait(i)
    \/ \E o \in OngoingCallIds :
         \/ FnComplete(o) \/ PubBegin(o)
         \/ PubIndexFresh(o) \/ PubAdopt(o) \/ PubIndexReuse(o)
         \/ PubAttachAddDep(o) \/ PubFinishOk(o)
         \/ PubAttachFailDropHold(o) \/ PubAttachFailCloseBarrier(o)
         \/ PubUnregister(o) \/ FnWindDown(o)
    \/ \E s \in Sessions : ReleaseSessionRecord(s) \/ ReleaseSessionCollect(s)
    \/ \E r \in 1..Len(res) : PruneCut(r)
    \/ EvalSpawn
    \/ \E e \in EvalIds :
         \/ EvalNoWork(e) \/ EvalStartAttempt(e) \/ EvalJoin(e)
         \/ EvalWake(e) \/ EvalAbandon(e)
    \/ \E r \in 1..Len(res) :
         EvalCallbackFinish(r) \/ EvalCallbackFinishCanceled(r)
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
(*   - WaiterCancel, WaiterCancelLate, ReadBarrierCancelHit/Wait, and      *)
(*     every failure-injection branch: possibilities, not obligations      *)
(*   - ReleaseSession and PruneCut: external events                        *)
(*   - FnWindDown: how long a canceled executor unwinds is external.       *)
(*     (No liveness configuration enables ModelNestedCalls, so this        *)
(*     cannot mask a wedge today.)                                         *)
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
    \/ DecodeLead(i) \/ DecodeLeadFinish(i) \/ DecodeJoin(i)
    \/ DecodeWake(i) \/ DecodeFailHit(i) \/ DecodeFailWait(i)

\* An evaluator's own forward steps. Abandoning is deliberately NOT here:
\* giving up is a caller's choice, never an obligation.
EvalProgress(e) ==
    \/ EvalNoWork(e) \/ EvalStartAttempt(e) \/ EvalJoin(e) \/ EvalWake(e)

\* The lazy callback goroutine always eventually finishes, canceled or
\* not. Fairness sits on the disjunction of both finish shapes for the
\* same reason it does for attachment: weak fairness on the success arm
\* alone would wrongly forbid persistent failure.
LazyCallbackProgress(r) ==
    EvalCallbackFinish(r) \/ EvalCallbackFinishCanceled(r)

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
    /\ \A r \in 1..MaxResults :
         WF_vars(r \in ResultIds /\ LazyCallbackProgress(r))

---------------------------------------------------------------------------
(***************************************************************************)
(* PROPERTIES. All but the last are safety invariants: TLC checks them in  *)
(* every reachable state. EventuallyTerminal is the one liveness           *)
(* property, checked against LiveSpec. Each .cfg selects only the subset   *)
(* that isolates its question - checking every property in every           *)
(* configuration would conflate independent questions: a scenario that     *)
(* exercises one failure could drown the property under test in violations *)
(* of unrelated properties.                                                *)
(***************************************************************************)

\* Basic shape sanity: bounds respected, counts non-negative, and no
\* counted edge without its record.
TypeOK ==
    /\ Len(invocations) <= MaxInvocations
    /\ Len(res) <= MaxResults
    /\ Len(evals) <= MaxEvals
    \* A counted edge's record can be gone only while its session's
    \* release is between its two critical sections (records wiped,
    \* decrements pending).
    /\ \A e \in countedEdges :
         e \in sessionEdges
           \/ (sessionRelease[e[1]].phase = "collecting"
                 /\ e[2] \in sessionRelease[e[1]].snap)
    /\ epoch \in {1, 2}
    /\ \A o \in OngoingCallIds : ongoingCalls[o].waiters >= 0
    /\ \A r \in ResultIds : res[r].lazyWaiters >= 0 /\ res[r].lazyRunning >= 0
    /\ \A i \in InvocationIds :
         /\ invocations[i].origin \in {"handler", "nested"}
         /\ invocations[i].refusedEpoch \in 0..epoch
         /\ invocations[i].lookupBarrierAtSelection
              \in {"none", "open", "closedOk", "closedErr"}

\* Ownership accounting is exact: for every registered result, the
\* incrementally-maintained counter equals the recount of its edges
\* (counted session edges + dependency parents + handoff holds +
\* persisted edge).
OwnershipExact ==
    \A r \in ResultIds :
        res[r].registered => res[r].own = DerivedOwn(r)

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
            /\ invocations[i].phase = "failed"
            /\ invocations[i].oc = 0
            /\ invocations[i].resId = 0

\* Racing alone never manufactures an execution failure: if no failure
\* injection is enabled, no invocation ends in "failed". Cancellation and a
\* released-session refusal have their own terminal phases.
NoSpuriousErrors ==
    (~FnCanFail /\ ~AttachCanFail /\ ~LeaseCanFail /\ ~DecodeCanFail) =>
        \A i \in InvocationIds : invocations[i].phase # "failed"

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

\* Success is permanent: once a result's evaluation completed, no callback
\* for it is running or can start again (the callback is cleared and every
\* start path checks lazyEvalComplete first).
LazySuccessPermanent ==
    \A r \in ResultIds : res[r].lazyComplete => res[r].lazyRunning = 0

\* An Evaluate caller never fails with a cancellation error it did not
\* cause. The code violates this: a caller arriving while a fully-
\* abandoned attempt is still winding down joins that attempt
\* (evaluateOne's join arm checks only that a wait channel is published)
\* and is handed the abandoners' cancellation error - a spurious failure
\* on a healthy, retryable result. "failedStale" records exactly that
\* outcome at wake time.
NoStaleCancelError ==
    \A e \in EvalIds : evals[e].phase # "failedStale"

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

\* The graceful-shutdown snapshot captures only clean, fully-retained
\* state: every kept row had a cleanly-closed (or never-armed) attach
\* barrier and ownership fully explained by persisted + dependency edges.
\* Violated when the flush races an unfinished publication - reachable
\* because the cache itself never waits for in-flight work; only the
\* server's drain does.
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
            (e \in EvalIds /\ evals[e].phase \in
                {"done", "failedCallback", "failedStale", "abandoned"})

\* Liveness (against LiveSpec): every issued call eventually terminates -
\* served, failed, or canceled, never wedged forever.
EventuallyTerminal ==
    \A i \in 1..MaxInvocations :
        (i \in InvocationIds) ~> (i \in InvocationIds /\ invocations[i].phase \in TerminalPhases)

=============================================================================
