-------------------------- MODULE CacheLifecycle --------------------------
(***************************************************************************)
(* A TLA+ model of the dagql cache's result lifecycle and concurrency      *)
(* kernel, as implemented on main @ f3cc3eb3f2 (2026-08-18).               *)
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
(*                                                                         *)
(* GRANULARITY                                                             *)
(* One atomic model action per Go critical section: one hold of egraphMu,  *)
(* callsMu, or sessionMu, or one lock-free region between holds. Races    *)
(* live BETWEEN critical sections; the model explores exactly those        *)
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
(*     (cache.go:3879-3883).                                               *)
(*   - MODEL AXIOM, assumed and never verified here: results the cache     *)
(*     considers equivalent are interchangeable.                           *)
(*   - Not modeled yet (planned later increments): session resources,      *)
(*     TTL/expiry, lazy evaluation, persisted-payload decode,              *)
(*     import/flush.                                                       *)
(*   - ReleaseSession can fire at ANY time, including while the session    *)
(*     has calls in flight. The server does prevent this today by          *)
(*     draining first (dagqlInFlight, engine/server/session.go:600-608),   *)
(*     but that is server politeness, not a cache guarantee - so the       *)
(*     model checks the cache without it. The DrainOnRelease constant      *)
(*     turns the politeness back on for configs that need it.              *)
(*                                                                         *)
(* BUG TOGGLES (TRUE = the fixed/guarded behavior, see each constant)      *)
(* Each toggle selects between the current or historical code shape and    *)
(* a fixed shape, so one spec can both reproduce known bugs and prove      *)
(* the fixes. Each .cfg file next to this spec sets one combination of     *)
(* toggles and checks the properties that answer one question - its name   *)
(* says which (e.g. CacheLifecycle_bug_canonical.cfg reproduces the        *)
(* canonical-adoption use-after-release; CacheLifecycle_fixed.cfg proves   *)
(* every safety property with all fixes on).                               *)
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
    AllowRelease,       \* enable the ReleaseSession action; off in configs
                        \* that isolate a bug from release races
    DrainOnRelease,     \* model the server's politeness around release:
                        \*   - TRUE: ReleaseSession waits until the
                        \*     session's calls are terminal (the
                        \*     dagqlInFlight drain, session.go:600-608)
                        \*     and released sessions issue no new calls
                        \*     (session.go:1297)
                        \*   - FALSE: the cache on its own, no politeness
                        \* TRUE isolates bugs that fire even WITH the
                        \* drain, e.g. the canonical-adoption race
    AllowPruneCut,      \* enable the PruneCut action; off in configs that
                        \* isolate a bug from prune races

    \* --- bug toggles: TRUE = fixed/guarded shape --------------------------
    FixCanonicalHoldEarly,
                        \* TRUE: the publication handoff hold is taken
                        \* inside the same critical section as the
                        \* canonical-equivalent pick (cache.go:4319-4329,
                        \* the shape on main since the fix).
                        \* FALSE: the pre-fix shape - pick first, hold in a
                        \* later critical section. Reproduces the
                        \* use-after-release race that
                        \* dagql/cache_canonical_race_test.go choreographs.
    GuardResurrection,  \* TRUE: indexWaitResultInEgraphLocked refuses to
                        \* re-register an already-collected result
                        \* (cache_egraph.go:1548, the shape on main).
                        \* FALSE: the collected result is re-registered.
    FixOnceErrGap,      \* FALSE (as on main): a waiter can pass the
                        \* sync.Once while publication is still running and
                        \* read initCompletedResultErr before it is
                        \* written - the acknowledged TODO race at
                        \* cache.go:4222. TRUE: that gap is closed.
    FixLostCancel,      \* FALSE (as on main): the withOperationLease error
                        \* branch (cache.go:3907-3915) returns without
                        \* calling the WithCancelCause cancel created at
                        \* cache.go:3887. TRUE: the cancel is called.
    FixJoinerPersistable,
                        \* HYPOTHETICAL fix; no fix exists on main.
                        \* FALSE (as on main): a joiner's IsPersistable
                        \* request can be lost (see the Join action).
                        \* TRUE: a persistable waiter upserts its own
                        \* persisted edge before returning.
    ModelAttachBarrier, \* TRUE: readers wait on the dependency-attachment
                        \* barrier as the code does. FALSE: barrier
                        \* removed, to demonstrate that readers would then
                        \* observe half-attached results.

    \* --- failure injection ------------------------------------------------
    FnCanFail,          \* the executed fn may return an error
    AttachCanFail,      \* attachDependencyResults may fail
    LeaseCanFail,       \* withOperationLease may fail (enables the
                        \* lost-cancel branch)

    \* --- lazy evaluation (increment 2) ------------------------------------
    ModelLazy,          \* master toggle for the lazy-evaluation machinery.
                        \* FALSE keeps every increment-1 configuration
                        \* byte-identical in behavior: no evaluators spawn
                        \* and no lazy action can fire.
    ModelLazySingleflight,
                        \* TRUE (as the code): at most one lazy callback
                        \* runs per result; later demands join the running
                        \* attempt (evaluateOne, cache.go:2990). FALSE
                        \* removes that coordination so a second callback
                        \* can start while one runs - a
                        \* deliberately-broken shape that demonstrates the
                        \* mutual-exclusion property is load-bearing.
    LazyCanFail,        \* the lazy callback may fail. Failure must leave
                        \* the result retryable (lazyEvalComplete is set
                        \* only on success, cache.go:3131-3134).
    MaxEvals,           \* how many Cache.Evaluate callers may be issued

    \* --- import / flush (increment 3) -------------------------------------
    ModelPersistence,   \* master toggle for the persistence machinery.
                        \* FALSE keeps every earlier configuration
                        \* byte-identical in behavior: the model starts
                        \* empty, never flushes, never restarts, and no
                        \* result carries an undecoded payload.
    ImportInit,         \* TRUE: the model may START from a small imported
                        \* graph instead of an empty cache - results
                        \* retained by persisted and dependency edges, no
                        \* session edges, payloads possibly still encoded
                        \* ("envelope"), exactly what importPersistedState
                        \* reconstructs after a clean restart.
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
                        \* (Cache.nextSharedResultID is monotonic)
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
    countedEdges,       \* the subset of sessionEdges whose ownership
                        \* increment has landed. The two sets differ only
                        \* inside trackSessionResult's window between its
                        \* two critical sections (cache.go:259-291)
    releasedSessions,   \* sessions whose ReleaseSession already ran
    lostCancels,        \* count of WithCancelCause cancels never discharged
    evals,              \* one record per issued Cache.Evaluate caller
                        \* (increment 2; stays empty when ModelLazy=FALSE)
    epoch,              \* 1 before the modeled restart, 2 after. One
                        \* graceful-shutdown/restart cycle per run.
    flushed             \* "none", or the snapshot the graceful shutdown
                        \* captured: one row per then-registered result.
                        \* Mirrors what snapshotPersistState writes
                        \* (cache_persistence_worker.go:27) minus the
                        \* e-graph detail this model abstracts away.

vars == <<invocations, res, ongoingCalls, ongoingCallIndex, sessionEdges, countedEdges,
          releasedSessions, lostCancels, evals, epoch, flushed>>

\* The currently-allocated ID ranges (sequences are 1-indexed).
ResultIds == 1..Len(res)
EvalIds   == 1..Len(evals)
InvocationIds    == 1..Len(invocations)
OngoingCallIds     == 1..Len(ongoingCalls)

\* phase values in which an invocation has finished, one way or another.
TerminalPhases == {"done", "failed", "canceled"}
\* pubState values in which publication has started but not finished.
PubInProgress == {"begun", "attaching", "adoptGap", "adopted"}

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
\*
\* res[r].orphan is the odd one out: it counts increments that landed with
\* no releasable edge behind them. trackSessionResult's second critical
\* section (cache.go:284-291) checks only that the result is still
\* registered - so if ReleaseSession deleted the session's map entry in
\* between, the increment still lands and nothing will ever decrement it.
\* Tracking those separately keeps OwnershipExact exact and makes the leak
\* visible to NoOrphanEdgesAtQuiescence.
DerivedOwn(r) ==
    SessEdgeCount(r) + DepParentCount(r)
      + HoldCount(r) + PersistedCount(r) + res[r].orphan

\* All registered results whose call is in equivalence class k - the
\* candidates a lookup for class k could return.
LiveInClass(k) ==
    {r \in ResultIds : res[r].registered /\ ClassOf[res[r].call] = k}

\* "The caller got a result it can actually keep": the session's edge is
\* recorded, and the result cannot be collected out from under the caller
\* right now. The second condition holds in one of two ways:
\*   - the edge's ownership increment has landed, or
\*   - a publication handoff hold still pins the result. This covers the
\*     window where a sibling waiter recorded the edge but its increment
\*     (cache.go:284-291) has not landed yet.
ProtectedReturn(s, r) ==
    /\ r # 0
    /\ <<s, r>> \in sessionEdges
    /\ \/ <<s, r>> \in countedEdges
       \/ HoldCount(r) >= 1

(***************************************************************************)
(* The release cascade: collect every registered result whose count is     *)
(* zero, drop its dependency edges, decrement the dependencies, repeat.    *)
(*                                                                         *)
(* Mirrors collectUnownedResultsLocked (cache.go:978-1025), which runs     *)
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
(* IMPORTED INITIAL STATES (increment 3). After a clean restart,           *)
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
     own |-> ownVal, deps |-> depsSet, orphan |-> 0,
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
     released |-> TRUE, own |-> 0, deps |-> {}, orphan |-> 0,
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
    /\ releasedSessions = {}
    /\ lostCancels = 0
    /\ evals = <<>>
    /\ epoch = 1
    /\ flushed = [done |-> FALSE, rows |-> <<>>]

(***************************************************************************)
(* Spawn: a client issues GetOrInitCall(session, call, persistable).       *)
(*                                                                         *)
(* Unless DrainOnRelease models the server's rejection of new work         *)
(* (session.go:1297), even a released session may still issue calls -     *)
(* nothing in the cache itself prevents that.                              *)
(***************************************************************************)
Spawn ==
    /\ Len(invocations) < MaxInvocations
    /\ \E s \in Sessions, c \in Calls, p \in BOOLEAN :
        /\ DrainOnRelease => s \notin releasedSessions
        /\ invocations' = Append(invocations,
             [sess |-> s, call |-> c, persistable |-> p, phase |-> "lookup",
              oc |-> 0, resId |-> 0, path |-> "none",
              claimedNew |-> FALSE,
              retLive |-> TRUE, retOwned |-> TRUE,
              retBarrierOK |-> TRUE, retPersisted |-> TRUE,
              retClean |-> TRUE])
    /\ UNCHANGED <<res, ongoingCalls, ongoingCallIndex, sessionEdges, countedEdges,
                   releasedSessions, lostCancels, evals, epoch, flushed>>

(***************************************************************************)
(* LookupHit: the lookup finds a live equivalent result and claims it for  *)
(* the session, all in ONE egraphMu critical section.                      *)
(*                                                                         *)
(* Go: lookupCacheForRequest, cache_egraph.go:950-1001. Inside the one     *)
(* lock hold:                                                              *)
(*   - a candidate in the request's equivalence class is selected          *)
(*   - the session edge is recorded and its ownership increment lands      *)
(*   - a persistable request also upserts the persisted edge               *)
(*     (lookupCacheForRequestLocked's IsPersistable arm)                   *)
(*                                                                         *)
(* claimedNew mirrors the code's alreadyTracked flag, which the error-arm  *)
(* rollback consults later (see ReadBarrierErrHit).                        *)
(*                                                                         *)
(* After the lock: the hit still passes the read barrier                   *)
(* (phase="readBarrier") before returning. The persisted-payload decode arm   *)
(* of that barrier needs imported state and is not modeled yet.            *)
(***************************************************************************)
LookupHit(i) ==
    /\ invocations[i].phase = "lookup"
    /\ \E r \in LiveInClass(ClassOf[invocations[i].call]) :
        LET s == invocations[i].sess
            \* The dedupe is on the RECORDED map entry, not on whether its
            \* increment has landed: an entry recorded by a concurrent
            \* trackSessionResult whose increment is still pending already
            \* counts as tracked, and suppresses this hit's increment.
            haveEdge == <<s, r>> \in sessionEdges
            withEdge == IF haveEdge THEN res
                        ELSE [res EXCEPT ![r].own = @ + 1]
            withPersist ==
                IF invocations[i].persistable /\ ~withEdge[r].persisted
                THEN [withEdge EXCEPT ![r].persisted = TRUE,
                                      ![r].own = @ + 1]
                ELSE withEdge
        IN /\ res' = withPersist
           /\ sessionEdges' = sessionEdges \cup {<<s, r>>}
           /\ countedEdges' = IF haveEdge THEN countedEdges
                              ELSE countedEdges \cup {<<s, r>>}
           /\ invocations' = [invocations EXCEPT ![i].phase = "readBarrier",
                                   ![i].resId = r,
                                   ![i].path = "hit",
                                   ![i].claimedNew = ~haveEdge]
    /\ UNCHANGED <<ongoingCalls, ongoingCallIndex, releasedSessions, lostCancels, evals, epoch, flushed>>

\* LookupMiss: the lookup finds nothing usable; fall through to the
\* singleflight. A miss is allowed even when a candidate exists - that
\* over-approximates the filtering this model leaves out, and exercises
\* the engine's accepted duplicate-execution window (cache.go:3879-3883).
LookupMiss(i) ==
    /\ invocations[i].phase = "lookup"
    /\ invocations' = [invocations EXCEPT ![i].phase = "join"]
    /\ UNCHANGED <<res, ongoingCalls, ongoingCallIndex, sessionEdges, countedEdges,
                   releasedSessions, lostCancels, evals, epoch, flushed>>

(***************************************************************************)
(* Join: an ongoing call for this (call, session) already exists - become  *)
(* one of its waiters instead of executing again.                          *)
(*                                                                         *)
(* Go: getOrInitCallInner, cache.go:3854-3877, one callsMu critical        *)
(* section: bump oc.waiters, and stamp IsPersistable onto the shared       *)
(* ongoingCall.                                                            *)
(*                                                                         *)
(* That stamp is a known, currently-unfixed race:                          *)
(*   - publication reads oc.isPersistable in its own egraphMu section      *)
(*     (cache.go:4600), without holding callsMu                            *)
(*   - the entry only leaves the Cache.ongoingCalls index later (see PubUnregister), so    *)
(*     joining stays possible after that read                              *)
(*   - a persistable joiner landing in that window is silently lost: its   *)
(*     result never gets the persisted edge it asked for                   *)
(* The PersistableHonored property catches this.                           *)
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
                   releasedSessions, lostCancels, evals, epoch, flushed>>

(***************************************************************************)
(* CreateOc: no ongoing call exists - create one and start executing.      *)
(*                                                                         *)
(* Go: getOrInitCallInner miss path, cache.go:3885-3946, still the same    *)
(* callsMu hold:                                                           *)
(*   - the WithCancelCause cancel is created (:3887)                       *)
(*   - the operation lease is acquired (:3907)                             *)
(*   - the ongoingCall is registered (:3938-3940; always, because the      *)
(*     default concurrency key - the session ID - is never empty)          *)
(*   - the fn goroutine starts (:3942)                                     *)
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
             pubState |-> "none", hold |-> FALSE, resId |-> 0,
             inIndex |-> TRUE])
       /\ ongoingCallIndex' = [ongoingCallIndex EXCEPT ![k] = Len(ongoingCalls) + 1]
       /\ invocations' = [invocations EXCEPT ![i].phase = "waiting", ![i].oc = Len(ongoingCalls) + 1,
                               ![i].path = "wait"]
    /\ UNCHANGED <<res, sessionEdges, countedEdges, releasedSessions,
                   lostCancels, evals, epoch, flushed>>

\* CreateOcLeaseFail: the operation lease cannot be acquired and the call
\* fails before anything starts.
\*
\* Go: the withOperationLease error branch, cache.go:3907-3915. Known,
\* currently-unfixed leak: this branch returns without calling the cancel
\* created at cache.go:3887. The parent context is WithoutCancel, so
\* nothing wedges - it is an undischarged cancel, not corruption. The
\* NoLostCancels property counts it. FixLostCancel=TRUE models adding the
\* missing cancel() call.
CreateOcLeaseFail(i) ==
    /\ LeaseCanFail
    /\ invocations[i].phase = "join"
    /\ ongoingCallIndex[<<invocations[i].call, invocations[i].sess>>] = 0
    /\ invocations' = [invocations EXCEPT ![i].phase = "failed"]
    /\ lostCancels' = IF FixLostCancel THEN lostCancels ELSE lostCancels + 1
    /\ UNCHANGED <<res, ongoingCalls, ongoingCallIndex, sessionEdges, countedEdges,
                   releasedSessions, evals, epoch, flushed>>

(***************************************************************************)
(* FnComplete: the executing fn finishes (the goroutine started at         *)
(* cache.go:3942). Three possible outcomes:                                *)
(*   - "fresh": fn computed a new detached value                           *)
(*   - "reuse": fn returned an already-attached result (for example, an    *)
(*     inner call hit cache). This drives publication's canonical-         *)
(*     adoption branch. The model picks any live result in the call's     *)
(*     class - the static-partition stand-in for output-class merging.     *)
(*   - error: only when FnCanFail is on                                    *)
(***************************************************************************)
FnComplete(o) ==
    /\ ongoingCalls[o].fnState = "running"
    /\ \/ ongoingCalls' = [ongoingCalls EXCEPT ![o].fnState = "done", ![o].outcome = "fresh"]
       \/ \E r \in LiveInClass(ClassOf[ongoingCalls[o].call]) :
            ongoingCalls' = [ongoingCalls EXCEPT ![o].fnState = "done",
                               ![o].outcome = "reuse", ![o].reuseFrom = r]
       \/ /\ FnCanFail
          /\ ongoingCalls' = [ongoingCalls EXCEPT ![o].fnState = "done", ![o].fnErr = TRUE]
    /\ UNCHANGED <<invocations, res, ongoingCallIndex, sessionEdges, countedEdges,
                   releasedSessions, lostCancels, evals, epoch, flushed>>

(***************************************************************************)
(* WaiterCancel: a waiter's own context is canceled while the fn is still  *)
(* running.                                                                *)
(*                                                                         *)
(* Go: wait's !completed branch, cache.go:4152-4175. Notes:                *)
(*   - only reachable before the fn finishes; once waitCh closes, every    *)
(*     waiter takes a completion path instead                              *)
(*   - the LAST canceling waiter removes the entry from the Cache.ongoingCalls index and   *)
(*     cancels the fn's context                                            *)
(*   - the handoff hold cannot be active here (publication has not         *)
(*     started), so there is no hold to release                            *)
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
    /\ UNCHANGED <<res, sessionEdges, countedEdges, releasedSessions,
                   lostCancels, evals, epoch, flushed>>

\* WaiterObserveFnErr: the fn failed; each waiter observes the error and
\* returns it. The last waiter removes the Cache.ongoingCalls index entry.
\* Go: wait's completionErr path, cache.go:4176-4186.
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
    /\ UNCHANGED <<res, sessionEdges, countedEdges, releasedSessions,
                   lostCancels, evals, epoch, flushed>>

(***************************************************************************)
(* PUBLICATION - initCompletedResult, entered through the sync.Once in     *)
(* wait (cache.go:4189). The next several actions form its state machine,  *)
(* driven by oc.pubState. The publishing waiter runs it to completion      *)
(* regardless of its own caller's context (the publication context is      *)
(* WithoutCancel, cache.go:4192-4199).                                     *)
(*                                                                         *)
(* PubBegin: entering the Once, plus the lock-free prologue - oc.res is    *)
(* set to a fresh empty &sharedResult{} at cache.go:4316 before any lock   *)
(* is taken. From here until publication finishes, the acknowledged TODO   *)
(* race at cache.go:4222 is open: another waiter can skip the Once and     *)
(* read initCompletedResultErr before it is written (see                   *)
(* WaiterEarlyReturn).                                                     *)
(***************************************************************************)
PubBegin(o) ==
    /\ ongoingCalls[o].fnState = "done" /\ ~ongoingCalls[o].fnErr
    /\ ongoingCalls[o].pubState = "none"
    /\ \E w \in InvocationIds : invocations[w].phase = "waiting" /\ invocations[w].oc = o
    /\ ongoingCalls' = [ongoingCalls EXCEPT ![o].pubState = "begun"]
    /\ UNCHANGED <<invocations, res, ongoingCallIndex, sessionEdges, countedEdges,
                   releasedSessions, lostCancels, evals, epoch, flushed>>

(***************************************************************************)
(* PubIndexFresh: publish a freshly computed value. One egraphMu critical  *)
(* section (ending at cache.go:4616) that does, in order:                  *)
(*   1. allocate and register the sharedResult - it is lookup-visible      *)
(*      from this instant, before attachment completes                     *)
(*   2. add exact dependency edges from the result-call refs, each         *)
(*      incrementing its dependency                                        *)
(*   3. upsert the persisted edge if oc.isPersistable (:4600 - the racy    *)
(*      read described at Join)                                            *)
(*   4. take the publication handoff hold                                  *)
(*   5. arm the dependency-attachment barrier (:4610-4614)                 *)
(***************************************************************************)
PubIndexFresh(o) ==
    /\ ongoingCalls[o].pubState = "begun"
    /\ ongoingCalls[o].outcome = "fresh"
    /\ Len(res) < MaxResults
    \* A fresh result may carry deferred lazy work (a value implementing
    \* HasLazyEvaluation, whose callback registerLazyEvaluation stores on
    \* the sharedResult at cache.go:2868). Which results are lazy is the
    \* producer's business, so the model picks nondeterministically.
    /\ \E lazyCb \in IF ModelLazy THEN {"none", "armed"} ELSE {"none"} :
       \E deps \in {{}} \cup {{d} : d \in {r \in ResultIds : res[r].registered}} :
        LET withDeps == [r \in DOMAIN res |->
                IF r \in deps THEN [res[r] EXCEPT !.own = @ + 1]
                ELSE res[r]]
            newRes == [call |-> ongoingCalls[o].call, registered |-> TRUE,
                       released |-> FALSE,
                       own |-> 1 + (IF ongoingCalls[o].isPersistable THEN 1 ELSE 0),
                       deps |-> deps, orphan |-> 0,
                       persisted |-> ongoingCalls[o].isPersistable,
                       barrier |-> "open",
                       \* fresh results have their typed payload in memory;
                       \* only imported entries carry encoded envelopes
                       payload |-> "decoded",
                       decodePhase |-> "idle", decodeErr |-> "none",
                       laundered |-> FALSE,
                       \* lazy-evaluation state, mirroring the lazyMu block
                       \* on sharedResult (cache.go:1583-1596):
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
                   releasedSessions, lostCancels, evals, epoch, flushed>>

(***************************************************************************)
(* ADOPTION BRANCH: the fn returned an already-attached result, so         *)
(* publication adopts the canonical equivalent instead of indexing a new   *)
(* sharedResult.                                                           *)
(***************************************************************************)

\* The canonical equivalent: the lowest-ID live result in the class of the
\* result the fn returned - or that result itself if nothing in the class
\* is live. Mirrors canonicalEquivalentSharedResultLocked.
CanonicalPick(o) ==
    LET k == ClassOf[res[ongoingCalls[o].reuseFrom].call]
        live == LiveInClass(k)
    IN IF live = {} THEN ongoingCalls[o].reuseFrom
       ELSE CHOOSE r \in live : \A q \in live : r <= q

\* PubAdoptWithHold: the FIXED shape, on main since the fix
\* (cache.go:4318-4329): one egraphMu critical section both picks the
\* canonical equivalent AND takes the handoff hold, so nothing can collect
\* the adopted result before publication finishes.
PubAdoptWithHold(o) ==
    /\ FixCanonicalHoldEarly
    /\ ongoingCalls[o].pubState = "begun"
    /\ ongoingCalls[o].outcome = "reuse"
    /\ LET r == CanonicalPick(o) IN
        /\ res' = [res EXCEPT ![r].own = @ + 1]
        /\ ongoingCalls' = [ongoingCalls EXCEPT ![o].pubState = "adopted",
                              ![o].hold = TRUE, ![o].resId = r]
    /\ UNCHANGED <<invocations, ongoingCallIndex, sessionEdges, countedEdges,
                   releasedSessions, lostCancels, evals, epoch, flushed>>

\* PubAdoptNoHold + PubAdoptLateHold: the PRE-FIX shape
\* (FixCanonicalHoldEarly=FALSE). The pick dropped egraphMu with no hold;
\* the hold only landed when publication re-acquired the lock. A session
\* release in the gap could collect the adopted result - the historical
\* use-after-release race that dagql/cache_canonical_race_test.go spends
\* 180 lines choreographing.
PubAdoptNoHold(o) ==
    /\ ~FixCanonicalHoldEarly
    /\ ongoingCalls[o].pubState = "begun"
    /\ ongoingCalls[o].outcome = "reuse"
    /\ ongoingCalls' = [ongoingCalls EXCEPT ![o].pubState = "adoptGap",
                          ![o].resId = CanonicalPick(o)]
    /\ UNCHANGED <<invocations, res, ongoingCallIndex, sessionEdges, countedEdges,
                   releasedSessions, lostCancels, evals, epoch, flushed>>

PubAdoptLateHold(o) ==
    /\ ongoingCalls[o].pubState = "adoptGap"
    /\ res' = [res EXCEPT ![ongoingCalls[o].resId].own = @ + 1]
    /\ ongoingCalls' = [ongoingCalls EXCEPT ![o].pubState = "adopted", ![o].hold = TRUE]
    /\ UNCHANGED <<invocations, ongoingCallIndex, sessionEdges, countedEdges,
                   releasedSessions, lostCancels, evals, epoch, flushed>>

(***************************************************************************)
(* PubIndexReuse: finish publication for an adopted result - the egraphMu  *)
(* section containing indexWaitResultInEgraphLocked. Behavior:             *)
(*   - if the adopted result was collected in the meantime:                *)
(*       - with the guard (cache_egraph.go:1548, the shape on main):       *)
(*         publication fails rather than serve a corpse                    *)
(*       - without the guard (GuardResurrection=FALSE, pre-fix):           *)
(*         resultsByID[res.id] = res re-registers the collected result -   *)
(*         a resurrection                                                  *)
(*   - oc.isPersistable is consumed here for adopted results (:4600)       *)
(*   - no attach barrier: adopted results were already attached            *)
(*     (resWasCacheBacked, cache.go:4608)                                  *)
(***************************************************************************)
PubIndexReuse(o) ==
    /\ ongoingCalls[o].pubState = "adopted"
    /\ LET r == ongoingCalls[o].resId IN
       IF ~res[r].registered /\ GuardResurrection
       THEN /\ ongoingCalls' = [ongoingCalls EXCEPT ![o].pubState = "failed"]
            /\ UNCHANGED res
       ELSE LET reReg == IF res[r].registered THEN res
                         ELSE [res EXCEPT ![r].registered = TRUE]
                withPersist ==
                    IF ongoingCalls[o].isPersistable /\ ~reReg[r].persisted
                    THEN [reReg EXCEPT ![r].persisted = TRUE,
                                       ![r].own = @ + 1]
                    ELSE reReg
            IN /\ res' = withPersist
               /\ ongoingCalls' = [ongoingCalls EXCEPT ![o].pubState = "done"]
    /\ UNCHANGED <<invocations, ongoingCallIndex, sessionEdges, countedEdges,
                   releasedSessions, lostCancels, evals, epoch, flushed>>

(***************************************************************************)
(* ATTACHMENT PHASE for fresh results. attachDependencyResults runs        *)
(* OUTSIDE egraphMu (cache.go:4618) while the result is already            *)
(* lookup-visible - that is why the barrier exists. Steps:                 *)
(*   - PubAttachAddDep: attachment discovers an embedded child result and  *)
(*     records the dependency edge; each such AddExplicitDependency is     *)
(*     its own egraphMu critical section (cache.go:2110-2140)              *)
(*   - PubFinishOk: attachment succeeded; close the barrier clean          *)
(*   - PubAttachFail: attachment failed; release the hold, cascade, close  *)
(*     the barrier with the error (cache.go:4618-4638)                     *)
(*                                                                         *)
(* STATED ASSUMPTION: dependency edges never form a cycle. The Go cache    *)
(* does not enforce this - addExplicitDependencyLocked (cache.go:2110)     *)
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
                   releasedSessions, lostCancels, evals, epoch, flushed>>

PubFinishOk(o) ==
    /\ ongoingCalls[o].pubState = "attaching"
    /\ res' = [res EXCEPT ![ongoingCalls[o].resId].barrier = "closedOk"]
    /\ ongoingCalls' = [ongoingCalls EXCEPT ![o].pubState = "done"]
    /\ UNCHANGED <<invocations, ongoingCallIndex, sessionEdges, countedEdges,
                   releasedSessions, lostCancels, evals, epoch, flushed>>

PubAttachFail(o) ==
    /\ AttachCanFail
    /\ ongoingCalls[o].pubState = "attaching"
    /\ res' = DecAndCascade(
         [res EXCEPT ![ongoingCalls[o].resId].barrier = "closedErr"], ongoingCalls[o].resId)
    /\ ongoingCalls' = [ongoingCalls EXCEPT ![o].pubState = "failed", ![o].hold = FALSE]
    /\ UNCHANGED <<invocations, ongoingCallIndex, sessionEdges, countedEdges,
                   releasedSessions, lostCancels, evals, epoch, flushed>>

\* PubUnregister: the entry leaves the Cache.ongoingCalls index - the tail of the Once
\* (wait, cache.go:4215-4218), in its own callsMu critical section AFTER
\* publication finished. Joining stays possible until this fires; a
\* persistable joiner landing in that window is the lost update described
\* at Join.
PubUnregister(o) ==
    /\ ongoingCalls[o].pubState \in {"done", "failed"}
    /\ ongoingCalls[o].inIndex
    /\ ongoingCallIndex' = [ongoingCallIndex EXCEPT ![<<ongoingCalls[o].call, ongoingCalls[o].sess>>] = 0]
    /\ ongoingCalls' = [ongoingCalls EXCEPT ![o].inIndex = FALSE]
    /\ UNCHANGED <<invocations, res, sessionEdges, countedEdges, releasedSessions,
                   lostCancels, evals, epoch, flushed>>

(***************************************************************************)
(* WaiterEarlyReturn: a waiter returns from a publication that has not     *)
(* finished - the acknowledged TODO race at cache.go:4222.                 *)
(*                                                                         *)
(* How it happens in the code:                                             *)
(*   - one waiter is inside the sync.Once running publication              *)
(*   - another waiter skips the Once and reads initCompletedResultErr      *)
(*     before it is written (reads nil = "success")                        *)
(*   - it then reads oc.res, which is non-nil from cache.go:4316 on but    *)
(*     possibly not yet indexed (ID still 0)                               *)
(*   - trackSessionResult skips ID-0 results and no barrier is armed yet,  *)
(*     so it returns holding no session edge at all                        *)
(*                                                                         *)
(* The waiters >= 2 guard: the publishing waiter is still counted, so the  *)
(* early-returner is never the publisher itself.                           *)
(***************************************************************************)
WaiterEarlyReturn(i) ==
    /\ ~FixOnceErrGap
    /\ invocations[i].phase = "waiting"
    /\ LET o == invocations[i].oc
           r == ongoingCalls[o].resId
       IN /\ ongoingCalls[o].pubState \in PubInProgress
          /\ ongoingCalls[o].waiters >= 2
          /\ ongoingCalls' = [ongoingCalls EXCEPT ![o].waiters = @ - 1]
          /\ invocations' = [invocations EXCEPT
               ![i].phase = "done",
               ![i].resId = r,
               ![i].retLive = r # 0 /\ res[r].registered /\ ~res[r].released,
               ![i].retOwned = ProtectedReturn(invocations[i].sess, r),
               ![i].retBarrierOK =
                    r # 0 /\ res[r].barrier \in {"none", "closedOk"},
               ![i].retPersisted =
                    ~invocations[i].persistable \/ (r # 0 /\ res[r].persisted),
               ![i].retClean = r = 0 \/ ~res[r].laundered]
    /\ UNCHANGED <<res, ongoingCallIndex, sessionEdges, countedEdges, releasedSessions,
                   lostCancels, evals, epoch, flushed>>

\* WaiterObservePubErr: publication failed; each waiter observes the error
\* and returns it. The last waiter releases the handoff hold if it is
\* still active (the resurrection-guard failure path keeps it held).
\* Go: wait's initCompletedResultErr path, cache.go:4224-4240.
WaiterObservePubErr(i) ==
    /\ invocations[i].phase = "waiting"
    /\ LET o == invocations[i].oc
           last == ongoingCalls[o].waiters = 1
           dropHold == last /\ ongoingCalls[o].hold
       IN /\ ongoingCalls[o].pubState = "failed"
          /\ ~ongoingCalls[o].inIndex   \* Once-completion ordering; see
                               \* WaiterClaimRecord
          /\ ongoingCalls' = [ongoingCalls EXCEPT ![o].waiters = @ - 1,
                                ![o].hold = @ /\ ~last]
          /\ res' = IF dropHold THEN DecAndCascade(res, ongoingCalls[o].resId)
                    ELSE res
          /\ invocations' = [invocations EXCEPT ![i].phase = "failed"]
    /\ UNCHANGED <<ongoingCallIndex, sessionEdges, countedEdges, releasedSessions,
                   lostCancels, evals, epoch, flushed>>

(***************************************************************************)
(* WAITER SUCCESS PATH. In code order (wait, cache.go:4242-4270):          *)
(*   1. trackSessionResult - claim the session edge while the handoff      *)
(*      hold still pins the result                                         *)
(*   2. waiters--                                                          *)
(*   3. the last waiter releases the handoff hold                          *)
(*   4. the read barrier (ensurePersistedHitValueLoaded)                   *)
(*                                                                         *)
(* trackSessionResult (cache.go:259-291) is TWO critical sections:         *)
(*   - sessionMu: record the edge in the session's map                     *)
(*   - egraphMu: land the ownership increment - only if the result is      *)
(*     still registered                                                    *)
(* The two model actions below mirror that split; the gap between them is  *)
(* where several real races live.                                          *)
(***************************************************************************)

\* WaiterClaimRecord: record the session edge (or skip if already
\* recorded). First critical section of trackSessionResult.
WaiterClaimRecord(i) ==
    /\ invocations[i].phase = "waiting"
    /\ ongoingCalls[invocations[i].oc].pubState = "done"
    \* Ordering note: every waiter passes through the COMPLETED sync.Once,
    \* whose tail already deleted the Cache.ongoingCalls index entry (see
    \* PubUnregister). So no Join can interleave with waiter claims. Only
    \* the WaiterEarlyReturn bug path escapes this ordering.
    /\ ~ongoingCalls[invocations[i].oc].inIndex
    /\ LET r == ongoingCalls[invocations[i].oc].resId
           s == invocations[i].sess
       IN IF <<s, r>> \in sessionEdges
          THEN /\ invocations' = [invocations EXCEPT ![i].phase = "depart",
                                       ![i].resId = r]
               /\ UNCHANGED sessionEdges
          ELSE /\ sessionEdges' = sessionEdges \cup {<<s, r>>}
               /\ invocations' = [invocations EXCEPT ![i].phase = "claimCount",
                                       ![i].resId = r]
    /\ UNCHANGED <<res, ongoingCalls, ongoingCallIndex, countedEdges, releasedSessions,
                   lostCancels, evals, epoch, flushed>>

\* WaiterClaimCount: land the ownership increment for the edge recorded in
\* WaiterClaimRecord. Second critical section of trackSessionResult.
\*
\* The increment runs because THIS caller recorded the edge (the captured
\* `acquired` flag) and is conditional only on the result still being
\* registered (cache.go:286-289). It does NOT re-check that the session's
\* map entry still exists, and it does not deduplicate against a
\* concurrent re-claim. So if ReleaseSession wiped the record in the gap -
\* and possibly a new hit re-recorded and re-counted it - this increment
\* lands with no releasable record of its own: a permanent retention
\* leak. The model books it as res[r].orphan, which keeps OwnershipExact
\* exact and lets NoOrphanEdgesAtQuiescence expose the leak.
WaiterClaimCount(i) ==
    /\ invocations[i].phase = "claimCount"
    /\ LET r == invocations[i].resId
           s == invocations[i].sess
           releasable == /\ <<s, r>> \in sessionEdges
                         /\ <<s, r>> \notin countedEdges
       IN IF res[r].registered
          THEN IF releasable
               THEN /\ res' = [res EXCEPT ![r].own = @ + 1]
                    /\ countedEdges' = countedEdges \cup {<<s, r>>}
               ELSE /\ res' = [res EXCEPT ![r].own = @ + 1,
                                          ![r].orphan = @ + 1]
                    /\ UNCHANGED countedEdges
          ELSE UNCHANGED <<res, countedEdges>>
    /\ invocations' = [invocations EXCEPT ![i].phase = "depart"]
    /\ UNCHANGED <<ongoingCalls, ongoingCallIndex, sessionEdges, releasedSessions, lostCancels, evals, epoch, flushed>>

\* WaiterDepart: waiters--, under callsMu. The last waiter goes on to
\* release the handoff hold. Go: wait, cache.go:4256-4260.
WaiterDepart(i) ==
    /\ invocations[i].phase = "depart"
    /\ LET o == invocations[i].oc
           last == ongoingCalls[o].waiters = 1
       IN /\ ongoingCalls' = [ongoingCalls EXCEPT ![o].waiters = @ - 1]
          /\ invocations' = [invocations EXCEPT
               ![i].phase = IF last /\ ongoingCalls[o].hold
                         THEN "releaseHold" ELSE "readBarrier"]
    /\ UNCHANGED <<res, ongoingCallIndex, sessionEdges, countedEdges, releasedSessions,
                   lostCancels, evals, epoch, flushed>>

\* WaiterReleaseHold: the last waiter drops the publication handoff hold,
\* in its own egraphMu section. From here on, only real edges (session,
\* dependency, persisted) keep the result alive.
\* Go: wait, cache.go:4261-4270.
WaiterReleaseHold(i) ==
    /\ invocations[i].phase = "releaseHold"
    /\ LET o == invocations[i].oc IN
       /\ ongoingCalls' = [ongoingCalls EXCEPT ![o].hold = FALSE]
       /\ res' = DecAndCascade(res, ongoingCalls[o].resId)
       /\ invocations' = [invocations EXCEPT ![i].phase = "readBarrier"]
    /\ UNCHANGED <<ongoingCallIndex, sessionEdges, countedEdges, releasedSessions,
                   lostCancels, evals, epoch, flushed>>

(***************************************************************************)
(* THE READ BARRIER: every return path waits for the result's dependency   *)
(* attachment to finish before handing the result out.                     *)
(*                                                                         *)
(* Go: ensurePersistedHitValueLoaded, cache_persistence_import.go:545-571. *)
(* Outcomes:                                                               *)
(*   - barrier closed clean (or never armed): return the result            *)
(*   - barrier closed with an error, HIT path: return the error AND roll   *)
(*     the session edge back (lookupCacheForRequest error arm,             *)
(*     cache_egraph.go:1002-1022). The rollback deletes the map record     *)
(*     unconditionally but decrements only if this call created the edge   *)
(*     (the alreadyTracked / claimedNew flag) - see ReadBarrierErrHit.     *)
(*   - barrier closed with an error, WAIT path: return the error; no       *)
(*     rollback, the claimed session edge simply remains                   *)
(*     (wait, cache.go:4272-4276)                                          *)
(*                                                                         *)
(* With ModelAttachBarrier=FALSE the reader skips the wait entirely -      *)
(* used to demonstrate that the barrier is what prevents half-attached     *)
(* reads.                                                                  *)
(*                                                                         *)
(* Completion is also where each invocation records its return-time        *)
(* evidence (the ret* flags) for the properties to inspect.                *)
(***************************************************************************)
ReadBarrierOk(i) ==
    /\ invocations[i].phase = "readBarrier"
    /\ LET r == invocations[i].resId
           \* The hypothetical joiner-persistable fix (FixJoinerPersistable):
           \* a persistable waiter whose edge publication missed upserts
           \* it in its own egraphMu section before returning.
           fixUpsert == /\ FixJoinerPersistable
                        /\ invocations[i].persistable
                        /\ res[r].registered
                        /\ ~res[r].persisted
       IN /\ ModelAttachBarrier => res[r].barrier \in {"none", "closedOk"}
          \* An encoded payload must be decoded before the result can be
          \* returned; the decode actions below handle that arm and loop
          \* back here once the payload is in memory.
          /\ res[r].payload = "decoded"
          /\ res' = IF fixUpsert
                    THEN [res EXCEPT ![r].persisted = TRUE, ![r].own = @ + 1]
                    ELSE res
          /\ invocations' = [invocations EXCEPT
               ![i].phase = "done",
               ![i].retLive = res[r].registered /\ ~res[r].released,
               ![i].retOwned = ProtectedReturn(invocations[i].sess, r),
               ![i].retBarrierOK = res[r].barrier \in {"none", "closedOk"},
               ![i].retPersisted = ~invocations[i].persistable \/ res[r].persisted
                                    \/ fixUpsert,
               ![i].retClean = ~res[r].laundered]
    /\ UNCHANGED <<ongoingCalls, ongoingCallIndex, sessionEdges, countedEdges,
                   releasedSessions, lostCancels, evals, epoch, flushed>>

ReadBarrierErrHit(i) ==
    /\ ModelAttachBarrier
    /\ invocations[i].phase = "readBarrier"
    /\ invocations[i].path = "hit"
    /\ LET r == invocations[i].resId
           s == invocations[i].sess
       IN /\ res[r].barrier = "closedErr"
          /\ sessionEdges' = sessionEdges \ {<<s, r>>}
          /\ countedEdges' = countedEdges \ {<<s, r>>}
          /\ res' = IF invocations[i].claimedNew THEN DecAndCascade(res, r) ELSE res
          /\ invocations' = [invocations EXCEPT ![i].phase = "failed"]
    /\ UNCHANGED <<ongoingCalls, ongoingCallIndex, releasedSessions, lostCancels, evals, epoch, flushed>>

ReadBarrierErrWait(i) ==
    /\ ModelAttachBarrier
    /\ invocations[i].phase = "readBarrier"
    /\ invocations[i].path = "wait"
    /\ res[invocations[i].resId].barrier = "closedErr"
    /\ invocations' = [invocations EXCEPT ![i].phase = "failed"]
    /\ UNCHANGED <<res, ongoingCalls, ongoingCallIndex, sessionEdges, countedEdges,
                   releasedSessions, lostCancels, evals, epoch, flushed>>

(***************************************************************************)
(* PERSISTED-PAYLOAD DECODE (increment 3). An imported result can exist    *)
(* with its payload still encoded as a persisted envelope; the read        *)
(* barrier decodes it on first use (ensurePersistedHitValueLoaded's        *)
(* decode loop, cache_persistence_import.go:573-701). Per result:          *)
(*   - one caller becomes the decode leader (publishes                     *)
(*     persistDecodeWaitCh, :611-612) and performs the decode itself       *)
(*   - later callers wait on that channel                                  *)
(*   - the finish (:617-619) latches the error, CLEARS the channel, and    *)
(*     closes it - so unlike the lazy singleflight there is no lingering   *)
(*     published channel: after any finish, the next demand leads a        *)
(*     fresh attempt. Failure is therefore always retryable; success       *)
(*     installs the payload permanently.                                   *)
(* A woken waiter re-reads the CURRENT latched error, which a newer        *)
(* leader may have already reset to none - in that case the waiter just    *)
(* loops: re-checks the payload and either returns it, rejoins a running   *)
(* attempt, or leads a new one. That loop is the "continue" in the Go.     *)
(*                                                                         *)
(* A decode failure surfaces exactly like an attach-barrier failure: the   *)
(* HIT path runs the rollback in lookupCacheForRequest's error arm         *)
(* (cache_egraph.go:1002-1022) - the same rollback whose record-vs-count   *)
(* asymmetry the finding_rollback configuration demonstrates; decode is    *)
(* its wide door. The WAIT path just returns the error.                    *)
(***************************************************************************)

DecodeLead(i) ==
    /\ invocations[i].phase = "readBarrier"
    /\ LET r == invocations[i].resId IN
       /\ ModelAttachBarrier => res[r].barrier \in {"none", "closedOk"}
       /\ res[r].payload = "envelope"
       /\ res[r].decodePhase = "idle"
       /\ res' = [res EXCEPT ![r].decodePhase = "running",
                             ![r].decodeErr = "none"]
       /\ invocations' = [invocations EXCEPT ![i].phase = "decoding"]
    /\ UNCHANGED <<ongoingCalls, ongoingCallIndex, sessionEdges, countedEdges,
                   releasedSessions, lostCancels, evals, epoch, flushed>>

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
                   releasedSessions, lostCancels, evals, epoch, flushed>>

DecodeJoin(i) ==
    /\ invocations[i].phase = "readBarrier"
    /\ LET r == invocations[i].resId IN
       /\ ModelAttachBarrier => res[r].barrier \in {"none", "closedOk"}
       /\ res[r].payload = "envelope"
       /\ res[r].decodePhase = "running"
       /\ invocations' = [invocations EXCEPT ![i].phase = "decodeJoined"]
    /\ UNCHANGED <<res, ongoingCalls, ongoingCallIndex, sessionEdges, countedEdges,
                   releasedSessions, lostCancels, evals, epoch, flushed>>

DecodeWake(i) ==
    /\ invocations[i].phase = "decodeJoined"
    /\ LET r == invocations[i].resId IN
       /\ res[r].decodePhase = "idle"
       /\ invocations' = [invocations EXCEPT ![i].phase =
            IF res[r].decodeErr = "fail" THEN "decodeErr" ELSE "readBarrier"]
    /\ UNCHANGED <<res, ongoingCalls, ongoingCallIndex, sessionEdges, countedEdges,
                   releasedSessions, lostCancels, evals, epoch, flushed>>

\* Decode failed for a HIT-path caller: the same rollback as a barrier
\* error - delete the session-map record unconditionally, decrement only
\* if this call created the edge (the alreadyTracked / claimedNew
\* asymmetry), cascade.
DecodeFailHit(i) ==
    /\ invocations[i].phase = "decodeErr"
    /\ invocations[i].path = "hit"
    /\ LET r == invocations[i].resId
           s == invocations[i].sess
       IN /\ sessionEdges' = sessionEdges \ {<<s, r>>}
          /\ countedEdges' = countedEdges \ {<<s, r>>}
          /\ res' = IF invocations[i].claimedNew
                    THEN DecAndCascade(res, r) ELSE res
          /\ invocations' = [invocations EXCEPT ![i].phase = "failed"]
    /\ UNCHANGED <<ongoingCalls, ongoingCallIndex, releasedSessions,
                   lostCancels, evals, epoch, flushed>>

\* Decode failed for a WAIT-path caller: the error just propagates; the
\* session edge claimed after publication remains (wait, cache.go:4272-4276).
DecodeFailWait(i) ==
    /\ invocations[i].phase = "decodeErr"
    /\ invocations[i].path = "wait"
    /\ invocations' = [invocations EXCEPT ![i].phase = "failed"]
    /\ UNCHANGED <<res, ongoingCalls, ongoingCallIndex, sessionEdges, countedEdges,
                   releasedSessions, lostCancels, evals, epoch, flushed>>

(***************************************************************************)
(* ReleaseSession: drop every counted session edge the session holds, and  *)
(* collect whatever that leaves unowned. One egraphMu critical section.    *)
(*                                                                         *)
(* Go: Cache.ReleaseSession, cache.go:759-833. In this model it can fire   *)
(* at ANY time (unless DrainOnRelease is on) - the cache itself does not   *)
(* wait for the session's in-flight calls. Once per session.               *)
(***************************************************************************)
ReleaseSession(s) ==
    /\ AllowRelease
    /\ s \notin releasedSessions
    /\ DrainOnRelease =>
         \A i \in InvocationIds : invocations[i].sess = s => invocations[i].phase \in TerminalPhases
    /\ LET mine == {e \in countedEdges : e[1] = s}
           rf0 == [r \in DOMAIN res |->
                     IF <<s, r>> \in mine
                     THEN [res[r] EXCEPT !.own = @ - 1]
                     ELSE res[r]]
       IN /\ res' = Cascade(rf0)
          /\ countedEdges' = countedEdges \ mine
          /\ sessionEdges' = {e \in sessionEdges : e[1] # s}
          /\ releasedSessions' = releasedSessions \cup {s}
    /\ UNCHANGED <<invocations, ongoingCalls, ongoingCallIndex, lostCancels, evals, epoch, flushed>>

(***************************************************************************)
(* PruneCut: cut one persisted root edge and let the normal cascade        *)
(* collect whatever that leaves unowned. Fireable at any time.             *)
(*                                                                         *)
(* Go: removePersistedEdge, cache.go:912-941. This one action is the only  *)
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
                   releasedSessions, lostCancels, evals, epoch, flushed>>

(***************************************************************************)
(* FLUSH AND RESTART (increment 3). Graceful shutdown removes every        *)
(* session (each release preceded by the server's drain when the server    *)
(* behaves), then Cache.Close snapshots the retained graph in one          *)
(* egraphMu hold and writes it out (persistCurrentState ->                 *)
(* snapshotPersistState, cache_persistence_worker.go:14/:27; ordering in   *)
(* GracefulStop, engine/server/server.go:771). The model requires only     *)
(* what GracefulStop's structure guarantees - all sessions released -      *)
(* NOT that in-flight work has finished: the cache does not enforce        *)
(* that, so a snapshot racing an unfinished publication is a reachable    *)
(* capture and the FlushCleanCapture property judges it.                   *)
(*                                                                         *)
(* Each snapshot row records, besides the result's durable state, two      *)
(* verdicts used only by properties:                                       *)
(*   dirty    - the attach barrier was not cleanly closed at capture       *)
(*              (still open, or closed with an error). The Go writes no    *)
(*              such marker: attachDeps state lives only in memory, which  *)
(*              is exactly why a restart serves these entries as if they   *)
(*              were fine. (Encoding a half-attached payload may also      *)
(*              simply fail and abort the flush - the safe outcome; the    *)
(*              model explores the successful-encode worst case.)          *)
(*   ownClean - ownership at capture was fully explained by persisted +    *)
(*              dependency edges, nothing transient.                       *)
(***************************************************************************)

Flush ==
    /\ ModelPersistence
    /\ epoch = 1
    /\ ~flushed.done
    /\ releasedSessions = Sessions
    /\ flushed' = [done |-> TRUE, rows |-> [r \in 1..Len(res) |->
         [keep      |-> res[r].registered,
          call      |-> res[r].call,
          persisted |-> res[r].persisted,
          deps      |-> res[r].deps,
          dirty     |-> res[r].barrier \in {"open", "closedErr"},
          ownClean  |-> res[r].own =
              PersistedCount(r) + DepParentCount(r)]]]
    /\ UNCHANGED <<invocations, res, ongoingCalls, ongoingCallIndex,
                   sessionEdges, countedEdges, releasedSessions, lostCancels,
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
         [ongoingCalls[o] EXCEPT !.fnState = "canceled", !.pubState = "none",
                                 !.hold = FALSE, !.inIndex = FALSE,
                                 !.waiters = 0]]
    /\ ongoingCallIndex' = [k \in Calls \X Sessions |-> 0]
    /\ evals' = [e \in EvalIds |->
         IF evals[e].phase \in {"demand", "waiting"}
         THEN [evals[e] EXCEPT !.phase = "abandoned"]
         ELSE evals[e]]
    /\ sessionEdges' = {}
    /\ countedEdges' = {}
    /\ releasedSessions' = {}
    /\ lostCancels' = 0
    /\ UNCHANGED flushed

---------------------------------------------------------------------------
(***************************************************************************)
(* LAZY EVALUATION (increment 2). A resolver can return a result whose     *)
(* expensive materialization is deferred: the value carries a callback,    *)
(* stored on the sharedResult (registerLazyEvaluation, cache.go:2868).     *)
(* Anyone later needing the materialized value calls Cache.Evaluate,       *)
(* which coordinates all callers per result (evaluateOne,                  *)
(* cache.go:2990):                                                         *)
(*   - if evaluation already completed, return immediately                 *)
(*   - if an attempt is in flight, join it as a waiter                     *)
(*   - otherwise start the callback in a goroutine and wait               *)
(* Success is permanent (lazyEvalComplete set, callback cleared,           *)
(* cache.go:3131-3134). Failure leaves the callback in place so a later    *)
(* Evaluate retries. Each waiter can abandon its wait independently        *)
(* (waitForLazyEvaluation's ctx.Done arm, cache.go:2934-2944); the LAST    *)
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
                   sessionEdges, countedEdges, releasedSessions, lostCancels,
                   epoch, flushed>>

\* Fast path: nothing to do - evaluation already completed, or the value
\* carries no callback (evaluateOne, cache.go:3016-3020).
EvalNoWork(e) ==
    /\ evals[e].phase = "demand"
    /\ LET r == evals[e].target IN
       res[r].lazyComplete \/ res[r].lazyCb = "none"
    /\ evals' = [evals EXCEPT ![e].phase = "done"]
    /\ UNCHANGED <<invocations, res, ongoingCalls, ongoingCallIndex,
                   sessionEdges, countedEdges, releasedSessions, lostCancels,
                   epoch, flushed>>

\* Start the callback: no attempt is in flight, so this caller becomes the
\* leader - one lazyMu critical section publishes the wait channel and the
\* cancel, then the callback goroutine starts (evaluateOne,
\* cache.go:3054-3128). With ModelLazySingleflight=FALSE the "no attempt
\* in flight" guard is dropped: a second callback can start concurrently,
\* which is exactly what the real singleflight exists to prevent.
EvalStartAttempt(e) ==
    /\ evals[e].phase = "demand"
    /\ LET r == evals[e].target IN
       /\ res[r].lazyCb = "armed"
       /\ ~res[r].lazyComplete
       /\ IF ModelLazySingleflight
          THEN res[r].lazyPhase = "idle"
          ELSE res[r].lazyPhase \in {"idle", "running"}
       /\ res' = [res EXCEPT
            ![r].lazyPhase = "running",
            ![r].lazyWaiters = @ + 1,
            ![r].lazyRunning = @ + 1,
            ![r].lazyErr = "none"]
       /\ evals' = [evals EXCEPT ![e].phase = "waiting"]
    /\ UNCHANGED <<invocations, ongoingCalls, ongoingCallIndex,
                   sessionEdges, countedEdges, releasedSessions, lostCancels,
                   epoch, flushed>>

\* Join an attempt already in flight (evaluateOne's join arm,
\* cache.go:3038-3053). The guard is only "a wait channel is published" -
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
                   sessionEdges, countedEdges, releasedSessions, lostCancels,
                   epoch, flushed>>

\* The callback finishes on its own (the goroutine tail, cache.go:3129-
\* 3150): latch the outcome; success also marks completion and clears the
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
                   sessionEdges, countedEdges, releasedSessions, lostCancels,
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
                   sessionEdges, countedEdges, releasedSessions, lostCancels,
                   epoch, flushed>>

\* A waiter wakes after the callback finished (waitForLazyEvaluation's
\* wait-channel arm, cache.go:2915-2933): it reads the latched error,
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
                   sessionEdges, countedEdges, releasedSessions, lostCancels,
                   epoch, flushed>>

\* A waiter gives up (its own context canceled - waitForLazyEvaluation's
\* ctx.Done arm, cache.go:2934-2944). Only the LAST waiter to leave
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
                   sessionEdges, countedEdges, releasedSessions, lostCancels,
                   epoch, flushed>>

---------------------------------------------------------------------------
\* Everything that can happen, from any state: some invocation takes its
\* next step, some ongoing call's fn or publication advances, a session is
\* released, or a persisted edge is pruned.
Next ==
    \/ Spawn
    \/ \E i \in InvocationIds :
         \/ LookupHit(i) \/ LookupMiss(i)
         \/ Join(i) \/ CreateOc(i) \/ CreateOcLeaseFail(i)
         \/ WaiterCancel(i) \/ WaiterObserveFnErr(i)
         \/ WaiterEarlyReturn(i) \/ WaiterObservePubErr(i)
         \/ WaiterClaimRecord(i) \/ WaiterClaimCount(i)
         \/ WaiterDepart(i) \/ WaiterReleaseHold(i)
         \/ ReadBarrierOk(i) \/ ReadBarrierErrHit(i) \/ ReadBarrierErrWait(i)
         \/ DecodeLead(i) \/ DecodeLeadFinish(i) \/ DecodeJoin(i)
         \/ DecodeWake(i) \/ DecodeFailHit(i) \/ DecodeFailWait(i)
    \/ \E o \in OngoingCallIds :
         \/ FnComplete(o) \/ PubBegin(o)
         \/ PubIndexFresh(o) \/ PubAdoptWithHold(o)
         \/ PubAdoptNoHold(o) \/ PubAdoptLateHold(o)
         \/ PubIndexReuse(o)
         \/ PubAttachAddDep(o) \/ PubFinishOk(o) \/ PubAttachFail(o)
         \/ PubUnregister(o)
    \/ \E s \in Sessions : ReleaseSession(s)
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
(*   - Spawn: clients may stop arriving                                    *)
(*   - WaiterCancel and every failure-injection branch: possibilities,     *)
(*     not obligations                                                     *)
(*   - ReleaseSession and PruneCut: external events                        *)
(*                                                                         *)
(* Two placement subtleties:                                               *)
(*   - FnComplete's fairness guarantees SOME outcome, not a successful     *)
(*     one (the outcome choice inside stays nondeterministic)              *)
(*   - fairness sits on the disjunction of the attach outcomes             *)
(*     (PubFinishOk or PubAttachFail), never on the success arm alone -    *)
(*     fairness on the success arm would wrongly forbid persistent         *)
(*     failure                                                             *)
(***************************************************************************)
SystemProgress(o) ==
    \/ FnComplete(o) \/ PubBegin(o)
    \/ PubIndexFresh(o) \/ PubAdoptWithHold(o)
    \/ PubAdoptNoHold(o) \/ PubAdoptLateHold(o)
    \/ PubIndexReuse(o)
    \/ PubFinishOk(o) \/ PubAttachFail(o)
    \/ PubUnregister(o)

WaiterProgress(i) ==
    \/ LookupHit(i) \/ LookupMiss(i) \/ Join(i) \/ CreateOc(i)
    \/ WaiterObserveFnErr(i) \/ WaiterObservePubErr(i)
    \/ WaiterClaimRecord(i) \/ WaiterClaimCount(i)
    \/ WaiterDepart(i) \/ WaiterReleaseHold(i)
    \/ ReadBarrierOk(i) \/ ReadBarrierErrHit(i) \/ ReadBarrierErrWait(i)
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
    \* Vacuous when MaxEvals = 0, so increment-1 liveness runs are
    \* untouched by the lazy machinery.
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
(* configuration would conflate independent bugs: a configuration that     *)
(* deliberately reproduces one known race would drown the property under   *)
(* test in violations of unrelated properties.                             *)
(***************************************************************************)

\* Basic shape sanity: bounds respected, counts non-negative, and no
\* counted edge without its record.
TypeOK ==
    /\ Len(invocations) <= MaxInvocations
    /\ Len(res) <= MaxResults
    /\ Len(evals) <= MaxEvals
    /\ countedEdges \subseteq sessionEdges
    /\ epoch \in {1, 2}
    /\ \A o \in OngoingCallIds : ongoingCalls[o].waiters >= 0
    /\ \A r \in ResultIds : res[r].lazyWaiters >= 0 /\ res[r].lazyRunning >= 0

\* Ownership accounting is exact: for every registered result, the
\* incrementally-maintained counter equals the recount of its edges
\* (counted session edges + dependency parents + handoff holds +
\* persisted edge + orphaned increments).
OwnershipExact ==
    \A r \in ResultIds :
        res[r].registered => res[r].own = DerivedOwn(r)

\* No ownership count ever goes below zero. (The code checks this at
\* runtime and errors, cache.go:909-911; here it must be unreachable.)
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

\* A persistable request that completes successfully observes a persisted
\* edge on its result. (Violated on main by the joiner race described at
\* the Join action.)
PersistableHonored ==
    \A i \in InvocationIds : invocations[i].phase = "done" => invocations[i].retPersisted

\* Every created WithCancelCause cancel is eventually discharged.
\* (Violated on main by the lease-error branch; see CreateOcLeaseFail.)
NoLostCancels == lostCancels = 0

\* Racing alone never manufactures a user-visible error: if no failure
\* injection is enabled, no invocation ends in "failed". (Interesting in
\* the guard-only canonical configuration: the resurrection guard turns
\* the use-after-release into a spurious error - safety preserved, error
\* manufactured by pure racing.)
NoSpuriousErrors ==
    (~FnCanFail /\ ~AttachCanFail /\ ~LeaseCanFail) =>
        \A i \in InvocationIds : invocations[i].phase # "failed"

\* Once every session is released and all activity has settled, no session
\* ownership may remain: no counted edges, no orphaned increments.
\* Violated by edges claimed AFTER their session released - retention
\* leaks that only the server's drain prevents today.
NoOrphanEdgesAtQuiescence ==
    (/\ releasedSessions = Sessions
     /\ Len(invocations) = MaxInvocations
     /\ \A i \in InvocationIds : invocations[i].phase \in TerminalPhases
     /\ \A o \in OngoingCallIds : ongoingCalls[o].pubState \in {"none", "done", "failed"}
                          /\ ~ongoingCalls[o].hold)
    => /\ countedEdges = {}
       /\ \A r \in ResultIds : res[r].registered => res[r].orphan = 0

\* A result whose dependency attachment failed must not be retained beyond
\* the failed call that produced it. The code violates this for persistable
\* calls: the persisted edge is added inside the publication critical
\* section (cache.go:4601), BEFORE attachment runs (:4618), and the
\* attach-error path releases only the handoff hold - the persisted edge
\* survives. The result stays registered and lookup-visible with its
\* barrier closed on an error, so every future equivalent call finds it,
\* waits on the barrier, and fails - it never falls back to executing.
\* Nothing in the code ever clears this state: the error lives only in
\* memory, so a graceful shutdown flushes the entry like any retained
\* result and a restart re-imports it without the error, serving a payload
\* whose attachment never completed. Until then, only pruning the
\* persisted edge (or wiping the store) recovers.
NoRetainedPoisonedEntry ==
    \A r \in ResultIds :
        (res[r].registered /\ res[r].barrier = "closedErr") =>
            ~res[r].persisted

(***************************************************************************)
(* LAZY-EVALUATION PROPERTIES (increment 2). Declared before the lazy      *)
(* actions were modeled; each names what the code's lazyMu coordination    *)
(* is supposed to guarantee.                                               *)
(***************************************************************************)

\* At most one lazy callback runs per result at any moment - the whole
\* point of the per-result singleflight in evaluateOne. Expected to fail
\* only in the configuration that deliberately removes the singleflight.
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
(* IMPORT/FLUSH PROPERTIES (increment 3). Declared before the              *)
(* persistence actions were modeled.                                       *)
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

\* A result imported from a snapshot row that was captured dirty
\* (mid-attachment, or attach-failed) is never served to a caller. The
\* code violates this: the dirtiness lives only in memory, so the
\* restarted engine serves the entry as if it were fine.
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
