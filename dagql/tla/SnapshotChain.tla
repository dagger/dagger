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
(* Downloads (ModelDownloads; always on in Go). With it off, an         *)
(* importer reads bytes itself (Fetch/Fetched), as Go did before the     *)
(* download coordinator, and the model is the one above.                 *)
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
(* Applying apart (ModelApart; on overlayfs outside a user namespace in  *)
(* Go, cm.applyLayersApart). From the first layer the walk found missing, *)
(* every layer's goroutine starts at once (importChainApart,              *)
(* applyChainLayer): under the layer's chain-ID lock (clocks), it skips a *)
(* layer the indexes now have, or gets its bytes and applies them into a  *)
(* parentless mutable snapshot, holding it and the lock (ap). The main    *)
(* loop takes the results in layer order: it commits an applied layer     *)
(* onto its prefix under the key lock unless the index has it, imports a  *)
(* skipped one in order, and fails on a failed one, after which the drain *)
(* waits for every goroutine and releases each later result. Applied     *)
(* snapshots are not counted against MaxSnapshots; commits are.          *)
(*                                                                     *)
(* Prefetch (ModelPrefetch; dagql/cache_part_prefetch.go). The prefetcher *)
(* joins or starts a download as holder 3 with no chain (a nil owner),   *)
(* then links the blob into a TTL lease of its own and leaves. That      *)
(* lease goes when its TTL passes, its offers are retired or an install  *)
(* consumes the blob: PrefetchExpire, enabled at any time. Once every    *)
(* blob of a chain is held, importer 2 imports it ahead of demand         *)
(* (importAhead) and keeps its ref as owner 2 until the ahead is dropped  *)
(* (retirement, replacement by another meaning, TTL) and the janitor or  *)
(* close releases it; an import that ends after the drop releases its   *)
(* own. Importer 1 and owner 1 are demand. Offers are abstracted away: a  *)
(* replacement of the same meaning changes nothing here, and any hold or *)
(* ahead may go at any time. Close cancels, waits, then releases all.     *)
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
          ModelDownloads, ModelPrefetch, ModelApart,
          MutDropDownloadPin, MutLeaveBeforeLink, MutRetryOwn, MutAheadSkipsReuse,
          MutAheadKeptOnFailure, MutApartKeptOnFailure, MutApartNoChainLock,
          MutApartSkipsReuse, MutPrefetchLeaveEarly, MutAheadKeptWhenGone,
          MutCloseSkipsDropped
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
(* With prefetch, importer 2 is the prefetcher's import ahead (importAhead) *)
(* and owner 2 the ref it keeps; importer 1 and owner 1 are demand.        *)
AheadImporter == 2
Demand(i) == ~ModelPrefetch \/ i # AheadImporter
(* An apart layer's goroutine (applyChainLayer): running, or ended with its *)
(* result waiting in its channel, holding its mutable snapshot ("applied")  *)
(* and, once acquired, its chain lock. "committed": the main loop has      *)
(* committed it and holds the lock until the layer's import ends.          *)
SlotRunning == {"lock", "fetch", "waitDl", "ready", "applying"}
SlotEnded == {"applied", "skipped", "failed"}

VARIABLES snapshots, blobs, index, workers, locks, owners, producer,
          starts, seen, bad, dl, pf, ap, clocks, ahead, pclose
downloadVars == <<dl, pf>>
(* ap[i][l]: importer i's goroutine for layer l applied apart. clocks[l]:  *)
(* the importer holding layer l's chain-ID lock, or 0.                     *)
apartVars == <<ap, clocks>>
(* ahead: the import ahead's place in the prefetcher, "live" in aheads,    *)
(* "dropped" in droppedAheads, "gone" once the janitor or close took it.   *)
(* pclose: the prefetcher's close.                                         *)
aheadVars == <<ahead, pclose>>
vars == <<snapshots, blobs, index, workers, locks, owners, producer,
          starts, seen, bad, downloadVars, apartVars, aheadVars>>

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
    /\ ap = [i \in Importers |-> [l \in Layers |-> "none"]]
    /\ clocks = [l \in Layers |-> 0]
    /\ ahead = "none"
    /\ pclose = "open"

(* A demand import starts (ImportChain). *)
Start(i) ==
    /\ Demand(i)
    /\ starts < MaxStarts
    /\ workers[i].phase \in {"idle", "done", "failed"}
    /\ \E target \in Layers : workers' = [workers EXCEPT
         ![i] = [EmptyWorker EXCEPT !.phase = "lock", !.target = target,
                                    !.first = WalkIndex(0, 1, target)]]
    /\ starts' = starts + 1
    /\ dl' = [o \in Objects |-> Disown(dl[o], i)]
    /\ UNCHANGED <<snapshots, blobs, index, locks, owners, producer, seen, bad, pf, apartVars, aheadVars>>

(* importLayer's key lock: a layer imported in order. Applying apart, the *)
(* first missing layer starts every goroutine instead (ApartStart).        *)
Lock(i) ==
    /\ workers[i].phase = "lock"
    /\ ~(ModelApart /\ workers[i].layer >= workers[i].first
          /\ ap[i][workers[i].layer] = "none")
    /\ locks[Key(i)] = 0
    /\ locks' = [locks EXCEPT ![Key(i)] = i]
    /\ workers' = [workers EXCEPT ![i].phase = "lookup"]
    /\ UNCHANGED <<snapshots, blobs, index, owners, producer, starts, seen, bad, downloadVars, apartVars, aheadVars>>

(* Where a miss goes: bytes, or the commit of the layer applied apart.   *)
Miss(i) == IF ap[i][workers[i].layer] = "applied" THEN "commit" ELSE "blob"

(* A miss goes to bytes, or with a layer applied apart to its commit     *)
(* (commitAppliedLayer's reuseImportedLayer).                            *)
Lookup(i) ==
    /\ workers[i].phase = "lookup"
    /\ workers' = [workers EXCEPT ![i].handle = Candidate(i),
         ![i].snapshotChoice = IF Indexed(i) THEN Candidate(i) ELSE 0,
         ![i].phase = IF ap[i][workers[i].layer] = "applied" /\
                         (Candidate(i) = 0 \/ MutApartSkipsReuse)
                      THEN "commit"
                      ELSE IF Candidate(i) = 0 \/
                         (MutAheadSkipsReuse /\ workers[i].layer \in workers[i].aheads)
                      THEN Miss(i) ELSE "attach"]
    /\ bad' = bad \cup (IF Indexed(i) /\ workers'[i].handle # Candidate(i)
                           THEN {"snapshotSelection"} ELSE {})
                   \cup (IF Indexed(i) /\ workers'[i].phase = "commit"
                           THEN {"commitAfterSnapshotChoice"} ELSE {})
    /\ UNCHANGED <<snapshots, blobs, index, locks, owners, producer, starts, seen, downloadVars, apartVars, aheadVars>>

Attach(i) ==
    /\ workers[i].phase = "attach"
    /\ LET missing == Ancestry(workers[i].handle) \ workers[i].pins.snaps IN
       IF missing # {}
       THEN \E s \in missing : workers' = [workers EXCEPT ![i].pins.snaps = @ \cup {s}]
       ELSE workers' = [workers EXCEPT ![i].pins.bytes = @ \cup Recorded(workers[i].handle),
                                       ![i].phase = "validate"]
    /\ UNCHANGED <<snapshots, blobs, index, locks, owners, producer, starts, seen, bad, downloadVars, apartVars, aheadVars>>

(* A lost candidate's pins go; the blobs layers applied apart linked into *)
(* the import's lease stay.                                               *)
Validate(i) ==
    /\ workers[i].phase = "validate"
    /\ IF Survives(workers[i].handle)
       THEN /\ workers' = [workers EXCEPT ![i].phase = "advance"]
            /\ seen' = seen \cup {"snapshotReuse"} \cup
                 (IF \E o \in Owners : workers[i].handle \in owners[o].pins.snaps
                  THEN {"sharedOwner"} ELSE {}) \cup
                 (IF ModelPrefetch /\ Demand(i) /\
                     workers[i].handle \in owners[AheadImporter].pins.snaps
                  THEN {"aheadReuse"} ELSE {}) \cup
                 (IF ap[i][workers[i].layer] = "applied" THEN {"apartReuse"} ELSE {})
            /\ UNCHANGED index
       ELSE /\ workers' = [workers EXCEPT ![i].phase = Miss(i), ![i].handle = 0,
                  ![i].pins = [Pin(EmptyPins, workers[i].prefix) EXCEPT !.bytes = @ \cup
                      {b \in workers[i].pins.bytes : ModelApart /\ b >= workers[i].layer}]]
            /\ index' = [index EXCEPT ![Key(i)] = 0]
            /\ seen' = seen \cup {"lostCandidate"}
    /\ UNCHANGED <<snapshots, blobs, locks, owners, producer, starts, bad, downloadVars, apartVars, aheadVars>>

BlobLookup(i) ==
    /\ workers[i].phase = "blob"
    /\ workers' = [workers EXCEPT ![i].blobChoice = workers[i].layer \in blobs,
         ![i].phase = IF workers[i].layer \in blobs THEN "pinBlob" ELSE "fetch"]
    /\ bad' = bad \cup (IF workers[i].layer \in blobs /\ workers'[i].phase # "pinBlob"
                           THEN {"blobSelection"} ELSE {})
    /\ UNCHANGED <<snapshots, blobs, index, locks, owners, producer, starts, seen, downloadVars, apartVars, aheadVars>>

PinBlob(i) ==
    /\ workers[i].phase = "pinBlob"
    /\ workers' = [workers EXCEPT ![i].pins.bytes = @ \cup {workers[i].layer},
                                  ![i].phase = "checkBlob"]
    /\ UNCHANGED <<snapshots, blobs, index, locks, owners, producer, starts, seen, bad, downloadVars, apartVars, aheadVars>>

CheckBlob(i) ==
    /\ workers[i].phase = "checkBlob"
    /\ workers' = [workers EXCEPT ![i].blobChoice = workers[i].layer \in blobs,
         ![i].phase = IF workers[i].layer \in blobs THEN "ready" ELSE "fetch"]
    /\ seen' = IF workers[i].layer \in blobs THEN seen \cup {"blobReuse"} ELSE seen
    /\ UNCHANGED <<snapshots, blobs, index, locks, owners, producer, starts, bad, downloadVars, apartVars, aheadVars>>

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
    /\ UNCHANGED <<snapshots, blobs, index, locks, owners, producer, starts, downloadVars, apartVars, aheadVars>>

(* With downloads: fetchChainContent waits on its own download started   *)
(* ahead (held), or joins one in progress, or starts one (joinFetch).    *)
FetchHeld(i) ==
    /\ ModelDownloads
    /\ workers[i].phase = "fetch"
    /\ workers[i].layer \in workers[i].aheads
    /\ workers' = [workers EXCEPT ![i].phase = "waitDl"]
    /\ seen' = seen \cup {"fetch", "aheadUsed"}
    /\ bad' = FetchBad(i)
    /\ UNCHANGED <<snapshots, blobs, index, locks, owners, producer, starts, downloadVars, apartVars, aheadVars>>

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
    /\ UNCHANGED <<snapshots, blobs, index, locks, owners, producer, starts, pf, apartVars, aheadVars>>

(* fill: at a layer's loop top from the first layer the walk found       *)
(* missing, or once this layer's bytes are in (chain.fetched), before its *)
(* apply, a later layer's download starts, unless its blob is present.   *)
FetchAhead(i, l) ==
    /\ ModelDownloads
    /\ \/ workers[i].phase = "ready"
       \/ workers[i].phase = "lock" /\ workers[i].layer >= workers[i].first
    /\ l \in (workers[i].layer + 1)..workers[i].target
    /\ l \notin workers[i].aheads
    /\ ap[i][l] = "none"
    /\ l \notin blobs
    /\ dl' = [dl EXCEPT ![Src(l)] = Join(Src(l), i)]
    /\ workers' = [workers EXCEPT ![i].aheads = @ \cup {l}]
    /\ seen' = seen \cup {"fetchAhead"}
         \cup (IF workers[i].aheads # {} THEN {"aheadSet"} ELSE {})
         \cup (IF workers[i].first > 1 THEN {"aheadPastPrefix"} ELSE {})
    /\ UNCHANGED <<snapshots, blobs, index, locks, owners, producer, starts, bad, pf, apartVars, aheadVars>>

(* runFetch ends: the content lands, held by the download's lease.       *)
DownloadDone(o) ==
    /\ dl[o].phase = "running"
    /\ dl' = [dl EXCEPT ![o].phase = "done"]
    /\ blobs' = blobs \cup DoneBlobs(o)
    /\ seen' = seen \cup {"downloadDone"}
    /\ UNCHANGED <<snapshots, index, workers, locks, owners, producer, starts, bad, pf, apartVars, aheadVars>>

DownloadFail(o) ==
    /\ SourceCanFail
    /\ dl[o].phase = "running"
    /\ dl' = [dl EXCEPT ![o].phase = "failed"]
    /\ blobs' = blobs \cup FailBlobs(o)
    /\ seen' = seen \cup {"downloadFail"}
    /\ UNCHANGED <<snapshots, index, workers, locks, owners, producer, starts, bad, pf, apartVars, aheadVars>>

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
    /\ UNCHANGED <<snapshots, blobs, index, locks, owners, producer, starts, bad, pf, apartVars, aheadVars>>

(* Reached only with MutLeaveBeforeLink: the link after the hold went.   *)
Link(i) ==
    /\ workers[i].phase = "link"
    /\ workers' = [workers EXCEPT ![i].phase = "ready",
                                  ![i].pins.bytes = @ \cup {workers[i].layer}]
    /\ UNCHANGED <<snapshots, blobs, index, locks, owners, producer, starts, seen, bad, downloadVars, apartVars, aheadVars>>

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
    /\ UNCHANGED <<snapshots, blobs, index, locks, owners, producer, starts, pf, apartVars, aheadVars>>

(* Its own attempt failed: the import fails (Cleanup lets go).           *)
OwnFailure(i) ==
    /\ ModelDownloads
    /\ workers[i].phase = "waitDl"
    /\ LET o == Src(workers[i].layer) IN
       /\ AttemptPhase(i, o) = "failed"
       /\ AttemptOwner(i, o) = i
    /\ workers' = [workers EXCEPT ![i].phase = "cleanup"]
    /\ seen' = seen \cup {"ownFailure"}
    /\ UNCHANGED <<snapshots, blobs, index, locks, owners, producer, starts, bad, downloadVars, apartVars, aheadVars>>


(* Applying apart (ModelApart; import.go importChainApart).              *)
(* importChainApart: from the first layer the walk found missing, each    *)
(* layer's goroutine starts (applyChainLayer) with its download held      *)
(* ahead, if any (chainAhead.take). chainAheadWindow exceeds every chain  *)
(* here, so all start at once; a started goroutine may still wait.       *)
ApartStart(i) ==
    /\ ModelApart
    /\ workers[i].phase = "lock"
    /\ workers[i].layer >= workers[i].first
    /\ ap[i][workers[i].layer] = "none"
    /\ ap' = [ap EXCEPT ![i] = [l \in Layers |->
               IF l >= workers[i].layer /\ l <= workers[i].target THEN "lock" ELSE @[l]]]
    /\ workers' = [workers EXCEPT ![i].phase = "await"]
    /\ seen' = seen \cup {"apart"}
    /\ UNCHANGED <<snapshots, blobs, index, locks, owners, producer, starts, bad,
                   downloadVars, clocks, aheadVars>>

(* A goroutine ends: it lets go of layer l's download (applyChainLayer's *)
(* deferred leaveFetch or dropFetch of held; a joined one it left after   *)
(* linking, or drops now on failure).                                     *)
SlotEnd(i, l) ==
    /\ workers' = [workers EXCEPT ![i].aheads = @ \ {l}]
    /\ dl' = [dl EXCEPT ![Src(l)] = Drop(Src(l), i)]

(* applyChainLayer takes layer l's chain-ID lock (acquireImportLock on    *)
(* chain/<chainID>): an import of the same layers holding it has not yet *)
(* committed the layer. Under it, chainLocalPrefix says whether the       *)
(* indexes now have the layers up to l: the goroutine then ends with no  *)
(* snapshot, and the layer is imported in order, which reuses it.         *)
(* MutApartNoChainLock takes no lock.                                     *)
SlotLock(i, l) ==
    /\ ap[i][l] = "lock"
    /\ MutApartNoChainLock \/ clocks[l] = 0
    /\ clocks' = IF MutApartNoChainLock THEN clocks ELSE [clocks EXCEPT ![l] = i]
    /\ IF WalkIndex(0, 1, l) > l
       THEN /\ ap' = [ap EXCEPT ![i][l] = "skipped"]
            /\ SlotEnd(i, l)
            /\ seen' = seen \cup {"apartSkip"}
       ELSE /\ ap' = [ap EXCEPT ![i][l] = "fetch"]
            /\ UNCHANGED <<workers, dl, seen>>
    /\ UNCHANGED <<snapshots, blobs, index, locks, owners, producer, starts, bad, pf, aheadVars>>

(* fetchLayerContent, importLayerContent: a present blob is pinned        *)
(* (pinContent, pin and check in one step here); else the download held  *)
(* ahead is awaited, or one is joined or started (fetchChainContent).     *)
SlotFetch(i, l) ==
    /\ ap[i][l] = "fetch"
    /\ IF l \in blobs
       THEN /\ workers' = [workers EXCEPT ![i].pins.bytes = @ \cup {l}]
            /\ ap' = [ap EXCEPT ![i][l] = "ready"]
            /\ UNCHANGED <<dl, seen>>
       ELSE /\ ap' = [ap EXCEPT ![i][l] = "waitDl"]
            /\ dl' = IF l \in workers[i].aheads THEN dl
                     ELSE [dl EXCEPT ![Src(l)] = Join(Src(l), i)]
            /\ seen' = seen \cup {"fetch"} \cup
                 (IF l \notin workers[i].aheads /\ dl[Src(l)].phase # "none"
                  THEN {"apartJoin"} ELSE {})
            /\ UNCHANGED workers
    /\ UNCHANGED <<snapshots, blobs, index, locks, owners, producer, starts, bad, pf,
                   clocks, aheadVars>>

(* fetchChainContent: the blob is linked into the import's lease while    *)
(* the download holds it; a joined download is then let go, one held     *)
(* ahead when the goroutine ends.                                         *)
SlotWait(i, l) ==
    /\ ap[i][l] = "waitDl"
    /\ AttemptPhase(i, Src(l)) = "done"
    /\ workers' = [workers EXCEPT ![i].pins.bytes = @ \cup {l}]
    /\ dl' = IF l \in workers[i].aheads THEN dl
             ELSE [dl EXCEPT ![Src(l)] = Leave(dl[Src(l)], i)]
    /\ ap' = [ap EXCEPT ![i][l] = "ready"]
    /\ seen' = seen \cup {"downloadUsed"}
    /\ UNCHANGED <<snapshots, blobs, index, locks, owners, producer, starts, bad, pf,
                   clocks, aheadVars>>

(* fetchChainContent: another import's failed download is retried, as by *)
(* RetryDownload; the goroutine's own failed one fails it.               *)
SlotRetry(i, l) ==
    /\ ap[i][l] = "waitDl"
    /\ AttemptPhase(i, Src(l)) = "failed"
    /\ MutRetryOwn \/ AttemptOwner(i, Src(l)) # i
    /\ dl' = [dl EXCEPT ![Src(l)] = Retry(Src(l), i)]
    /\ bad' = bad \cup (IF AttemptOwner(i, Src(l)) = i THEN {"ownRetry"} ELSE {})
    /\ seen' = seen \cup RetrySeen(Src(l), i)
    /\ workers' = [workers EXCEPT ![i].aheads = @ \ {l}]
    /\ UNCHANGED <<snapshots, blobs, index, locks, owners, producer, starts, pf, apartVars,
                   aheadVars>>

SlotOwnFailure(i, l) ==
    /\ ap[i][l] = "waitDl"
    /\ AttemptPhase(i, Src(l)) = "failed"
    /\ AttemptOwner(i, Src(l)) = i
    /\ ap' = [ap EXCEPT ![i][l] = "failed"]
    /\ SlotEnd(i, l)
    /\ seen' = seen \cup {"ownFailure"}
    /\ UNCHANGED <<snapshots, blobs, index, locks, owners, producer, starts, bad, pf, clocks,
                   aheadVars>>

(* cm.New with no parent, then applyLayer into it (upperOverlay).         *)
SlotApply(i, l) ==
    /\ ap[i][l] = "ready"
    /\ ap' = [ap EXCEPT ![i][l] = "applying"]
    /\ seen' = seen \cup {"apply"}
    /\ UNCHANGED <<snapshots, blobs, index, workers, locks, owners, producer, starts, bad,
                   downloadVars, clocks, aheadVars>>

(* applyChainLayer returns its mutable snapshot, still holding the lock.  *)
SlotApplied(i, l) ==
    /\ ap[i][l] = "applying"
    /\ ap' = [ap EXCEPT ![i][l] = "applied"]
    /\ SlotEnd(i, l)
    /\ seen' = seen \cup (IF \E m \in Layers : m < l /\ ap[i][m] \in SlotRunning
                          THEN {"apartOutOfOrder"} ELSE {})
    /\ UNCHANGED <<snapshots, blobs, index, locks, owners, producer, starts, bad, pf, clocks,
                   aheadVars>>

(* A source, apply or lock error, or the import's cancellation (applyCtx, *)
(* canceled by the drain): the goroutine releases its mutable snapshot    *)
(* and returns the error, with the chain lock if it took it.              *)
SlotFail(i, l) ==
    /\ ap[i][l] \in SlotRunning
    /\ SourceCanFail \/ workers[i].phase = "cleanup"
    /\ ap' = [ap EXCEPT ![i][l] = "failed"]
    /\ SlotEnd(i, l)
    /\ UNCHANGED <<snapshots, blobs, index, locks, owners, producer, starts, seen, bad, pf,
                   clocks, aheadVars>>

(* importChainApart's <-results[i], in layer order: a failed layer fails *)
(* the import; an applied one is committed and a skipped one imported in *)
(* order (importInOrder), both under the layer's key lock (Lock).         *)
Await(i) ==
    /\ workers[i].phase = "await"
    /\ ap[i][workers[i].layer] \in SlotEnded
    /\ workers' = [workers EXCEPT ![i].phase =
         IF ap[i][workers[i].layer] = "failed" THEN "cleanup" ELSE "lock"]
    /\ UNCHANGED <<snapshots, blobs, index, locks, owners, producer, starts, seen, bad,
                   downloadVars, apartVars, aheadVars>>

(* commitAppliedLayer after a miss: commitOnto rebases the mutable        *)
(* snapshot onto the import's prefix, and recordImportedLayer indexes it. *)
CommitApart(i) ==
    /\ workers[i].phase = "commit"
    /\ Len(snapshots) < MaxSnapshots
    /\ LET s == Len(snapshots) + 1 IN
       /\ snapshots' = Append(snapshots, NewSnapshot(workers[i].prefix, workers[i].layer, TRUE))
       /\ workers' = [workers EXCEPT ![i].handle = s, ![i].phase = "advance",
                                     ![i].pins.snaps = @ \cup {s}]
       /\ index' = [index EXCEPT ![Key(i)] = s]
    /\ ap' = [ap EXCEPT ![i][workers[i].layer] = "committed"]
    /\ bad' = bad \cup
         (IF Survives(workers[i].snapshotChoice) THEN {"commitAfterSnapshotChoice"} ELSE {})
    /\ seen' = seen \cup {"apartCommit"}
    /\ UNCHANGED <<blobs, locks, owners, producer, starts, downloadVars, clocks, aheadVars>>

(* Prefetch (ModelPrefetch; dagql/cache_part_prefetch.go). Offers are   *)
(* not modeled: each layer's blob has at most one prefetch, and any       *)
(* retirement, replacement, TTL or consumption may end it at any time.    *)
(* A job's download of a blob it wants, absent from the content store    *)
(* (partPrefetcher.run and fetch; snapshots PrefetchContent): it joins or *)
(* starts the download, as no chain (a nil owner).                       *)
PrefetchStart(l) ==
    /\ ModelPrefetch /\ ModelDownloads
    /\ pclose = "open"
    /\ pf[l].phase = "none"
    /\ l \notin blobs
    /\ dl' = [dl EXCEPT ![Src(l)] = Join(Src(l), Prefetcher)]
    /\ pf' = [pf EXCEPT ![l].phase = "fetching"]
    /\ seen' = seen \cup {"prefetchStart"}
    /\ UNCHANGED <<snapshots, blobs, index, workers, locks, owners, producer, starts, bad, apartVars, aheadVars>>

(* PrefetchContent's fetchChainContent links the blob into the TTL lease  *)
(* while the download holds it, then leaves; settle makes it a hold.      *)
(* MutPrefetchLeaveEarly leaves first and links after (PrefetchLink).     *)
PrefetchSettle(l) ==
    /\ pf[l].phase = "fetching"
    /\ AttemptPhase(Prefetcher, Src(l)) = "done"
    /\ pf' = [pf EXCEPT ![l].phase = IF MutPrefetchLeaveEarly THEN "linking" ELSE "held"]
    /\ dl' = [dl EXCEPT ![Src(l)] = Leave(dl[Src(l)], Prefetcher)]
    /\ seen' = seen \cup {"prefetchHeld"}
    /\ UNCHANGED <<snapshots, blobs, index, workers, locks, owners, producer, starts, bad, apartVars, aheadVars>>

(* Reached only with MutPrefetchLeaveEarly: the link after the leave.     *)
PrefetchLink(l) ==
    /\ pf[l].phase = "linking"
    /\ pf' = [pf EXCEPT ![l].phase = "held"]
    /\ UNCHANGED <<snapshots, blobs, index, workers, locks, owners, producer, starts, seen, bad, dl, apartVars, aheadVars>>

(* fetchChainContent: another's failed download is retried; the          *)
(* prefetch's own failed one ends it (settle with an error, no hold).     *)
PrefetchRetry(l) ==
    /\ pf[l].phase = "fetching"
    /\ AttemptPhase(Prefetcher, Src(l)) = "failed"
    /\ AttemptOwner(Prefetcher, Src(l)) # Prefetcher
    /\ dl' = [dl EXCEPT ![Src(l)] = Retry(Src(l), Prefetcher)]
    /\ seen' = seen \cup {"prefetchRetry"} \cup
         (IF "retryJoin" \in RetrySeen(Src(l), Prefetcher) THEN {"prefetchRetryJoin"} ELSE {})
    /\ UNCHANGED <<snapshots, blobs, index, workers, locks, owners, producer, starts, bad, pf, apartVars, aheadVars>>

PrefetchGiveUp(l) ==
    /\ pf[l].phase = "fetching"
    /\ AttemptPhase(Prefetcher, Src(l)) = "failed"
    /\ AttemptOwner(Prefetcher, Src(l)) = Prefetcher
    /\ pf' = [pf EXCEPT ![l].phase = "gone"]
    /\ dl' = [dl EXCEPT ![Src(l)] = Drop(Src(l), Prefetcher)]
    /\ UNCHANGED <<snapshots, blobs, index, workers, locks, owners, producer, starts, seen, bad, apartVars, aheadVars>>

(* The job is canceled mid-download: its offer retired or replaced        *)
(* (goneLocked, cancelUnwantedLocked), or the prefetcher closing.         *)
PrefetchCancel(l) ==
    /\ pf[l].phase = "fetching"
    /\ pf' = [pf EXCEPT ![l].phase = "gone"]
    /\ dl' = [dl EXCEPT ![Src(l)] = Drop(Src(l), Prefetcher)]
    /\ seen' = seen \cup {"prefetchCancel"}
    /\ UNCHANGED <<snapshots, blobs, index, workers, locks, owners, producer, starts, bad, apartVars, aheadVars>>

(* The hold's lease is released: its offers retired (goneLocked, then     *)
(* releaseIdle), its TTL passed (releaseIdle), or an install pinned the   *)
(* blob (partPrefetchConsumed). A replacement passes holds on.           *)
PrefetchExpire(l) ==
    /\ pf[l].phase = "held"
    /\ pf' = [pf EXCEPT ![l].phase = "gone"]
    /\ seen' = seen \cup {"prefetchExpire"}
    /\ UNCHANGED <<snapshots, blobs, index, workers, locks, owners, producer, starts, bad, dl, apartVars, aheadVars>>

(* checkAheadLocked: once every blob of an offer's chain is held, its     *)
(* chain is imported ahead of demand (importAhead's ImportChain), by the  *)
(* AheadImporter, under a chain owner of its own. One import ahead at a  *)
(* time here; Go may run a gone one beside its replacement's.            *)
AheadStart ==
    /\ ModelPrefetch
    /\ pclose = "open"
    /\ ahead \in {"none", "gone"}
    /\ starts < MaxStarts
    /\ workers[AheadImporter].phase \in {"idle", "done", "failed"}
    /\ owners[AheadImporter].tip = 0
    /\ \E target \in Layers :
         /\ \A l \in 1..target : pf[l].phase = "held"
         /\ workers' = [workers EXCEPT ![AheadImporter] = [EmptyWorker EXCEPT
              !.phase = "lock", !.target = target, !.first = WalkIndex(0, 1, target)]]
    /\ starts' = starts + 1
    /\ dl' = [o \in Objects |-> Disown(dl[o], AheadImporter)]
    /\ ahead' = "live"
    /\ seen' = seen \cup {"aheadStart"}
    /\ UNCHANGED <<snapshots, blobs, index, locks, owners, producer, bad, pf, apartVars, pclose>>

(* importAhead's import returned its ref: kept (a.ref) while the ahead is *)
(* in aheads and the prefetcher open; else released, and taken out of     *)
(* aheads if still there. MutAheadKeptWhenGone keeps it once gone too.    *)
AheadEnd ==
    /\ ModelPrefetch
    /\ workers[AheadImporter].phase = "returned"
    /\ LET w == workers[AheadImporter]
           keep == pclose = "open" /\ (ahead = "live" \/ MutAheadKeptWhenGone) IN
       /\ owners' = IF keep
                    THEN [owners EXCEPT ![AheadImporter] =
                           [tip |-> w.prefix, pins |-> Pin(EmptyPins, w.prefix),
                            expected |-> Recorded(w.prefix) \cap blobs]]
                    ELSE owners
       /\ ahead' = IF ~keep /\ ahead = "live" THEN "none" ELSE ahead
       /\ seen' = seen \cup {IF keep THEN "aheadKept" ELSE "aheadReleased"}
    /\ workers' = [workers EXCEPT ![AheadImporter] = [@ EXCEPT !.phase = "done",
                     !.pins = EmptyPins, !.prefix = 0, !.handle = 0]]
    /\ UNCHANGED <<snapshots, blobs, index, locks, producer, starts, bad, downloadVars,
                   apartVars, pclose>>

(* importAhead's import failed or was canceled: an ahead still in aheads  *)
(* is taken out (forgetAheadLocked).                                       *)
AheadFailed ==
    /\ ModelPrefetch
    /\ workers[AheadImporter].phase = "failed"
    /\ ahead = "live"
    /\ ahead' = "none"
    /\ UNCHANGED <<snapshots, blobs, index, workers, locks, owners, producer, starts, seen, bad,
                   downloadVars, apartVars, pclose>>

(* dropAheadLocked: the offer retired or replaced by one of another       *)
(* meaning (goneLocked), or the ahead's TTL passed (releaseIdle). It      *)
(* cancels a running import (Fail) and goes to droppedAheads.            *)
AheadDrop ==
    /\ ahead = "live"
    /\ ahead' = "dropped"
    /\ seen' = seen \cup {"aheadDrop"}
    /\ UNCHANGED <<snapshots, blobs, index, workers, locks, owners, producer, starts, bad,
                   downloadVars, apartVars, pclose>>

(* The janitor (releaseIdle) releases a dropped ahead's ref, if its       *)
(* import had kept one; a still running import releases its own.          *)
AheadJanitor ==
    /\ ahead = "dropped"
    /\ pclose # "closed"
    /\ ahead' = "gone"
    /\ owners' = [owners EXCEPT ![AheadImporter] = [tip |-> 0, pins |-> EmptyPins, expected |-> {}]]
    /\ UNCHANGED <<snapshots, blobs, index, workers, locks, producer, starts, seen, bad,
                   downloadVars, apartVars, pclose>>

(* closePartPrefetch sets closed and cancels every job and import ahead.  *)
PrefetchClose ==
    /\ ModelPrefetch
    /\ pclose = "open"
    /\ pclose' = "closing"
    /\ seen' = seen \cup {"prefetchClose"}
    /\ UNCHANGED <<snapshots, blobs, index, workers, locks, owners, producer, starts, bad,
                   downloadVars, apartVars, ahead>>

(* closePartPrefetch, after wg.Wait (every job and import ahead ended):   *)
(* every hold, and every ahead in aheads or droppedAheads, is released.   *)
(* MutCloseSkipsDropped releases none in droppedAheads.                   *)
PrefetchCloseDone ==
    /\ pclose = "closing"
    /\ \A l \in Layers : pf[l].phase \notin {"fetching", "linking"}
    /\ workers[AheadImporter].phase \in {"idle", "done", "failed"}
    /\ pf' = [l \in Layers |-> IF pf[l].phase = "held" THEN [phase |-> "gone"] ELSE pf[l]]
    /\ LET tracked == ahead = "live" \/ (ahead = "dropped" /\ ~MutCloseSkipsDropped) IN
       /\ owners' = IF tracked
                    THEN [owners EXCEPT ![AheadImporter] =
                           [tip |-> 0, pins |-> EmptyPins, expected |-> {}]]
                    ELSE owners
       /\ ahead' = IF tracked THEN "gone" ELSE ahead
    /\ pclose' = "closed"
    /\ UNCHANGED <<snapshots, blobs, index, workers, locks, producer, starts, seen, bad, dl,
                   apartVars>>

Fetched(i) ==
    /\ workers[i].phase = "reading"
    /\ blobs' = blobs \cup {workers[i].layer}
    /\ workers' = [workers EXCEPT ![i].phase = "ready", ![i].write = FALSE,
                                  ![i].pins.bytes = @ \cup {workers[i].layer}]
    /\ UNCHANGED <<snapshots, index, locks, owners, producer, starts, seen, bad, downloadVars, apartVars, aheadVars>>

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
    /\ UNCHANGED <<snapshots, blobs, index, locks, owners, producer, starts, downloadVars, apartVars, aheadVars>>

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
    /\ UNCHANGED <<blobs, locks, owners, producer, starts, seen, bad, downloadVars, apartVars, aheadVars>>

(* The import of this layer ends: its ahead download, if held, is let   *)
(* go (import.go leaveFetch); those of later layers stay held. Applied   *)
(* apart, the layer's result is released (appliedLayer.release: its      *)
(* mutable snapshot if reused instead, and its chain lock), and the next *)
(* layer's result is awaited (importChainApart's loop).                  *)
Advance(i) ==
    /\ workers[i].phase = "advance"
    /\ LET held == workers[i].layer \in workers[i].aheads
           l == workers[i].layer IN
       /\ workers' = [workers EXCEPT ![i].prefix = workers[i].handle,
            ![i].phase = IF l = workers[i].target THEN "returned"
                         ELSE IF ap[i][l + 1] # "none" THEN "await" ELSE "lock",
            ![i].layer = IF workers[i].layer = workers[i].target THEN @ ELSE @ + 1,
            ![i].aheads = @ \ {workers[i].layer}]
       /\ dl' = IF held THEN [dl EXCEPT ![Src(workers[i].layer)] =
                                  Drop(Src(workers[i].layer), i)]
                ELSE dl
       /\ seen' = IF held /\ workers[i].snapshotChoice # 0
                  THEN seen \cup {"aheadUnused"} ELSE seen
       /\ ap' = [ap EXCEPT ![i][l] = "none"]
       /\ clocks' = [clocks EXCEPT ![l] = IF @ = i THEN 0 ELSE @]
    /\ locks' = [locks EXCEPT ![Key(i)] = 0]
    /\ UNCHANGED <<snapshots, blobs, index, owners, producer, starts, bad, pf, aheadVars>>

(* Owner attachment may take several metadata writes in Go. Operation    *)
(* pins protect the whole closure until all succeed, so GC cannot change *)
(* the resources during this action. Failure keeps the operation owner.  *)
(* With prefetch, demand adopts into its own owner; the import ahead's    *)
(* ref is kept by AheadEnd.                                              *)
Adopt(i, o) ==
    /\ workers[i].phase = "returned"
    /\ Demand(i)
    /\ ModelPrefetch => o = i
    /\ owners' = [owners EXCEPT ![o] =
         [tip |-> workers[i].prefix, pins |-> Pin(EmptyPins, workers[i].prefix),
          expected |-> Recorded(workers[i].prefix) \cap blobs]]
    /\ workers' = [workers EXCEPT ![i].phase = "done", ![i].pins = EmptyPins,
                                  ![i].prefix = 0, ![i].handle = 0]
    /\ seen' = seen \cup {"adopt"}
    /\ UNCHANGED <<snapshots, blobs, index, locks, producer, starts, bad, downloadVars, apartVars, aheadVars>>

(* A demand owner releases its ref. *)
Release(o) ==
    /\ Demand(o)
    /\ owners[o].tip # 0
    /\ owners' = [owners EXCEPT ![o] = [tip |-> 0, pins |-> EmptyPins, expected |-> {}]]
    /\ UNCHANGED <<snapshots, blobs, index, workers, locks, producer, starts, seen, bad, downloadVars, apartVars, aheadVars>>

(* Failure includes a canceled source/apply or a caller abandoning a ref. *)
Fail(i) ==
    /\ SourceCanFail
    /\ workers[i].phase \notin {"idle", "done", "failed", "cleanup"}
    /\ workers' = [workers EXCEPT ![i].phase = "cleanup"]
    /\ seen' = IF workers[i].prefix # 0 THEN seen \cup {"prefixFailure"} ELSE seen
    /\ UNCHANGED <<snapshots, blobs, index, locks, owners, producer, starts, bad, downloadVars, apartVars, aheadVars>>

(* Cleanup lets go of every download the importer holds (dropFetch, and   *)
(* chainAhead.drop for those held ahead). MutAheadKeptOnFailure keeps the *)
(* holds of later layers' downloads. Applying apart, it first waits for   *)
(* every layer's goroutine to end, then releases each result: mutable     *)
(* snapshot and chain lock (importChainApart's deferred drain).           *)
(* MutApartKeptOnFailure releases none past the failed layer.             *)
Cleanup(i) ==
    /\ workers[i].phase = "cleanup"
    /\ \A l \in Layers : ap[i][l] \notin SlotRunning
    /\ LET kept == {l \in Layers : MutApartKeptOnFailure /\ l > workers[i].layer /\
                                    ap[i][l] # "none"} IN
       /\ ap' = [ap EXCEPT ![i] = [l \in Layers |-> IF l \in kept THEN @[l] ELSE "none"]]
       /\ clocks' = [l \in Layers |-> IF clocks[l] = i /\ l \notin kept THEN 0 ELSE clocks[l]]
    /\ workers' = [workers EXCEPT ![i] = [EmptyWorker EXCEPT !.phase = "failed"]]
    /\ locks' = [k \in Keys |-> IF locks[k] = i THEN 0 ELSE locks[k]]
    /\ dl' = IF MutAheadKeptOnFailure
             THEN [o \in Objects |->
                     IF \E l \in workers[i].aheads : l # workers[i].layer /\ Src(l) = o
                     THEN dl[o] ELSE Drop(o, i)]
             ELSE LeaveAll(i)
    /\ seen' = IF \E l \in Layers : l > workers[i].layer /\ ap[i][l] = "applied"
               THEN seen \cup {"apartReleaseApplied"} ELSE seen
    /\ UNCHANGED <<snapshots, blobs, index, owners, producer, starts, bad, pf, aheadVars>>

(* GC may remove an unowned suffix or whole chain. Index rows remain hints. *)
Prune(s) ==
    /\ s \in Present
    /\ LET removed == {a \in Present : s \in Ancestry(a)} IN
       /\ removed \cap PinnedSnapshots = {}
       /\ snapshots' = [a \in IDs |->
            IF a \in removed THEN [snapshots[a] EXCEPT !.present = FALSE] ELSE snapshots[a]]
    /\ seen' = seen \cup {"prune"}
    /\ UNCHANGED <<blobs, index, workers, locks, owners, producer, starts, bad, downloadVars, apartVars, aheadVars>>

PruneBlob(b) ==
    /\ b \in blobs \ PinnedBytes
    /\ blobs' = blobs \ {b}
    /\ UNCHANGED <<snapshots, index, workers, locks, owners, producer, starts, seen, bad, downloadVars, apartVars, aheadVars>>

ExportAttach ==
    /\ producer.phase = "select"
    /\ LET missing == Ancestry(producer.tip) \ producer.pins.snaps IN
       IF missing # {}
       THEN \E s \in missing : producer' = [producer EXCEPT !.pins.snaps = @ \cup {s}]
       ELSE producer' = [producer EXCEPT !.pins.bytes = @ \cup Recorded(producer.tip),
                                         !.phase = "check"]
    /\ UNCHANGED <<snapshots, blobs, index, workers, locks, owners, starts, seen, bad, downloadVars, apartVars, aheadVars>>

ExportCheck ==
    /\ producer.phase = "check"
    /\ producer' = [producer EXCEPT !.phase =
         IF Survives(producer.tip) THEN "blob" ELSE "release"]
    /\ UNCHANGED <<snapshots, blobs, index, workers, locks, owners, starts, seen, bad, downloadVars, apartVars, aheadVars>>

ExportBlob ==
    /\ producer.phase = "blob"
    /\ IF producer.layer \in blobs
       THEN /\ producer' = [producer EXCEPT !.phase = "pinBlob"]
            /\ UNCHANGED blobs
       ELSE /\ producer' = [producer EXCEPT !.phase = "register",
                                              !.pins.bytes = @ \cup {producer.layer}]
            /\ blobs' = blobs \cup {producer.layer}
    /\ seen' = seen \cup {IF producer.layer \in blobs THEN "exportExisting" ELSE "exportGenerated"}
    /\ UNCHANGED <<snapshots, index, workers, locks, owners, starts, bad, downloadVars, apartVars, aheadVars>>

ExportPinBlob ==
    /\ producer.phase = "pinBlob"
    /\ producer' = [producer EXCEPT !.phase = "checkBlob", !.pins.bytes = @ \cup {producer.layer}]
    /\ UNCHANGED <<snapshots, blobs, index, workers, locks, owners, starts, seen, bad, downloadVars, apartVars, aheadVars>>

ExportCheckBlob ==
    /\ producer.phase = "checkBlob"
    /\ producer' = [producer EXCEPT !.phase =
         IF producer.layer \in blobs THEN "register" ELSE "release"]
    /\ UNCHANGED <<snapshots, blobs, index, workers, locks, owners, starts, seen, bad, downloadVars, apartVars, aheadVars>>

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
    /\ UNCHANGED <<blobs, locks, starts, seen, downloadVars, apartVars, aheadVars>>

Consume ==
    /\ producer.phase = "provider"
    /\ producer' = [producer EXCEPT !.phase = "release"]
    /\ seen' = seen \cup {"consume"}
    /\ UNCHANGED <<snapshots, blobs, index, workers, locks, owners, starts, bad, downloadVars, apartVars, aheadVars>>

ExportRelease ==
    /\ producer.phase = "release"
    /\ producer' = [producer EXCEPT !.phase = "done", !.pins = EmptyPins]
    /\ UNCHANGED <<snapshots, blobs, index, workers, locks, owners, starts, seen, bad, downloadVars, apartVars, aheadVars>>

Next ==
    \/ \E i \in Importers : Start(i) \/ Lock(i) \/ Lookup(i) \/ Attach(i) \/ Validate(i)
         \/ BlobLookup(i) \/ PinBlob(i) \/ CheckBlob(i) \/ Fetch(i) \/ Fetched(i)
         \/ Apply(i) \/ Commit(i) \/ Advance(i) \/ Fail(i) \/ Cleanup(i)
         \/ FetchHeld(i) \/ FetchJoin(i) \/ (\E l \in Layers : FetchAhead(i, l)) \/ LeaveDownload(i)
         \/ Link(i) \/ RetryDownload(i) \/ OwnFailure(i)
         \/ \E o \in Owners : Adopt(i, o)
         \/ ApartStart(i) \/ Await(i) \/ CommitApart(i)
         \/ \E l \in Layers : SlotLock(i, l) \/ SlotFetch(i, l) \/ SlotWait(i, l)
              \/ SlotRetry(i, l) \/ SlotOwnFailure(i, l) \/ SlotApply(i, l)
              \/ SlotApplied(i, l) \/ SlotFail(i, l)
    \/ \E o \in Objects : DownloadDone(o) \/ DownloadFail(o)
    \/ \E l \in Layers : PrefetchStart(l) \/ PrefetchSettle(l) \/ PrefetchLink(l)
         \/ PrefetchRetry(l) \/ PrefetchGiveUp(l) \/ PrefetchCancel(l) \/ PrefetchExpire(l)
    \/ AheadStart \/ AheadEnd \/ AheadFailed \/ AheadDrop \/ AheadJanitor
    \/ PrefetchClose \/ PrefetchCloseDone
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
    /\ pf \in [Layers -> [phase : {"none", "fetching", "linking", "held", "gone"}]]
    /\ ap \in [Importers -> [Layers -> {"none", "committed"} \cup SlotRunning \cup SlotEnded]]
    /\ clocks \in [Layers -> {0} \cup Importers]
    /\ ahead \in {"none", "live", "dropped", "gone"}
    /\ pclose \in {"open", "closing", "closed"}

ApplyExclusive == \A i, j \in Importers :
    (workers[i].phase = "applying" /\ workers[j].phase = "applying" /\ Key(i) = Key(j)) => i = j
(* Two imports never apply one chain layer apart at once: the second waits *)
(* on the chain lock until the first has committed it, then reuses it.   *)
ChainApplyExclusive == \A i, j \in Importers, l \in Layers :
    (ap[i][l] \in {"applying", "applied"} /\ ap[j][l] \in {"applying", "applied"}) => i = j
(* Bytes started ahead of a layer's lookup are allowed; a fetch or apply *)
(* after a surviving snapshot choice still is not, nor is committing a    *)
(* layer applied apart. ownRetry is NoOwnRetry's.                         *)
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
ApplyHasBytes == \A i \in Importers :
    /\ workers[i].phase \in {"ready", "applying"} =>
         workers[i].layer \in blobs \cap workers[i].pins.bytes
    /\ \A l \in Layers : ap[i][l] \in {"ready", "applying"} => l \in blobs \cap workers[i].pins.bytes
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
         /\ \A l \in Layers : ap[i][l] = "none" /\ clocks[l] # i
(* A prefetch hold's TTL lease has the blob: it is linked while the       *)
(* download still holds it.                                               *)
PrefetchHeldPresent == \A l \in Layers : pf[l].phase = "held" => l \in blobs
(* An import ahead's kept ref is always one the prefetcher can release:   *)
(* in aheads or droppedAheads.                                            *)
AheadRefTracked == ModelPrefetch =>
    (owners[AheadImporter].tip # 0 => ahead \in {"live", "dropped"})
(* A closed prefetcher holds nothing: no hold, download or import ahead.  *)
PrefetchClosed == pclose = "closed" =>
    /\ \A l \in Layers : pf[l].phase \notin {"fetching", "linking", "held"}
    /\ \A o \in Objects : ~Holds(Prefetcher, o)
    /\ owners[AheadImporter].tip = 0
=============================================================================
