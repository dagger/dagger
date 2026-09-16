# Batch 2: value transfer, schema recovery and foreign paths

Design candidate, 16 September 2026. Authority is the [converged explanation](../proposed-design-explained.md#L1), blob `54dcdac7d7d46e18231d8e05f3834bc6bd5cfe52`, coordinator commit `8c8ecebf1476b419418e9bea8bbef732432dbd43`, and [implementation order v4](../implementation-order.md#L1). Both were taken from coordinator commit `d08211224e2f9c479492db2cf0227c6b7c48f7b7` in standalone signed-off commit `306de7d8e0`. The common terms name `8c8ecebf1476`, which carries that explanation blob with order v3; `d08211224e` supplies v4.

Source is the current worktree on `remote-cache-rebased-foundations`, `1ca9f28a60f1d9597c1b0df01e65a91707ce3b0f`, built on upstream/main `dfe204d216a579657e00604494ef3268f1062113`. Links below were checked here. **Branch-only** identifies retained foundations, not upstream functionality. Previous transfer candidate `8045c735` and module-selection candidate `43916966` were comparison material. This is their replacement in one document; it defines no serving rows, bindings, exclusion classifier or reason ledger. No production code, tests, models or builds were run.

## 1. Resulting behavior

A runs `CacheProbe.report(input: File) -> Report`, whose `dirs` field contains exec-produced Directories. A exports the result graph and one selected Directory chain. The graph includes the Module, HTTP File, its HTTPState receiver, saved producers and exact dependencies. Only selected immutable snapshots supply bytes.

B imports that metadata with fresh result IDs. Its ordinary module and HTTP setup can establish matching input digests, so its ordinary `report` call hits without entering the function. Reading known metadata downloads nothing. A later snapshot demand is handled by batch 4: local equivalent, offered chain, then the saved producer. Every immutable snapshot is eligible for selection, including host captures, both Git tree backends and views. Mutable backing stays local.

Bob saves the object ID and reloads it in his module-aware request. Schema recovery prefers Bob's installed equivalent Module. A subsequent `summary(notes: File @defaultPath("notes.md"))` reads Bob's current context. The saved exec still uses its original exact inputs. With no installed equivalent, the recovery order and the two accepted foreign-context failures in section 8 apply.

Import never acquires inputs to make a hit admissible. A failed download falls through to the saved producer. A producer unable to restore a previously resolved input returns its ordinary error, even if a fresh enclosing function could succeed with different content. Lost ownership of content B obtained remains a defect. These are the [accepted system rules](../proposed-design-explained.md#L12), not new cache identity rules.

## 2. Current mechanisms and the gap

| Current mechanism | Change in this batch |
| --- | --- |
| **Branch-only** [single-row capture][capture] holds one row and refuses active evaluation/bookkeeping. | Hold and copy the complete graph, including offer owners; normalize storage and relocate IDs. |
| **Branch-only** [declared reference visitor][visitor] walks encoded fields/calls without typed construction. | Extend it to explicit offer records and apply the extras filter inside recipe-form IDs. |
| [Ordinary lookup][matching] uses recipes, extra digests and structural inputs. | Import prepared rows into that same graph. Do not transport learned associations. |
| [Direct dependencies][dependencies] retain results; [required sets][requirements] union direct children. | Add a distinct owner for each offer, with retention separate from the result's own dependency walk. |
| **Branch-only** [Container parts][parts], [persisted parts][container-persist] and [saved File producers][file-encode]. | Define pending foreign output forms; batch 4 supplies acquisition. |
| **Branch-only** [ExportChain][export-chain] and [ImportChain][import-chain] protect chain lifetimes. | Open only selected completed outputs and lend providers through the export callback. |
| [ModDepsForCall][moddeps-call] loads recorded Module references through the session-canonical loader. | Prefer installed equivalent operational Modules, then an eligible exact recorded row. |
| [LocalModuleSource][local-source] stores a host path without its origin. | Persist a foreign flag and guard operational uses of that recorded path. |

The schema changes apply at recovery, not to the normal object representation. Existing [reading-server classes and fallback schema resolution][wrapping] and [bound tools' defining schema][bound-tools] remain authoritative in their existing routes.

## 3. Transfer identities

Add `call.ExtraDigestLabelRemoteCache` at the following existing attachment sites. Keep the digest expression, arguments, branch conditions and construction behavior. The [label constant][label] is branch-only.

| Site | Exact source and meaning |
| --- | --- |
| Host directory | [Attachment][host-label]: existing captured content digest. |
| Immutable HTTP File | [Attachment][http-label]: path, decimal permissions, body digest, raw Last-Modified, optional caller checksum. |
| GitRef | [Attachment][git-ref-label]: existing repository/ref inputs, including resource information where applicable. |
| GitCommit | [Attachment][git-commit-label]: existing commit identity. |
| GitRef.tree | [Attachment][git-tree-label], only inside the existing remote-backend branch. |
| GitCommit.tree | [Attachment][git-commit-tree-label], same existing branch condition. |
| Module._implementationScoped | [Attachment][module-label]: existing source implementation digest plus variant. |
| ModuleSource._implementationScoped | [Attachment][source-label]: existing source implementation digest. |
| Workspace-taking function returning a module object | [Attachment][workspace-label]: the entire existing returned-field aggregate. |
| `container.from` on an empty container | [Existing branch-only attachment][image-label]: manifest/platform content identity is already marked; retain it without duplicating the label. |

The first eight sites take the added argument through the existing variadic [WithContentDigest][content-digest]. The Workspace site additionally requires widening [AnyResult.WithContentDigestAny][any-content-digest] and both implementations, [Result][result-any-content-digest] and [ObjectResult][object-any-content-digest], with the same `additionalLabels ...string` parameter. Both forward those labels to WithContentDigest. Pass `call.ExtraDigestLabelRemoteCache` at the [Workspace attachment][workspace-label]; it is the only listed site requiring this interface change.

GitRef and GitCommit identities are marked unconditionally, including for a local backend. Their digest folds the repository Directory's [content-preferred digest][local-git-ref-input], [also for GitCommit][local-git-commit-input], which may itself be unmarked. This aggregate is accepted under the Human's reproducibility decision just as the Workspace aggregate is. The existing resource inputs in those same digests and the retained resource dependencies keep SSH-backed rows behind their session-resource requirement. Local Git **tree** identity is unchanged; exporting its snapshot does not invent a remote tree digest. Workspace aggregate fields use [content-preferred identities][content-fields], which can include a build-output digest. The whole aggregate travels even when a field's own unmarked content digest does not. No field-origin tracking is added.

Per-client/per-session/per-call literals stay literal. For example, [RequestedCacheInput][cache-inputs] and [dynamic argument normalization before lookup][dynamic-inputs] still determine the ordinary request. A deliberately identical recipe can hit without a prior digest-producing setup. There is no observation window, new checksum or universal Git-kind identity.

## 4. Canonical records and ownership

These proposed Go records are the shared contract for batches 4 and 5. They are engine APIs, not a service protocol. JSON field names use lower camel case as shown. All numeric IDs below refer to result rows; runtime offer-owner IDs never serialize.

```go
type TransferOrdinal uint64 // one based; zero only for optional absence

type ValueSelection struct {
    Roots   []AnyResult
    Outputs []SelectedValueOutput
}
type SelectedValueOutput struct {
    Result  AnyResult
    Address PersistedPartAddress
}
type PersistedPartAddress struct {
    OutputPath PersistedRefPath `json:"outputPath,omitempty"`
    Part       PartKey          `json:"part"`
}
type SnapshotValue struct {
    Kind     string                      `json:"kind"` // directory, file, snapshot
    Path     string                      `json:"path,omitempty"`
    Platform *ocispecs.Platform           `json:"platform,omitempty"`
    Services []TransferredServiceBinding `json:"services,omitempty"`
}
type TransferredServiceBinding struct {
    ServiceResultID uint64   `json:"serviceResultID"`
    Hostname        string   `json:"hostname"`
    Aliases         []string `json:"aliases,omitempty"`
}
type BlobAddress struct {
    URL           string `json:"url"`
    ExpiresAtUnix int64  `json:"expiresAtUnix,omitempty"`
}
type OfferedChain struct {
    Layers     []snapshots.ExportLayer         `json:"layers"`
    Addresses  map[digest.Digest]BlobAddress    `json:"addresses,omitempty"`
    RenewalKey string                         `json:"renewalKey,omitempty"`
}
type PersistedOfferOwner struct {
    DependencyIDs []uint64 `json:"dependencyIDs,omitempty"`
}
type PersistedPartOffer struct {
    Address PersistedPartAddress `json:"address"`
    Value   SnapshotValue        `json:"value"`
    Chain   OfferedChain         `json:"chain"`
    Owner   PersistedOfferOwner  `json:"owner"`
}
type TransferredValue struct {
    Ordinal       TransferOrdinal         `json:"ordinal"`
    Record        PersistedRecord         `json:"record"`
    DependencyIDs []uint64                `json:"dependencyIDs,omitempty"` // direct only
    ExpiresAtUnix int64                    `json:"expiresAtUnix,omitempty"`
}
type TransferredRoot struct {
    Ordinal       TransferOrdinal `json:"ordinal"`
    ExpiresAtUnix int64            `json:"expiresAtUnix,omitempty"` // retention edge
}
type TransferredOutput struct {
    Ordinal TransferOrdinal      `json:"ordinal"`
    Address PersistedPartAddress `json:"address"`
    State   string               `json:"state"` // pending, completed, absent, metadata
    Value   *SnapshotValue       `json:"value,omitempty"`
    Chain   *OfferedChain        `json:"chain,omitempty"`
    Owner   *PersistedOfferOwner `json:"owner,omitempty"` // required with Chain
}
type ValueBundle struct {
    Version int                 `json:"version"` // 1
    Roots   []TransferredRoot   `json:"roots"`
    Values  []TransferredValue  `json:"values"`
    Outputs []TransferredOutput `json:"outputs,omitempty"`
}
type ImportedValue struct {
    Ordinal  TransferOrdinal
    ResultID uint64
}
func (c *Cache) WithExportedValues(ctx context.Context, selection ValueSelection,
    cfg config.RefConfig, consume func(context.Context, *ExportedValues) error) error
func (c *Cache) ImportValues(ctx context.Context, bundle ValueBundle) ([]ImportedValue, error)
```

`Record.ResultID`, its root envelope self ID and every contained result reference use the owning row's ordinal in a bundle. `DependencyIDs` contains only direct result dependencies. Dependencies reachable solely through an offer are still rows in `Values`, reached through that offer's owner. Topological ordering follows both paths. Roots identify retention edges, not every retained dependency. Zero expiry has today's no-expiry meaning. No A-local storage link is present in a bundle. A row's description and record type are local reporting data and do not travel; imported rows start with both empty, unlike [local checkpoint restoration][row-reporting].

`TransferredOutput` and `CapturedCodecOutput` use the same output states: `pending`, `completed`, `absent`, `metadata`. Completed describes the captured immutable output; transferring its description/chain does not make the imported output locally completed.

`OutputPath` is the [typed field/index path][ref-path], not a dotted string. A standalone File/Directory uses part `snapshot`; Container uses `metadata`, `fs`, `execMeta`, `mount:<target>`. The full `(row, OutputPath, Part)` address is the key. Offer descriptors may have a different path from the original output descriptor. Installing such a descriptor does not change the saved producer's inputs.

Add only root-envelope `Imported bool` and `PendingOffers []PersistedPartOffer` to [PersistedResultEnvelope][envelope]. Mirror Imported and the immutable slot records on `sharedResult`, so clearing an encoded envelope at typed publication cannot lose them. Root-only fields are invalid on inline envelopes or `result_ref` items. Merge common row metadata into later typed encodings under its own graph lock. Provide `IsImportedResult(AnyResult) bool` for early sharing. No binding or dependency-classification field exists.

### 4.1 One offer is one owner

R has two offers O1 and O2 referring to Service S; R's producer also needs S. R's ordinary `deps` contains S once. O1 and O2 each own S once. Replacing O1 removes R's slot hold on O1, not either of the other edges. An active acquisition's O1 hold keeps S alive after replacement.

The runtime shape is a cache-local `offerOwner` containing an internal owner ID, immutable `PersistedOfferOwner`, an incoming hold count, and its held dependency rows. A `partOffer` pairs an immutable offer description with this owner. R's slot map owns one hold on each attached owner. Acquisition/capture holds are counted independently. There is no owning O→R edge. The optional originating row/address is debug data only. Do not put O in `resultsByID`, manufacture a Query frame, allocate a public handle, or index its digest. [Persistence requires non-Query result frames][frame-required]; this object is deliberately another owner kind.

Batch 2 implements the owner lifetime primitives needed by import, capture and boot: creation, hold transfer, release, collection, prune, accounting and debug support. Batch 5 uses them for live replacement, acquisition holds and settlement. Under E (`egraphMu`): prepare an owner with deduplicated exact dependencies; validate registration and acyclicity; increment each dependency's existing incoming ownership once; attach its slot hold. The graph cycle walk follows direct edges **and** attached offer owners. An O→D→R→O cycle is invalid even though resource aggregation ignores O. Releasing an owner's last hold removes it from the owner registry and decrements each dependency, feeding the ordinary [result collection queue][collection]. Collecting R first detaches its offer slots, then its direct dependencies. Callbacks and snapshot releases run outside E with a non-canceled cleanup context.

For ownership scans, add one internal iterator over a result's direct children and slot owners, and one over an owner's result children. Use those same edges in capture, cycle checks, [prune closure and simulation][prune], usage accounting and debug snapshots. Keep reverse offer-parent membership separate from `depParents`: the latter propagates result requirements. A detached owner with active holds is a retention root until its last hold ends; a slot owner lives through R's owners. Active owner holds protect their result closure in prune just as active result holds do. Count offer metadata once per owner, result/snapshot usage once by existing row/storage identity, and expose owner ID, originating slot, hold count and dependency IDs as a separate debug category. No owner has an independent TTL, prune candidate or cache identity. Pruning a persisted R edge releases offers only when ordinary ownership permits R's collection.

The durable owner record is nested in its pending offer (`Owner.DependencyIDs`), so boot creates one owner per slot without an owner-ID table. This is explicit ownership, not another spelling of a reason ledger. Once a part's installed output commits, R receives the actual output's **direct** references before release of any offer owner. Settlement after lease sync detaches every current offer for that address, including a replacement that arrived during the winning attempt. Active holders remain independent. Batch 4 supplies final-part settlement; batch 5 serializes it with acceptance under E→G. No offer can attach to an already-final part.

The cross-batch runtime API is `newOfferOwnerLocked(ctx, PersistedOfferOwner) (*offerOwner, error)`, `retainOfferOwnerLocked(*offerOwner)` and `releaseOfferOwnerLocked(ctx, *offerOwner) (collectionQueue, error)`. Creation returns one preparation hold; successful slot publication transfers that hold to the slot. A refused publication releases it. Acquisition/capture explicitly retains and later releases another hold. The owner record is immutable; address refresh publishes an immutable offer description without changing the owner's references, while replacement with changed references prepares a new owner. These internal helpers operate under E and never invoke release callbacks themselves.

Batch 2 also supplies `attachPartOfferLocked`, `replacePartOfferLocked` and `retirePartOfferLocked`, each taking the receiver and full `PersistedPartAddress`; attach/replace also take the prepared `partOffer`. Attach transfers its preparation hold to an empty slot. Replace transfers the new hold and releases the old slot hold. Retire removes the slot at settlement and releases its hold. Each successful slot mutation bumps `transferRevision` (OfferRev); an address refresh uses replace with the same owner and a separately retained hold. Batch 5 calls these primitives under its acceptance/settlement gate; collection and boot use the same removal primitive. Release callbacks still run outside E.

### 4.2 Resources and revision counters

A result's required set is its own resource handle plus the sets of its direct result dependencies, recursively. Do not traverse slot owners at any depth. This fits [today's direct-edge union][requirements] if offer edges remain separate. Import computes the sets dependencies-first from handles and direct edges; persist those handles/edges and recompute on boot, rather than trusting a transferred aggregate set or generation. Installed-output direct edges propagate requirements through ordinary `depParents` and [serve-time rechecks][session-loader].

An offer referencing a socket-dependent Service cannot make an otherwise usable result miss. Demand checks the selected owner's referenced results' own-dependency sets against that session, and skips only that offer when unsatisfied. Early sharing compares donor, receiver and copied references using this same own-dependency walk. Slot changes neither grow nor shrink lookup requirements. An owner hold grants no resource or service access.

| Revision | Owner and lock | Incrementing events | Reads that compare it |
| --- | --- | --- | --- |
| `payloadRevision` | Batch 4, `payloadMu`; batch 6 consumes | Encoded payload/descriptor/complete desired-map replacement, typed publication, applied-map snapshot changes | Live capture, checkpoint copy, source lease, encoded install and decode |
| Typed `OutputRev` | Batch 2 defines the writer for File and Directory at their completed-field writers under the object's own lock; Container's existing guard is reused; batch 4 consumes | Mutation of an already typed output/installed metadata | Typed capture/checkpoint and source probes; typed install's expected output check |
| `transferRevision` / `OfferRev` | Batch 2 primitives under E; batch 5 calls them | Attach, replace, retire or refresh a pending slot | Capture/checkpoint slot snapshots; selected-offer lease and installation |
| `dependencyOwnershipRevision` | Batch 2 defines under E | Direct dependency membership or attached owner membership changes | Capture/checkpoint ownership graph; prepared graph/install rechecks |
| `requiredSessionResourcesGen` | Existing DagQL set update under E, atomic counter | Actual change to the own-dependency required set | Capture/checkpoint and existing selection/serve-time resource checks |

`dependencyOwnershipRevision` now detects graph changes, not reason classifications. Offer replacement advances it even when both owners name identical children. Owner records are immutable; slot revision detects their replacement, while each child's own revision covers changes beneath them. Active hold-count changes alone do not invalidate a held capture. All revisions are process-local, never transported or restored as authoritative counters. Until batch 4 introduces payload/output writers, batch 2 establishes the capture comparison hooks and uses the existing coherent locked copies; the later writers must advance those counters atomically with publication.

## 5. Export without evaluation

### 5.1 Held capture and relocation

Factor [CapturePersistedRecord][capture] into an internal helper for an already-held row and admitted operation. `WithExportedValues` admits one operation; it must not reopen admission for every dependency after Close starts draining.

1. Under E validate selected registered roots, attachment and expiry. Walk direct dependencies and attached owners. Hold every unique result and owner; detect cycles. Record frame pointers, edge/slot snapshots, expiry and the three graph-side counters. Selected output rows must belong to this closure. A detached root or an independently selected Query value is invalid.
2. Release E. Copy one row at a time under **that row's** D (`sharedResult.lazyMu`), the [per-row lazy-state mutex][lazy-mutex]. Refuse active attempts, unfinished bookkeeping or incomplete attachment. Copy encoded payload and links together under that row's `payloadMu`. File and Directory gain a nonblocking persistence guard equivalent to [Container's existing guard](../../../../core/container_persistence.go#L96), refusing capture with `ErrPersistStateNotReady` while the [body latch](../../../../core/lazy_state.go#L57) is held or the output is being published. The same lock-order rationale applies: never block acquiring an object lock under cache locks, because the body may itself need the cache. Copy each typed output with its revision. Release the row's D and all copy guards before moving to the next row; never hold two rows' lazyMu together. The HTTPState tuple is copied under its mutex by batch 1's encoder change. No snapshot open, producer decode, schema reconstruction or field evaluation occurs.
3. After all row copies, recheck typed output revisions with the same nonblocking guards and payload revision/representation under each row's `payloadMu`, releasing each row's guards before the next. With every copy guard released, reacquire E for the cross-row consistency recheck: compare `transferRevision`, `dependencyOwnershipRevision`, `requiredSessionResourcesGen`, frames, edges, attachment and expiries for the whole held closure. Encoded capture records absence of typed outputs and uses payload revision to detect typed publication. A changed or busy row returns `ErrPersistStateNotReady` for the affected root. There is no retry loop or global freeze.
4. Validate the copied graph, normalize foreign payloads, assign contiguous ordinals, visit and relocate declared references, filter frame extras, then validate the rewritten graph and foreign forms again. Preserve explicit-only direct edges. Owners and ordinary edges retain their separate meanings.
5. Map selected parts and open their completed immutable chains. Forward other pending offers as metadata. Invoke the callback only after a complete valid bundle is ready. Release chain handles, opened refs, owner/result holds, then the counted operation, joining cleanup errors outside locks.

A local completed output beside an unretired offer means unfinished installation: live capture returns not-ready, even if that part was not selected. It never forwards that redundant offer. A local checkpoint alone may contain that pair; boot settles its ownership as specified below. Pending or absent selected outputs are per-output outcomes and do not invalidate a closed metadata graph. No producer is required merely to transfer an eager leaf.

Use [VisitEncodedReferences][visitor] and its Self, Child, Call, OutputRole and LocalBacking kinds. For each ordinary row, declared call/payload child references must be its exact direct dependencies; a self descriptor is not an edge. For an offer, the common visitor reports `PersistedRefChild` for `Owner.DependencyIDs` and all `Value.Services[].ServiceResultID` occurrences under that offer path. Validate them against the **offer owner's** dependencies, not R.deps. Descriptor service IDs must be included in the owner's set; extra explicit owner dependencies are retained. Carry an owner-context in the traversal rather than flattening these references into ordinary dependencies. Implement `visitPersistedPartOffer(offer, path, visit)` once for envelope slots and selected-output records. The ownership validator invokes it with an owner-scoped callback; the ordinary row callback validates only the frame/body/direct-edge portion. Relocation uses the same numeric-ID rewrite callback for both. Scope is supplied by the typed walker, never inferred from arbitrary user JSON field names. No special closure-reference kind for a removed binding remains.

Handle-form call IDs keep their recursive type while their numeric row ID is relocated. Lists, nullable roots, inline objects and nested descriptive calls use existing codecs. Do not rewrite arbitrary numeric JSON fields. Query receiver absence is the [existing nil-reference convention][query-ref]; no invented Query ordinal. Decode uses B's ambient Query through [persisted core helpers][persisted-object]. Nonzero missing IDs are errors.

At every copied call vertex keep only digests marked `remote-cache` on **that vertex**, plus an already-present `content` annotation for the same digest. Filter nested frames and recipe-form IDs too, using a memoized copy of the recipe ID DAG and the existing digest reconstruction path. Reset cached digests after changing annotations. Clear runtime `ResultCallRef.shared` pointers; never pull extra labels from an egraph class. A marked Workspace aggregate stays opaque: filtering does not recompute its field inputs. No resultTerms, learned aliases, output classes or request frames enter a bundle.

### 5.2 Part mapping and chain lifetime

Replace the old classifier with a pure codec method:

```go
type PersistedTransferCodec interface {
    NormalizeForeign(PersistedPayloadVisit) (ForeignPayload, error)
    ValidateForeign(PersistedPayloadVisit) error
    MapSnapshotParts(PersistedPayloadVisit) ([]CapturedCodecOutput, error)
}
type CapturedCodecOutput struct {
    Address    PersistedPartAddress
    State      string // pending, completed, absent, metadata
    Value      *SnapshotValue
    Role       string
    SnapshotID string // capture only, never serialized
}
type ForeignPayload struct { JSON json.RawMessage }
type ExportedValues struct {
    Bundle ValueBundle
    Chains *SelectedChains // borrowed until consume returns
}
func OpenSelectedChains(ctx context.Context, capture *HeldCapturedClosure,
    outputs []SelectedValueOutput, cfg config.RefConfig) (*SelectedChains, error)
func (*SelectedChains) Release(context.Context) error // idempotent
```

Register the optional transfer codec alongside the [existing family visitor][family]. Storage/foreign-specific families must supply all applicable methods; other families retain their validated JSON. Core implements these methods; DagQL never imports core. Map the **captured local** record before clearing its links, then associate that mapping with normalized ordinal records. `SelectedChains` entries contain ordinal, full address, immutable Layers and an in-process `content.InfoReaderProvider`; it owns opened refs and ExportChain handles, not the borrowed closure. Release it before the closure.

| Selected output | Mapping, without evaluation |
| --- | --- |
| File or Directory | Read its captured path/platform/services and snapshot role. A selector view reopens the parent's snapshot, so that role exports the **whole parent chain**, not a file-filtered chain. [File selector][file-selector], [Directory selector][dir-selector]. Do not follow a preferred equivalent parent to choose different storage. |
| Container `fs` / `execMeta` | Read the captured part descriptor and `fs` / `meta` role. Metadata and authoritative absence need no chain. |
| Container `mount:<target>` | Find Target and Directory/File kind in captured ordered mount metadata; read the part's `mount_dir:<index>` or `mount_file:<index>` role and descriptor from [stored part mapping][part-mapping]. This is the mount's recorded source snapshot, already reopened into its accessor; mount metadata alone has no source result ID. Never borrow another container's positional index. |
| Inline value / `result_ref` | Recurse through the existing typed path. A `result_ref` changes to the referenced row; it does not invent an inline body. |
| Pending output with offer | Forward its existing offer record/owner dependencies without opening storage. |
| Pending output without a completed local descriptor | Report pending/unavailable for that selection. Do not force its parent or infer a chain from producer arguments. |

The service must know that selecting a view or a mount can export all bytes in the underlying snapshot. No selector/host/Git classifier is involved; input ancestry does not restrict export. A selected completed local descriptor with a missing link or failed open is an ordinary error, not pending. Canonical scratch exports its actual empty chain.

For each selected completed output, open the captured local ID using `GetBySnapshotID(NoUpdateLastUsed)`, then call [ExportChain][export-chain]. The closure hold protects the initial open; ExportChain owns an independent resource pin while layers/providers are borrowed. This may produce export blobs/compression; it is storage work, not value evaluation. Cancellation or a later chain failure releases every earlier handle/ref. The callback may copy metadata and consume providers, but cannot keep providers after return.

Locally opened chain descriptions are placed in `Bundle.Outputs`, with a required Owner record when Chain is present; the transport caller fills supplied addresses/renewal key before import. Build that owner's set from the descriptor's service IDs plus the exact rows carrying resource handles in the captured source row's own-dependency closure, skipping offer owners. This preserves the source row's resource requirements without retaining its entire producer as an offer dependency. Those rows already belong to the captured graph. Visit and relocate this Owner and Value exactly like a pending offer; descriptions without Chain have no owner. On import they become a slot with its own explicit owner; old direct dependencies stay direct. Existing offers travel only in `PendingOffers`, not duplicated as a second same-address chain. Reject ambiguous duplicate descriptions. A pending offer may travel without an available address; later local reuse or renewal determines its usefulness.

## 6. Foreign forms and live import

### 6.1 Pure codec validation

`NormalizeForeign` acts only on a copy at export. `ValidateForeign` checks the supplied form, without repairing it, on import **before ID reservation** and again after relocation. The raw walker invokes it for root and inline families; the caller adds row identity to codec/path errors. Empty storage links alone are not a valid foreign form. Known families' validators and declared visitors must both succeed.

| Family | Bundle form; refused input | Local checkpoint |
| --- | --- | --- |
| File / Directory | Only `transfer_pending`. Preserve Path/Platform/Services, raw LazyKind/LazyJSON, `valueKnown` and `producerState` (`pending`, `completed`, `none`). No A link or stored ID. Native snapshot/lazy forms are refused even with no link. State none has no producer; other states require a valid kind/JSON pair. | Native snapshot forms and valid foreign pending forms are both valid. |
| Container | Keep Metadata.Value/Consumed, ordered mounts and raw recipe. Only pending or absent filesystem Parts, plus metadata; completed parts become pending with `valueKind` and their recorded Role/Path/Platform/Services. Keep `producerState`. Refuse native completed directory/file/snapshot parts. | Native completed parts and pending parts may coexist, with their own local links. |
| HTTPState | `foreign_uninitialized`: URL, no snapshot/ID, ETag, LastModified or ContentDigest. Refuse native/missing discriminator or any retained validator/digest/backing. | Initialization on B uses native state and persists its actual tuple. |
| CacheVolume | `foreign_uninitialized` with existing Key/Namespace/Sharing/Owner/Selector/Source configuration, no mutable backing. Keep SourceResultID and its direct edge. Refuse missing/native discriminator or any backing. | Native and valid foreign-uninitialized forms are allowed. Normal B initialization persists native state with its backing. |
| Filesync / remote Git mirrors | `foreign_uninitialized` with StableClientID/Drive or RemoteURL configuration, no mutable backing. Refuse missing/native form. | Normal B initialization/persistence. |
| Local-kind ModuleSource | Existing Local payload with `Foreign=true`; missing/false is refused. Preserve its path/configuration/Workspace and all declared references. Git/Directory kinds keep their existing forms. | Native false and imported true are distinct durable states; clone preserves the flag. |
| Other persistable families | Existing shape and exact reference validation. Unknown family fails explicitly. | Existing codec rules. |

The relevant native codecs are [File][file-decode], [Directory][dir-decode], [Container][container-persist], [HTTPState][http-encode], [CacheVolume][volume-encode], [filesync mirror][filesync-encode], [Git mirror][mirror-encode], and [ModuleSource][source-encode]. HTTPState's visitor must report LocalBacking, not immutable OutputRole. Mutable fields are cleared together; a body-less validator could cause 304 failure, and an old body digest could discard an equal fresh body. The [HTTP resolution branches][http-resolve] are the evidence. Batch 1 owns the mutex-protected encoder tuple copy; its stateless File producer records no HTTPState reference, but the File's original call frame still owns that state.

A transferred filesystem shell retains raw producer bytes until batch 4 needs them. Do not use the native snapshot restore path for it or decode the typed producer at import. `valueKnown` distinguishes captured description from an unset accessor. `producerState=completed` records the original producer's completion, not completion of a B-local output. Batch 4 owns independent output finality and acquisition. At batch 2's standalone implementation boundary, a pending filesystem demand reports an explicit unavailable-part error until batch 4 supplies that invoker; metadata and the schema tests are executable here. Native missing-link/decode errors are never converted into the foreign form.

### 6.2 Atomic publication

1. Copy bundle bytes/records. Validate version, contiguous ordinals, full addresses, shapes, direct edges and owner dependencies, acyclicity, roots and expiry. Verify filtered extras rather than accepting an unmarked extra. Run foreign validators and all declared-reference walks, including selected-output Owner/Value references as well as envelope offers. Require no local snapshot/backing IDs or links. A completed-local-output plus pending offer pair is invalid in a bundle.
2. Turn newly supplied selected chains into pending offers using their validated Owner records and exact service/resource references. References must already lie in the bundle closure; an address cannot introduce a result. Preserve all direct edges. Revalidate slots, descriptors and owner graph. Set Imported on all result rows.
3. Under a short E reservation allocate fresh monotonically increasing result IDs. Gaps on failure are harmless. Outside E build private rows and owners; none is in public maps. Relocate every occurrence to B IDs and validate again. Set temporary frame shared pointers only to these private rows for recipe/structural preparation. Never substitute a currently equivalent B row for a declared reference.
4. Prepare all fallible hashing/provenance/identity work before publication, using the existing [identity teaching algorithm][teaching]. Factor preparation from application: no import failure may leave an earlier class union behind. Offers have no identity plan. No schema, producer, storage, source lookup or resource acquisition runs in preparation.
5. Under E recheck cancellation and selected-root expiry. Register all rows/owners, direct edges and independent offer holds; compute own-dependency requirements, install ordinary pruneable root edges and apply validated ordinary posting plans. This is one non-I/O transaction with no recoverable failure after its first mutation. No direct-ID reader, request lookup or frame walk can see a partial import. An already-admitted operation may finish while Close drains it.
6. Unlock and return the root ordinal→fresh-ID mapping. Cancellation after publication returns the committed mapping, not an apparent rollback. Batch 6's sharing notification takes its own holds; it cannot delay or veto import. Release temporary preparation holds on every path outside locks.

Use [ordinary persisted retention edges][retention]. Preserve absolute root/row expiries; dependencies live through their owners, not new permanent roots. Do not copy administrative unpruneable bits. Expired rows still needed as exact dependencies remain represented; expiry filters new candidates, not an existing owner's exact reference. Reject an expired independently requested root. Errors before publication release private owners and leak neither rows nor associations.

### 6.3 Lock ordering

E protects registration, direct/offer edges, slot pointers, requirements and posting. D is the per-row `sharedResult.lazyMu`, excluding that row's attempts/bookkeeping while copying; core live guards are nonblocking. Capture releases E before D/core or payload copying, copies and releases one row before the next, and never holds two rows' lazyMu together. Other rows remain free to evaluate. After all copies it releases every copy guard before the cross-row revision recheck under E. It never enters G under D. `payloadMu` protects immutable representation copies/swaps only. Encoded installation uses E→G→payloadMu; typed decode publication uses payloadMu alone. G may perform prepared leaf accessor stores, but cannot wait for core bodies or acquire E/D. Snapshot opens, providers, lease sync, schema actions and release callbacks all run outside E/G/D. Batch 4 owns the gate/task kernel; this batch uses its publication boundaries without implementing a second coordinator.

## 7. Persistence and synchronization boundary

The worktree constants are [schema 19][schema-version] and [envelope 3][envelope-version]. Choose **schema 20, envelope 4, bundle 1**, one hard cut without migration. These numbers are freshly verified against this base. Persist Imported, root-level offers with owner records, direct dependencies, original producers, per-part state and B-local role links. No owner runtime IDs, hold counts, revision counters, provider closures, gates or renewal requests persist.

An encoded receiver has an optional immutable `snapshotLinkIntent` representing the **complete** desired role map, including an explicitly empty map. Batch 6 supplies that intent at encoded installation; batch 4 supplies payload revisions and decode publication. Applied `snapshotOwnerLinks` remains the old map until [lease sync][lease-sync] succeeds. The [current desired-link reader][desired-links] and [checkpoint copy][checkpoint-copy] must read the intent when present instead of treating the applied map as desired.

Decode copies envelope, desired map and payload revision together. Its PersistDecodeContext uses that copied map for the row being decoded; an empty copied map must not trigger a fallback to a newer live map. Decode outside locks; publish under `payloadMu` only if still encoded at that revision. On mismatch dispose only the losing preparation and retry the current representation. An encoded installer uses E→G→payloadMu; both writers meet at payloadMu. Preserve the row-owned pin/cleanup bucket when typed publication adds its value cleanup; never overwrite it.

Decode-side row sync may attach the role independently, but never settles the owning generation, releases its protection or retires its offer. Waiting for that generation inside decode could deadlock a sharing pass preparing an ancestor. The owning task later does its own idempotent sync and final-part settlement. Partial attempted roles belong to cleanup even if the applied map never recorded success. This consumes the retained A/B/L synchronization resolutions; it introduces no closing sync pass.

After operation drain, a stable installed output may checkpoint with its complete desired map while its independent pin survives process exit. The checkpoint does not mark live sync complete. Running/invalid body state still reports not-ready. [Snapshot generation][checkpoint] fails the whole checkpoint on an encoding error; normal dirty-close/reset behavior stays intact. There is no promise that a failed Close can be retried.

Boot is ordered:

1. Validate all local envelopes, direct references, offer-owner records and the complete graph. Valid native **and** foreign forms are allowed. Register private raw rows, direct edges and reconstructed owner edges together. A pending offer is not flattened into direct result dependencies. No callers are admitted yet.
2. Restore every saved snapshot-owner link. A native completed form missing a required link retains its existing error. An installed output with a redundant pending offer is valid only in this local checkpoint form; restore its direct references and owner link first.
3. Retire all redundant offers for completed local addresses by ordinary owner release. Independent direct edges survive. Recompute own-dependency requirements; discard transferred/stale derived sets. Restore ordinary local associations only for valid surviving rows, then make them usable. Local checkpoints can preserve legitimate learned associations; cross-engine bundles cannot.
4. Return from [NewCache and release previous-process transfer leases][server-boot], then admit work. Pending producers stay raw, and future acquisition/sharing starts normally. Validation or owner-attachment failure follows the existing whole-cache reset, not per-row remote repair. Unit peers must honor `PersistenceResetReason` as the engine server does.

A middle engine can forward untouched rows/offers without decoding. It normalizes B-local backing anew on foreign export, preserves expiry and follows the complete direct-plus-offer graph. Owner records reconstruct independent ownership on every hop. Only unconsumed slots with no completed local output forward.

## 8. Schema recovery and the foreign-path boundary

### 8.1 Exact request-specific recovery

[InstallCoreSchemaLoaders][schema-loaders] installs the cold type fallback and the node loader. The handle path reads ResultCallByResultID, calls ModDepsForCall, builds `deps.Schema`, then loads the ID. [InterfaceType.loadImpl][interface-loader] uses the same sequence. Merely changing a later ID-value decoder would be too late.

Keep `Query.ModDepsForCall`'s public signature and its full `WalkResultCall`. Add a core helper that snapshots `CurrentServedDeps(ctx).Mods()` and breadth-first visits each user's `Deps.Mods()`, direct modules first in builder order. Skip core modules and source-less builder shells; deduplicate by exact operational result ID, including self entries. Sources are [CurrentServedDeps][served-deps], [Mods][builder-mods] and [ModuleResult][module-result]. For each candidate call [ImplementationScopedModule][scoped-selection] once outside cache/schema locks. These are normal cached selections during schema recovery; they may do ordinary work or error. They do not run at import. Keep the operational Module as the selected value, its scoped row as comparison only.

Add the following narrow DagQL selection entry in the [existing result loader][session-loader]:

```go
type SchemaModuleCandidate struct {
    ModuleResultID uint64 // actual installed operational module
    ScopedResultID uint64 // comparison row from its normal selection
}
func (c *Cache) LoadResultByResultIDForSchema(ctx context.Context,
    sessionID string, dag *Server, recordedID uint64,
    installed []SchemaModuleCandidate) (AnyResult, error)
```

It is used only for Module references in schema recovery. Under one E section, with normal session-operation admission:

1. Resolve the registered recorded row. Check installed candidates in the supplied stable order by intersection of their scoped row's [ordinary output classes][output-classes] with the recorded row's classes. Do not compare names, paths or TypeDefs. A candidate must still be registered, cleanly attached and accessible to the session. It is already an installed, owned Module; expiry of its old cache entry does not evict that installed schema. Acquire the selected **operational** row's session ownership, never canonicalize it afterwards.
2. If none matches, select the exact recorded row when it is registered, not expired and satisfies session resources, retaining normal attachment/error behavior. A persisted but undecoded row is present.
3. Otherwise call today's canonical-equivalent-for-session selector. Preserve its fallback to the recorded row when no sibling qualifies, and its final resource refusal. An absent recorded row returns the existing missing-shared-result error before canonical selection; do not search aliases for a collected ID.
4. Capture the chosen row's requirement generation and session ownership under the same lock. Use the existing decode/wrap path outside E and the existing post-decode requirement/session-release checks. A decode/local-storage error is not permission to choose another Module. Factoring the loader must preserve those checks for every existing call site.

At every Module-typed visit, core calls this entry, asserts the Module type, and uses today's source-backed/seen-ID append logic. Continue traversing that recorded frame after a successful match. Receiver/argument/implicit-input and construction-ancestor Module references matter as well as `frame.Module`. The original frame is not rewritten. WalkResultCall may already report a missing-frame error before visiting an absent target; preserve that error too.

A dependency-owned return type is resolved by the selected operational Module's ordinary dependencies. If the call separately names that dependency's Module, it appears in the transitive candidate list. A selected M's scope is not evidence about a type owned by N. Class comparison takes no core locks or schema actions under E. Per-call caching of candidate/decision lists is bounded by the installed graph; no process-wide equivalence memo exists.

Normal reading-server wrapping, cold scalar/enum schema fallback, `node`, `LoadType`, interface recovery and bound-tool lazy loading keep their existing behavior. The two schema callbacks gain the order above through ModDepsForCall; no generic load mode or captured defining class is replaced globally. Exact [persisted producer references][exact-ref] keep their sessionless load and original inputs.

With no installed equivalent, an eligible native recorded Module wins even if an imported sibling has a lower ID. The two accepted residual cases are a frame naming the imported Module itself, and an ineligible recorded row whose eligible canonical substitute is imported. A contextual operation needing that foreign path then returns the explicit error below. A saved ID does not remember an earlier reader's checkout.

### 8.2 One checked accessor, at actual path use

Add `Foreign bool` to LocalModuleSource, preserving the existing path's JSON spelling. Export sets it for local kind; import validates it; ordinary native constructors default false. Clone and local persistence preserve it. Metadata decoding, digest comparison and formatting do not call the accessor.

```go
var ErrForeignModuleContext = errors.New("local module context belongs to another engine")
func (src *ModuleSource) LocalContextDirectoryPath() (string, error)
```

The accessor checks kind/payload, then rejects Foreign with an error wrapping that sentinel and naming the module. Otherwise return the existing path. Do not classify it as NotFound, `os.ErrNotExist` or `ErrNoGitContext`, and do not replace it with a current directory, captured context or a guessed B module.

Crucially, preserve actual upstream Workspace routes. [LoadContextDir][context-dir] first uses WorkspaceFromContext; [ModuleSourceFS.directory][source-fs] uses the source's Workspace before its local branch. Those reads need no recorded local path and remain valid. The old comparison candidate's blanket guard before Workspace dispatch is withdrawn. Put the check immediately before operational local-path use; never fall back to that path after a Workspace error. A local LoadContextGit still requires host Git materialization even after a Workspace Directory read, so reject its foreign path before starting that host-dependent branch.

| Operational reader or conversion | Required placement |
| --- | --- |
| [loadContextFromSource][context-local], [LoadContextFile][context-file] | Local branch before deriving/joining paths or requesting caller filesync; keep their existing Workspace alternatives. |
| [LoadContextGit][context-git] | Before the local path is passed to MaterializeHostGitCheckout; preserve Git/Directory-backed behavior and optional no-Git handling. |
| [innerEnvFile][env-reader] | After a legitimate source-Workspace route, before the host `.env` branch. The new error must survive optional-file handling. |
| [ResolveDepToSource][local-deps] | Guard the local-parent path used for a fresh host moduleSource selection. Workspace-relative dependency selection remains valid. ParseRefString's Stat/Exists calls are guarded at their own local branches. |
| [ModuleSourceFS.Stat / Exists][source-stat] | Local CallerStatFS branch before joining the host path. Exists must return the error, not false. ContextDirectory/ReadFile use the guarded context methods. |
| [Dependency config construction][config-paths] | Guard local parent/related operands before exporting a reusable source reference. This conversion can enable a fresh local selection; in-memory comparisons do not. |
| [ModTreeNode scale-out query][scale-out] | Guard local Source before refString, and local ContextSource before converting AsString into a defaultPathContextSourceRef. |
| [Toolchain loading][toolchain-paths] | Guard local defaultPathContextSrc before passing its formatted ref into BuildLegacyAsModuleArgs. |
| [pendingRelatedModule][pending-related] | Guard related and default-path local sources before constructing Ref, DefaultPathContextSourceRef or LegacyCallerModuleDir. Return an error and propagate it at all three callers. |

The [withSourceSubpath lexical containment check][source-subpath] is data-only: keep the existing validation and preserve Foreign through the clone. Its subsequent context/default reload can succeed through a usable source Workspace; only an actual host fallback invokes the accessor. [Related-item validation][related-paths], [deduplication][deduplicate-paths], [local update comparisons][update-paths] and [removal comparisons][remove-paths] likewise use paths as data and retain the original source objects/flags through clones. Do not add a foreign-context error to those comparisons. The config conversion in the table is distinct because it exports a reusable reference instead of retaining the flagged source object.

Keep `AsString()` and the [localContextDirectoryPath schema field][path-field] returning recorded data: neither performs host access or creates a fresh local selection. [ModuleSource formatting][source-string], [ResultCallModule's descriptive Ref][module-ref], [RootAddress][root-address] and diagnostic/telemetry strings follow the same rule. The constructors/encoder merely store the flag/path. Consumers converting either recorded string into a fresh local load must call the accessor first; string formatting itself cannot erase the flag's boundary. No DagQL identity or global module-reference comparison changes are required. Existing `canonicalModuleReference` remains a data comparison, not permission to load a foreign path.

During implementation inspect every `ContextDirectoryPath` and `AsString()` use in core and engine/server, including freshly rebased callers. Classify data-only reads separately from selections. Do not use an error-type catch-all to suppress the sentinel: optional Git and `.env` paths accept only their existing absence errors. This is a finite reader conversion, not a generic field interpreter or host-source binding.

## 9. Gated fixture field

Batch 2 now owns the former resolution-G field. Add `installRemoteCacheFixture` at [CoreMod.Install][core-install], after core types, using the existing JSON scalar. The sole gate is engine-process `_DAGGER_TEST_REMOTE_CACHE_FIXTURE_ROOT`. Unset/empty installs nothing and performs no filesystem work. Enabled configuration must name an existing absolute directory. Capture it in the handler; forks use their invocation's Query/cache/session. Add the name to [isCoreRootField][core-root-field] so it works before module setup, without enabling the field on ordinary engines. Install once per server with `View(AllVersion)` and `DoNotCache` on every operation, so bundle writes, imports and counter reads always execute and teach no fixture-call alias.

```graphql
extend type Query {
  _remoteCacheFixture(operation: String!, path: String! = "", ids: [ID!]! = []): JSON!
}
```

| Operation | Behavior in this batch |
| --- | --- |
| `export` | Require handles and a relative path. Internally acquire temporary **exact** registered root holds with current session-resource checks, without canonicalizing, schema reconstruction or typed decode. Wrap those raw rows for WithExportedValues; select metadata only. Atomically write the bundle in its callback and return ordinal/source-ID/type-handle mapping. |
| `import` | Require path, no IDs. Decode the typed bundle, call ImportValues and return committed ordinal→fresh-ID/type-handle mapping. Do not load the imported objects afterwards. |
| `report` | Optional exact IDs, no path. Copy raw frame, Imported, direct dependencies and separate offer-owner records plus selected debug associations and body-entry counts. No demand or lasting row ownership. |
| `recordBody` | No path/IDs. Require CurrentFunctionCall and derive its name/parent/receiver and engine client from that context. Append one actual entry record, not a caller-supplied count. |

Use internal exact raw-root acquisition with session-operation admission and the same requirement generation recheck as value serving, not a new public schema lease. Temporary holds end on every handler outcome. After a pre-export hold acquisition, Close may refuse the later export admission normally; no file or equality state was committed. Handle construction uses the validated recursive type and fresh numeric ID, never a scalar float conversion. [JSON][json] encodes a JSON string; clients decode typed uint64 fields.

Bundle paths resolve under `<root>/bundles`; body entries under `<root>/body-entries`. Reject absolute paths, `..`, NUL, empty/dot filenames and arguments irrelevant to the operation. Use `os.Root` for rooted file operations and temporary-file rename, so a symlink cannot escape; no string-prefix containment check. Each engine has its own fixture root, separate from cache/snapshot storage. The harness copies committed files after export acknowledgement. Report ignores temporary files, treats missing counters as zero and rejects malformed committed records.

The handler derives entries from [FunctionCall and ParentTyped][function-call]. The small Go module calls `recordBody` synchronously as the first statement of each measured function through its existing engine GraphQL client. A hit skips that statement; a changed-argument negative control increments it. Write one uniquely named JSON record per entry, grouped by parent/function/exact receiver/client in report; concurrent calls never update one shared counter file. Await measured calls before comparing counts. Reports are not transactions across live graph and filesystem changes. Keep counters across clean restart with the fixture volume; fresh scenarios use fresh roots.

This field moves metadata in batch 2, sufficient for real object/schema/context recovery tests. Local core unit peers test selected chain bytes with real stores. Batch 7 extends this same gated field for chain files/providers and the complete CacheProbe acquisition fixture; no new listener or fake remote-cache service is introduced. Reuse [existing dev-engine persistence fixtures][native-fixture]. Disabled-gate behavior, invalid paths, uncached repeated operations and forked-schema invocation are part of this batch's tests.

## 10. Implementation sequence and verification

Land buildable private implementation commits in this order:

1. Common records, owner lifetime/collection primitives and visitors; schema 20/envelope 4 cut. Keep offer retention separate from lookup requirements. Add import/boot owner reconstruction before live replacement exists.
2. Foreign codec normalization/validation and part mapping, consuming batch 1's registered producers and locked HTTPState tuple. Add raw pending shells without a speculative producer invoker.
3. Held export, selected chain lifetime, fresh-ID import preparation and atomic publication; complete desired-role checkpoint representation and hooks for batch 4/6 writers. Add the identity labels. Land the File/Directory nonblocking guards and typed OutputRev writers in this commit or a preceding commit, with the negative test that capture is refused while the body latch is held and the Container control still passes.
4. Schema-specific loader selection and installed-candidate traversal, followed by the foreign-path accessor and all operational reader conversions. Preserve exact saved producer loads and existing bound-tool paths.
5. Gated fixture field and focused unit/native cases. Batch 4 consumes pending forms and revisions, batch 5 consumes explicit owner records/lifetime and final-part hook, batch 6 supplies sharing/encoded installation, and batch 7 extends the full integration proof.

Batch 1's four Git records, stateless `FileHTTPResolveLazy`, builtin Container recipe keyed by `_builtinContainer` and schema File's `FileBlobLazy` must join the same codec registry and declared visitor. Transfer never interprets their operation bodies. Changeset producers are included only if batch 1 establishes required-workflow need under the new explanation, not to preserve an old list. `_builtinContainer` remains an unrefined whole producer for batch 4. Shared source/interface disagreement returns through the coordinator; no direct author coordination was used here.

The full current [engine-debugging skill][debug-skill] was read before selecting these **future** checks. No listed command was run. Keep package filters narrow and engine suites sequential.

| Proposed test | Evidence required |
| --- | --- |
| `TestValueTransferCapture` (`./dagql`) | Diamond/direct-only and offer-only closure; pending/completed rows; attachment/attempt/bookkeeping busy; each revision changes during copying, including encoded-to-typed and output-only changes; concurrent offer replacement/capture/prune; cancellation releases every hold; no evaluation. |
| `TestValueTransferReferences` (`./dagql`, external core codec peers) | Fresh unrelated IDs, nested frames/recipe IDs/list/null/inline shapes, integers beyond float precision, owner-local reference validation; dangling IDs, cycles through owners and malformed roots rejected before publication. Marker filtering on every call vertex and no alias reconstruction. |
| `TestValueTransferImportPublication` (`./dagql`) | Failure after earlier identity plans prepared leaves no rows, roots or class unions. Import racing ordinary lookup exposes either absence or the whole graph. Before/after commit cancellation and concurrent Close return honest outcomes. |
| `TestValueTransferParts` (`./core`) | Real chain bytes for host capture, both Git trees, Container mount and a nested File/Directory view. Assert the view exports its underlying whole chain, and unselected sibling parts never open. Pending selection does not evaluate; broken completed local link/open stays an error. |
| `TestValueTransferForeignForms` (`./core` plus import peers) | Native File/Directory/Container forms in a bundle, missing Foreign, HTTP validators/digest without body, missing/native mirror or CacheVolume discriminator, and any CacheVolume backing all refused, as roots and nested references, before and after relocation. CacheVolume `foreign_uninitialized` with configuration and no backing is valid. Valid native checkpoints still decode; initialized B HTTP survives local restart and is cleared on onward transfer. |
| `TestValueTransferOfferOwners` (`./dagql`) | R and two owners all retain S independently; replacing one while held releases only its slot. Cycle refusal edits nothing. A socket used only by offers never enters lookup requirements, including through a dependent R. Direct installed reference adds/propagates it normally. Debug/prune/usage include owners without lookup entries. |
| `TestValueTransferPersistence` (`./dagql`, real stores) | Pending metadata/offers A→B→C, no owner runtime IDs; schema/envelope mismatch cold-start; completed output plus redundant offer accepted only locally and retired after owner attachment. Desired-map checkpoint and receiver pin survive restart; failed attachment honors whole-cache reset. |
| `TestModDepsForCallInstalledPreference` (`./core`) | Stable direct/transitive candidates, each Module ref position, dependency-owned return; both import orders; installed operational ID selected over lower imported scope. No installed match: eligible exact native row wins; ineligible row uses existing canonical/resource rules; missing row keeps existing error; undecoded row is present. |
| `TestForeignModuleContextReaders` (`./core`, `./core/schema`, `./engine/server`) | Every operational reader/conversion above returns the sentinel before host I/O on foreign input. Formatting and the path schema field return the recorded string. `ForeignWithSourceSubpathWorkspace`: a foreign source with a usable source Workspace succeeds, preserving Foreign. `ForeignLocalItemRemoval`: removing a local item succeeds with Foreign preserved. Both controls avoid the recorded host path; a subsequent actual host fallback still returns the sentinel. Optional .env/Git/Exists preserves the error. Native/Git/Directory controls and valid bound/source Workspace routes retain current behavior. |
| `TestRemoteCacheFixture` (`./core/schema`) | Gate absent, invalid root/paths, no path escape, uncached operations, exact IDs, typed JSON counts, cancellation, schema forks and concurrent body records. |

Example bounded unit commands: `go test ./dagql -run '^TestValueTransfer' -count=1`, then `go test ./core -run '^(TestValueTransfer|TestModDepsForCallInstalledPreference|TestForeignModuleContextReaders)' -count=1`. Run the schema/server foreign-reader cases separately. The targeted race command is `go test -race ./dagql -run '^(TestValueTransferCapture|TestValueTransferImportPublication|TestValueTransferOfferOwners)$' -count=1`; it checks real ownership/publication races, not string round trips.

Coordinator addendum 2 (`39a664f7383f5f80223df100b6870c6d16f41ea0`) bounds standalone batch 2 acceptance to the warmed-runtime order: B prepares its SDK and module runtime through normal `AsModule`, without serving the schema or entering `report`, then imports and serves its operational Module. Import after schema installation remains covered too. The fully cold order is retained as a separately skipped test naming addendum 2; it is a batch 4/7 acceptance case, requiring acquisition through local-equivalent selection or batch 1's builtin producer. Lookup eligibility is unchanged and batch 2 adds no acquisition.

The native batch-2 suite is `RemoteCacheTransferSuite/TestSchemaRecovery`. Use two dev engines with distinct state and clients, sharing only fixture bundle files. A executes the real module's report; B imports before/after normal module loading, obtains an ordinary report hit, saves its handle, reloads through `node(id:)` and calls a previously uncalled File/Directory contextual method after editing B's notes. Assert the actual function-entry counter and returned B contents. Release sessions and cleanly restart; in a new module-aware request repeat the saved-handle path with B's installed Module. A bare request retaining an eligible native recorded Module must prefer it over a lower imported sibling. Construct each of the two residual foreign-context cases separately and assert its explicit error, with an ordinary no-import control. Include interface, custom scalar/enum recovery and a bound-tool lazy-load/rebinding control using the defining schema.

The future command follows the current skill: `dagger api call engine-dev test --pkg ./core/integration --run='RemoteCacheTransferSuite/TestSchemaRecovery'`. Batch 2 need not demand report's pending exec Directories in this test. Batch 7 must extend the same CacheProbe and field to demand selected bytes, fail downloads, run the actual saved producer, verify donor release/restart and the accepted old-input failure. Those end-to-end acquisition outcomes cannot be claimed from metadata import or administrative-ID demand here.

The source audit supports the proposed calls and ownership paths; dynamic correctness, allocation cost, sharing retention and actual cross-engine callback skipping remain unmeasured. No additional product decision is proposed by this candidate.

[capture]: ../../../../dagql/cache_persistence_capture.go#L16
[visitor]: ../../../../dagql/cache_persistence_codec.go#L527
[matching]: ../../../../dagql/cache_egraph.go#L720
[dependencies]: ../../../../dagql/cache.go#L2907
[requirements]: ../../../../dagql/cache.go#L587
[parts]: ../../../../core/container_parts.go#L15
[container-persist]: ../../../../core/container_persistence.go#L25
[file-encode]: ../../../../core/file.go#L242
[export-chain]: ../../../../engine/snapshots/remote.go#L14
[import-chain]: ../../../../engine/snapshots/import.go#L8
[moddeps-call]: ../../../../core/query.go#L309
[local-source]: ../../../../core/modulesource.go#L2031
[wrapping]: ../../../../dagql/cache.go#L2463
[bound-tools]: ../../../../core/llm_object_tools.go#L55
[label]: ../../../../dagql/call/id.go#L75
[host-label]: ../../../../core/schema/host.go#L357
[http-label]: ../../../../core/schema/http.go#L239
[git-ref-label]: ../../../../core/schema/git.go#L1444
[git-commit-label]: ../../../../core/schema/git.go#L1816
[git-tree-label]: ../../../../core/schema/git.go#L1755
[git-commit-tree-label]: ../../../../core/schema/git.go#L1858
[module-label]: ../../../../core/schema/module.go#L3090
[source-label]: ../../../../core/schema/modulesource.go#L3451
[workspace-label]: ../../../../core/modfunc.go#L963
[content-digest]: ../../../../dagql/cache.go#L3301
[any-content-digest]: ../../../../dagql/types.go#L128
[result-any-content-digest]: ../../../../dagql/cache.go#L3490
[object-any-content-digest]: ../../../../dagql/cache.go#L3634
[local-git-ref-input]: ../../../../core/schema/git.go#L1427
[local-git-commit-input]: ../../../../core/schema/git.go#L1799
[image-label]: ../../../../core/schema/container.go#L1243
[content-fields]: ../../../../core/object.go#L338
[cache-inputs]: ../../../../dagql/cache_inputs.go#L102
[dynamic-inputs]: ../../../../dagql/objects.go#L614
[ref-path]: ../../../../dagql/cache_persistence_codec.go#L293
[envelope]: ../../../../dagql/cache_persistence_self.go#L40
[row-reporting]: ../../../../dagql/cache_persistence_import.go#L171
[lazy-mutex]: ../../../../dagql/cache.go#L2180
[frame-required]: ../../../../dagql/cache_persistence_worker.go#L242
[collection]: ../../../../dagql/cache.go#L1481
[prune]: ../../../../dagql/cache_prune.go#L530
[session-loader]: ../../../../dagql/cache_persistence_resolver.go#L66
[query-ref]: ../../../../dagql/call_request_input.go#L37
[persisted-object]: ../../../../core/persisted_object.go#L30
[family]: ../../../../dagql/cache_persistence_codec.go#L438
[file-selector]: ../../../../core/file.go#L587
[dir-selector]: ../../../../core/directory.go#L1148
[part-mapping]: ../../../../core/container_persistence.go#L309
[file-decode]: ../../../../core/file.go#L286
[dir-decode]: ../../../../core/directory.go#L307
[http-encode]: ../../../../core/http.go#L165
[volume-encode]: ../../../../core/cache.go#L298
[filesync-encode]: ../../../../core/client_filesync_mirror.go#L99
[mirror-encode]: ../../../../core/git_remote_mirror.go#L104
[source-encode]: ../../../../core/modulesource.go#L788
[http-resolve]: ../../../../core/http.go#L232
[teaching]: ../../../../dagql/cache_egraph.go#L1444
[retention]: ../../../../dagql/cache.go#L1359
[schema-version]: ../../../../dagql/cache.go#L150
[envelope-version]: ../../../../dagql/cache_persistence_self.go#L32
[lease-sync]: ../../../../dagql/cache.go#L1607
[desired-links]: ../../../../dagql/cache.go#L1562
[checkpoint-copy]: ../../../../dagql/cache_persistence_worker.go#L91
[checkpoint]: ../../../../dagql/cache_persistence_worker.go#L240
[server-boot]: ../../../../engine/server/server.go#L619
[schema-loaders]: ../../../../core/schema_build.go#L22
[interface-loader]: ../../../../core/interface.go#L124
[served-deps]: ../../../../engine/server/session.go#L3264
[builder-mods]: ../../../../core/moddeps.go#L131
[module-result]: ../../../../core/module.go#L2370
[scoped-selection]: ../../../../core/module.go#L326
[output-classes]: ../../../../dagql/cache_egraph.go#L1261
[exact-ref]: ../../../../dagql/cache_persistence_codec.go#L162
[context-dir]: ../../../../core/modulesource.go#L1506
[source-fs]: ../../../../core/modulesource.go#L2433
[context-local]: ../../../../core/modulesource.go#L1624
[context-file]: ../../../../core/modulesource.go#L1808
[context-git]: ../../../../core/modulesource.go#L1981
[env-reader]: ../../../../core/modulesource.go#L1145
[local-deps]: ../../../../core/modulesource.go#L2135
[source-stat]: ../../../../core/modulesource.go#L2520
[source-subpath]: ../../../../core/schema/modulesource.go#L1285
[path-field]: ../../../../core/schema/modulesource.go#L1592
[related-paths]: ../../../../core/schema/modulesource.go#L1633
[deduplicate-paths]: ../../../../core/schema/modulesource.go#L1696
[update-paths]: ../../../../core/schema/modulesource.go#L1822
[remove-paths]: ../../../../core/schema/modulesource.go#L1931
[config-paths]: ../../../../core/schema/modulesource.go#L2059
[scale-out]: ../../../../core/modtree.go#L587
[toolchain-paths]: ../../../../core/schema/modulesource.go#L3822
[pending-related]: ../../../../engine/server/session_workspaces.go#L2249
[source-string]: ../../../../core/modulesource.go#L1029
[module-ref]: ../../../../core/module.go#L2347
[root-address]: ../../../../core/modtree.go#L699
[core-install]: ../../../../core/schema/coremod.go#L175
[core-root-field]: ../../../../engine/server/session_workspaces.go#L1720
[json]: ../../../../core/json.go#L56
[native-fixture]: ../../../../core/integration/engine_persistence_test.go#L34
[debug-skill]: ../../../../skills/engine-debugging/SKILL.md#L1

[function-call]: ../../../../core/typedef.go#L2407
