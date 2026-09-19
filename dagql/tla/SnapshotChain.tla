-------------------------- MODULE SnapshotChain --------------------------
(* Immutable snapshot transfer below DagQL. No result identity or policy. *)
(* Two importers walk one prefix and suffix. Snapshot IDs are allocated in *)
(* creation order, so reapplying a lost parent changes the suffix key.     *)
(*                                                                     *)
(* Lookup, resource attachment, and presence validation are separate.     *)
(* containerd v2.2.5 AddResource checks no target presence. Its metadata   *)
(* writes serialize with GC; a resource attached before validation keeps  *)
(* a surviving target alive. Missing targets are ordinary reuse misses.  *)
(* Handles and indexes never affect GC. Temporary pins last through      *)
(* return and owner adoption. Cleanup removes only the operation's pins. *)
(* Snapshot pins include actual ancestry and recorded content. Export    *)
(* records new content on every existing snapshot owner as Go does.      *)
(*                                                                     *)
(* One action is a metadata critical section or an I/O boundary. The     *)
(* parent/diff lock spans lookup, fetch, apply, and publication; no model *)
(* metadata lock spans bytes. Fetch and apply have independent phases.   *)
(* File bytes, compression, mounts, and restart codecs need Go evidence. *)
(* Root 0 is already normalized; omitted local roots are a Go concern.   *)
(*                                                                     *)
(* Selection evidence is captured at lookup, not at later I/O entry:    *)
(* an export may publish while an importer already holds a valid miss.   *)
(* Choice fields, owner expected bytes, seen and bad are evidence only. No action  *)
(* guard reads them. Bounds                                             *)
(* limit external requests and physical creations, never property state. *)
(*                                                                     *)
(* Go mapping: ImportChain/ImportImage own one private resourcePin.       *)
(* importLayer holds importLayerLocker through Lookup, Apply and Commit. *)
(* Attach/Validate split AttachLease around its resource writes and      *)
(* final snapshot Stats. Blob actions split pinContent and               *)
(* importLayerContent; Apply is Applier.Apply. Advance keeps operation    *)
(* pins; Adopt represents caller AttachLease then returned Ref.Release.  *)
(* Export actions map to ExportChain, ensureExportBlob,                   *)
(* recordSnapshotContent and registerExportLayer. Consume ends only when *)
(* the provider consumer releases ExportChain/ExportedImage. One producer*)
(* abstracts export exclusion; concurrent cancellation needs Go evidence.*)
EXTENDS Naturals, Sequences, FiniteSets, TLC

CONSTANTS MaxStarts, MaxSnapshots, LocalBuild, SourceCanFail
Importers == {1, 2}
Owners == {1, 2}
Layers == {1, 2}
Keys == (0..MaxSnapshots) \X Layers

VARIABLES snapshots, blobs, index, workers, locks, owners, producer,
          starts, seen, bad
vars == <<snapshots, blobs, index, workers, locks, owners, producer,
          starts, seen, bad>>

IDs == 1..Len(snapshots)
RECURSIVE Ancestry(_)
Ancestry(s) == IF s = 0 THEN {} ELSE {s} \cup Ancestry(snapshots[s].parent)
Present == {s \in IDs : snapshots[s].present}
Recorded(s) == {snapshots[a].layer : a \in {x \in Ancestry(s) : snapshots[x].content}}
Key(i) == <<workers[i].prefix, workers[i].layer>>
Candidate(i) == index[Key(i)]
Survives(s) == s # 0 /\ Ancestry(s) \subseteq Present
Indexed(i) == Survives(Candidate(i))
EmptyPins == [snaps |-> {}, bytes |-> {}]
EmptyWorker == [phase |-> "idle", prefix |-> 0, layer |-> 1, target |-> 1,
                handle |-> 0, snapshotChoice |-> 0, blobChoice |-> FALSE,
                pins |-> EmptyPins, mount |-> FALSE,
                write |-> FALSE]
PinnedSnapshots == UNION {workers[i].pins.snaps : i \in Importers}
                   \cup UNION {owners[o].pins.snaps : o \in Owners}
                   \cup producer.pins.snaps
PinnedBytes == UNION {workers[i].pins.bytes : i \in Importers}
               \cup UNION {owners[o].pins.bytes : o \in Owners}
               \cup producer.pins.bytes
Pin(p, s) == [snaps |-> p.snaps \cup Ancestry(s), bytes |-> p.bytes \cup Recorded(s)]
NewSnapshot(parent, layer, content) ==
    [parent |-> parent, layer |-> layer, content |-> content, present |-> TRUE]

Init ==
    /\ snapshots = IF LocalBuild THEN
         <<NewSnapshot(0, 1, FALSE), NewSnapshot(1, 2, FALSE)>> ELSE <<>>
    /\ blobs \in IF LocalBuild THEN {{}, {1, 2}} ELSE {{}}
    /\ index = [k \in Keys |-> 0]
    /\ workers = [i \in Importers |-> EmptyWorker]
    /\ locks = [k \in Keys |-> 0]
    /\ owners = [o \in Owners |-> IF LocalBuild /\ o = 1
         THEN [tip |-> 2, pins |-> [snaps |-> {1, 2}, bytes |-> {}], expected |-> {}]
         ELSE [tip |-> 0, pins |-> EmptyPins, expected |-> {}]]
    /\ producer = [phase |-> IF LocalBuild THEN "select" ELSE "done",
                    tip |-> IF LocalBuild THEN 2 ELSE 0,
                    layer |-> 1, pins |-> EmptyPins]
    /\ starts = 0
    /\ seen = {}
    /\ bad = {}

Start(i) ==
    /\ starts < MaxStarts
    /\ workers[i].phase \in {"idle", "done", "failed"}
    /\ \E target \in Layers : workers' = [workers EXCEPT
         ![i] = [EmptyWorker EXCEPT !.phase = "lock", !.target = target]]
    /\ starts' = starts + 1
    /\ UNCHANGED <<snapshots, blobs, index, locks, owners, producer, seen, bad>>

Lock(i) ==
    /\ workers[i].phase = "lock"
    /\ locks[Key(i)] = 0
    /\ locks' = [locks EXCEPT ![Key(i)] = i]
    /\ workers' = [workers EXCEPT ![i].phase = "lookup"]
    /\ UNCHANGED <<snapshots, blobs, index, owners, producer, starts, seen, bad>>

Lookup(i) ==
    /\ workers[i].phase = "lookup"
    /\ workers' = [workers EXCEPT ![i].handle = Candidate(i),
         ![i].snapshotChoice = IF Indexed(i) THEN Candidate(i) ELSE 0,
         ![i].phase = IF Candidate(i) = 0 THEN "blob" ELSE "attach"]
    /\ bad' = bad \cup (IF Indexed(i) /\ workers'[i].handle # Candidate(i)
                           THEN {"snapshotSelection"} ELSE {})
    /\ UNCHANGED <<snapshots, blobs, index, locks, owners, producer, starts, seen>>

Attach(i) ==
    /\ workers[i].phase = "attach"
    /\ LET missing == Ancestry(workers[i].handle) \ workers[i].pins.snaps IN
       IF missing # {}
       THEN \E s \in missing : workers' = [workers EXCEPT ![i].pins.snaps = @ \cup {s}]
       ELSE workers' = [workers EXCEPT ![i].pins.bytes = @ \cup Recorded(workers[i].handle),
                                       ![i].phase = "validate"]
    /\ UNCHANGED <<snapshots, blobs, index, locks, owners, producer, starts, seen, bad>>

Validate(i) ==
    /\ workers[i].phase = "validate"
    /\ IF Survives(workers[i].handle)
       THEN /\ workers' = [workers EXCEPT ![i].phase = "advance"]
            /\ seen' = seen \cup {"snapshotReuse"} \cup
                 (IF \E o \in Owners : workers[i].handle \in owners[o].pins.snaps
                  THEN {"sharedOwner"} ELSE {})
            /\ UNCHANGED index
       ELSE /\ workers' = [workers EXCEPT ![i].phase = "blob", ![i].handle = 0,
                  ![i].pins = Pin(EmptyPins, workers[i].prefix)]
            /\ index' = [index EXCEPT ![Key(i)] = 0]
            /\ seen' = seen \cup {"lostCandidate"}
    /\ UNCHANGED <<snapshots, blobs, locks, owners, producer, starts, bad>>

BlobLookup(i) ==
    /\ workers[i].phase = "blob"
    /\ workers' = [workers EXCEPT ![i].blobChoice = workers[i].layer \in blobs,
         ![i].phase = IF workers[i].layer \in blobs THEN "pinBlob" ELSE "fetch"]
    /\ bad' = bad \cup (IF workers[i].layer \in blobs /\ workers'[i].phase # "pinBlob"
                           THEN {"blobSelection"} ELSE {})
    /\ UNCHANGED <<snapshots, blobs, index, locks, owners, producer, starts, seen>>

PinBlob(i) ==
    /\ workers[i].phase = "pinBlob"
    /\ workers' = [workers EXCEPT ![i].pins.bytes = @ \cup {workers[i].layer},
                                  ![i].phase = "checkBlob"]
    /\ UNCHANGED <<snapshots, blobs, index, locks, owners, producer, starts, seen, bad>>

CheckBlob(i) ==
    /\ workers[i].phase = "checkBlob"
    /\ workers' = [workers EXCEPT ![i].blobChoice = workers[i].layer \in blobs,
         ![i].phase = IF workers[i].layer \in blobs THEN "ready" ELSE "fetch"]
    /\ seen' = IF workers[i].layer \in blobs THEN seen \cup {"blobReuse"} ELSE seen
    /\ UNCHANGED <<snapshots, blobs, index, locks, owners, producer, starts, bad>>

Fetch(i) ==
    /\ workers[i].phase = "fetch"
    /\ workers' = [workers EXCEPT ![i].phase = "reading", ![i].write = TRUE]
    /\ seen' = seen \cup {"fetch"}
    /\ bad' = bad \cup
         (IF Survives(workers[i].snapshotChoice) THEN {"fetchAfterSnapshotChoice"} ELSE {})
         \cup (IF workers[i].blobChoice THEN {"fetchAfterBlobChoice"} ELSE {})
    /\ UNCHANGED <<snapshots, blobs, index, locks, owners, producer, starts>>

Fetched(i) ==
    /\ workers[i].phase = "reading"
    /\ blobs' = blobs \cup {workers[i].layer}
    /\ workers' = [workers EXCEPT ![i].phase = "ready", ![i].write = FALSE,
                                  ![i].pins.bytes = @ \cup {workers[i].layer}]
    /\ UNCHANGED <<snapshots, index, locks, owners, producer, starts, seen, bad>>

Apply(i) ==
    /\ workers[i].phase = "ready"
    /\ Len(snapshots) < MaxSnapshots
    /\ workers' = [workers EXCEPT ![i].phase = "applying", ![i].mount = TRUE]
    /\ bad' = bad \cup
         (IF Survives(workers[i].snapshotChoice) THEN {"applyAfterSnapshotChoice"} ELSE {})
    /\ seen' = seen \cup {"apply"} \cup
         (IF \E s \in IDs : ~snapshots[s].present /\
              <<snapshots[s].parent, snapshots[s].layer>> = Key(i)
          THEN {"reapply"} ELSE {})
    /\ UNCHANGED <<snapshots, blobs, index, locks, owners, producer, starts>>

(* Snapshot commit attaches its resource in the metadata transaction.    *)
(* Index publication follows under the key lock while that pin remains. *)
Commit(i) ==
    /\ workers[i].phase = "applying"
    /\ Len(snapshots) < MaxSnapshots
    /\ LET s == Len(snapshots) + 1 IN
       /\ snapshots' = Append(snapshots, NewSnapshot(workers[i].prefix, workers[i].layer, TRUE))
       /\ workers' = [workers EXCEPT ![i].handle = s, ![i].phase = "advance",
            ![i].mount = FALSE, ![i].pins.snaps = @ \cup {s}]
       /\ index' = [index EXCEPT ![Key(i)] = s]
    /\ UNCHANGED <<blobs, locks, owners, producer, starts, seen, bad>>

Advance(i) ==
    /\ workers[i].phase = "advance"
    /\ workers' = [workers EXCEPT ![i].prefix = workers[i].handle,
         ![i].phase = IF workers[i].layer = workers[i].target THEN "returned" ELSE "lock",
         ![i].layer = IF workers[i].layer = workers[i].target THEN @ ELSE @ + 1]
    /\ locks' = [locks EXCEPT ![Key(i)] = 0]
    /\ UNCHANGED <<snapshots, blobs, index, owners, producer, starts, seen, bad>>

(* Owner attachment may take several metadata writes in Go. Operation    *)
(* pins protect the whole closure until all succeed, so GC cannot change *)
(* the resources during this action. Failure keeps the operation owner.  *)
Adopt(i, o) ==
    /\ workers[i].phase = "returned"
    /\ owners' = [owners EXCEPT ![o] =
         [tip |-> workers[i].prefix, pins |-> Pin(EmptyPins, workers[i].prefix),
          expected |-> Recorded(workers[i].prefix) \cap blobs]]
    /\ workers' = [workers EXCEPT ![i].phase = "done", ![i].pins = EmptyPins,
                                  ![i].prefix = 0, ![i].handle = 0]
    /\ seen' = seen \cup {"adopt"}
    /\ UNCHANGED <<snapshots, blobs, index, locks, producer, starts, bad>>

Release(o) ==
    /\ owners[o].tip # 0
    /\ owners' = [owners EXCEPT ![o] = [tip |-> 0, pins |-> EmptyPins, expected |-> {}]]
    /\ UNCHANGED <<snapshots, blobs, index, workers, locks, producer, starts, seen, bad>>

(* Failure includes a canceled source/apply or a caller abandoning a ref. *)
Fail(i) ==
    /\ SourceCanFail
    /\ workers[i].phase \notin {"idle", "done", "failed", "cleanup"}
    /\ workers' = [workers EXCEPT ![i].phase = "cleanup"]
    /\ seen' = IF workers[i].prefix # 0 THEN seen \cup {"prefixFailure"} ELSE seen
    /\ UNCHANGED <<snapshots, blobs, index, locks, owners, producer, starts, bad>>

Cleanup(i) ==
    /\ workers[i].phase = "cleanup"
    /\ workers' = [workers EXCEPT ![i] = [EmptyWorker EXCEPT !.phase = "failed"]]
    /\ locks' = [k \in Keys |-> IF locks[k] = i THEN 0 ELSE locks[k]]
    /\ UNCHANGED <<snapshots, blobs, index, owners, producer, starts, seen, bad>>

(* GC may remove an unowned suffix or whole chain. Index rows remain hints. *)
Prune(s) ==
    /\ s \in Present
    /\ LET removed == {a \in Present : s \in Ancestry(a)} IN
       /\ removed \cap PinnedSnapshots = {}
       /\ snapshots' = [a \in IDs |->
            IF a \in removed THEN [snapshots[a] EXCEPT !.present = FALSE] ELSE snapshots[a]]
    /\ seen' = seen \cup {"prune"}
    /\ UNCHANGED <<blobs, index, workers, locks, owners, producer, starts, bad>>

PruneBlob(b) ==
    /\ b \in blobs \ PinnedBytes
    /\ blobs' = blobs \ {b}
    /\ UNCHANGED <<snapshots, index, workers, locks, owners, producer, starts, seen, bad>>

ExportAttach ==
    /\ producer.phase = "select"
    /\ LET missing == Ancestry(producer.tip) \ producer.pins.snaps IN
       IF missing # {}
       THEN \E s \in missing : producer' = [producer EXCEPT !.pins.snaps = @ \cup {s}]
       ELSE producer' = [producer EXCEPT !.pins.bytes = @ \cup Recorded(producer.tip),
                                         !.phase = "check"]
    /\ UNCHANGED <<snapshots, blobs, index, workers, locks, owners, starts, seen, bad>>

ExportCheck ==
    /\ producer.phase = "check"
    /\ producer' = [producer EXCEPT !.phase =
         IF Survives(producer.tip) THEN "blob" ELSE "release"]
    /\ UNCHANGED <<snapshots, blobs, index, workers, locks, owners, starts, seen, bad>>

ExportBlob ==
    /\ producer.phase = "blob"
    /\ IF producer.layer \in blobs
       THEN /\ producer' = [producer EXCEPT !.phase = "pinBlob"]
            /\ UNCHANGED blobs
       ELSE /\ producer' = [producer EXCEPT !.phase = "register",
                                              !.pins.bytes = @ \cup {producer.layer}]
            /\ blobs' = blobs \cup {producer.layer}
    /\ seen' = seen \cup {IF producer.layer \in blobs THEN "exportExisting" ELSE "exportGenerated"}
    /\ UNCHANGED <<snapshots, index, workers, locks, owners, starts, bad>>

ExportPinBlob ==
    /\ producer.phase = "pinBlob"
    /\ producer' = [producer EXCEPT !.phase = "checkBlob", !.pins.bytes = @ \cup {producer.layer}]
    /\ UNCHANGED <<snapshots, blobs, index, workers, locks, owners, starts, seen, bad>>

ExportCheckBlob ==
    /\ producer.phase = "checkBlob"
    /\ producer' = [producer EXCEPT !.phase =
         IF producer.layer \in blobs THEN "register" ELSE "release"]
    /\ UNCHANGED <<snapshots, blobs, index, workers, locks, owners, starts, seen, bad>>

(* Descriptor selection precedes registration; content reaches all owners. *)
ExportRegister ==
    /\ producer.phase = "register"
    /\ LET s == producer.layer IN
       /\ index' = [index EXCEPT ![<<snapshots[s].parent, snapshots[s].layer>>] = s]
       /\ snapshots' = [snapshots EXCEPT ![s].content = TRUE]
       /\ owners' = [o \in Owners |-> IF s \in owners[o].pins.snaps
            THEN [owners[o] EXCEPT !.pins.bytes = @ \cup {producer.layer},
                                  !.expected = @ \cup {producer.layer}] ELSE owners[o]]
       /\ workers' = [i \in Importers |-> IF s \in workers[i].pins.snaps
            THEN [workers[i] EXCEPT !.pins.bytes = @ \cup {producer.layer}] ELSE workers[i]]
    /\ producer' = [producer EXCEPT !.phase = IF producer.layer = 2 THEN "provider" ELSE "blob",
                                   !.layer = IF producer.layer = 2 THEN @ ELSE @ + 1]
    /\ bad' = bad \cup
         (IF index'[<<snapshots[producer.layer].parent, snapshots[producer.layer].layer>>] # producer.layer
          THEN {"exportRegistration"} ELSE {})
    /\ UNCHANGED <<blobs, locks, starts, seen>>

Consume ==
    /\ producer.phase = "provider"
    /\ producer' = [producer EXCEPT !.phase = "release"]
    /\ seen' = seen \cup {"consume"}
    /\ UNCHANGED <<snapshots, blobs, index, workers, locks, owners, starts, bad>>

ExportRelease ==
    /\ producer.phase = "release"
    /\ producer' = [producer EXCEPT !.phase = "done", !.pins = EmptyPins]
    /\ UNCHANGED <<snapshots, blobs, index, workers, locks, owners, starts, seen, bad>>

Next ==
    \/ \E i \in Importers : Start(i) \/ Lock(i) \/ Lookup(i) \/ Attach(i) \/ Validate(i)
         \/ BlobLookup(i) \/ PinBlob(i) \/ CheckBlob(i) \/ Fetch(i) \/ Fetched(i)
         \/ Apply(i) \/ Commit(i) \/ Advance(i) \/ Fail(i) \/ Cleanup(i)
         \/ \E o \in Owners : Adopt(i, o)
    \/ \E o \in Owners : Release(o)
    \/ \E s \in IDs : Prune(s)
    \/ \E b \in Layers : PruneBlob(b)
    \/ ExportAttach \/ ExportCheck \/ ExportBlob \/ ExportPinBlob \/ ExportCheckBlob
         \/ ExportRegister \/ Consume \/ ExportRelease
Spec == Init /\ [][Next]_vars

TypeOK ==
    /\ Len(snapshots) <= MaxSnapshots
    /\ \A s \in IDs : snapshots[s].parent \in 0..(s-1)
    /\ blobs \subseteq Layers
    /\ index \in [Keys -> 0..Len(snapshots)]
    /\ locks \in [Keys -> {0, 1, 2}]

ApplyExclusive == \A i, j \in Importers :
    (workers[i].phase = "applying" /\ workers[j].phase = "applying" /\ Key(i) = Key(j)) => i = j
ReuseBeforeBytes == bad = {}
RetainedAncestry ==
    /\ \A o \in Owners : Ancestry(owners[o].tip) \subseteq Present \cap owners[o].pins.snaps
    /\ \A i \in Importers :
         /\ Ancestry(workers[i].prefix) \subseteq Present \cap workers[i].pins.snaps
         /\ workers[i].phase = "advance" =>
              Ancestry(workers[i].handle) \subseteq Present \cap workers[i].pins.snaps
(* A new owner need not recover missing historical bytes from a usable   *)
(* snapshot. Continuing owners must retain present content recorded for  *)
(* them, including content first produced by a later local export.        *)
OwnerHasRecordedContent == \A o \in Owners :
    owners[o].expected \subseteq blobs \cap owners[o].pins.bytes
ApplyHasBytes == \A i \in Importers : workers[i].phase \in {"ready", "applying"} =>
    workers[i].layer \in blobs \cap workers[i].pins.bytes
ProviderHasResources == producer.phase = "provider" =>
    /\ Ancestry(producer.tip) \subseteq Present \cap producer.pins.snaps
    /\ Layers \subseteq blobs \cap producer.pins.bytes
CleanupComplete ==
    /\ producer.phase = "done" => producer.pins = EmptyPins
    /\ \A i \in Importers : workers[i].phase \in {"done", "failed"} =>
         /\ workers[i].pins = EmptyPins /\ workers[i].prefix = 0 /\ workers[i].handle = 0
         /\ ~workers[i].mount /\ ~workers[i].write
         /\ \A k \in Keys : locks[k] # i
=============================================================================
