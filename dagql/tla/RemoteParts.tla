--------------------------- MODULE RemoteParts ---------------------------
(* Remote-cache part acquisition above the snapshot store and beside the   *)
(* cache kernel. A separate module, like SnapshotChain: CacheLifecycle and *)
(* its configurations are untouched by it.                                 *)
(*                                                                         *)
(* One imported receiver row has two output parts, fs and meta, and one    *)
(* Lazy evaluation group whose write set is both. fs has two byte routes:  *)
(* a Ready local donor and an offered chain to download. meta has none and *)
(* is only ever produced by the group's operation. Each Task is one        *)
(* demandPart for one part.                                                *)
(*                                                                         *)
(* Modeled, as the code is after batches 4 to 6 (dagql/cache_part_*.go):   *)
(*   - output phase Pending / Installed / Complete, one way, separate from  *)
(*     the group phase Open / Preparing / Running / Evaluated and from     *)
(*     each task's own result;                                             *)
(*   - writer permits (TryAcquire, TryAcquireForDecision), the drain a     *)
(*     decision records at PrepareOriginal, the final source check and     *)
(*     BeginOriginal's seal; any gate, offer or donor change clears a      *)
(*     task's standing check, which is what the revision compare does;     *)
(*   - Commit's revalidation of its source, the permit and source release  *)
(*     before owner synchronization, a synchronization that can fail and   *)
(*     leave the output Installed with its bookkeeping owed, and a joiner  *)
(*     that pays it (joinPartInstallation);                                *)
(*   - the operation publishing only the write-set parts still Pending;    *)
(*   - offers accepted before the seal and refused after it, chain         *)
(*     addresses collapsed to usable / expired / bad with one renewal      *)
(*     episode per task, and source exhaustion;                            *)
(*   - cancellation at every wait.                                         *)
(*                                                                         *)
(* Abstracted away, and owed to Go tests: session lookup and which         *)
(* equivalent serves a read, publication and the e-graph, requirement      *)
(* sets (see RemoteOwners), the mailbox and its timer, HTTP, bytes,        *)
(* leases and real GC, persisted decode, and the reselect progress rule,   *)
(* which is a diagnostic over counters this model does not carry. The scan *)
(* correction appears as Scan's guard: a row that is being published is    *)
(* not scanned, it is scanned again.                                       *)
(*                                                                         *)
(* Fault selects one deliberate break; "none" is the code. Bounds limit    *)
(* external events (cancellations, offers, failed synchronizations, the    *)
(* donor leaving), never an internal cleanup step.                         *)
EXTENDS Naturals, FiniteSets, TLC

CONSTANTS Tasks, MaxCancels, MaxOffers, MaxSyncFailures, DonorCanLeave, Fault

Parts == {"fs", "meta"}
Addrs == {"usable", "expired", "bad"}
NoTask == 0

VARIABLES out, grp, writers, donor, offer, task, used, hist
vars == <<out, grp, writers, donor, offer, task, used, hist>>

IdleTask == [pc |-> "idle", part |-> "fs", src |-> "none", decision |-> FALSE,
             exhausted |-> FALSE, renewed |-> FALSE, drain |-> {}, checkOK |-> FALSE,
             res |-> "none"]

Init ==
    /\ out = [p \in Parts |-> [phase |-> "Pending", by |-> NoTask, via |-> "none",
                               decision |-> FALSE, owed |-> FALSE, lateOffer |-> FALSE]]
    /\ grp = [phase |-> "Open", task |-> NoTask, ended |-> "none"]
    /\ writers = {}
    /\ donor = [up |-> TRUE, holds |-> 0]
    /\ offer = [present |-> FALSE, addr |-> "usable", afterSeal |-> FALSE, duringPreparing |-> FALSE]
    /\ task = [t \in Tasks |-> IdleTask]
    /\ used = [cancels |-> 0, offers |-> 0, syncFails |-> 0]
    /\ hist = [installs |-> [p \in Parts |-> 0]]

Sealed == grp.phase \in {"Running", "Evaluated"}
WritersOn(p) == {w \in writers : w[2] = p}
ClearChecks(ts) == [t \in Tasks |-> IF t \in ts THEN task[t] ELSE [task[t] EXCEPT !.checkOK = FALSE]]
Publishing == \E t \in Tasks : task[t].pc \in {"commit", "publish"}

\* Source release: the Ready donor's hold. A chain holds its offer owner,
\* which RemoteOwners models.
ReleaseSrc(t) == IF task[t].src = "ready" THEN [donor EXCEPT !.holds = @ - 1] ELSE donor
ReleasePermit(t) == {w \in writers : w[1] # t}

Set(t, r) == [task EXCEPT ![t] = r]

Start(t) ==
    /\ task[t].pc = "idle"
    /\ \E p \in Parts : task' = Set(t, [IdleTask EXCEPT !.pc = "probe", !.part = p])
    /\ UNCHANGED <<out, grp, writers, donor, offer, used, hist>>

Probe(t) ==
    LET p == task[t].part IN
    /\ task[t].pc = "probe"
    /\ task' = Set(t, [task[t] EXCEPT
           !.pc = CASE out[p].phase = "Complete" -> "done"
                    [] out[p].phase = "Installed" -> "joinInstall"
                    [] OTHER -> "scan",
           !.res = IF out[p].phase = "Complete" THEN "ok" ELSE @])
    /\ UNCHANGED <<out, grp, writers, donor, offer, used, hist>>

\* joinPartInstallation: wait for the installer, or pay the bookkeeping it
\* left owed.
JoinInstall(t) ==
    LET p == task[t].part IN
    /\ task[t].pc = "joinInstall"
    /\ \/ /\ out[p].phase = "Complete"
          /\ task' = Set(t, [task[t] EXCEPT !.pc = "probe"])
          /\ UNCHANGED out
       \/ /\ out[p].phase = "Installed" /\ out[p].owed
          /\ out' = [out EXCEPT ![p].owed = FALSE]
          /\ task' = Set(t, [task[t] EXCEPT !.pc = "finish", !.src = "none", !.decision = FALSE])
    /\ UNCHANGED <<grp, writers, donor, offer, used, hist>>

ChainOpen(t) == offer.present /\ ~task[t].exhausted

Scan(t) ==
    LET p == task[t].part IN
    /\ task[t].pc = "scan"
    /\ ~Publishing
    /\ IF p = "fs" /\ donor.up
       THEN /\ donor' = [donor EXCEPT !.holds = @ + 1]
            /\ task' = Set(t, [task[t] EXCEPT !.pc = "acquire", !.src = "ready"])
       ELSE IF p = "fs" /\ ChainOpen(t)
       THEN /\ task' = Set(t, [task[t] EXCEPT !.pc = "acquire", !.src = "chain"])
            /\ UNCHANGED donor
       ELSE /\ task' = Set(t, [task[t] EXCEPT !.pc = "decide", !.src = "none"])
            /\ UNCHANGED donor
    /\ UNCHANGED <<out, grp, writers, offer, used, hist>>

\* TryAcquire.
Acquire(t) ==
    LET p == task[t].part
        seal == Sealed /\ Fault # "AcceptAfterSeal" IN
    /\ task[t].pc = "acquire"
    /\ IF out[p].phase # "Pending"
       THEN /\ task' = Set(t, [task[t] EXCEPT !.pc = "probe", !.src = "none"])
            /\ donor' = ReleaseSrc(t)
            /\ UNCHANGED writers
       ELSE IF grp.task # t /\ (seal \/ grp.phase = "Preparing")
       THEN /\ task' = Set(t, [task[t] EXCEPT !.pc = "joinGroup", !.src = "none"])
            /\ donor' = ReleaseSrc(t)
            /\ UNCHANGED writers
       ELSE IF WritersOn(p) # {}
       THEN /\ task' = Set(t, [task[t] EXCEPT !.pc = "scan", !.src = "none"])
            /\ donor' = ReleaseSrc(t)
            /\ UNCHANGED writers
       ELSE /\ writers' = writers \cup {<<t, p>>}
            /\ task' = [ClearChecks({t}) EXCEPT ![t] = [task[t] EXCEPT
                   !.pc = IF task[t].src = "chain" THEN "download" ELSE "commit"]]
            /\ UNCHANGED donor
    /\ UNCHANGED <<out, grp, offer, used, hist>>

\* installChainPart's content phase. One renewal episode per task.
GiveUpSource(t, exhausted) ==
    /\ writers' = ReleasePermit(t)
    /\ task' = Set(t, [task[t] EXCEPT !.pc = IF task[t].decision THEN "check" ELSE "scan",
                                      !.src = "none", !.exhausted = exhausted])

Download(t) ==
    /\ task[t].pc = "download"
    /\ \/ /\ ~offer.present
          /\ GiveUpSource(t, task[t].exhausted)
          /\ UNCHANGED offer
       \/ /\ offer.present /\ offer.addr = "usable"
          /\ task' = Set(t, [task[t] EXCEPT !.pc = "commit"])
          /\ UNCHANGED <<writers, offer>>
       \/ /\ offer.present /\ offer.addr = "bad"
          /\ GiveUpSource(t, TRUE)
          /\ UNCHANGED offer
       \/ /\ offer.present /\ offer.addr = "expired" /\ ~task[t].renewed
          /\ \/ /\ offer' = [offer EXCEPT !.addr = "usable"]
                /\ task' = Set(t, [task[t] EXCEPT !.renewed = TRUE])
                /\ UNCHANGED writers
             \/ /\ GiveUpSource(t, TRUE)
                /\ UNCHANGED offer
       \/ /\ offer.present /\ offer.addr = "expired" /\ task[t].renewed
          /\ GiveUpSource(t, TRUE)
          /\ UNCHANGED offer
    /\ UNCHANGED <<out, grp, donor, used, hist>>

\* CommitReadyPart: the source is revalidated; the permit and the source hold
\* end before owner synchronization starts.
Commit(t) ==
    LET p == task[t].part
        valid == IF task[t].src = "ready" THEN donor.up ELSE offer.present IN
    /\ task[t].pc = "commit"
    /\ writers' = ReleasePermit(t)
    /\ donor' = ReleaseSrc(t)
    /\ IF valid /\ out[p].phase = "Pending"
       THEN /\ out' = [out EXCEPT ![p] = [phase |-> "Installed", by |-> t, via |-> task[t].src,
                         decision |-> task[t].decision, owed |-> FALSE,
                         lateOffer |-> task[t].src = "chain" /\ offer.duringPreparing]]
            /\ hist' = [hist EXCEPT !.installs[p] = @ + 1]
            /\ task' = Set(t, [task[t] EXCEPT !.pc = "finish", !.src = "none"])
       ELSE /\ task' = Set(t, [task[t] EXCEPT !.pc = IF task[t].decision THEN "check" ELSE "scan", !.src = "none"])
            /\ UNCHANGED <<out, hist>>
    /\ UNCHANGED <<grp, offer, used>>

\* Owner synchronization and settlement. A decision that installed from a
\* source ends its group without running the operation.
Finish(t) ==
    LET p == task[t].part
        owner == grp.task = t /\ grp.phase = "Preparing" IN
    /\ task[t].pc = "finish"
    /\ \/ /\ out' = [out EXCEPT ![p].phase = "Complete"]
          /\ task' = Set(t, [task[t] EXCEPT !.pc = "done", !.res = "ok"])
          /\ grp' = IF owner THEN [phase |-> "Open", task |-> NoTask, ended |-> "ok"] ELSE grp
          /\ UNCHANGED used
       \/ /\ used.syncFails < MaxSyncFailures
          /\ used' = [used EXCEPT !.syncFails = @ + 1]
          /\ out' = [out EXCEPT ![p].owed = TRUE]
          /\ task' = Set(t, [task[t] EXCEPT !.pc = "done", !.res = "err"])
          /\ grp' = IF owner THEN [phase |-> "Open", task |-> NoTask, ended |-> "err"] ELSE grp
    /\ UNCHANGED <<writers, donor, offer, hist>>

\* joinLazyEvaluation. The joiner learns only that the group's task ended; it
\* probes its own part again. The fault takes that task's success for its own:
\* a decision that installed fs from a source succeeds without the operation
\* ever producing meta.
JoinGroup(t) ==
    /\ task[t].pc = "joinGroup"
    /\ grp.phase \in {"Open", "Evaluated"}
    /\ task' = Set(t, IF Fault = "CertifySibling" /\ grp.ended = "ok"
                      THEN [task[t] EXCEPT !.pc = "done", !.res = "ok"]
                      ELSE [task[t] EXCEPT !.pc = "probe"])
    /\ UNCHANGED <<out, grp, writers, donor, offer, used, hist>>

\* PrepareOriginal.
Decide(t) ==
    /\ task[t].pc = "decide"
    /\ CASE grp.phase = "Open" ->
              /\ grp' = [phase |-> "Preparing", task |-> t, ended |-> "none"]
              /\ task' = [ClearChecks({t}) EXCEPT ![t] = [task[t] EXCEPT !.pc = "drain", !.decision = TRUE,
                              !.drain = {w[1] : w \in writers}]]
         [] grp.phase = "Evaluated" ->
              \* "operation consumed without required output"
              /\ task' = Set(t, [task[t] EXCEPT !.pc = "done", !.res = "err"])
              /\ UNCHANGED grp
         [] OTHER ->
              /\ task' = Set(t, [task[t] EXCEPT !.pc = "joinGroup"])
              /\ UNCHANGED grp
    /\ UNCHANGED <<out, writers, donor, offer, used, hist>>

Drain(t) ==
    /\ task[t].pc = "drain"
    /\ \A w \in writers : w[1] \notin task[t].drain
    /\ task' = Set(t, [task[t] EXCEPT !.pc = "check", !.drain = {}])
    /\ UNCHANGED <<out, grp, writers, donor, offer, used, hist>>

\* CheckPartSources, and TryAcquireForDecision when a source turned up.
Check(t) ==
    LET p == task[t].part IN
    /\ task[t].pc = "check"
    /\ ~Publishing
    /\ IF out[p].phase # "Pending"
       THEN \* GateAlreadyInstalled: the decision ends without the operation.
            /\ task' = Set(t, [task[t] EXCEPT !.pc = "endDecision"])
            /\ UNCHANGED <<writers, donor>>
       ELSE IF p = "fs" /\ (donor.up \/ ChainOpen(t))
       THEN /\ writers = {}
            /\ writers' = {<<t, p>>}
            /\ donor' = IF donor.up THEN [donor EXCEPT !.holds = @ + 1] ELSE donor
            /\ task' = [ClearChecks({t}) EXCEPT ![t] = [task[t] EXCEPT
                   !.src = IF donor.up THEN "ready" ELSE "chain",
                   !.pc = IF donor.up THEN "commit" ELSE "download"]]
       ELSE /\ task' = Set(t, [task[t] EXCEPT !.pc = "begin", !.checkOK = TRUE])
            /\ UNCHANGED <<writers, donor>>
    /\ UNCHANGED <<out, grp, offer, used, hist>>

EndDecision(t) ==
    /\ task[t].pc = "endDecision"
    /\ grp' = IF grp.task = t /\ grp.phase = "Preparing" THEN [phase |-> "Open", task |-> NoTask, ended |-> "ok"] ELSE grp
    /\ task' = Set(t, [task[t] EXCEPT !.pc = "probe", !.decision = FALSE])
    /\ UNCHANGED <<out, writers, donor, offer, used, hist>>

\* BeginOriginal: the seal. It needs the standing check and a drained gate.
Begin(t) ==
    /\ task[t].pc = "begin"
    /\ IF task[t].checkOK /\ writers = {}
       THEN /\ grp' = [grp EXCEPT !.phase = "Running"]
            /\ task' = Set(t, [task[t] EXCEPT !.pc = "publish"])
       ELSE /\ task' = Set(t, [task[t] EXCEPT !.pc = "check", !.checkOK = FALSE])
            /\ UNCHANGED grp
    /\ UNCHANGED <<out, writers, donor, offer, used, hist>>

\* The operation ran; publishEvaluatedParts installs what is still Pending.
Publish(t) ==
    /\ task[t].pc = "publish"
    /\ out' = [p \in Parts |-> IF out[p].phase = "Pending"
                 THEN [phase |-> "Installed", by |-> t, via |-> "lazy", decision |-> TRUE, owed |-> FALSE, lateOffer |-> FALSE]
                 ELSE out[p]]
    /\ hist' = [hist EXCEPT !.installs = [p \in Parts |-> IF out[p].phase = "Pending" THEN @[p] + 1 ELSE @[p]]]
    /\ task' = Set(t, [task[t] EXCEPT !.pc = "sync"])
    /\ UNCHANGED <<grp, writers, donor, offer, used>>

Mine(t) == {p \in Parts : out[p].by = t /\ out[p].via = "lazy" /\ out[p].phase = "Installed"}

Sync(t) ==
    /\ task[t].pc = "sync"
    /\ \/ /\ out' = [p \in Parts |-> IF p \in Mine(t) THEN [out[p] EXCEPT !.phase = "Complete"] ELSE out[p]]
          /\ grp' = [phase |-> "Evaluated", task |-> NoTask, ended |-> "ok"]
          \* The demanded part may have been installed by another route; the
          \* demand loop probes it again.
          /\ task' = Set(t, [task[t] EXCEPT !.pc = "probe", !.decision = FALSE])
          /\ UNCHANGED used
       \/ /\ used.syncFails < MaxSyncFailures
          /\ used' = [used EXCEPT !.syncFails = @ + 1]
          /\ out' = [p \in Parts |-> IF p \in Mine(t) THEN [out[p] EXCEPT !.owed = TRUE] ELSE out[p]]
          /\ grp' = [phase |-> "Open", task |-> NoTask, ended |-> "err"]
          /\ task' = Set(t, [task[t] EXCEPT !.pc = "done", !.res = "err", !.decision = FALSE])
    /\ UNCHANGED <<writers, donor, offer, hist>>

Cancel(t) ==
    /\ task[t].pc \in {"joinInstall", "joinGroup", "drain", "download", "check", "scan"}
    /\ used.cancels < MaxCancels
    /\ used' = [used EXCEPT !.cancels = @ + 1]
    /\ writers' = ReleasePermit(t)
    /\ grp' = IF grp.task = t /\ grp.phase = "Preparing" THEN [phase |-> "Open", task |-> NoTask, ended |-> "err"] ELSE grp
    /\ task' = Set(t, [task[t] EXCEPT !.pc = "done", !.res = "canceled", !.src = "none", !.decision = FALSE])
    /\ UNCHANGED <<out, donor, offer, hist>>

\* An offer for fs arrives. Before the seal it is attached and invalidates
\* every standing source check; after the seal it is answered
\* ExecutionStarted and nothing is attached.
AcceptOffer ==
    /\ used.offers < MaxOffers
    /\ out["fs"].phase = "Pending"
    /\ used' = [used EXCEPT !.offers = @ + 1]
    /\ IF Sealed /\ Fault # "AcceptAfterSeal"
       THEN UNCHANGED <<offer, task>>
       ELSE /\ \E a \in Addrs : offer' = [present |-> TRUE, addr |-> a, afterSeal |-> Sealed,
                                          duringPreparing |-> grp.phase = "Preparing"]
            /\ task' = [t \in Tasks |-> [task[t] EXCEPT !.checkOK = FALSE, !.exhausted = FALSE, !.renewed = FALSE]]
    /\ UNCHANGED <<out, grp, writers, donor, hist>>

\* The donor's last owner goes. A held donor cannot be collected.
DonorLeave ==
    /\ DonorCanLeave /\ donor.up /\ donor.holds = 0
    /\ donor' = [donor EXCEPT !.up = FALSE]
    /\ task' = ClearChecks({})
    /\ UNCHANGED <<out, grp, writers, offer, used, hist>>

Next ==
    \/ \E t \in Tasks : Start(t) \/ Probe(t) \/ JoinInstall(t) \/ Scan(t) \/ Acquire(t) \/ Download(t)
                       \/ Commit(t) \/ Finish(t) \/ JoinGroup(t) \/ Decide(t) \/ Drain(t) \/ Check(t)
                       \/ EndDecision(t) \/ Begin(t) \/ Publish(t) \/ Sync(t) \/ Cancel(t)
    \/ AcceptOffer \/ DonorLeave

Spec == Init /\ [][Next]_vars

-----------------------------------------------------------------------------
Pcs == {"idle", "probe", "joinInstall", "scan", "acquire", "download", "commit", "finish", "joinGroup",
        "decide", "drain", "check", "endDecision", "begin", "publish", "sync", "done"}

TypeOK ==
    /\ \A p \in Parts : out[p].phase \in {"Pending", "Installed", "Complete"} /\ out[p].by \in Tasks \cup {NoTask}
    /\ grp.phase \in {"Open", "Preparing", "Running", "Evaluated"} /\ grp.task \in Tasks \cup {NoTask}
    /\ writers \subseteq Tasks \X Parts
    /\ donor.holds \in 0..Cardinality(Tasks)
    /\ \A t \in Tasks : task[t].pc \in Pcs /\ task[t].res \in {"none", "ok", "err", "canceled"}

\* A demand that succeeded was served a completed output of its own part.
ServedOutputIsComplete == \A t \in Tasks : task[t].res = "ok" => out[task[t].part].phase = "Complete"

\* An output is installed at most once: nothing overwrites a final output.
FinalOutputNeverOverwritten == \A p \in Parts : hist.installs[p] <= 1

\* One writer per address, and none beside a sealed operation.
WriterExclusive ==
    /\ \A p \in Parts : Cardinality(WritersOn(p)) <= 1
    /\ (grp.phase = "Running" /\ Fault # "AcceptAfterSeal") => writers = {}

\* An offer that arrives after the seal is never attached, so it cannot publish.
OfferAfterSealCannotPublish == ~offer.afterSeal /\ \A p \in Parts : ~(out[p].via = "chain" /\ offer.afterSeal)

\* Permits, source holds and group ownership belong to a task that is still
\* at the step that needs them, so every one of them ends.
HoldsHaveLiveOwners ==
    /\ \A w \in writers : task[w[1]].pc \in {"download", "commit"}
    /\ donor.holds = Cardinality({t \in Tasks : task[t].src = "ready"})
    /\ grp.phase \in {"Preparing", "Running"} => task[grp.task].pc \in
           {"drain", "check", "endDecision", "begin", "publish", "sync", "download", "commit", "finish"}

Quiescent == \A t \in Tasks : task[t].pc = "done"
QuiescentIsClean == Quiescent => writers = {} /\ donor.holds = 0 /\ grp.phase \in {"Open", "Evaluated"}

\* Owed bookkeeping is always attached to an installed, unsettled output.
OwedMeansInstalled == \A p \in Parts : out[p].owed => out[p].phase = "Installed"

-----------------------------------------------------------------------------
\* Reachability probes: each is checked as an invariant and must be VIOLATED.
WitnessDownloadedFsPendingMeta ==
    ~(out["fs"].via = "chain" /\ out["fs"].phase # "Pending" /\ out["meta"].phase = "Pending")
WitnessLateOfferWinsDuringPreparing ==
    ~(out["fs"].lateOffer /\ out["fs"].decision /\ out["fs"].phase = "Complete")
WitnessFsAcquiredBesideProducedMeta ==
    ~(out["fs"].via \in {"ready", "chain"} /\ out["meta"].via = "lazy" /\ \A p \in Parts : out[p].phase = "Complete")
=============================================================================
