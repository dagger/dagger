-------------------------- MODULE ClientLifecycle --------------------------
(***************************************************************************)
(* A TLC-checked model of engine client-runtime reclamation and its session *)
(* teardown boundary.                                                       *)
(*                                                                          *)
(* The dagql cache is intentionally abstract here. CacheLifecycle.tla owns  *)
(* the detailed cache algorithm and guarantees that a successful            *)
(* WaitSessionRelease returns only after deferred cleanup is done. This      *)
(* model consumes that contract as the cache phases live, deferred,          *)
(* cleaning, and done.                                                       *)
(*                                                                          *)
(* One action represents one Go serialization point: the session scope lock *)
(* for admission/close/lease/reclamation, or the session teardown goroutine  *)
(* for cache and telemetry phase changes.                                    *)
(***************************************************************************)
EXTENDS Naturals, FiniteSets, TLC

CONSTANTS
    Clients,
    Root,
    NoClient,
    ParentOf,
    Requests,
    Works,
    ModelBackground,
    ModelChildren,
    ModelTeardown

ASSUME /\ Root \in Clients
       /\ NoClient \notin Clients
       /\ DOMAIN ParentOf = Clients
       /\ ParentOf[Root] = NoClient
       /\ \A c \in Clients \ {Root} : ParentOf[c] \in Clients

LeaseKinds == {"transport", "request", "shared-work", "service", "child"}
BackgroundKinds == {"shared-work", "service"}
SessionPhases == {"live", "closing", "telemetryStopped", "removed"}
CachePhases == {"live", "deferred", "cleaning", "done"}
MetricPhases == {"live", "draining", "stopped"}
ItemPhases == {"idle", "active", "done"}

\* Configuration helper: one root with every other client its direct child.
RootParents == [c \in Clients |-> IF c = Root THEN NoClient ELSE Root]

VARIABLES
    sessionPhase,
    clients,
    leases,
    requests,
    work,
    cachePhase,
    clientMetrics,
    reclaimed

vars == <<sessionPhase, clients, leases, requests, work, cachePhase,
          clientMetrics, reclaimed>>

ClientRecord(published, accepting, runtime, transport) ==
    [published |-> published,
     accepting |-> accepting,
     runtime |-> runtime,
     transport |-> transport]

IdleRequest == [phase |-> "idle", client |-> Root]
IdleWork == [phase |-> "idle", client |-> Root, kind |-> "shared-work"]

Init ==
    /\ sessionPhase = "live"
    /\ clients = [c \in Clients |->
         IF c = Root
         THEN ClientRecord(TRUE, TRUE, TRUE, TRUE)
         ELSE ClientRecord(FALSE, FALSE, FALSE, FALSE)]
    /\ leases = [c \in Clients |->
         [k \in LeaseKinds |-> IF c = Root /\ k = "transport" THEN 1 ELSE 0]]
    /\ requests = [q \in Requests |-> IdleRequest]
    /\ work = [w \in Works |-> IdleWork]
    /\ cachePhase = "live"
    /\ clientMetrics = [c \in Clients |->
         IF c = Root THEN "live" ELSE "stopped"]
    /\ reclaimed = {}

ActiveRequests(c) ==
    {q \in Requests :
        requests[q].phase = "active" /\ requests[q].client = c}

ActiveWork(c, k) ==
    {w \in Works :
        work[w].phase = "active"
          /\ work[w].client = c
          /\ work[w].kind = k}

AllActiveWork ==
    {w \in Works : work[w].phase = "active"}

ActiveSharedWork ==
    {w \in Works :
        work[w].phase = "active" /\ work[w].kind = "shared-work"}

ActiveServiceWork ==
    {w \in Works :
        work[w].phase = "active" /\ work[w].kind = "service"}

LiveChildren(c) ==
    {child \in Clients :
        ParentOf[child] = c
          /\ clients[child].published
          /\ clients[child].runtime}

ExpectedLeaseCount(c, k) ==
    CASE k = "transport" -> IF clients[c].transport THEN 1 ELSE 0
      [] k = "request" -> Cardinality(ActiveRequests(c))
      [] k = "shared-work" -> Cardinality(ActiveWork(c, "shared-work"))
      [] k = "service" -> Cardinality(ActiveWork(c, "service"))
      [] k = "child" ->
           IF sessionPhase = "live" THEN Cardinality(LiveChildren(c)) ELSE 0

TotalLeases(c) ==
    leases[c]["transport"]
      + leases[c]["request"]
      + leases[c]["shared-work"]
      + leases[c]["service"]
      + leases[c]["child"]

HasDelegatingWork(c) ==
    ActiveRequests(c) # {}
      \/ ActiveWork(c, "shared-work") # {}
      \/ ActiveWork(c, "service") # {}

NoActiveProducers ==
    /\ \A c \in Clients : ActiveRequests(c) = {}
    /\ AllActiveWork = {}
    /\ cachePhase # "cleaning"

(***************************************************************************)
(* REQUEST AND DETACHED-WORK LIFECYCLES                                    *)
(***************************************************************************)

\* Go: getOrInitClient admits the request and publishes its request lease
\* while holding the session scope lock.
AdmitRequest(q, c) ==
    /\ sessionPhase = "live"
    /\ requests[q].phase = "idle"
    /\ clients[c].published
    /\ clients[c].accepting
    /\ clients[c].runtime
    /\ requests' = [requests EXCEPT
         ![q] = [phase |-> "active", client |-> c]]
    /\ leases' = [leases EXCEPT ![c]["request"] = @ + 1]
    /\ UNCHANGED <<sessionPhase, clients, work, cachePhase, clientMetrics, reclaimed>>

\* Go: request cleanup releases the exact lease admitted above.
FinishRequest(q) ==
    /\ requests[q].phase = "active"
    /\ LET c == requests[q].client IN
       /\ requests' = [requests EXCEPT ![q].phase = "done"]
       /\ leases' = [leases EXCEPT ![c]["request"] = @ - 1]
    /\ UNCHANGED <<sessionPhase, clients, work, cachePhase, clientMetrics, reclaimed>>

\* Go: DetachClientScope clones one typed lease from already-held executable
\* authority. Client close does not invalidate an accepted request's ability
\* to delegate, but authoritative session teardown does.
StartBackgroundWork(w, c) ==
    /\ ModelBackground
    /\ sessionPhase = "live"
    /\ work[w].phase = "idle"
    /\ clients[c].runtime
    /\ HasDelegatingWork(c)
    /\ \E k \in BackgroundKinds :
         /\ work' = [work EXCEPT
              ![w] = [phase |-> "active", client |-> c, kind |-> k]]
         /\ leases' = [leases EXCEPT ![c][k] = @ + 1]
    /\ UNCHANGED <<sessionPhase, clients, requests, cachePhase, clientMetrics, reclaimed>>

\* The work lease spans the callback's complete terminal transition. If this
\* is the final shared cache operation after ReleaseSession returned, the same
\* transition hands the abstract cache to its cleanup phase.
FinishBackgroundWork(w) ==
    /\ work[w].phase = "active"
    /\ LET c == work[w].client
           k == work[w].kind
           lastDeferredShared ==
               k = "shared-work"
                 /\ cachePhase = "deferred"
                 /\ Cardinality(ActiveSharedWork) = 1
       IN /\ work' = [work EXCEPT ![w].phase = "done"]
          /\ leases' = [leases EXCEPT ![c][k] = @ - 1]
          /\ cachePhase' =
               IF lastDeferredShared THEN "cleaning" ELSE cachePhase
    /\ UNCHANGED <<sessionPhase, clients, requests, clientMetrics, reclaimed>>

(***************************************************************************)
(* CLIENT REACHABILITY, CHILD OWNERSHIP, AND RECLAMATION                   *)
(***************************************************************************)

\* Go: proxy registration atomically publishes the child's transport lease
\* and the parent's child lease from a held request scope.
RegisterChild(q, child) ==
    /\ ModelChildren
    /\ sessionPhase = "live"
    /\ requests[q].phase = "active"
    /\ LET parent == requests[q].client IN
       /\ child # Root
       /\ ParentOf[child] = parent
       /\ ~clients[child].published
       /\ clients[parent].runtime
       /\ clients' = [clients EXCEPT
            ![child] = ClientRecord(TRUE, TRUE, TRUE, TRUE)]
       /\ leases' = [leases EXCEPT
            ![child]["transport"] = @ + 1,
            ![parent]["child"] = @ + 1]
       /\ clientMetrics' = [clientMetrics EXCEPT ![child] = "live"]
    /\ UNCHANGED <<sessionPhase, requests, work, cachePhase, reclaimed>>

\* Go: transport.Close and closeClientScope serialize the monotonic admission
\* stop with release of transport ownership.
CloseTransport(c) ==
    /\ sessionPhase = "live"
    /\ clients[c].published
    /\ clients[c].transport
    /\ clients' = [clients EXCEPT
         ![c].accepting = FALSE,
         ![c].transport = FALSE]
    /\ leases' = [leases EXCEPT ![c]["transport"] = @ - 1]
    /\ UNCHANGED <<sessionPhase, requests, work, cachePhase, clientMetrics, reclaimed>>

\* Go: maybeBeginClientRuntimeReclamationLocked marks the reclamation owner. The
\* closed runtime stays published while its client-bound metric provider drains.
BeginClientMetricDrain(c) ==
    /\ sessionPhase = "live"
    /\ clients[c].published
    /\ clients[c].runtime
    /\ ~clients[c].accepting
    /\ TotalLeases(c) = 0
    /\ clientMetrics[c] = "live"
    /\ clientMetrics' = [clientMetrics EXCEPT ![c] = "draining"]
    /\ UNCHANGED <<sessionPhase, clients, leases, requests, work, cachePhase, reclaimed>>

\* Go: finishClientRuntimeReclamation. Once the metric ForceFlush+Shutdown
\* barrier returns, the runtime is unpublished and its parent edge is released.
\* If authoritative teardown won meanwhile, it already released child edges.
FinishClientReclamation(c) ==
    /\ clientMetrics[c] = "draining"
    /\ clients' = [clients EXCEPT ![c].runtime = FALSE]
    /\ clientMetrics' = [clientMetrics EXCEPT ![c] = "stopped"]
    /\ reclaimed' = reclaimed \cup {c}
    /\ leases' =
         IF sessionPhase # "live" \/ ParentOf[c] = NoClient
         THEN leases
         ELSE [leases EXCEPT ![ParentOf[c]]["child"] = @ - 1]
    /\ UNCHANGED <<sessionPhase, requests, work, cachePhase>>

(***************************************************************************)
(* AUTHORITATIVE SESSION TEARDOWN AND TELEMETRY BARRIER                    *)
(***************************************************************************)

\* Go: markSessionRemoved + beginClientScopeTeardown. Teardown wins the scope
\* serialization point, rejects all new delegation, and releases transport
\* and parent-child reachability. Existing request/background leases drain
\* through their normal terminal actions.
BeginSessionTeardown ==
    /\ ModelTeardown
    /\ sessionPhase = "live"
    /\ sessionPhase' = "closing"
    /\ clients' = [c \in Clients |->
         [clients[c] EXCEPT !.accepting = FALSE, !.transport = FALSE]]
    /\ leases' = [c \in Clients |->
         [k \in LeaseKinds |->
             IF k \in {"transport", "child"} THEN 0 ELSE leases[c][k]]]
    /\ UNCHANGED <<requests, work, cachePhase, clientMetrics, reclaimed>>

\* The server begins cache release after handlers and services drain.
\* ReleaseSession may return with cleanup deferred to shared cache work.
BeginCacheRelease ==
    /\ ModelTeardown
    /\ sessionPhase = "closing"
    /\ cachePhase = "live"
    /\ \A c \in Clients : ActiveRequests(c) = {}
    /\ ActiveServiceWork = {}
    /\ cachePhase' =
         IF ActiveSharedWork = {} THEN "cleaning" ELSE "deferred"
    /\ UNCHANGED <<sessionPhase, clients, leases, requests, work, clientMetrics, reclaimed>>

\* Go: WaitSessionRelease observes completion only after cache release hooks
\* and per-session record deletion have finished.
FinishCacheCleanup ==
    /\ cachePhase = "cleaning"
    /\ cachePhase' = "done"
    /\ UNCHANGED <<sessionPhase, clients, leases, requests, work, clientMetrics, reclaimed>>

\* Go: shutdownTelemetry drains every still-live client metric provider after
\* cache cleanup and all typed producers have stopped.
StopClientMetrics(c) ==
    /\ sessionPhase = "closing"
    /\ cachePhase = "done"
    /\ NoActiveProducers
    /\ clientMetrics[c] = "live"
    /\ clientMetrics' = [clientMetrics EXCEPT ![c] = "stopped"]
    /\ UNCHANGED <<sessionPhase, clients, leases, requests, work, cachePhase, reclaimed>>

\* Go: shutdownTelemetry is the final producer barrier. In particular it may
\* not run merely because ReleaseSession returned in the deferred phase.
StopTelemetry ==
    /\ sessionPhase = "closing"
    /\ cachePhase = "done"
    /\ NoActiveProducers
    /\ \A c \in Clients : clientMetrics[c] = "stopped"
    /\ sessionPhase' = "telemetryStopped"
    /\ UNCHANGED <<clients, leases, requests, work, cachePhase, clientMetrics, reclaimed>>

\* The session record and retained runtimes are dropped only after telemetry
\* has stopped and no executable work remains.
RemoveSession ==
    /\ sessionPhase = "telemetryStopped"
    /\ sessionPhase' = "removed"
    /\ clients' = [c \in Clients |->
         [clients[c] EXCEPT !.accepting = FALSE,
                            !.runtime = FALSE,
                            !.transport = FALSE]]
    /\ leases' = [c \in Clients |-> [k \in LeaseKinds |-> 0]]
    /\ UNCHANGED <<requests, work, cachePhase, clientMetrics, reclaimed>>

Next ==
    \/ \E q \in Requests, c \in Clients : AdmitRequest(q, c)
    \/ \E q \in Requests : FinishRequest(q)
    \/ \E w \in Works, c \in Clients : StartBackgroundWork(w, c)
    \/ \E w \in Works : FinishBackgroundWork(w)
    \/ \E q \in Requests, child \in Clients : RegisterChild(q, child)
    \/ \E c \in Clients : CloseTransport(c)
    \/ \E c \in Clients : BeginClientMetricDrain(c)
    \/ \E c \in Clients : FinishClientReclamation(c)
    \/ BeginSessionTeardown
    \/ BeginCacheRelease
    \/ FinishCacheCleanup
    \/ \E c \in Clients : StopClientMetrics(c)
    \/ StopTelemetry
    \/ RemoveSession

Spec == Init /\ [][Next]_vars

SystemProgress ==
    \/ BeginCacheRelease
    \/ FinishCacheCleanup
    \/ \E c \in Clients : StopClientMetrics(c)
    \/ StopTelemetry
    \/ RemoveSession

LiveSpec ==
    /\ Spec
    /\ WF_vars(SystemProgress)
    /\ \A q \in Requests : WF_vars(FinishRequest(q))
    /\ \A w \in Works : WF_vars(FinishBackgroundWork(w))
    /\ \A c \in Clients : WF_vars(BeginClientMetricDrain(c))
    /\ \A c \in Clients : WF_vars(FinishClientReclamation(c))

(***************************************************************************)
(* SAFETY AND LIVENESS PROPERTIES                                          *)
(***************************************************************************)

TypeOK ==
    /\ sessionPhase \in SessionPhases
    /\ cachePhase \in CachePhases
    /\ \A c \in Clients : clientMetrics[c] \in MetricPhases
    /\ reclaimed \subseteq Clients
    /\ DOMAIN clients = Clients
    /\ DOMAIN leases = Clients
    /\ DOMAIN requests = Requests
    /\ DOMAIN work = Works
    /\ \A c \in Clients :
         /\ clients[c].published \in BOOLEAN
         /\ clients[c].accepting \in BOOLEAN
         /\ clients[c].runtime \in BOOLEAN
         /\ clients[c].transport \in BOOLEAN
         /\ DOMAIN leases[c] = LeaseKinds
         /\ \A k \in LeaseKinds : leases[c][k] \in Nat
         /\ clients[c].accepting => clients[c].runtime
         /\ clients[c].transport => clients[c].accepting
         /\ sessionPhase = "live" /\ clients[c].runtime
              => clientMetrics[c] # "stopped"
    /\ \A q \in Requests :
         /\ requests[q].phase \in ItemPhases
         /\ requests[q].client \in Clients
    /\ \A w \in Works :
         /\ work[w].phase \in ItemPhases
         /\ work[w].client \in Clients
         /\ work[w].kind \in BackgroundKinds
    /\ cachePhase # "live" => sessionPhase # "live"
    /\ sessionPhase \in {"telemetryStopped", "removed"} => cachePhase = "done"

\* Every typed lease is explained by one exact owner represented in the model.
LeaseExact ==
    \A c \in Clients, k \in LeaseKinds :
        leases[c][k] = ExpectedLeaseCount(c, k)

\* Executable work never observes a reclaimed runtime.
ActiveWorkHasRuntime ==
    /\ \A q \in Requests :
         requests[q].phase = "active"
           => /\ clients[requests[q].client].runtime
              /\ clientMetrics[requests[q].client] = "live"
    /\ \A w \in Works :
         work[w].phase = "active"
           => /\ clients[work[w].client].runtime
              /\ clientMetrics[work[w].client] = "live"

\* A live session cannot reclaim a client while any typed owner remains.
NoEarlyReclamation ==
    \A c \in Clients :
        sessionPhase = "live" /\ clients[c].published /\ ~clients[c].runtime
          => TotalLeases(c) = 0

\* Runtime reclamation completes only after the client-bound metric provider's
\* final flush and shutdown barrier.
MetricsStopBeforeReclamation ==
    \A c \in Clients :
        clients[c].published /\ ~clients[c].runtime
          => clientMetrics[c] = "stopped"

\* A live-runtime metric drain can begin only after reachability closes and
\* every typed producer lease reaches its terminal transition.
MetricsDrainAfterProducers ==
    \A c \in Clients :
        clientMetrics[c] = "draining"
          => /\ ~clients[c].accepting
             /\ TotalLeases(c) = 0

\* Runtime publication is monotonic for one client ID.
NoClientResurrection ==
    \A c \in reclaimed : ~clients[c].runtime

\* A published child runtime is an executable ownership edge on its parent.
ChildRetainsParent ==
    \A child \in Clients :
        sessionPhase = "live"
          /\ child # Root
          /\ clients[child].published
          /\ clients[child].runtime
            => /\ clients[ParentOf[child]].runtime
               /\ leases[ParentOf[child]]["child"] > 0

\* The session-owned trace/log providers stop only after every producer,
\* including deferred cache cleanup and every live client metric provider, has
\* reached its terminal transition.
TelemetryStopsAfterProducers ==
    sessionPhase \in {"telemetryStopped", "removed"}
      => /\ cachePhase = "done"
         /\ NoActiveProducers
         /\ \A c \in Clients : clientMetrics[c] = "stopped"

\* Once a live-session client is closed and lease-free, fair reclamation
\* eventually unpublishes its runtime.
QuiescentClientEventuallyReclaimed ==
    \A c \in Clients :
        (sessionPhase = "live"
          /\ clients[c].published
          /\ clients[c].runtime
          /\ ~clients[c].accepting
          /\ TotalLeases(c) = 0)
            ~> ~clients[c].runtime

\* Once teardown has no external producers and cache cleanup is runnable or
\* complete, fair system progress reaches final session removal.
QuiescentTeardownEventuallyCompletes ==
    (sessionPhase = "closing"
      /\ AllActiveWork = {}
      /\ \A c \in Clients : ActiveRequests(c) = {}
      /\ cachePhase \in {"cleaning", "done"})
        ~> sessionPhase = "removed"

=============================================================================
