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
(*                                                                     *)
(* Downloads (ModelDownloads; FETCH_AHEAD on in Go). With it off, an    *)
(* importer reads bytes itself (Fetch/Fetched), as Go does with          *)
(* _EXPERIMENTAL_DAGGER_FETCH_AHEAD=0, and the model is the one above.   *)
(* With it on, every chain download is one download agent per object,   *)
(* the per-digest coordinator of engine/snapshots/content_fetch.go: it   *)
(* runs under its own lease, detached from the importer that started it, *)
(* while any holder holds it (joinFetch :63, runFetch :98, leaveFetch    *)
(* :115). A holder links the content into its own pins before it lets go *)
(* (fetchChainContent :156, link :183), so the bytes are never unpinned.  *)
(* An importer starts the downloads of the layers after the one it      *)
(* imports ahead (import.go chainAhead.fill, fetchAhead): at each layer's *)
(* loop top, outside its key lock and before its lookup, from the first   *)
(* layer the import indexes lack (chainLocalPrefix, walked once at Start, *)
(* with no I/O), and once a layer's bytes are in (pull.go chain.fetched). *)
(* It holds each until that layer's import ends (import.go take, leave).  *)
(* Engine slots and the window bound how many start, never which: any    *)
(* subset of the later layers may start, in any order. A failed download*)
(* is terminal for the import that started it; a holder of another's    *)
(* failed download starts the next attempt in its place, or joins the    *)
(* one another holder started (:186-198). The last holder to leave a     *)
(* running download cancels it (dropFetch :142).                         *)
(* Objects are what one download fetches: one blob each here; packs map  *)
(* several layers to one object through Src and Members.                 *)
(*                                                                     *)
(* Prefetch (ModelPrefetch), kept for a later background prefetch: off   *)
(* in every configuration here, as the engine prefetches nothing yet.    *)
(* The prefetcher joins or starts a download as holder 3 with no chain   *)
(* (a nil owner), then links the blob into a TTL lease of its own and    *)
(* leaves. That lease goes when its TTL passes, its offer is retired or  *)
(* an install consumes the blob: PrefetchExpire, enabled at any time.    *)
(*                                                                     *)
(* Attempts: dl[o] is o's current attempt, the inflight entry. A retry    *)
(* replaces a failed one; its other holders keep only that failure, and  *)
(* its owner, in dl[o].failed until they see it, as Go's references to  *)
(* the old contentFetch do. A replaced attempt is failed and pins       *)
(* nothing here, which only allows more GC. An importer retrying the     *)
(* download it held ahead lets that hold go then, not when the layer's   *)
(* import ends: it is a failed attempt no longer current. Owners are    *)
(* chain imports: a restarted importer owns none of its earlier attempts.*)
(* Mutation constants each re-break one property and are FALSE in every  *)
(* green configuration.                                                  *)
EXTENDS Naturals, Sequences, FiniteSets, TLC

CONSTANTS NumLayers, MaxStarts, MaxSnapshots, LocalBuild, SourceCanFail,
          ModelDownloads, ModelPrefetch,
          MutDropDownloadPin, MutLeaveBeforeLink, MutRetryOwn, MutAheadSkipsReuse,
          MutAheadKeptOnFailure
Importers == {1, 2}
Owners == {1, 2}
Layers == 1..NumLayers
Keys == (0..MaxSnapshots) \X Layers
(* A download's object, and the layers it brings in. *)
Objects == Layers
Src(l) == l
Members(o) == {o}
(* The blobs a finished or failed download leaves present. *)
DoneBlobs(o) == Members(o)
FailBlobs(o) == {}
(* Download holders: importers, and the prefetcher. *)
Prefetcher == 3
Holders == Importers \cup {Prefetcher}
(* An attempt's owner; 0 once the chain import that started it ended. *)
OwnerIDs == {0} \cup Holders

VARIABLES snapshots, blobs, index, workers, locks, owners, producer,
          starts, seen, bad, dl, pf
downloadVars == <<dl, pf>>
vars == <<snapshots, blobs, index, workers, locks, owners, producer,
          starts, seen, bad, downloadVars>>

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
(* aheads: the layers whose downloads this importer holds ahead. first:   *)
(* the first layer its prefix walk found the import indexes lack.         *)
EmptyWorker == [phase |-> "idle", prefix |-> 0, layer |-> 1, target |-> 1,
                handle |-> 0, snapshotChoice |-> 0, blobChoice |-> FALSE,
                pins |-> EmptyPins, mount |-> FALSE,
                write |-> FALSE, aheads |-> {}, first |-> 1]
(* chainLocalPrefix: from parent p and layer l, the first layer up to t   *)
(* the index has no entry for. Entries are hints: a stale one only means *)
(* that layer downloads when it imports.                                  *)
RECURSIVE WalkIndex(_, _, _)
WalkIndex(p, l, t) == IF l > t \/ index[<<p, l>>] = 0 THEN l
                      ELSE WalkIndex(index[<<p, l>>], l + 1, t)
(* failed: <<holder, owner>> of each replaced attempt still held.        *)
EmptyDownload == [phase |-> "none", holders |-> {}, owner |-> 0, failed |-> {}]
(* A holder leaves; the last one ends the download: a running one is      *)
(* canceled, a done one's lease released.                                *)
Leave(d, h) == IF d.holders \ {h} = {} THEN [EmptyDownload EXCEPT !.failed = d.failed]
               ELSE [d EXCEPT !.holders = @ \ {h}]
(* h's attempt at o: the current one, or a replaced (failed) one.        *)
Holds(h, o) == h \in dl[o].holders \/ \E x \in OwnerIDs : <<h, x>> \in dl[o].failed
AttemptPhase(h, o) == IF h \in dl[o].holders THEN dl[o].phase ELSE "failed"
AttemptOwner(h, o) == IF h \in dl[o].holders THEN dl[o].owner
                      ELSE CHOOSE x \in OwnerIDs : <<h, x>> \in dl[o].failed
Drop(o, h) == IF h \in dl[o].holders THEN Leave(dl[o], h)
              ELSE [dl[o] EXCEPT !.failed = {p \in @ : p[1] # h}]
LeaveAll(h) == [o \in Objects |-> Drop(o, h)]
(* Every new attempt, owned by h, in d's place: fields other than its   *)
(* phase, holders and owner carry over.                                 *)
Attempt(d, hs, h) == [d EXCEPT !.phase = "running", !.holders = hs, !.owner = h]
(* Join d, or start an attempt owned by h in its place. *)
JoinD(d, h) == IF d.phase = "none" THEN Attempt(d, {h}, h)
               ELSE [d EXCEPT !.holders = @ \cup {h}]
Join(o, h) == JoinD(dl[o], h)
(* h saw its attempt fail and goes again (joinFetch with failed): a      *)
(* failed current attempt is replaced by h's own; else h joins whatever  *)
(* attempt is current, or starts one if none is.                         *)
Retry(o, h) ==
    IF h \in dl[o].holders
    THEN Attempt([dl[o] EXCEPT !.failed = @ \cup
                    {<<x, dl[o].owner>> : x \in dl[o].holders \ {h}}], {h}, h)
    ELSE JoinD(Drop(o, h), h)
(* A chain import ended: attempts it started are no later import's own.  *)
Disown(d, h) == [d EXCEPT !.owner = IF @ = h THEN 0 ELSE @,
                          !.failed = {<<p[1], IF p[2] = h THEN 0 ELSE p[2]>> : p \in @}]
DownloadPins == IF MutDropDownloadPin THEN {}
                ELSE UNION {Members(o) : o \in {x \in Objects :
                       dl[x].phase = "done" /\ dl[x].holders # {}}}
PrefetchPins == {l \in Layers : pf[l].phase = "held"}
PinnedSnapshots == UNION {workers[i].pins.snaps : i \in Importers}
                   \cup UNION {owners[o].pins.snaps : o \in Owners}
                   \cup producer.pins.snaps
PinnedBytes == UNION {workers[i].pins.bytes : i \in Importers}
               \cup UNION {owners[o].pins.bytes : o \in Owners}
               \cup producer.pins.bytes
               \cup DownloadPins \cup PrefetchPins
Pin(p, s) == [snaps |-> p.snaps \cup Ancestry(s), bytes |-> p.bytes \cup Recorded(s)]
NewSnapshot(parent, layer, content) ==
    [parent |-> parent, layer |-> layer, content |-> content, present |-> TRUE]

Init ==
    /\ snapshots = IF LocalBuild THEN
         [s \in Layers |-> NewSnapshot(s - 1, s, FALSE)] ELSE <<>>
    /\ blobs \in IF LocalBuild THEN {{}, Layers} ELSE {{}}
    /\ index = [k \in Keys |-> 0]
    /\ workers = [i \in Importers |-> EmptyWorker]
    /\ locks = [k \in Keys |-> 0]
    /\ owners = [o \in Owners |-> IF LocalBuild /\ o = 1
         THEN [tip |-> NumLayers, pins |-> [snaps |-> Layers, bytes |-> {}], expected |-> {}]
         ELSE [tip |-> 0, pins |-> EmptyPins, expected |-> {}]]
    /\ producer = [phase |-> IF LocalBuild THEN "select" ELSE "done",
                    tip |-> IF LocalBuild THEN NumLayers ELSE 0,
                    layer |-> 1, pins |-> EmptyPins]
    /\ starts = 0
    /\ seen = {}
    /\ bad = {}
    /\ dl = [o \in Objects |-> EmptyDownload]
    /\ pf = [l \in Layers |-> [phase |-> "none"]]

Start(i) ==
    /\ starts < MaxStarts
    /\ workers[i].phase \in {"idle", "done", "failed"}
    /\ \E target \in Layers : workers' = [workers EXCEPT
         ![i] = [EmptyWorker EXCEPT !.phase = "lock", !.target = target,
                                    !.first = WalkIndex(0, 1, target)]]
    /\ starts' = starts + 1
    /\ dl' = [o \in Objects |-> Disown(dl[o], i)]
    /\ UNCHANGED <<snapshots, blobs, index, locks, owners, producer, seen, bad, pf>>

Lock(i) ==
    /\ workers[i].phase = "lock"
    /\ locks[Key(i)] = 0
    /\ locks' = [locks EXCEPT ![Key(i)] = i]
    /\ workers' = [workers EXCEPT ![i].phase = "lookup"]
    /\ UNCHANGED <<snapshots, blobs, index, owners, producer, starts, seen, bad, downloadVars>>

Lookup(i) ==
    /\ workers[i].phase = "lookup"
    /\ workers' = [workers EXCEPT ![i].handle = Candidate(i),
         ![i].snapshotChoice = IF Indexed(i) THEN Candidate(i) ELSE 0,
         ![i].phase = IF Candidate(i) = 0 \/
                         (MutAheadSkipsReuse /\ workers[i].layer \in workers[i].aheads)
                      THEN "blob" ELSE "attach"]
    /\ bad' = bad \cup (IF Indexed(i) /\ workers'[i].handle # Candidate(i)
                           THEN {"snapshotSelection"} ELSE {})
    /\ UNCHANGED <<snapshots, blobs, index, locks, owners, producer, starts, seen, downloadVars>>

Attach(i) ==
    /\ workers[i].phase = "attach"
    /\ LET missing == Ancestry(workers[i].handle) \ workers[i].pins.snaps IN
       IF missing # {}
       THEN \E s \in missing : workers' = [workers EXCEPT ![i].pins.snaps = @ \cup {s}]
       ELSE workers' = [workers EXCEPT ![i].pins.bytes = @ \cup Recorded(workers[i].handle),
                                       ![i].phase = "validate"]
    /\ UNCHANGED <<snapshots, blobs, index, locks, owners, producer, starts, seen, bad, downloadVars>>

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
    /\ UNCHANGED <<snapshots, blobs, locks, owners, producer, starts, bad, downloadVars>>

BlobLookup(i) ==
    /\ workers[i].phase = "blob"
    /\ workers' = [workers EXCEPT ![i].blobChoice = workers[i].layer \in blobs,
         ![i].phase = IF workers[i].layer \in blobs THEN "pinBlob" ELSE "fetch"]
    /\ bad' = bad \cup (IF workers[i].layer \in blobs /\ workers'[i].phase # "pinBlob"
                           THEN {"blobSelection"} ELSE {})
    /\ UNCHANGED <<snapshots, blobs, index, locks, owners, producer, starts, seen, downloadVars>>

PinBlob(i) ==
    /\ workers[i].phase = "pinBlob"
    /\ workers' = [workers EXCEPT ![i].pins.bytes = @ \cup {workers[i].layer},
                                  ![i].phase = "checkBlob"]
    /\ UNCHANGED <<snapshots, blobs, index, locks, owners, producer, starts, seen, bad, downloadVars>>

CheckBlob(i) ==
    /\ workers[i].phase = "checkBlob"
    /\ workers' = [workers EXCEPT ![i].blobChoice = workers[i].layer \in blobs,
         ![i].phase = IF workers[i].layer \in blobs THEN "ready" ELSE "fetch"]
    /\ seen' = IF workers[i].layer \in blobs THEN seen \cup {"blobReuse"} ELSE seen
    /\ UNCHANGED <<snapshots, blobs, index, locks, owners, producer, starts, bad, downloadVars>>

FetchBad(i) == bad \cup
    (IF Survives(workers[i].snapshotChoice) THEN {"fetchAfterSnapshotChoice"} ELSE {})
    \cup (IF workers[i].blobChoice THEN {"fetchAfterBlobChoice"} ELSE {})

(* Without downloads: the importer's own writer (importLayerContent).    *)
Fetch(i) ==
    /\ ~ModelDownloads
    /\ workers[i].phase = "fetch"
    /\ workers' = [workers EXCEPT ![i].phase = "reading", ![i].write = TRUE]
    /\ seen' = seen \cup {"fetch"}
    /\ bad' = FetchBad(i)
    /\ UNCHANGED <<snapshots, blobs, index, locks, owners, producer, starts, downloadVars>>

(* With downloads: fetchChainContent waits on its own download started   *)
(* ahead (held), or joins one in progress, or starts one (joinFetch).    *)
FetchHeld(i) ==
    /\ ModelDownloads
    /\ workers[i].phase = "fetch"
    /\ workers[i].layer \in workers[i].aheads
    /\ workers' = [workers EXCEPT ![i].phase = "waitDl"]
    /\ seen' = seen \cup {"fetch", "aheadUsed"}
    /\ bad' = FetchBad(i)
    /\ UNCHANGED <<snapshots, blobs, index, locks, owners, producer, starts, downloadVars>>

FetchJoin(i) ==
    /\ ModelDownloads
    /\ workers[i].phase = "fetch"
    /\ workers[i].layer \notin workers[i].aheads
    /\ LET o == Src(workers[i].layer) IN
       /\ dl' = [dl EXCEPT ![o] = Join(o, i)]
       /\ workers' = [workers EXCEPT ![i].phase = "waitDl"]
       /\ seen' = seen \cup {"fetch"} \cup
            (IF dl[o].phase = "none" THEN {} ELSE {"join"}) \cup
            (IF Prefetcher \in dl[o].holders \/ dl[o].owner = Prefetcher
             THEN {"prefetchJoin"} ELSE {})
    /\ bad' = FetchBad(i)
    /\ UNCHANGED <<snapshots, blobs, index, locks, owners, producer, starts, pf>>

(* fill: at a layer's loop top from the first layer the walk found       *)
(* missing, or once this layer's bytes are in (chain.fetched), before its *)
(* apply, a later layer's download starts, unless its blob is present.   *)
FetchAhead(i, l) ==
    /\ ModelDownloads
    /\ \/ workers[i].phase = "ready"
       \/ workers[i].phase = "lock" /\ workers[i].layer >= workers[i].first
    /\ l \in (workers[i].layer + 1)..workers[i].target
    /\ l \notin workers[i].aheads
    /\ l \notin blobs
    /\ dl' = [dl EXCEPT ![Src(l)] = Join(Src(l), i)]
    /\ workers' = [workers EXCEPT ![i].aheads = @ \cup {l}]
    /\ seen' = seen \cup {"fetchAhead"}
         \cup (IF workers[i].aheads # {} THEN {"aheadSet"} ELSE {})
         \cup (IF workers[i].first > 1 THEN {"aheadPastPrefix"} ELSE {})
    /\ UNCHANGED <<snapshots, blobs, index, locks, owners, producer, starts, bad, pf>>

(* runFetch ends: the content lands, held by the download's lease.       *)
DownloadDone(o) ==
    /\ dl[o].phase = "running"
    /\ dl' = [dl EXCEPT ![o].phase = "done"]
    /\ blobs' = blobs \cup DoneBlobs(o)
    /\ seen' = seen \cup {"downloadDone"}
    /\ UNCHANGED <<snapshots, index, workers, locks, owners, producer, starts, bad, pf>>

DownloadFail(o) ==
    /\ SourceCanFail
    /\ dl[o].phase = "running"
    /\ dl' = [dl EXCEPT ![o].phase = "failed"]
    /\ blobs' = blobs \cup FailBlobs(o)
    /\ seen' = seen \cup {"downloadFail"}
    /\ UNCHANGED <<snapshots, index, workers, locks, owners, producer, starts, bad, pf>>

(* The phase after an importer links a done download's blob. *)
AfterLeave(i) == "ready"

(* fetchChainContent: link the blob into the importer's pins while the   *)
(* download still holds it, then let go (a held ahead download stays     *)
(* the importer's until its layer's import ends).                        *)
LeaveDownload(i) ==
    /\ ModelDownloads
    /\ workers[i].phase = "waitDl"
    /\ LET o == Src(workers[i].layer) IN
       /\ AttemptPhase(i, o) = "done"
       /\ IF MutLeaveBeforeLink
          THEN /\ dl' = [dl EXCEPT ![o] = Leave(dl[o], i)]
               /\ workers' = [workers EXCEPT ![i].phase = "link",
                                             ![i].aheads = @ \ {workers[i].layer}]
          ELSE /\ dl' = IF workers[i].layer \in workers[i].aheads THEN dl
                        ELSE [dl EXCEPT ![o] = Leave(dl[o], i)]
               /\ workers' = [workers EXCEPT ![i].phase = AfterLeave(i),
                                             ![i].pins.bytes = @ \cup {workers[i].layer}]
    /\ seen' = seen \cup {"downloadUsed"}
    /\ UNCHANGED <<snapshots, blobs, index, locks, owners, producer, starts, bad, pf>>

(* Reached only with MutLeaveBeforeLink: the link after the hold went.   *)
Link(i) ==
    /\ workers[i].phase = "link"
    /\ workers' = [workers EXCEPT ![i].phase = "ready",
                                  ![i].pins.bytes = @ \cup {workers[i].layer}]
    /\ UNCHANGED <<snapshots, blobs, index, locks, owners, producer, starts, seen, bad, downloadVars>>

(* The seen markers of a retry: another's failed attempt left in place, *)
(* or another holder's replacement joined.                              *)
RetrySeen(o, h) ==
    {"retry"} \cup (IF h \in dl[o].holders /\ dl[o].holders # {h} THEN {"stale"} ELSE {})
    \cup (IF h \notin dl[o].holders /\ dl[o].phase # "none" THEN {"retryJoin"} ELSE {})

(* Another import's failed download: this importer goes again; its held *)
(* ahead hold, a failed attempt now, goes with it.                       *)
RetryDownload(i) ==
    /\ ModelDownloads
    /\ workers[i].phase = "waitDl"
    /\ LET o == Src(workers[i].layer) IN
       /\ AttemptPhase(i, o) = "failed"
       /\ MutRetryOwn \/ AttemptOwner(i, o) # i
       /\ dl' = [dl EXCEPT ![o] = Retry(o, i)]
       /\ bad' = bad \cup (IF AttemptOwner(i, o) = i THEN {"ownRetry"} ELSE {})
       /\ seen' = seen \cup RetrySeen(o, i)
    /\ workers' = [workers EXCEPT ![i].aheads = @ \ {workers[i].layer}]
    /\ UNCHANGED <<snapshots, blobs, index, locks, owners, producer, starts, pf>>

(* Its own attempt failed: the import fails (Cleanup lets go).           *)
OwnFailure(i) ==
    /\ ModelDownloads
    /\ workers[i].phase = "waitDl"
    /\ LET o == Src(workers[i].layer) IN
       /\ AttemptPhase(i, o) = "failed"
       /\ AttemptOwner(i, o) = i
    /\ workers' = [workers EXCEPT ![i].phase = "cleanup"]
    /\ seen' = seen \cup {"ownFailure"}
    /\ UNCHANGED <<snapshots, blobs, index, locks, owners, producer, starts, bad, downloadVars>>

(* Prefetch, kept for a later background prefetch; off here. *)
PrefetchStart(l) ==
    /\ ModelPrefetch /\ ModelDownloads
    /\ pf[l].phase = "none"
    /\ l \notin blobs
    /\ dl' = [dl EXCEPT ![Src(l)] = Join(Src(l), Prefetcher)]
    /\ pf' = [pf EXCEPT ![l].phase = "fetching"]
    /\ seen' = seen \cup {"prefetchStart"}
    /\ UNCHANGED <<snapshots, blobs, index, workers, locks, owners, producer, starts, bad>>

(* Link into the TTL lease while the download holds the blob, then leave. *)
PrefetchSettle(l) ==
    /\ pf[l].phase = "fetching"
    /\ AttemptPhase(Prefetcher, Src(l)) = "done"
    /\ pf' = [pf EXCEPT ![l].phase = "held"]
    /\ dl' = [dl EXCEPT ![Src(l)] = Leave(dl[Src(l)], Prefetcher)]
    /\ seen' = seen \cup {"prefetchHeld"}
    /\ UNCHANGED <<snapshots, blobs, index, workers, locks, owners, producer, starts, bad>>

PrefetchRetry(l) ==
    /\ pf[l].phase = "fetching"
    /\ AttemptPhase(Prefetcher, Src(l)) = "failed"
    /\ AttemptOwner(Prefetcher, Src(l)) # Prefetcher
    /\ dl' = [dl EXCEPT ![Src(l)] = Retry(Src(l), Prefetcher)]
    /\ seen' = seen \cup {"prefetchRetry"} \cup
         (IF "retryJoin" \in RetrySeen(Src(l), Prefetcher) THEN {"prefetchRetryJoin"} ELSE {})
    /\ UNCHANGED <<snapshots, blobs, index, workers, locks, owners, producer, starts, bad, pf>>

PrefetchGiveUp(l) ==
    /\ pf[l].phase = "fetching"
    /\ AttemptPhase(Prefetcher, Src(l)) = "failed"
    /\ AttemptOwner(Prefetcher, Src(l)) = Prefetcher
    /\ pf' = [pf EXCEPT ![l].phase = "gone"]
    /\ dl' = [dl EXCEPT ![Src(l)] = Drop(Src(l), Prefetcher)]
    /\ UNCHANGED <<snapshots, blobs, index, workers, locks, owners, producer, starts, seen, bad>>

(* The offer retired mid-download (goneLocked, cancelUnwantedLocked).    *)
PrefetchCancel(l) ==
    /\ pf[l].phase = "fetching"
    /\ pf' = [pf EXCEPT ![l].phase = "gone"]
    /\ dl' = [dl EXCEPT ![Src(l)] = Drop(Src(l), Prefetcher)]
    /\ seen' = seen \cup {"prefetchCancel"}
    /\ UNCHANGED <<snapshots, blobs, index, workers, locks, owners, producer, starts, bad>>

(* TTL, a retired offer or a consuming install releases the hold.         *)
PrefetchExpire(l) ==
    /\ pf[l].phase = "held"
    /\ pf' = [pf EXCEPT ![l].phase = "gone"]
    /\ seen' = seen \cup {"prefetchExpire"}
    /\ UNCHANGED <<snapshots, blobs, index, workers, locks, owners, producer, starts, bad, dl>>

Fetched(i) ==
    /\ workers[i].phase = "reading"
    /\ blobs' = blobs \cup {workers[i].layer}
    /\ workers' = [workers EXCEPT ![i].phase = "ready", ![i].write = FALSE,
                                  ![i].pins.bytes = @ \cup {workers[i].layer}]
    /\ UNCHANGED <<snapshots, index, locks, owners, producer, starts, seen, bad, downloadVars>>

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
    /\ UNCHANGED <<snapshots, blobs, index, locks, owners, producer, starts, downloadVars>>

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
    /\ UNCHANGED <<blobs, locks, owners, producer, starts, seen, bad, downloadVars>>

(* The import of this layer ends: its ahead download, if held, is let   *)
(* go (import.go leaveFetch); those of later layers stay held.           *)
Advance(i) ==
    /\ workers[i].phase = "advance"
    /\ LET held == workers[i].layer \in workers[i].aheads IN
       /\ workers' = [workers EXCEPT ![i].prefix = workers[i].handle,
            ![i].phase = IF workers[i].layer = workers[i].target THEN "returned" ELSE "lock",
            ![i].layer = IF workers[i].layer = workers[i].target THEN @ ELSE @ + 1,
            ![i].aheads = @ \ {workers[i].layer}]
       /\ dl' = IF held THEN [dl EXCEPT ![Src(workers[i].layer)] =
                                  Drop(Src(workers[i].layer), i)]
                ELSE dl
       /\ seen' = IF held /\ workers[i].snapshotChoice # 0
                  THEN seen \cup {"aheadUnused"} ELSE seen
    /\ locks' = [locks EXCEPT ![Key(i)] = 0]
    /\ UNCHANGED <<snapshots, blobs, index, owners, producer, starts, bad, pf>>

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
    /\ UNCHANGED <<snapshots, blobs, index, locks, producer, starts, bad, downloadVars>>

Release(o) ==
    /\ owners[o].tip # 0
    /\ owners' = [owners EXCEPT ![o] = [tip |-> 0, pins |-> EmptyPins, expected |-> {}]]
    /\ UNCHANGED <<snapshots, blobs, index, workers, locks, producer, starts, seen, bad, downloadVars>>

(* Failure includes a canceled source/apply or a caller abandoning a ref. *)
Fail(i) ==
    /\ SourceCanFail
    /\ workers[i].phase \notin {"idle", "done", "failed", "cleanup"}
    /\ workers' = [workers EXCEPT ![i].phase = "cleanup"]
    /\ seen' = IF workers[i].prefix # 0 THEN seen \cup {"prefixFailure"} ELSE seen
    /\ UNCHANGED <<snapshots, blobs, index, locks, owners, producer, starts, bad, downloadVars>>

(* Cleanup lets go of every download the importer holds (dropFetch, and   *)
(* chainAhead.drop for those held ahead). MutAheadKeptOnFailure keeps the *)
(* holds of later layers' downloads.                                      *)
Cleanup(i) ==
    /\ workers[i].phase = "cleanup"
    /\ workers' = [workers EXCEPT ![i] = [EmptyWorker EXCEPT !.phase = "failed"]]
    /\ locks' = [k \in Keys |-> IF locks[k] = i THEN 0 ELSE locks[k]]
    /\ dl' = IF MutAheadKeptOnFailure
             THEN [o \in Objects |->
                     IF \E l \in workers[i].aheads : l # workers[i].layer /\ Src(l) = o
                     THEN dl[o] ELSE Drop(o, i)]
             ELSE LeaveAll(i)
    /\ UNCHANGED <<snapshots, blobs, index, owners, producer, starts, seen, bad, pf>>

(* GC may remove an unowned suffix or whole chain. Index rows remain hints. *)
Prune(s) ==
    /\ s \in Present
    /\ LET removed == {a \in Present : s \in Ancestry(a)} IN
       /\ removed \cap PinnedSnapshots = {}
       /\ snapshots' = [a \in IDs |->
            IF a \in removed THEN [snapshots[a] EXCEPT !.present = FALSE] ELSE snapshots[a]]
    /\ seen' = seen \cup {"prune"}
    /\ UNCHANGED <<blobs, index, workers, locks, owners, producer, starts, bad, downloadVars>>

PruneBlob(b) ==
    /\ b \in blobs \ PinnedBytes
    /\ blobs' = blobs \ {b}
    /\ UNCHANGED <<snapshots, index, workers, locks, owners, producer, starts, seen, bad, downloadVars>>

ExportAttach ==
    /\ producer.phase = "select"
    /\ LET missing == Ancestry(producer.tip) \ producer.pins.snaps IN
       IF missing # {}
       THEN \E s \in missing : producer' = [producer EXCEPT !.pins.snaps = @ \cup {s}]
       ELSE producer' = [producer EXCEPT !.pins.bytes = @ \cup Recorded(producer.tip),
                                         !.phase = "check"]
    /\ UNCHANGED <<snapshots, blobs, index, workers, locks, owners, starts, seen, bad, downloadVars>>

ExportCheck ==
    /\ producer.phase = "check"
    /\ producer' = [producer EXCEPT !.phase =
         IF Survives(producer.tip) THEN "blob" ELSE "release"]
    /\ UNCHANGED <<snapshots, blobs, index, workers, locks, owners, starts, seen, bad, downloadVars>>

ExportBlob ==
    /\ producer.phase = "blob"
    /\ IF producer.layer \in blobs
       THEN /\ producer' = [producer EXCEPT !.phase = "pinBlob"]
            /\ UNCHANGED blobs
       ELSE /\ producer' = [producer EXCEPT !.phase = "register",
                                              !.pins.bytes = @ \cup {producer.layer}]
            /\ blobs' = blobs \cup {producer.layer}
    /\ seen' = seen \cup {IF producer.layer \in blobs THEN "exportExisting" ELSE "exportGenerated"}
    /\ UNCHANGED <<snapshots, index, workers, locks, owners, starts, bad, downloadVars>>

ExportPinBlob ==
    /\ producer.phase = "pinBlob"
    /\ producer' = [producer EXCEPT !.phase = "checkBlob", !.pins.bytes = @ \cup {producer.layer}]
    /\ UNCHANGED <<snapshots, blobs, index, workers, locks, owners, starts, seen, bad, downloadVars>>

ExportCheckBlob ==
    /\ producer.phase = "checkBlob"
    /\ producer' = [producer EXCEPT !.phase =
         IF producer.layer \in blobs THEN "register" ELSE "release"]
    /\ UNCHANGED <<snapshots, blobs, index, workers, locks, owners, starts, seen, bad, downloadVars>>

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
    /\ producer' = [producer EXCEPT !.phase = IF producer.layer = NumLayers THEN "provider" ELSE "blob",
                                   !.layer = IF producer.layer = NumLayers THEN @ ELSE @ + 1]
    /\ bad' = bad \cup
         (IF index'[<<snapshots[producer.layer].parent, snapshots[producer.layer].layer>>] # producer.layer
          THEN {"exportRegistration"} ELSE {})
    /\ UNCHANGED <<blobs, locks, starts, seen, downloadVars>>

Consume ==
    /\ producer.phase = "provider"
    /\ producer' = [producer EXCEPT !.phase = "release"]
    /\ seen' = seen \cup {"consume"}
    /\ UNCHANGED <<snapshots, blobs, index, workers, locks, owners, starts, bad, downloadVars>>

ExportRelease ==
    /\ producer.phase = "release"
    /\ producer' = [producer EXCEPT !.phase = "done", !.pins = EmptyPins]
    /\ UNCHANGED <<snapshots, blobs, index, workers, locks, owners, starts, seen, bad, downloadVars>>

Next ==
    \/ \E i \in Importers : Start(i) \/ Lock(i) \/ Lookup(i) \/ Attach(i) \/ Validate(i)
         \/ BlobLookup(i) \/ PinBlob(i) \/ CheckBlob(i) \/ Fetch(i) \/ Fetched(i)
         \/ Apply(i) \/ Commit(i) \/ Advance(i) \/ Fail(i) \/ Cleanup(i)
         \/ FetchHeld(i) \/ FetchJoin(i) \/ (\E l \in Layers : FetchAhead(i, l)) \/ LeaveDownload(i)
         \/ Link(i) \/ RetryDownload(i) \/ OwnFailure(i)
         \/ \E o \in Owners : Adopt(i, o)
    \/ \E o \in Objects : DownloadDone(o) \/ DownloadFail(o)
    \/ \E l \in Layers : PrefetchStart(l) \/ PrefetchSettle(l) \/ PrefetchRetry(l)
         \/ PrefetchGiveUp(l) \/ PrefetchCancel(l) \/ PrefetchExpire(l)
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
    /\ dl \in [Objects -> [phase : {"none", "running", "done", "failed"},
                            holders : SUBSET Holders, owner : OwnerIDs,
                            failed : SUBSET (Holders \X OwnerIDs)]]
    /\ \A o \in Objects, h \in Holders :
         Cardinality({p \in dl[o].failed : p[1] = h}) + (IF h \in dl[o].holders THEN 1 ELSE 0) <= 1
    /\ pf \in [Layers -> [phase : {"none", "fetching", "held", "gone"}]]

ApplyExclusive == \A i, j \in Importers :
    (workers[i].phase = "applying" /\ workers[j].phase = "applying" /\ Key(i) = Key(j)) => i = j
(* Bytes started ahead of a layer's lookup are allowed; a fetch or apply *)
(* after a surviving snapshot choice still is not. ownRetry is NoOwnRetry's. *)
ReuseBeforeBytes == bad \ {"ownRetry"} = {}
(* An importer never retries a download it started itself. *)
NoOwnRetry == "ownRetry" \notin bad
(* A done download's holders, importers or the prefetcher, have not yet  *)
(* linked its blob: it stays present until the last one leaves. With a   *)
(* prefetch, this is the joined importer linking before the TTL pin can  *)
(* expire: the download's own lease holds the blob meanwhile.            *)
JoinedBlobPresent == \A o \in Objects :
    (dl[o].phase = "done" /\ dl[o].holders # {}) => Members(o) \subseteq blobs
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
         /\ workers[i].aheads = {} /\ \A o \in Objects : ~Holds(i, o)
=============================================================================
