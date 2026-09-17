------------------------- MODULE RemoteCheckpoint -------------------------
(* An imported receiver's parts across one checkpoint and one restart,     *)
(* beside the other Remote modules and separate from CacheLifecycle. The   *)
(* receiver R has two parts. fs can be installed from a local donor's      *)
(* snapshot; mnt has a pending offer, whose owner holds one dependency row *)
(* K, and otherwise comes from R's saved producer.                         *)
(*                                                                         *)
(* Modeled (dagql/cache_part_install.go, cache_part_task.go,               *)
(* cache_persistence_*.go, engine/server/server.go's boot order):          *)
(*   - an install records the part's desired role at once and pins its     *)
(*     snapshot; owner synchronization later applies the role as an owner  *)
(*     lease and only then drops the pin. It can fail, leaving the part    *)
(*     Installed, desired but not applied, with bookkeeping owed; the      *)
(*     retry synchronizes only, it never runs the producer again;          *)
(*   - a snapshot survives GC while the donor's owner, the receiver's      *)
(*     applied lease or a pin protects it; pins are transfer leases and    *)
(*     outlive the process;                                                *)
(*   - the checkpoint saves the complete desired roles, the pending offer  *)
(*     and the producer. Restart reverts to the checkpoint, keeps only the *)
(*     owner leases the checkpoint names, attaches an owner lease for      *)
(*     every saved role, and only after that releases the old pins. A      *)
(*     saved role whose snapshot is gone is a whole-cache reset, never a   *)
(*     repair;                                                             *)
(*   - a receiver that loses its last ordinary owner is collected, and its *)
(*     leases, pins and offer end with it.                                 *)
(*                                                                         *)
(* Abstracted away: session lookup, publication, the part gate and byte    *)
(* routes (RemoteParts), offer owners beyond one hold (RemoteOwners), the  *)
(* sharing pass (RemoteSharing), the persistence database, bytes. Bounds   *)
(* limit external events only: failed synchronizations, one clean restart. *)
(*                                                                         *)
(* Assumed, and discharged by CacheLifecycle's own invariants: a flush is  *)
(* a clean capture with referential integrity (FlushCleanCapture,          *)
(* FlushReferentialIntegrity) and holds are exact (OwnershipExact). Named  *)
(* gap, with no CacheLifecycle invariant behind it: that a process which   *)
(* dies without its checkpoint boots into a whole-cache reset; Go's reset  *)
(* tests and native TestEncodedRestart/LocalRestoreReset carry it.         *)
EXTENDS Naturals, FiniteSets, TLC

CONSTANTS MaxSyncFailures, Fault

Parts == {"fs", "mnt"}
NoSave == [valid |-> FALSE, roles |-> {}, complete |-> {}, offer |-> FALSE, rLive |-> FALSE]

VARIABLES epoch, boot, rLive, rOwned, donorOwned, phase, desired, applied, owed, pins, alive,
          offer, kLive, prodRuns, saved, used, hist
vars == <<epoch, boot, rLive, rOwned, donorOwned, phase, desired, applied, owed, pins, alive,
          offer, kLive, prodRuns, saved, used, hist>>

Init ==
    /\ epoch = 1 /\ boot = "up"
    /\ rLive = TRUE /\ rOwned = TRUE /\ donorOwned = TRUE
    /\ phase = [p \in Parts |-> "Pending"]
    /\ desired = {} /\ applied = {} /\ pins = {}
    /\ owed = [p \in Parts |-> FALSE]
    \* fs's snapshot exists in the donor; mnt's does not exist yet.
    /\ alive = [p \in Parts |-> p = "fs"]
    /\ offer = TRUE /\ kLive = TRUE
    /\ prodRuns = 0
    /\ saved = NoSave
    /\ used = [syncFails |-> 0]
    /\ hist = [unsyncedAtFlush |-> {}, desiredAtFlush |-> {}, reset |-> FALSE, syncFailed |-> FALSE,
               producerAfterRestart |-> FALSE]

Up == boot = "up" /\ rLive

Protected(p) == (p = "fs" /\ donorOwned) \/ (rLive /\ p \in applied) \/ p \in pins

\* fs from the donor's snapshot: desired at once, pinned until synchronized.
InstallFromDonor ==
    /\ Up /\ phase["fs"] = "Pending" /\ alive["fs"]
    /\ phase' = [phase EXCEPT !["fs"] = "Installed"]
    /\ desired' = desired \cup {"fs"}
    /\ pins' = pins \cup {"fs"}
    /\ UNCHANGED <<epoch, boot, rLive, rOwned, donorOwned, applied, owed, alive, offer, kLive, prodRuns, saved, used, hist>>

\* mnt from its offered chain: the download creates the snapshot.
InstallFromOffer ==
    /\ Up /\ phase["mnt"] = "Pending" /\ offer /\ kLive
    /\ phase' = [phase EXCEPT !["mnt"] = "Installed"]
    /\ desired' = desired \cup {"mnt"}
    /\ pins' = pins \cup {"mnt"}
    /\ alive' = [alive EXCEPT !["mnt"] = TRUE]
    /\ UNCHANGED <<epoch, boot, rLive, rOwned, donorOwned, applied, owed, offer, kLive, prodRuns, saved, used, hist>>

\* No source: the saved producer runs and publishes what is still pending.
RunProducer ==
    LET todo == {p \in Parts : phase[p] = "Pending"} IN
    /\ Up /\ todo # {} /\ (~offer \/ phase["mnt"] # "Pending") /\ ~(alive["fs"] /\ phase["fs"] = "Pending")
    /\ prodRuns' = prodRuns + 1
    /\ phase' = [p \in Parts |-> IF p \in todo THEN "Installed" ELSE phase[p]]
    /\ desired' = desired \cup todo
    /\ pins' = pins \cup todo
    /\ alive' = [p \in Parts |-> alive[p] \/ p \in todo]
    /\ hist' = [hist EXCEPT !.producerAfterRestart = @ \/ epoch = 2]
    /\ UNCHANGED <<epoch, boot, rLive, rOwned, donorOwned, applied, owed, offer, kLive, saved, used>>

\* Owner synchronization and settlement. The redundant offer retires when its
\* output settles, which ends its owner's hold on K.
Settle(p) ==
    /\ phase' = [phase EXCEPT ![p] = "Complete"]
    /\ applied' = applied \cup {p}
    /\ pins' = pins \ {p}
    /\ owed' = [owed EXCEPT ![p] = FALSE]
    /\ offer' = IF p = "mnt" THEN FALSE ELSE offer

Sync(p) ==
    /\ Up /\ phase[p] = "Installed" /\ ~owed[p]
    /\ \/ /\ Settle(p)
          /\ UNCHANGED <<used, hist>>
       \/ /\ used.syncFails < MaxSyncFailures
          /\ used' = [used EXCEPT !.syncFails = @ + 1]
          /\ owed' = [owed EXCEPT ![p] = TRUE]
          /\ hist' = [hist EXCEPT !.syncFailed = TRUE]
          /\ UNCHANGED <<phase, applied, pins, offer>>
    /\ UNCHANGED <<epoch, boot, rLive, rOwned, donorOwned, desired, alive, kLive, prodRuns, saved>>

\* The bookkeeping-only retry.
Retry(p) ==
    /\ Up /\ owed[p]
    /\ Settle(p)
    /\ prodRuns' = IF Fault = "RepeatProducer" THEN prodRuns + 1 ELSE prodRuns
    /\ UNCHANGED <<epoch, boot, rLive, rOwned, donorOwned, desired, alive, kLive, saved, used, hist>>

DropDonorOwner ==
    /\ donorOwned /\ donorOwned' = FALSE
    /\ UNCHANGED <<epoch, boot, rLive, rOwned, phase, desired, applied, owed, pins, alive, offer, kLive, prodRuns, saved, used, hist>>

\* The receiver's last ordinary owner goes: the row is collected, and its
\* leases, its pins and its offer go with it, once.
DropReceiverOwner ==
    /\ boot = "up" /\ rLive /\ rOwned
    /\ rOwned' = FALSE /\ rLive' = FALSE
    /\ applied' = {} /\ pins' = {} /\ desired' = {}
    /\ owed' = [p \in Parts |-> FALSE]
    /\ offer' = FALSE
    /\ UNCHANGED <<epoch, boot, donorOwned, phase, alive, kLive, prodRuns, saved, used, hist>>

\* K is held by the offer's owner; nothing else owns it here.
CollectK ==
    /\ kLive /\ ~offer /\ ~(saved.valid /\ saved.offer /\ epoch = 1)
    /\ kLive' = FALSE
    /\ UNCHANGED <<epoch, boot, rLive, rOwned, donorOwned, phase, desired, applied, owed, pins, alive, offer, prodRuns, saved, used, hist>>

GC(p) ==
    /\ alive[p] /\ ~Protected(p)
    /\ alive' = [alive EXCEPT ![p] = FALSE]
    /\ UNCHANGED <<epoch, boot, rLive, rOwned, donorOwned, phase, desired, applied, owed, pins, offer, kLive, prodRuns, saved, used, hist>>

\* The clean shutdown's checkpoint: complete desired roles, not only the
\* applied ones. Nothing but GC happens between it and the restart; a process
\* that dies without it is a whole-cache reset in Go and is not modeled.
Flush ==
    /\ epoch = 1 /\ boot = "up" /\ ~saved.valid
    /\ boot' = "down"
    /\ saved' = [valid |-> TRUE, rLive |-> rLive,
                 roles |-> IF Fault = "RestoreAppliedOnly" THEN applied ELSE desired,
                 complete |-> {p \in Parts : phase[p] = "Complete"},
                 offer |-> offer]
    /\ hist' = [hist EXCEPT !.unsyncedAtFlush = desired \ applied, !.desiredAtFlush = desired]
    /\ UNCHANGED <<epoch, rLive, rOwned, donorOwned, phase, desired, applied, owed, pins, alive, offer, kLive, prodRuns, used>>

\* The process ends and starts again from the checkpoint. Owner leases the
\* checkpoint does not name are stale and deleted; pins survive as they are.
Restart ==
    /\ epoch = 1 /\ boot = "down" /\ saved.valid
    /\ epoch' = 2
    /\ boot' = IF Fault = "UnpinBeforeAttach" THEN "unpin" ELSE "attach"
    /\ rLive' = saved.rLive /\ rOwned' = saved.rLive
    /\ desired' = saved.roles
    /\ applied' = applied \cap saved.roles
    /\ phase' = [p \in Parts |-> IF p \in saved.roles THEN "Installed" ELSE "Pending"]
    /\ owed' = [p \in Parts |-> FALSE]
    /\ offer' = saved.offer /\ saved.rLive
    /\ prodRuns' = 0
    /\ UNCHANGED <<donorOwned, pins, alive, kLive, saved, used, hist>>

\* Boot restores every saved owner. A role whose snapshot is gone cannot be
\* restored: the whole cache is reset, nothing is repaired.
BootAttach ==
    /\ boot = "attach"
    /\ IF rLive /\ \E p \in desired : ~alive[p]
       THEN /\ rLive' = FALSE /\ desired' = {} /\ applied' = {} /\ offer' = FALSE
            /\ hist' = [hist EXCEPT !.reset = TRUE]
            /\ UNCHANGED phase
       ELSE /\ applied' = IF rLive THEN desired ELSE {}
            /\ phase' = [p \in Parts |-> IF rLive /\ p \in desired THEN "Complete" ELSE phase[p]]
            /\ UNCHANGED <<rLive, desired, offer, hist>>
    /\ boot' = IF Fault = "UnpinBeforeAttach" THEN "up" ELSE "unpin"
    /\ UNCHANGED <<epoch, rOwned, donorOwned, owed, pins, alive, kLive, prodRuns, saved, used>>

\* Only then are the previous process's transfer pins released.
BootUnpin ==
    /\ boot = "unpin"
    /\ pins' = {}
    /\ boot' = IF Fault = "UnpinBeforeAttach" THEN "attach" ELSE "up"
    /\ UNCHANGED <<epoch, rLive, rOwned, donorOwned, phase, desired, applied, owed, alive, offer, kLive, prodRuns, saved, used, hist>>

Next ==
    \/ InstallFromDonor \/ InstallFromOffer \/ RunProducer
    \/ \E p \in Parts : Sync(p) \/ Retry(p) \/ GC(p)
    \/ DropDonorOwner \/ DropReceiverOwner \/ CollectK
    \/ Flush \/ Restart \/ BootAttach \/ BootUnpin

Spec == Init /\ [][Next]_vars

-----------------------------------------------------------------------------
TypeOK ==
    /\ epoch \in 1..2 /\ boot \in {"up", "down", "attach", "unpin"}
    /\ \A p \in Parts : phase[p] \in {"Pending", "Installed", "Complete"}
    /\ desired \subseteq Parts /\ applied \subseteq Parts /\ pins \subseteq Parts
    /\ prodRuns \in 0..3

\* Every role the receiver desires has a living snapshot that the receiver
\* itself protects, by its lease or by a pin, in both epochs and during boot.
DesiredRolesStayProtected ==
    rLive => \A p \in desired : alive[p] /\ (p \in applied \/ p \in pins)

\* The checkpoint carries the complete desired roles.
CheckpointHasCompleteDesiredRoles == saved.valid => saved.roles = hist.desiredAtFlush

\* A restart never ends in a reset: nothing the checkpoint names was lost.
RestartNeverResets == ~hist.reset

\* The producer runs at most once in a process; a bookkeeping retry never
\* runs it again.
ProducerRunsOnce == prodRuns <= 1

\* A pending offer's dependency is alive while the offer is.
OfferDependencyLive == offer => kLive

\* An applied role is a desired role, and a complete part has its lease and
\* no pin.
RolesConsistent ==
    /\ rLive => applied \subseteq desired
    /\ (rLive /\ boot = "up") => \A p \in Parts : phase[p] = "Complete" => p \in applied /\ p \notin pins

-----------------------------------------------------------------------------
\* Reachability probes: each is checked as an invariant and must be VIOLATED.
WitnessInstalledDesiredSurvivesEpoch ==
    ~(epoch = 2 /\ boot = "up" /\ rLive /\ \E p \in hist.unsyncedAtFlush : phase[p] = "Complete" /\ p \in applied /\ ~donorOwned)
WitnessFailedFinishLastOwnerCollected == ~(hist.syncFailed /\ ~rLive /\ pins = {} /\ epoch = 1)
WitnessProducerPreservedAcrossRestart == ~(hist.producerAfterRestart /\ \A p \in Parts : phase[p] = "Complete")
WitnessPendingOfferRestored == ~(epoch = 2 /\ boot = "up" /\ offer /\ phase["mnt"] = "Pending")
=============================================================================
