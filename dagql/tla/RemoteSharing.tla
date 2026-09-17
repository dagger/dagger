--------------------------- MODULE RemoteSharing ---------------------------
(* One snapshot sharing pass and its successor, beside RemoteParts and     *)
(* RemoteOwners and like them separate from CacheLifecycle. Three rows of  *)
(* one output class: a complete local donor D, an imported receiver R      *)
(* that lacks both parts fs and mnt, and a bystander member X.             *)
(*                                                                         *)
(* Modeled, as the code is after batch 6 (dagql/cache_snapshot_sharing.go, *)
(* cache_part_install.go):                                                 *)
(*   - a queued cohort holds each member once; the worker takes it, plans  *)
(*     its slots, prepares every slot, then commits them in order, then    *)
(*     releases every member hold, and only then finishes each receipt;    *)
(*   - an encoded receiver's slots are prepared in order from a prefix:    *)
(*     slot two's envelope carries slot one's role and expects the         *)
(*     revision slot one will leave; a receiver that is already decoded    *)
(*     takes one slot per pass, and the step that releases the members     *)
(*     queues the successor cohort before the first decrement, so the      *)
(*     donor is held continuously until the successor pass;                *)
(*   - Commit refuses a slot whose receiver no longer has the expected     *)
(*     revision, which a foreground decode or install causes;              *)
(*   - each preparation pins its donor snapshot; the pin ends when the     *)
(*     slot is refused or its receipt's owner synchronization succeeds. A  *)
(*     failed synchronization leaves the part Installed with bookkeeping   *)
(*     owed and the pin held, and a later demand pays it without pinning,  *)
(*     holding or reading anything again; receipts synchronize             *)
(*     independently;                                                      *)
(*   - the donor's ordinary owner can go at any time; rows carry a stored  *)
(*     ownership count and are collected at zero, and the invariants       *)
(*     recount ownership from the actual holders.                          *)
(*                                                                         *)
(* Abstracted away: the triggers that queue a cohort (the run starts with  *)
(* one queued), donor selection among several equivalents, the decode      *)
(* preflight and Service reconstruction, the part gate inside a slot       *)
(* (RemoteParts; a foreground demand for an address with a prepared slot   *)
(* joins it, so the foreground install here needs the address free),       *)
(* bytes, leases, restart. Bounds limit external events only.              *)
(*                                                                         *)
(* Assumed, and discharged by CacheLifecycle's own invariants: stored      *)
(* counts are exact and never underflow (OwnershipExact, NoUnderflow), a   *)
(* collected row never returns (NoResurrection), and a slot's task is the  *)
(* only attempt on its address (LazyMutualExclusion). Named gap, with no   *)
(* CacheLifecycle invariant behind it: that each slot outcome has exactly  *)
(* one reporter and a pass does not end while a Body owns a preparation    *)
(* (batch 6 amendment A2); batch 6's three cancellation tests carry it.    *)
EXTENDS Naturals, Sequences, FiniteSets, TLC

CONSTANTS MaxForeground, MaxSyncFailures, StartDecoded, Fault

Rows == {"D", "R", "X"}
Parts == <<"fs", "mnt">>
PartSet == {"fs", "mnt"}

VARIABLES live, count, donorOwned, xRetained, form, rev, phase, desired, owed, pins,
          cohort, successor, pass, used, hist
vars == <<live, count, donorOwned, xRetained, form, rev, phase, desired, owed, pins,
          cohort, successor, pass, used, hist>>

Init ==
    /\ live = [r \in Rows |-> TRUE]
    /\ donorOwned = TRUE /\ xRetained = TRUE
    \* one ordinary owner each, plus the queued cohort's hold
    /\ count = [r \in Rows |-> 2]
    /\ form \in (IF StartDecoded THEN {"decoded"} ELSE {"encoded"})
    /\ rev = 1
    /\ phase = [p \in PartSet |-> "Pending"]
    /\ desired = {}
    /\ owed = [p \in PartSet |-> FALSE]
    /\ pins = [p \in PartSet |-> 0]
    /\ cohort = [state |-> "queued", members |-> Rows]
    /\ successor = [queued |-> FALSE, members |-> {}]
    /\ pass = [pc |-> "none", slots |-> <<>>, typed |-> FALSE]
    /\ used = [fg |-> 0, syncFails |-> 0]
    /\ hist = [installs |-> [p \in PartSet |-> 0], passes |-> 0, decodedDuringFinish |-> FALSE,
               typedTwoSlots |-> FALSE, donorUnownedInFirstPass |-> FALSE]

Move(cnt, ds, as) == [r \in Rows |-> (cnt[r] + (IF r \in as THEN 1 ELSE 0)) - (IF r \in ds THEN 1 ELSE 0)]
Slot(p) == [part |-> p, state |-> "planned", roles |-> {}, expRev |-> 0]
SlotIdx == 1..Len(pass.slots)

\* The worker takes the cohort and plans its slots: every part the receiver
\* still lacks, or only the first of them for a decoded receiver.
Take ==
    LET wanted == SelectSeq(Parts, LAMBDA p : phase[p] = "Pending")
        typed == form = "decoded"
        planned == IF ~live["D"] \/ ~live["R"] THEN <<>>
                   ELSE IF typed /\ Len(wanted) > 1 /\ Fault # "TypedTwoSlots" THEN <<wanted[1]>> ELSE wanted IN
    /\ cohort.state = "queued" /\ pass.pc = "none"
    /\ cohort' = [cohort EXCEPT !.state = "active"]
    /\ pass' = [pc |-> "prepare", typed |-> typed, slots |-> [i \in 1..Len(planned) |-> Slot(planned[i])]]
    /\ hist' = [hist EXCEPT !.passes = @ + 1, !.typedTwoSlots = @ \/ (typed /\ Len(planned) > 1)]
    /\ UNCHANGED <<live, count, donorOwned, xRetained, form, rev, phase, desired, owed, pins, successor, used>>

\* prepareReadyPartFromBase, in slot order. The first slot observes the row;
\* a later one requires that observation to stand and builds on the prefix.
Prepare ==
    /\ pass.pc = "prepare"
    /\ IF \E i \in SlotIdx : pass.slots[i].state = "planned"
       THEN LET i == CHOOSE j \in SlotIdx : pass.slots[j].state = "planned" /\ \A k \in SlotIdx : k < j => pass.slots[k].state # "planned"
                p == pass.slots[i].part
                first == i = 1 \/ pass.slots[1].state # "prepared"
                base == IF first \/ Fault = "StaleRoleMap" THEN desired ELSE pass.slots[i - 1].roles
                exp == IF first THEN rev ELSE pass.slots[i - 1].expRev + 1
                stands == first \/ rev = pass.slots[1].expRev
                ok == phase[p] = "Pending" /\ live["D"] /\ stands IN
            /\ pass' = [pass EXCEPT !.slots[i] = IF ok
                          THEN [part |-> p, state |-> "prepared", roles |-> base \cup {p}, expRev |-> exp]
                          ELSE [@ EXCEPT !.state = "refused"]]
            /\ pins' = IF ok THEN [pins EXCEPT ![p] = @ + 1] ELSE pins
       ELSE /\ pass' = [pass EXCEPT !.pc = "commit"]
            /\ UNCHANGED pins
    /\ UNCHANGED <<live, count, donorOwned, xRetained, form, rev, phase, desired, owed, cohort, successor, used, hist>>

\* CommitReadyPart in slot order, all after every preparation.
Commit ==
    /\ pass.pc = "commit"
    /\ IF \E i \in SlotIdx : pass.slots[i].state = "prepared"
       THEN LET i == CHOOSE j \in SlotIdx : pass.slots[j].state = "prepared" /\ \A k \in SlotIdx : k < j => pass.slots[k].state # "prepared"
                p == pass.slots[i].part
                ok == phase[p] = "Pending" /\ rev = pass.slots[i].expRev IN
            IF ok
            THEN /\ phase' = [phase EXCEPT ![p] = "Installed"]
                 /\ desired' = pass.slots[i].roles
                 /\ rev' = rev + 1
                 /\ pass' = [pass EXCEPT !.slots[i].state = "committed"]
                 /\ hist' = [hist EXCEPT !.installs[p] = @ + 1]
                 /\ UNCHANGED pins
            ELSE /\ pass' = [pass EXCEPT !.slots[i].state = "refused"]
                 /\ pins' = [pins EXCEPT ![p] = @ - 1]
                 /\ UNCHANGED <<phase, desired, rev, hist>>
       ELSE /\ pass' = [pass EXCEPT !.pc = "release"]
            /\ UNCHANGED <<phase, desired, rev, pins, hist>>
    /\ UNCHANGED <<live, count, donorOwned, xRetained, form, owed, cohort, successor, used>>

Filled == \E i \in SlotIdx : pass.slots[i].state \in {"committed", "finished"}
MoreToFill == \E p \in PartSet : phase[p] = "Pending"

\* Every member hold ends here. For a decoded receiver that was filled and
\* still lacks a part, the successor cohort takes its holds first, in the
\* same step.
Release ==
    LET again == pass.typed /\ Filled /\ MoreToFill
        atomic == again /\ Fault # "DecrementBeforeSuccessor" IN
    /\ pass.pc = "release"
    /\ count' = IF atomic THEN count ELSE Move(count, cohort.members, {})
    /\ successor' = IF atomic THEN [queued |-> TRUE, members |-> cohort.members] ELSE successor
    /\ cohort' = [state |-> "idle", members |-> {}]
    /\ pass' = [pass EXCEPT !.pc = IF again /\ ~atomic THEN "requeue" ELSE "finish"]
    /\ UNCHANGED <<live, donorOwned, xRetained, form, rev, phase, desired, owed, pins, used, hist>>

\* Only the fault reaches this: the successor takes its holds after the
\* members were already released.
Requeue ==
    /\ pass.pc = "requeue"
    /\ successor' = [queued |-> TRUE, members |-> Rows]
    /\ count' = Move(count, {}, Rows)
    /\ pass' = [pass EXCEPT !.pc = "finish"]
    /\ UNCHANGED <<live, donorOwned, xRetained, form, rev, phase, desired, owed, pins, cohort, used, hist>>

\* FinishReadyPart for one receipt: its own owner synchronization.
Finish(i) ==
    LET p == pass.slots[i].part IN
    /\ pass.pc = "finish" \/ (Fault = "FinishBeforeRelease" /\ pass.pc \in {"commit", "release"})
    /\ i \in SlotIdx /\ pass.slots[i].state = "committed"
    /\ pass' = [pass EXCEPT !.slots[i].state = "finished"]
    /\ \/ /\ phase' = [phase EXCEPT ![p] = "Complete"]
          /\ pins' = [pins EXCEPT ![p] = @ - 1]
          /\ UNCHANGED <<owed, used>>
       \/ /\ used.syncFails < MaxSyncFailures
          /\ used' = [used EXCEPT !.syncFails = @ + 1]
          /\ owed' = [owed EXCEPT ![p] = TRUE]
          /\ UNCHANGED <<phase, pins>>
    /\ UNCHANGED <<live, count, donorOwned, xRetained, form, rev, desired, cohort, successor, hist>>

EndPass ==
    /\ pass.pc = "finish" /\ \A i \in SlotIdx : pass.slots[i].state # "committed"
    /\ pass' = [pc |-> "none", slots |-> <<>>, typed |-> FALSE]
    /\ cohort' = IF successor.queued THEN [state |-> "queued", members |-> successor.members] ELSE cohort
    /\ successor' = [queued |-> FALSE, members |-> {}]
    /\ UNCHANGED <<live, count, donorOwned, xRetained, form, rev, phase, desired, owed, pins, used, hist>>

\* A demand joins the installation and pays the owed bookkeeping: no pin, no
\* hold, no read.
PayOwed(p) ==
    /\ owed[p] /\ phase[p] = "Installed"
    /\ owed' = [owed EXCEPT ![p] = FALSE]
    /\ phase' = [phase EXCEPT ![p] = "Complete"]
    /\ pins' = [pins EXCEPT ![p] = IF Fault = "RetryRepins" THEN @ ELSE @ - 1]
    /\ UNCHANGED <<live, count, donorOwned, xRetained, form, rev, desired, cohort, successor, pass, used, hist>>

SlotHolds(p) == \E i \in SlotIdx : pass.slots[i].part = p /\ pass.slots[i].state \in {"prepared", "committed"}

\* The one foreground event: a decode of the encoded receiver, or an ordinary
\* demand installing a part whose address no slot holds.
ForegroundDecode ==
    /\ used.fg < MaxForeground /\ form = "encoded" /\ live["R"]
    /\ used' = [used EXCEPT !.fg = @ + 1]
    /\ form' = "decoded"
    /\ rev' = rev + 1
    /\ hist' = [hist EXCEPT !.decodedDuringFinish = @ \/ (pass.pc = "finish" /\ \E i \in SlotIdx : pass.slots[i].state = "committed")]
    /\ UNCHANGED <<live, count, donorOwned, xRetained, phase, desired, owed, pins, cohort, successor, pass>>

ForegroundInstall(p) ==
    /\ used.fg < MaxForeground /\ phase[p] = "Pending" /\ ~SlotHolds(p) /\ live["D"] /\ live["R"]
    /\ used' = [used EXCEPT !.fg = @ + 1]
    /\ phase' = [phase EXCEPT ![p] = "Complete"]
    /\ desired' = desired \cup {p}
    /\ rev' = rev + 1
    /\ hist' = [hist EXCEPT !.installs[p] = @ + 1]
    /\ UNCHANGED <<live, count, donorOwned, xRetained, form, owed, pins, cohort, successor, pass>>

DropDonorOwner ==
    /\ donorOwned
    /\ donorOwned' = FALSE
    /\ count' = Move(count, {"D"}, {})
    /\ hist' = [hist EXCEPT !.donorUnownedInFirstPass = hist.passes <= 1]
    /\ UNCHANGED <<live, xRetained, form, rev, phase, desired, owed, pins, cohort, successor, pass, used>>

DropBystander ==
    /\ xRetained
    /\ xRetained' = FALSE
    /\ count' = Move(count, {"X"}, {})
    /\ UNCHANGED <<live, donorOwned, form, rev, phase, desired, owed, pins, cohort, successor, pass, used, hist>>

Collect(r) ==
    /\ live[r] /\ count[r] = 0
    /\ live' = [live EXCEPT ![r] = FALSE]
    /\ UNCHANGED <<count, donorOwned, xRetained, form, rev, phase, desired, owed, pins, cohort, successor, pass, used, hist>>

Next ==
    \/ Take \/ Prepare \/ Commit \/ Release \/ Requeue \/ EndPass
    \/ \E i \in 1..2 : Finish(i)
    \/ \E p \in PartSet : PayOwed(p) \/ ForegroundInstall(p)
    \/ ForegroundDecode \/ DropDonorOwner \/ DropBystander
    \/ \E r \in Rows : Collect(r)

Spec == Init /\ [][Next]_vars

-----------------------------------------------------------------------------
TypeOK ==
    /\ \A r \in Rows : count[r] \in 0..4
    /\ form \in {"encoded", "decoded"}
    /\ \A p \in PartSet : phase[p] \in {"Pending", "Installed", "Complete"} /\ pins[p] \in 0..2
    /\ pass.pc \in {"none", "prepare", "commit", "release", "requeue", "finish"}
    /\ Len(pass.slots) <= 2

\* Ownership recounted from the actual holders.
Holders(r) ==
    (IF r = "D" /\ donorOwned THEN 1 ELSE 0) + (IF r = "R" THEN 1 ELSE 0) + (IF r = "X" /\ xRetained THEN 1 ELSE 0)
    + (IF cohort.state # "idle" /\ r \in cohort.members THEN 1 ELSE 0)
    + (IF successor.queued /\ r \in successor.members THEN 1 ELSE 0)
EveryHoldHasALiveOwner == \A r \in Rows : live[r] => count[r] = Holders(r)

\* A cohort's hold keeps its member alive: nothing queued or active names a
\* collected row. For a decoded receiver this is the continuous donor hold.
MembersAreLive ==
    /\ cohort.state # "idle" => \A r \in cohort.members : live[r]
    /\ successor.queued => \A r \in successor.members : live[r]

\* Every cohort hold has ended before any receipt is finished.
MembersReleasedBeforeFinish ==
    (\E i \in SlotIdx : pass.slots[i].state = "finished") => cohort.state # "active"

\* The published envelope names every part that has been installed: a later
\* slot never overwrites an earlier one's role.
DesiredCoversInstalled == \A p \in PartSet : phase[p] # "Pending" => p \in desired

FinalOutputNeverOverwritten == \A p \in PartSet : hist.installs[p] <= 1

\* One pin per prepared or unsettled part, none once it is complete, and none
\* left behind by a pass except where bookkeeping is owed.
PinsBalanced ==
    /\ \A p \in PartSet : pins[p] = (IF SlotHolds(p) \/ owed[p] THEN 1 ELSE 0)
    /\ \A p \in PartSet : phase[p] = "Complete" => pins[p] = 0

\* A receiver that was already decoded when its pass was planned has one slot.
DecodedReceiverOneSlotPerPass == ~hist.typedTwoSlots

-----------------------------------------------------------------------------
\* Reachability probes: each is checked as an invariant and must be VIOLATED.
WitnessDonorCollectedBeforeReceiverRead ==
    ~(~live["D"] /\ \A p \in PartSet : phase[p] = "Complete")
WitnessDecodedWhileFinishPaused == ~hist.decodedDuringFinish
WitnessTwoPartsInOnePass ==
    ~(hist.passes = 1 /\ pass.pc = "none" /\ \A p \in PartSet : phase[p] = "Complete" /\ hist.installs[p] = 1 /\ used.fg = 0)
\* A decoded receiver filled over two passes although the donor lost its
\* ordinary owner: only the cohorts' continuous hold kept the donor.
WitnessSuccessorFillsDecodedReceiver ==
    ~(hist.passes = 2 /\ form = "decoded" /\ used.fg = 0 /\ ~donorOwned /\ hist.donorUnownedInFirstPass
      /\ \A p \in PartSet : phase[p] = "Complete")
WitnessStaleRevisionRefusesSlot ==
    ~(\E i \in SlotIdx : pass.slots[i].state = "refused" /\ live["D"] /\ phase[pass.slots[i].part] = "Pending")
=============================================================================
