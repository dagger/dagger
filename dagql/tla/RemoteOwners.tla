--------------------------- MODULE RemoteOwners ---------------------------
(* Offer owners and the ownership they give and take, beside RemoteParts   *)
(* and like it separate from CacheLifecycle. Three rows: the imported      *)
(* receiver R, a Service S that needs session handle h, and a child C that *)
(* depends on R. Two offer owners can be published into R's one offer slot *)
(* for its one pending part. Two sessions demand it; only s1 holds h.      *)
(*                                                                         *)
(* Modeled (dagql/cache_offer_owner.go, cache_part_source.go,              *)
(* cache_part_install.go, cache_part_content.go settlePart):               *)
(*   - an offer owner is alive while a slot publishes it or an acquisition *)
(*     holds it, and while alive it holds each row it names; those holds   *)
(*     are not dependency edges;                                           *)
(*   - publication into the slot replaces the previous owner, which        *)
(*     survives through its acquisitions; an owner whose rows reach the    *)
(*     receiver is rejected before anything is mutated;                    *)
(*   - the ordinary hit is filtered by the receiver's own requirement,     *)
(*     the closure over dependency edges only; an offer whose rows the     *)
(*     session cannot satisfy is skipped, the hit is not refused;          *)
(*   - Commit checks the owner and every row it names, turns them into the *)
(*     receiver's own dependency edges and requirement, and drops the      *)
(*     acquisition hold before settlement; settlement retires whatever     *)
(*     owner the slot holds then, the replacement included;                *)
(*   - every row carries a stored ownership count, moved by the actions as *)
(*     the code moves incomingOwnershipCount, and is collected at zero.    *)
(*     The invariants recount ownership from the actual edges and holds.   *)
(*                                                                         *)
(* Abstracted away: how a session finds the receiver (one Lookup step      *)
(* stands for the e-graph lookup and its filter), publication of results,  *)
(* the part gate and byte routes (RemoteParts), bytes, leases, restart.    *)
(* Bounds limit external events only: slot publications, retention drops.  *)
(*                                                                         *)
(* Assumed, and discharged by CacheLifecycle's own invariants: stored      *)
(* counts are exact and never underflow (OwnershipExact, NoUnderflow), a   *)
(* collected row never returns (NoResurrection), and a row's stored        *)
(* requirement equals its direct closure before any offer is involved      *)
(* (RequiredExact). This module re-asserts the first and the last over its *)
(* own actions, so none is a silent axiom.                                 *)
EXTENDS Naturals, FiniteSets, TLC

CONSTANTS MaxPubs, MaxDrops, Fault

Rows == {"R", "S", "C"}
Sessions == {"s1", "s2"}
Owners == {"o1", "o2"}
Handles == {"h"}
Bound == [s \in Sessions |-> IF s = "s1" THEN {"h"} ELSE {}]
Handle == [r \in Rows |-> IF r = "S" THEN {"h"} ELSE {}]
DepChoices == {{"S"}, {"C"}, {"S", "C"}}
None == "none"

VARIABLES live, count, retained, edges, deps, required, owner, slot, part, acq, used, hist
vars == <<live, count, retained, edges, deps, required, owner, slot, part, acq, used, hist>>

DeadOwner == [alive |-> FALSE, deps |-> {}, slots |-> 0, holds |-> 0]
IdleAcq == [pc |-> "idle", owner |-> None]

Init ==
    /\ live = [r \in Rows |-> TRUE]
    /\ retained = Rows
    /\ edges = {}
    /\ deps = {<<"C", "R">>}
    /\ count = [r \in Rows |-> IF r = "R" THEN 2 ELSE 1]
    /\ required = [r \in Rows |-> Handle[r]]
    /\ owner = [o \in Owners |-> DeadOwner]
    /\ slot = None
    /\ part = "Pending"
    /\ acq = [s \in Sessions |-> IdleAcq]
    /\ used = [pubs |-> 0, drops |-> 0]
    /\ hist = [wronglyRefused |-> FALSE]

RECURSIVE Reach(_, _, _)
Reach(ds, from, seen) ==
    LET next == {d \in Rows : \E f \in from : <<f, d>> \in ds} \ seen IN
    IF next = {} THEN seen ELSE Reach(ds, next, seen \cup next)
ClosureIn(ds, r) == Reach(ds, {r}, {r})
DirectClosure(r) == ClosureIn(deps, r)
RequiredIn(ds, r) == UNION {Handle[x] : x \in ClosureIn(ds, r)}
DirectRequired(r) == RequiredIn(deps, r)
OwnerRequired(o) == UNION {required[x] : x \in owner[o].deps}
Satisfies(s, hs) == hs \subseteq Bound[s]

\* Subtract one from each row of ds, add one to each row of as.
Move(cnt, ds, as) == [r \in Rows |-> (cnt[r] + (IF r \in as THEN 1 ELSE 0)) - (IF r \in ds THEN 1 ELSE 0)]

\* The owner record after one slot or hold ends, and the rows it releases if
\* that was its last.
AfterEnd(o, slots, holds) ==
    LET s == owner[o].slots - slots
        h == owner[o].holds - holds IN
    IF s + h = 0 THEN DeadOwner ELSE [owner[o] EXCEPT !.slots = s, !.holds = h]
Released(o, slots, holds) ==
    IF (owner[o].slots - slots) + (owner[o].holds - holds) = 0 THEN owner[o].deps ELSE {}

\* Publish owner o, naming rows d, into the receiver's slot.
PublishSlot(o, d) ==
    /\ used.pubs < MaxPubs /\ part = "Pending" /\ live["R"]
    /\ ~owner[o].alive /\ slot # o
    /\ used' = [used EXCEPT !.pubs = @ + 1]
    /\ IF (\E x \in d : ~live[x] \/ "R" \in DirectClosure(x))
       THEN \* Rejected before any mutation: a collected row, or a cycle.
            UNCHANGED <<count, owner, slot>>
       ELSE LET old == slot
                kill == Fault = "ReleaseOnReplace" /\ old # None
                freed == IF old = None THEN {} ELSE IF kill THEN owner[old].deps ELSE Released(old, 1, 0) IN
            /\ slot' = o
            /\ owner' = [x \in Owners |->
                   IF x = o THEN [alive |-> TRUE, deps |-> d, slots |-> 1, holds |-> 0]
                   ELSE IF x = old THEN (IF kill THEN [DeadOwner EXCEPT !.holds = owner[old].holds] ELSE AfterEnd(old, 1, 0))
                   ELSE owner[x]]
            \* Two steps in the code, one here: the new owner's holds, then
            \* the old owner's release.
            /\ count' = Move(Move(count, {}, d), freed, {})
    /\ UNCHANGED <<live, retained, edges, deps, required, part, acq, hist>>

\* The ordinary hit. Its filter is the receiver's stored own requirement.
Lookup(s) ==
    LET filter == required["R"] \cup (IF Fault = "OfferResourcesInLookup" /\ slot # None THEN OwnerRequired(slot) ELSE {}) IN
    /\ live["R"] /\ <<s, "R">> \notin edges /\ acq[s].pc = "idle"
    /\ IF Satisfies(s, filter)
       THEN /\ edges' = edges \cup {<<s, "R">>}
            /\ count' = Move(count, {}, {"R"})
            /\ UNCHANGED hist
       ELSE /\ hist' = [hist EXCEPT !.wronglyRefused = @ \/ Satisfies(s, DirectRequired("R"))]
            /\ UNCHANGED <<edges, count>>
    /\ UNCHANGED <<live, retained, deps, required, owner, slot, part, acq, used>>

\* Source selection for the pending part. An offer the session may not use is
\* skipped; the demand goes on without it.
StartAcquire(s) ==
    /\ <<s, "R">> \in edges /\ acq[s].pc = "idle" /\ part = "Pending"
    /\ IF slot # None /\ owner[slot].alive /\ Satisfies(s, OwnerRequired(slot))
       THEN /\ acq' = [acq EXCEPT ![s] = [pc |-> "selected", owner |-> slot]]
            /\ owner' = [owner EXCEPT ![slot].holds = @ + 1]
       ELSE /\ acq' = [acq EXCEPT ![s] = [pc |-> "skipped", owner |-> None]]
            /\ UNCHANGED owner
    /\ UNCHANGED <<live, count, retained, edges, deps, required, slot, part, used, hist>>

\* CommitReadyPart for an admitted chain. The acquisition hold ends at the
\* publication handoff, before settlement.
Install(s) ==
    LET o == acq[s].owner
        ok == owner[o].alive /\ (\A x \in owner[o].deps : live[x]) /\ part = "Pending"
               /\ Satisfies(s, OwnerRequired(o))
        new == {x \in owner[o].deps : <<"R", x>> \notin deps}
        keep == Fault = "RetainOwnerInRetry" /\ ok
        freed == IF keep THEN {} ELSE Released(o, 0, 1) IN
    /\ acq[s].pc = "selected"
    /\ owner' = [owner EXCEPT ![o] = IF keep THEN @ ELSE AfterEnd(o, 0, 1)]
    /\ IF ok
       THEN /\ deps' = deps \cup {<<"R", x>> : x \in new}
            \* The receiver's requirement grows and the growth cascades to
            \* every row that depends on it (preparePartDependenciesLocked).
            /\ required' = [r \in Rows |-> IF live[r] THEN RequiredIn(deps \cup {<<"R", x>> : x \in new}, r) ELSE required[r]]
            /\ count' = Move(Move(count, {}, new), freed, {})
            /\ part' = "Installed"
            /\ acq' = [acq EXCEPT ![s].pc = "installed"]
       ELSE /\ count' = Move(count, freed, {})
            /\ acq' = [acq EXCEPT ![s] = IdleAcq]
            /\ UNCHANGED <<deps, required, part>>
    /\ UNCHANGED <<live, retained, edges, slot, used, hist>>

\* settlePart: the output completes and the slot's current owner retires.
Settle(s) ==
    /\ acq[s].pc = "installed"
    /\ part' = "Complete"
    /\ acq' = [acq EXCEPT ![s].pc = "done"]
    /\ IF slot = None
       THEN UNCHANGED <<owner, count, slot>>
       ELSE /\ owner' = [owner EXCEPT ![slot] = AfterEnd(slot, 1, 0)]
            /\ count' = Move(count, Released(slot, 1, 0), {})
            /\ slot' = None
    /\ UNCHANGED <<live, retained, edges, deps, required, used, hist>>

CancelAcquire(s) ==
    LET o == acq[s].owner IN
    /\ acq[s].pc = "selected"
    /\ owner' = [owner EXCEPT ![o] = AfterEnd(o, 0, 1)]
    /\ count' = Move(count, Released(o, 0, 1), {})
    /\ acq' = [acq EXCEPT ![s] = [pc |-> "done", owner |-> None]]
    /\ UNCHANGED <<live, retained, edges, deps, required, slot, part, used, hist>>

ReleaseSession(s) ==
    /\ <<s, "R">> \in edges /\ acq[s].pc \in {"idle", "skipped", "done"}
    /\ edges' = edges \ {<<s, "R">>}
    /\ count' = Move(count, {"R"}, {})
    /\ acq' = [acq EXCEPT ![s].pc = "done"]
    /\ UNCHANGED <<live, retained, deps, required, owner, slot, part, used, hist>>

DropRetention(r) ==
    /\ r \in retained /\ used.drops < MaxDrops
    /\ used' = [used EXCEPT !.drops = @ + 1]
    /\ retained' = retained \ {r}
    /\ count' = Move(count, {r}, {})
    /\ UNCHANGED <<live, edges, deps, required, owner, slot, part, acq, hist>>

\* Collection follows zero ownership. A collected receiver retires its slot.
Collect(r) ==
    LET out == {d \in Rows : <<r, d>> \in deps}
        retire == r = "R" /\ slot # None
        freed == IF retire THEN Released(slot, 1, 0) ELSE {} IN
    /\ live[r] /\ count[r] = 0
    /\ live' = [live EXCEPT ![r] = FALSE]
    /\ deps' = {e \in deps : e[1] # r}
    /\ owner' = IF retire THEN [owner EXCEPT ![slot] = AfterEnd(slot, 1, 0)] ELSE owner
    /\ slot' = IF retire THEN None ELSE slot
    /\ count' = Move(Move(count, out, {}), freed, {})
    /\ UNCHANGED <<retained, edges, required, part, acq, used, hist>>

Next ==
    \/ \E o \in Owners, d \in DepChoices : PublishSlot(o, d)
    \/ \E s \in Sessions : Lookup(s) \/ StartAcquire(s) \/ Install(s) \/ Settle(s) \/ CancelAcquire(s) \/ ReleaseSession(s)
    \/ \E r \in Rows : DropRetention(r) \/ Collect(r)

Spec == Init /\ [][Next]_vars

-----------------------------------------------------------------------------
TypeOK ==
    /\ \A r \in Rows : count[r] \in 0..8
    /\ slot \in Owners \cup {None}
    /\ part \in {"Pending", "Installed", "Complete"}
    /\ \A s \in Sessions : acq[s].pc \in {"idle", "selected", "skipped", "installed", "done"}

\* Ownership recounted from what actually holds each row.
Holders(r) ==
    Cardinality({e \in edges : e[2] = r}) + (IF r \in retained THEN 1 ELSE 0)
    + Cardinality({e \in deps : e[2] = r}) + Cardinality({o \in Owners : owner[o].alive /\ r \in owner[o].deps})
EveryHoldHasALiveOwner == \A r \in Rows : live[r] => count[r] = Holders(r)

\* Nothing that is alive names a collected row.
NoReferenceToCollected ==
    /\ \A e \in deps : live[e[1]] /\ live[e[2]]
    /\ \A e \in edges : live[e[2]]
    /\ \A o \in Owners : owner[o].alive => \A x \in owner[o].deps : live[x]

\* An owner lives exactly as long as a slot or an acquisition holds it, and
\* every acquisition's owner is alive.
OwnerLivesWhileHeld ==
    /\ \A o \in Owners : owner[o].alive <=> owner[o].slots + owner[o].holds > 0
    /\ \A o \in Owners : owner[o].holds = Cardinality({s \in Sessions : acq[s].pc = "selected" /\ acq[s].owner = o})
    /\ \A o \in Owners : owner[o].slots = (IF slot = o THEN 1 ELSE 0)
    /\ \A s \in Sessions : acq[s].pc = "selected" => owner[acq[s].owner].alive

\* The stored own requirement is the closure over dependency edges, and no
\* offer edge enters it at any depth.
OwnRequirementIsDirectClosure == \A r \in Rows : live[r] => required[r] = DirectRequired(r)

\* A session that satisfies the receiver's own requirement is never refused
\* the ordinary hit because of what an offer names.
OrdinaryHitNotGatedByOffers == ~hist.wronglyRefused

\* No offer owner names a row that reaches its receiver.
NoOwnershipCycle == \A o \in Owners : owner[o].alive => \A x \in owner[o].deps : "R" \notin DirectClosure(x)

\* Once the part is complete nothing of the offer machinery remains.
SettledLeavesNoOffer == part = "Complete" => slot = None /\ \A o \in Owners : ~owner[o].alive \/ owner[o].holds > 0
SettledAndIdleLeavesNoOwner ==
    (part = "Complete" /\ \A s \in Sessions : acq[s].pc # "selected") => \A o \in Owners : ~owner[o].alive

-----------------------------------------------------------------------------
\* Reachability probes: each is checked as an invariant and must be VIOLATED.
WitnessOldAcquisitionSurvivesReplacement ==
    ~(\E s \in Sessions : acq[s].pc = "selected" /\ slot # None /\ acq[s].owner # slot /\ owner[acq[s].owner].alive)
WitnessUnauthorizedHitOfferSkipped ==
    ~(<<"s2", "R">> \in edges /\ acq["s2"].pc = "skipped" /\ slot # None)
WitnessOfferRowOutlivesItsRetention ==
    ~("S" \notin retained /\ live["S"] /\ count["S"] > 0 /\ <<"R", "S">> \notin deps)
=============================================================================
