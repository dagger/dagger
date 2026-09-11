# Persisted values and reference closures

This plan explains how an ordinary module return can survive saving, restart and another save with its value, GraphQL return type and referenced objects intact. It gives the Human and implementer the value representation required by later transfer between engines. It covers generic values and ordinary metadata/action objects; the complete LLM conversation has a separate commissioned plan. **Proposed**.

**Source:** `17f7dd89f4c2a3d5c3197e593e0e0488ebccc2a3`. **Document branch:** `local-persistence-consistency-review-45b88`, worktree `managed-1ab26e7e1bfcf22ff1d9ece0`. The checkout remains at `45b88f2b81c0b6fa14d82e2f4a2dba289d012261`; every source file cited here is identical at the named source commit. Checks for this plan are source reading and document validation only. No new tests or implementation have run. **Verified**.

## 1. An ordinary return survives restart

A module returns the environment entry `CI=true` from a Container. `EnvVariable` stores just `Name` and `Value` in `core/envvars.go:9-12`. The returned value has a cache row, the `sharedResult` that owns its identity and dependencies (`dagql/cache.go:2029-2065`). The core-object return converter loads that existing result; attachment can retain the same row under the module call's persistence policy (`core/schema/coremod.go:777-809`, `core/typedef.go:303-325`, `dagql/cache.go:5412-5431`). This is a source-backed route, not a newly demonstrated module invocation. **Verified / Inferred**.

Today an admitted object without an encoder fails the whole selected save (`dagql/cache_persistence_self.go:105-117`, `dagql/cache_persistence_worker.go:248-254`). **Verified**.

Add an explicit two-field payload so the returned entry can be read after restart and saved again. Complete the analogous missing object support and repair nulls, numbers and list types at the same boundary. Successful calls keep their present eligibility; unrelated saved results are not silently discarded to make a save succeed. **Proposed**.

That current save failure leaves the store marked unclean; the next startup resets saved results (`dagql/cache.go:4253-4272`, `dagql/cache.go:459-475`). Completing representation prevents this known trigger, while preserving the existing response to other save failures. **Verified / Proposed**.

For this example, the proposed object body is `{"name":"CI","value":"true"}` inside `PersistedResultEnvelope`, the generic saved record (`dagql/cache_persistence_self.go:25-35`). This envelope still carries row identity and concrete type. This is proposed payload notation. **Proposed**.

## 2. Terms and claim status

| Term | Concrete meaning |
| --- | --- |
| Row | `sharedResult`, the cache-owned state behind an `AnyResult` view (`dagql/cache.go:2029`). **Verified**. |
| Attached result | A result with a nonzero cache-local row identity accepted by `Cache.PersistedResultID` (`dagql/cache_persistence_resolver.go:34-49`). **Verified**. |
| Engine handle | A `call.ID` containing a result ID and complete type wrapper (`dagql/call/id.go:598-614`). **Verified**. |
| Persisted owner / root | A `persistedEdge` keeps a row alive independently of its requesting session; that retained row is a root (`dagql/cache.go:1308-1340`). **Verified**. |
| Capture | Building the selected disk graph before its transaction (`dagql/cache_persistence_worker.go:27`, `dagql/cache_persistence_worker.go:345`). **Verified**. |
| Reference closure | The root and rows reached through its `sharedResult.deps`; capture follows these edges (`dagql/cache_persistence_worker.go:275-341`). **Verified**. |
| Envelope | `PersistedResultEnvelope`, holding a null, scalar, list or object body (`dagql/cache_persistence_self.go:25-35`). **Verified**. |
| Codec | The encoder/decoder pair for one saved representation (`dagql/cache_persistence_self.go:46-64`). **Verified**. |
| Inline item | An envelope item with zero `ResultID`; a nonzero item instead names a separately attached row (`dagql/cache_persistence_self.go:286-317`). **Verified**. |
| Declared type | Recursive `ResultCallType`, including `NamedType`, `Elem` and `NonNull` (`dagql/result_call_frame.go:24-28`). **Verified**. |
| Recorded call | `ResultCall`, the stored receiver, field, arguments, module and return type (`dagql/result_call_frame.go:188-209`). **Verified**. |
| Installed class | An `ObjectType` registered on a server, supplying concrete Go type and selectable fields (`dagql/server.go:878`, `dagql/objects.go:179`). **Verified**. |
| Resource requirement | `SessionResourceHandle` availability required for a client to use a saved result (`dagql/cache_persistence_resolver.go:66-162`). **Verified**. |
| Snapshot role | A named storage use within a result; current `PersistedSnapshotRefLink` records `RefKey` and `Role` (`dagql/cache_persistence_self.go:394-397`). **Verified**. |

**Verified** identifies source facts. **Inferred** identifies consequences or reachable routes derived from them. **Proposed** specifies this batch's required behavior. **Required proof** names checks to run during implementation; none is a passing-check claim. **Open** identifies unresolved scope or evidence.

## 3. The actual row at each moment

`EnvVariable.Name` and `Value` are filled when creating the entry; its persistence support needs no runtime client or mutable connection (`core/envvars.go:9-12`, `core/schema/container.go:2348`). The cache owns the surrounding state below. **Verified**.

| Actual fields | Meaning, changes and protection |
| --- | --- |
| `id`, `resultCall` | Stable row identity and recorded call, including declared type. `resultCallMu` protects publication of the call pointer (`dagql/cache.go:2029-2054`). **Verified**. |
| `self`, `hasValue`, `isObject`, `objClass`, `persistedEnvelope` | Typed value or bytes awaiting decode. Decode publishes under `payloadMu` and clears the envelope (`dagql/cache_persistence_import.go:742-790`). **Verified**. |
| `deps`, `depParents`, `incomingOwnershipCount` | Exact child references and owners, changed under `egraphMu` during attachment and retention (`dagql/cache.go:2800-2899`, `dagql/cache.go:1308-1340`). **Verified**. |
| `sessionResourceHandle`, `requiredSessionResources` | Direct and transitive requirements; import rebuilds the closure (`dagql/cache_persistence_import.go:359-386`). **Verified**. |
| `snapshotOwnerLinks` | Result-owned storage links under `payloadMu`; `leaseSyncMu` serializes lease reconciliation (`dagql/cache.go:2298-2314`, `dagql/cache.go:1556-1621`). **Verified**. |

Use illustrative row 42 for the environment entry. These numbers are explanatory, not measurements. **Proposed example**.

| Moment | Concrete state |
| --- | --- |
| Core entry created | `id=42`, `self={Name:"CI", Value:"true"}`, `hasValue=true`, `isObject=true`, no envelope; no persisted owner yet. |
| Module return retained | Same row and original call; one persisted edge owns it. No new resource handle or snapshot link. |
| Current save | Missing encoder causes capture error; no successful new body for row 42 (`dagql/cache_persistence_worker.go:248-254`). **Verified** consequence. |
| Proposed save | Row 42 and its call are written with the two-field object body and persisted owner. |
| Restart before read | `id=42`, `self=nil`, `hasValue=false`, `isObject=true`, saved envelope present; persisted owner restored. |
| First read | Same row, decoded entry in `self`, `hasValue=true`, envelope cleared. |
| Second save | Same value and references are encoded again. If no first read occurred, the saved envelope is copied instead (`dagql/cache_persistence_worker.go:470-475`). |

The proposed states reuse the existing import/publication mechanics in `dagql/cache_persistence_import.go:164-188` and `dagql/cache_persistence_import.go:757-790`. **Proposed**.

## 4. Preserve nulls, numbers and declared lists

The module list converter creates each nullable child separately; top-level field normalization does not normalize every stored child (`core/modtypes.go:275-309`, `core/modtypes.go:405-416`, `dagql/objects.go:693-707`). The generic encoder tests object/list forms before scalar fallback, without first unwrapping the nullable (`dagql/cache_persistence_self.go:105-201`). **Verified**.

| Value before save | Current risk | Required representation |
| --- | --- | --- |
| Attached absent Int, row 51 | `scalar_json` containing `null` reaches an integer decoder (`dagql/cache_persistence_self.go:255-279`). | `kind="null", resultID=51`; preserve its call, requirements and list position. |
| Integer `9007199254740993` | Decoding through `any` uses a floating-point number (`dagql/cache_persistence_self.go:259-266`). | Exact numeric token through both saves. |
| Empty `[[Int!]!]!` | First-item/flattened-name inference loses recursive shape (`dagql/cache_persistence_self.go:320-335`). | Use the complete recorded call type, including both list levels. |
| Interface-valued list | First concrete object does not define the declared interface element (`core/interface.go:76-120`). | Keep the declared interface type and each concrete object's own row/schema. |

The right column is **Proposed**. Current numeric/type consequences are **Inferred** from the cited algorithms; prior isolated evidence is described in section 10.

1. In `encodePersistedResultEnvelope`, record original row ID, resource handle and declared call type before dereferencing. Classify the normalized value; keep original identity even when dereference returns another view or absence (`dagql/cache.go:3063-3085`). **Proposed**.
2. In null import, preserve the same ID, call, handle, edges and requirements. Set `isObject=false`, `hasValue=true`, `persistedEnvelope=nil`, and `self` to an invalid `DynamicNullable`, the runtime nullable wrapper whose `Elem` describes its type (`dagql/nullables.go:409-427`). Use the complete declared type, reject absence for a non-null declaration, and add no lazy body. Its `Type()` matches a fresh `DynamicNullable{Elem: declaredInner}`; `DerefValue` reports absence; the parent's `NthValue` returns the attached row; re-encoding writes null with that same ID. Inline null remains nil (`dagql/cache_persistence_import.go:178-185`, `dagql/cache.go:3063-3085`, `dagql/cache.go:3133-3172`). **Proposed**.
3. Replace scalar decoding through `json.Unmarshal` into `any` with one shared `UseNumber` reader that requires exactly one complete JSON value. Apply it to the generic envelope and `decodePersistedModuleObjectValue`; keep existing scalar range/error conversion (`dagql/types.go:321-372`, `core/object.go:975-983`). **Proposed**.
4. Apply lossless untyped JSON decoding to `Module.SDKConfig`, `Module.WorkspaceConfig` and `ModuleSource` configuration, plus the new standalone SDK payload (`core/module.go:999-1017`, `core/module.go:1107`, `core/modulesource.go:921`). The ordinary module return path already uses `UseNumber`; keep that precision through persistence and subsequent SDK conversion (`core/typedef.go:2486-2498`). **Proposed**.
5. Replace list element inference with a recursive descriptor backed by `ResultCall.Type.Elem`; remove `ElemTypeName`. Reject missing or incompatible declared structure. Preserve nonzero children as exact row loads, and derive inline child calls from the parent's element type (`dagql/cache_persistence_self.go:292-335`). **Proposed**.

Rebuild declared types with existing shapes: `DynamicResultArrayOutput` for a non-null list, `DynamicNullable` around a nullable level, and `persistedTypedRef` for named leaves. Set the array's `Elem` from the recursive declaration, never from a concrete item. A present nullable value has `Valid=true` and its actual array in `Value`; an absent value has `Valid=false`. Descriptor-only nullable elements need no actual value (`dagql/builtins.go:144-163`, `dagql/nullables.go:409-441`). **Proposed**.

Choose this composition over a new restored-array wrapper. Before dereference its `Type()` preserves the nullable declaration; the ordinary `DerefValue` view exposes the array with the same row identity and non-null outer type, as fresh nullable results do (`dagql/cache.go:2947-2954`, `dagql/cache.go:3063-3085`). `Element`, `NthValue` and attachment remain the existing array methods (`dagql/builtins.go:159-204`). Do not add enumeration to an absent nullable or require it before ordinary nullable dereference. The prior scratch shape comparison covered empty nested and optional-element arrays, not proof of every view; acceptance now compares all these interfaces with fresh values. **Proposed / Evidence limit**.

## 5. References use one codec contract

An engine handle is a `call.ID` containing a cache-local result ID and exact return type (`dagql/call/id.go:598-614`). Suppose a private module field holds a handle to engine A's row 42. Engine B already uses 42 for another object. A transfer bundle, the later batch's collection of selected encoded rows, can assign that object ordinal 3. After B reserves row 901, the field must identify 901 with the original type wrapper. These identities are illustrative. **Proposed example**.

`call.ID` has two forms: an engine handle stores `ResultID` and `RootType`; a recipe stores a graph of calls indexed by digest (`dagql/call/id.go:598-624`). `ModuleObject` also has a tagged private `call_id` value. Its current helper directly encodes/decodes that ID outside the numeric result-reference helper (`core/object.go:817-851`, `core/object.go:973-974`, `core/persisted_object.go:39-74`). **Verified**.

Introduce `PersistEncodeContext` and `PersistDecodeContext` in `dagql`, replacing the cache-only encoder argument and the decoder's separate server/result/call arguments. Each context explicitly carries the owner row, its recorded call and the relevant cache access; decode also carries the defining server. Pass them through generic, object and `Lazy.EncodePersisted` codecs (`dagql/cache_persistence_self.go:37-64`, `core/lazy_state.go:15-19`). The following names and API behavior are proposed, not existing Go declarations. **Proposed**.

| Seam | Exact responsibility |
| --- | --- |
| Encode `ResultRef(AnyResult)` / decode `ResultRef(uint64)` | Emit an attached local row ID; load that exact local row. Optional zero remains explicit absence. These operations do not mint value equality. |
| Encode `CallID(*call.ID)` / decode `CallID(string)` | Preserve the tagged form. Handles carry translated row identity plus exact recursive type. Recipe trees remain typed call descriptions, including nested arguments and modules. No execution. |
| Encode `SnapshotRole(role, localRefKey)` / decode `SnapshotRole(ownerID, role)` | Record/resolve a declared storage role using existing snapshot-link data. Neither operation opens storage or treats another manager's key as local. |
| `VisitEncodedReferences` | Walk and optionally replace declared references in envelopes, recorded calls and explicit object/lazy payloads. It requires no installed module schema, provider, filesystem opening or typed value construction. |

All table entries are **Proposed**. The visitor also returns the declared field/list-index/typed-ID position relative to its exact owner, interpreted by codec family and envelope version. Later observation bindings may name these paths. A path does not establish requester correspondence or value/content equality; known snapshot views still need their type-specific mapping. Update `encodePersistedObjectRef`, `encodePersistedCallID`, both load helpers and snapshot helpers in `core/persisted_object.go:39-153` to use these contexts. Typed loads retain the existing exact-ID path with no session-selected equivalent substitution; the outer client load still enforces transitive resource requirements (`dagql/cache_persistence_resolver.go:126-162`). **Proposed**.

The visitor must report reference purpose: self identity, required child row, descriptive call reference, immutable output storage, or local mutable backing. Distinguish the latter two through the declared snapshot-role variant and codec family; add no durable local/foreign flag. CacheVolume and mirror mutable roles remain local configuration/backing, while immutable output roles carry transferable descriptions (`core/cache.go:307-330`, `core/client_filesync_mirror.go:104-132`, `core/git_remote_mirror.go:108-132`). **Proposed**.

Self identity is not an ownership edge. A tree-table node index is not a cache row. Nonzero `ResultCallRef.ResultID` values retain their existing dependency ownership; digest-only call descriptions add no invented row (`dagql/cache.go:5536-5585`, `dagql/cache.go:5693-5708`). Walk `ResultCall.Receiver`, `Module.ResultRef`, argument/implicit-input literals and nested `ResultCallRef.Call`; preserve shared call subgraphs while building fresh frames (`dagql/result_call_frame.go:63-90`, `dagql/result_call_frame.go:139-155`, `dagql/result_call_frame.go:188-209`). **Proposed**.

The same walk covers envelope/list `ResultID`, module/type/workspace references, and every lazy payload's parent/source/module references, including `ParentResultID` (`core/directory.go:484-568`, `core/file.go:358-383`). Container lazy dispatch continues to use its recorded field; the visitor receives that frame as well as the body (`core/container.go:4369-4390`). **Proposed**.

Use one explicit `ObjectCodec` discriminator in the existing object envelope. It identifies the Go payload family; `TypeName` still identifies the GraphQL value. For example, every user-named `ModuleObject` uses one module-object visitor although typed decoding needs its particular module and type definition (`core/object.go:767-795`). Register pure visitors for built-in payload families, including the schema package's Label and HealthcheckConfig, at engine initialization independently of user-schema installation. Dispatch is by envelope version and family; add no separate per-family version or parallel payload representation. Unknown families fail explicitly. **Proposed**.

Use the existing explicit payload structs/tags for visitors. Preserve opaque `json.RawMessage` leaves, exact numbers and ordering. Recipe ID traversal must respect the current recipe-only literal format and must not mutate protobuf pointers returned by `ToProto` (`dagql/call/literal.go:74-79`, `dagql/call/id.go:597-624`). Ordinary numbers, strings, schema text, raw tool arguments and provider tool-call `CallID` strings are not cache references (`core/llm.go:341-344`). Client IDs retain their original meaning; credentials and runtime pointers are not visitor inputs. **Proposed**.

**Choose eager relocation.** Transfer ordinals exist only in the bundle. The later admission batch reserves all B IDs, rewrites declared references once, validates the translated closure and establishes ownership before exposing rows. B stores entirely local envelopes and frames. Its untouched save can keep copying those bytes (`dagql/cache_persistence_worker.go:470-475`). The alternative durable ordinal-to-B namespace table adds per-payload lifetime and atomic update rules without a demonstrated benefit when the visitor already exists. **Proposed / Recommendation**.

This batch supplies and verifies the visitor; the live-description batch owns transfer allocation/publication. Missing actual referenced state can make a selected export unavailable. It does not authorize dropping a required child, excluding its type permanently, or weakening local capture errors. Snapshot transfer must resolve `(owner, role)` into B's role state before typed decode; storage-description/acquisition details belong to their owning batches. **Proposed**.

## 6. Ordinary payloads and owned children

Add paired object codecs in the packages declaring these types. The table is the payload inventory, not a runtime eligibility list. Plain data members are inline; separately attached results remain references. Empty strings, null versus present pointers, list positions and field order where observable must survive. **Proposed**.

| Values and source-backed producing route | Fields to preserve |
| --- | --- |
| `EnvVariable`, `Label`: Container environment/labels (`core/schema/container.go:2348`, `core/schema/container.go:2403`). | `Name`, `Value` (`core/envvars.go:9`, `core/schema/container.go:2387`). |
| `Port`: exposed ports (`core/schema/container.go:4572`). | `Port`, `Protocol`, nullable `Description`, `ExperimentalSkipHealthcheck` (`core/net.go:15-19`). |
| `HealthcheckConfig`: optional Container health check (`core/schema/container.go:3264`). | `Args`, `Shell`, `Timeout`, `Interval`, `StartPeriod`, `StartInterval`, `Retries` (`core/schema/container.go:3242-3250`). |
| `SDKConfig`: module/source metadata (`core/schema/modulesource.go:306`). | `Source`, `Debug`, lossless `Config`, `Experimental` (`core/modulesource.go:127-131`). |
| `modules.ModuleConfigClient`: `ModuleSource.configClients` items (`core/modulesource.go:216`, `core/schema/modulesource.go:307`). | `Generator`, `Directory` (`core/modules/config.go:451-464`). Add paired codecs in that package and a visitor declaring no references. |
| `GitBundleRef`, `LLMSkill`: bundle refs and discovered skills (`core/schema/git.go:275-284`, `core/schema/llm.go:446`). | Respectively `Name`/`SHA` and `Name`/`Description` (`core/git_bundle.go:55-58`, `core/llm_skills.go:42-44`). |
| `Schema`: schema construction/merge (`core/schema/schematool.go:18-45`). | Parsed `Introspection`, including directives/definitions; reuse `Contents` and validated parsing (`core/schematool.go:18-62`). No live server. |
| `LLMContentBlock`, `LLMMessage`: message fields (`core/schema/llm.go:230-235`, `core/schema/llm.go:581`). | Block `Kind`, `Text`, provider `CallID`, `ToolName`, raw `Arguments`, `Errored`, `Signature`; message `Role`, ordered `Content`, nullable `TokenUsage` with `InputTokens`, `OutputTokens`, `CachedTokenReads`, `CachedTokenWrites`, `TotalTokens` (`core/llm.go:335-353`, `core/llm.go:407-412`, `core/llm.go:227-238`). |
| `CurrentModule`: runtime reflection (`core/schema/module.go:2074-2088`). | Attached `Module` (`core/module.go:2693-2695`); add a hook that attaches/replaces it and returns the dependency. Never substitute the caller's current module. |
| `WorkspaceMigration`, `WorkspaceMigrationStep`: explicit migration reports (`core/schema/workspace_migrate.go:51-72`). | Report `Changes`, ordered `Steps`; step `Code`, `Description`, ordered `Warnings`, `Changes` (`core/workspace_migration.go:6-16`). |
| `Cloud`, `TerminalLegacy`: configuration root and legacy terminal result (`core/schema/cloud.go:17-31`, `core/schema/container.go:4697-4734`). | Empty body; later dynamic/placeholder fields retain current behavior (`core/cloud.go:5`, `core/container.go:7498`). |

The declarations/routes are **Verified**; retention through a default module return is **Inferred**. `ModuleConfigClient` is installed from another package, so a scan limited to core-package receivers misses it. Its parent's inline payload does not encode separately attached item rows (`core/modulesource.go:487`, `core/modulesource.go:807`). A module can return an existing core object through the ordinary converter (`core/schema/coremod.go:777-809`); acceptance must exercise an actual returned client item. Payload and attachment changes are **Proposed**. This plan adds no new production invocation evidence.

Migration `Changes` fields are raw `*Changeset` values. Reuse their explicit inline before/after directory-reference payload and dependency hook for both report and steps (`core/changeset.go:416-460`, `core/changeset.go:522-559`). Do not manufacture rows for these raw values or serialize their internal cached computations. **Proposed**.

### 6.1 Module-tree actions

`ModTreeNode` stores parent, function name/description, root `Module`, defining `OriginalModule`, `Type`, and four action flags; `DagqlServer` is derived runtime state (`core/modtree.go:26-40`). Reuse `persistedModTreeEncoder`, `decodePersistedModTree` and `attachModTreeNodeDependencyResults` for the following payloads (`core/modtree.go:1182-1405`). **Verified** mechanisms; **Proposed** reuse.

| Value | Additional saved state and attachment |
| --- | --- |
| `Agent`, `TerminalTarget` | Node-table reference (`core/agents.go:14-16`, `core/terminals.go:13-15`). |
| `Check` | Node, `Completed`, `Passed`, `IsGenerate`, nullable `Error` result; attach the error when present (`core/checks.go:14-23`). |
| `Up` | Node and ordered `PortMappings` (`core/up.go:16-19`). |
| `AgentGroup`, `CheckGroup`, `UpGroup`, `TerminalGroup` | Optional root Node; ordered `Agents`, `Checks`, `Ups` or `Terminals`; exact attached `BoundWorkspace` (`core/agents.go:20-31`, `core/checks.go:25-36`, `core/up.go:21-31`, `core/terminals.go:18-24`). |

All saved-state selections are **Proposed**, grounded in the cited fields. Use one node table per payload, preserving parent sharing and all member trees even when the group root is nil. Attachment visits each tree and the bound workspace, replaces fields with attached results and returns the owned children. Follow GeneratorGroup's existing workspace encoding and attachment (`core/generators.go:637-758`). **Proposed**.

Do not follow the groups' comments that assume loading an ID reruns field selection: payload decoding does not do so (`core/agents.go:23-31`, `dagql/cache_persistence_self.go:53-58`). Decode derives the server from saved module references, preserving defining-module authority (`core/modtree.go:1269-1309`, `core/modtree.go:654-681`). It runs no action and starts no terminal/service. **Verified** discrepancy; **Proposed** behavior.

Save a failed completed Check as a reported value: `Check.Run` returns that value with nil error (`core/checks.go:242-263`). A failed group instead returns nil/error (`core/checks.go:116-134`); do not use that route to claim a published failed group. Keep Generator/GeneratorGroup's existing synthetic specification, completion, changes, workspace base/result and `LoadFailures` as comparison coverage (`core/generators.go:467-553`, `core/generators.go:637-758`). **Verified / Proposed**.

### 6.2 Nested module values and defining schema

Keep `ModuleObject.Fields`' tagged null/scalar/map/list/result/typed-ID forms (`core/object.go:745-880`, `core/object.go:967-1008`). Existing attachment turns a private handle into an exact attached child (`core/object.go:684-722`). Preserve that ownership; route any remaining tagged typed IDs through `CallID`. Semantic declared object fields must remain attached references; preserve their raw-ID rejection (`core/object.go:759-760`, `core/object.go:785-786`). **Proposed**.

A private recipe ID is a call description, not proof of an already materialized child. The visitor must neither execute it nor invent an ownership edge from a digest. A private handle requires its actual row and exact type. This is transfer correctness for existing forms, not a newly demonstrated dangling-handle production defect. **Proposed / Evidence limit**.

Typed decoding continues to use the saved call's modules and concrete type definition. Generalize the import type-name collection and resolver to include missing scalar/enum types as well as objects, using `resultServerForCall`; retain current failures when required schema is absent (`dagql/cache_persistence_import.go:563-577`, `dagql/cache_persistence_import.go:724-738`, `core/schema_build.go:22-44`). A cold module-enum fixture must establish this path; no production failure for it is claimed. Interface returns decode as their actual implementation objects (`core/interface.go:76-120`). **Proposed / Required proof**.

The later conversation codec uses these same seams for bound receiver/module/workspace/skill-directory/service references and lazy typed IDs. Frozen defining schema text remains data tied to its defining module. Effective LLM configuration can be an immutable internal input; it needs no new reference row. Complete LLM behavior remains in that separate plan (`core/llm_object_tools.go:48-62`, `core/llm.go:1277-1327`). A message whose retained closure includes its LLM receiver still needs that batch; adding the message codec alone does not complete that save. **Proposed**.

## 7. Preserve ownership and resource limits

Capture follows settled dependency edges and aborts on any encoding error (`dagql/cache_persistence_worker.go:248-254`, `dagql/cache_persistence_worker.go:275-341`). New hooks express owned references through this existing graph. No codec adds permanent roots or extends expiry. Failed attachment must retain existing cleanup behavior (`dagql/cache.go:2800-2899`). **Verified / Proposed**.

Import restores dependency/resource closure before use; publishing a decoded value must not overwrite its inherited requirements (`dagql/cache_persistence_import.go:359-386`, `dagql/cache_persistence_import.go:757-772`). Exact internal child loads do not replace the outer client's resource check (`dagql/cache_persistence_resolver.go:126-162`). Snapshot owners are restored before storage cleanup, and post-decode lease synchronization remains required (`dagql/cache_persistence_import.go:502-539`, `dagql/cache_persistence_import.go:778-790`). **Verified**; preserve these rules. **Proposed**.

Secret/UnixSocket handles require fresh session binding (`core/secret.go:55-64`, `core/socket.go:67-124`). Host-IP Service sockets instead store raw `URLVal`, `PortForwardVal` and `SourceClientID` (`core/service.go:102-108`, `core/service.go:152-160`). Preserve those original client identities and present use-time errors. Do not rebind an A-specific host to B or claim every decoded Service can run there. A missing required host capability may prevent particular transfer/use; no new host-service policy is introduced. **Verified / Proposed**.

### 7.1 Remote Git fields remain usable

`RemoteGitRepository` holds `SSHAuthSocket`, `AuthToken`, `AuthHeader` and `Services`, but its current payload omits all four (`core/git_remote.go:38-51`, `core/git.go:508-513`, `core/git.go:552-558`). Attachment retains them, while decode builds a backend without them (`core/git.go:390-440`, `core/git.go:597-613`). Later service startup, SSH mounting and HTTP authentication read those fields (`core/git_remote.go:177-181`, `core/git_remote.go:224-225`, `core/git_remote.go:263-274`). This is a **Verified** representation omission and **Inferred** use consequence, not a newly observed runtime failure.

Add optional `SSHAuthSocketResultID`, `AuthTokenResultID`, `AuthHeaderResultID`, and `Services []persistedServiceBinding` to `persistedRemoteGitRepositoryPayload`. Use exact reference contexts for each load and the existing service-binding shape (`ServiceResultID`, `Hostname`, `Aliases`) and helpers (`core/service.go:111-115`, `core/service.go:255-291`). Preserve service order. The visitor enumerates all those declared reference positions. Preserve empty versus present handles; resolve actual secret/socket material only through ordinary fresh resource use. No plaintext credentials enter the payload. **Proposed**.

Add explicit `MirrorResultID` too. The ordinary Git constructor already supplies a mirror and attachment owns it (`core/schema/git.go:938-960`, `core/git.go:377-388`). Decode loads the exact prepared local mirror reference instead of relying on its current URL-based `Select` to create an unrecorded dependency. Zero preserves an absent mirror; the existing use path reports missing backing (`core/git_remote.go:521-522`). The next section assigns preparation of transferred configuration. **Proposed**.

### 7.2 Mutable backing uses B's ordinary rows

Keep local missing-role errors for both mirror codecs (`core/client_filesync_mirror.go:154-160`, `core/git_remote_mirror.go:141-148`, `core/persisted_object.go:129-136`). This batch supplies context/family/role classification only. The producer batch factors ordinary mirror construction and `EnsureCreated` from the current query fields (`core/schema/query.go:129-149`). **Proposed / Verified**.

The description batch transfers mirror configuration, never A's mutable snapshot keys. It selects or constructs B's own row through `_remoteGitMirror` or `_clientFilesyncMirror`, and maps the incoming declared reference to that B row (`core/schema/query.go:42-52`). Before exposing a GitRepository, admission installs its owning dependency on that prepared mirror. A decode-time selection alone does not call `AttachDependencyResults` and cannot repair a dropped edge (`dagql/cache_persistence_import.go:742-790`, `dagql/cache.go:2800-2899`). **Proposed / Verified**.

RemoteGitMirror is retained by GitRepository. No immutable dependency on ClientFilesyncMirror was established in the source audit; do not invent one. If filesync mirror configuration itself is selected for transfer, use its B constructor under the same rule. Actual host computation still validates compatible client access; that belongs to the producer batch. Do not weaken local decoding or add an A mirror row with a missing local role. **Proposed / Evidence limit**.

Existing filesystem, service, volume, module, generator, Git/HTTP and metadata codecs also adopt the context/visitor contract. Pending and completed forms remain covered, including untouched completed descriptors and Container's partial parts (`core/directory.go:227-329`, `core/file.go:212-308`, `core/container_persistence.go:42-78`). New executable inputs and live capture synchronization belong to the producer plan. Local mutable cache-volume/filesync/mirror support remains; their mutable contents are outside remote transfer, not a reason to discard immutable dependent results. **Proposed**.

### 7.3 Existing codec coverage

The existing encoder inventory below must have matching visitor coverage. A family with no references declares that explicitly. These are **Verified** encoder locations, not proof of complete field fidelity or independent restart usability. Check saved semantic fields against each actual value type as well as registering its visitor; the Git omission above demonstrates why counting codec names is insufficient. **Required proof**.

| Existing payload family | Encoder entries |
| --- | --- |
| Filesystems and resources | Container `core/container_persistence.go:42`; Directory `core/directory.go:227`; File `core/file.go:212`; Service `core/service.go:117`; Volume `core/volume.go:204`; Secret `core/secret.go:55`; Socket `core/socket.go:67`; CacheVolume `core/cache.go:307`; ClientFilesyncMirror `core/client_filesync_mirror.go:104`; RemoteGitMirror `core/git_remote_mirror.go:108`. |
| Module/workspace/action state | Module `core/module.go:1020`; ModuleSource `core/modulesource.go:787`; ModuleObject `core/object.go:745`; Workspace `core/workspace.go:750`; WorkspaceGit `core/workspace.go:1032`; WorkspaceModule `core/workspace_module.go:34`; WorkspaceModuleSetting `core/workspace_module.go:76`; WorkspaceSDK `core/workspace_module.go:126`; Generator `core/generators.go:555`; GeneratorGroup `core/generators.go:637`. |
| Immutable outputs | GitRepository `core/git.go:514`; GitRef `core/git.go:627`; GitCommit `core/git.go:680`; GitBundle `core/git_bundle.go:128`; HTTPState `core/http.go:165`; Changeset `core/changeset.go:416`; GeneratedCode `core/codegen.go:31`; SearchResult `core/search.go:48`; SearchSubmatch `core/search.go:119`. |
| Definitions | Function `core/typedef.go:89`; FunctionArg `core/typedef.go:658`; TypeDef `core/typedef.go:854`; ObjectTypeDef `core/typedef.go:1222`; FieldTypeDef `core/typedef.go:1510`; InterfaceTypeDef `core/typedef.go:1627`; ScalarTypeDef `core/typedef.go:1795`; ListTypeDef `core/typedef.go:1844`; InputTypeDef `core/typedef.go:1916`; EnumTypeDef `core/typedef.go:2021`; EnumMemberTypeDef `core/typedef.go:2202`. |
| Data/reflection | Address `core/address.go:30`; Host `core/host.go:27`; EnvFile `core/envfile.go:52`; JSONValue `core/jsonvalue.go:27`; Error `core/error.go:105`; ErrorValue `core/error.go:157`; DiffStat `core/changeset.go:131`; Stat `core/directory.go:3375`; FunctionCall `core/typedef.go:2465`; FunctionCallArgValue `core/typedef.go:2557`; SourceMap `core/typedef.go:2599`; LLMTokenUsage `core/llm.go:282`; LLMVariable `core/llm.go:2736`. |

Enums and scalar wrappers use generic scalar decoding, not new object codecs (`core/schema/query.go:54-76`). Root Query and interface markers describe schema/context. Binding has no established producing route; engine inspection types are hidden from module SDKs (`core/env.go:10-23`, `core/env.go:46-51`). No retained concrete Void or inspection-object route was established in the prior audit. Keep these as reachability questions, not permanent exclusions; any concrete retained route found while completing coverage requires representation and ownership before claiming completion. **Verified / Open**.

## 8. Format and implementation order

Keep the existing hard reset without migration. Increment `cachePersistenceSchemaVersion` and envelope version for changed null/type/family interpretation; allocate actual numbers against the implementation stack (`dagql/cache.go:143`, `dagql/cache_persistence_self.go:25`). Every later PR that changes interpretation must use a distinct compatible transition; do not reuse a version for incompatible intermediate shapes. Reset discards the saved result database and worker snapshot/content storage (`engine/server/server.go:532-555`, `engine/server/server.go:676-689`). **Proposed** with **Verified** reset cost.

1. Add the contexts, pure reference visitor and object-family registration; convert every existing object/lazy codec and direct typed-ID helper. Extend the existing explicit payload structs. **Proposed**.
2. Implement null identity, exact numeric decoding, recursive array descriptors and type/schema preparation. Update import and untouched-save validation together. **Proposed**.
3. Add the metadata/message/report codecs and action-tree attachment/codecs from section 6; repair remote Git fields and exact mirror references from section 7.1. These can form a separate commit within this same PR. **Proposed**.
4. Establish the focused controls below, including ordinary module dispatch and the complete registered-codec inventory. Coordinate the reference seam with live descriptions and conversation authors. **Required proof**.

Cost is proportional to encoded data and reference count. Eager relocation parses selected payloads once and writes B-local bytes; it adds no provider call or storage opening. Memoized call/tree traversal must preserve sharing instead of expanding the graph (`dagql/result_call_frame.go:79-90`). The object-family tag adds small per-object metadata; no measured performance result is claimed. Missing codecs add the data and owned children that successful retention already requires. **Inferred / Proposed**.

Generic reflection over all Go fields would serialize runtime state; rejecting unsupported successful calls or saving only convenient types would change policy. Neither is selected. Partial capture recovery and the frozen lane's shutdown error-reporting correction are outside this PR. **Decision**.

## 9. Focused acceptance and deliberate controls

Run these during implementation, using plain engine/cache/data fixtures and the repository debugging skill. No fake remote service, real provider call or broad suite is needed. **Required proof**.

| Case | Required observation and deliberate control |
| --- | --- |
| Ordinary module return | Actual module returns EnvVariable, Port list, Schema and a ModuleConfigClient item. Save, restart, field selection and second save succeed. Remove one encoder: capture still fails as a whole; no silent partial save. |
| Null identity | Restored `Type()` matches fresh DynamicNullable; `DerefValue` reports absence; parent `NthValue` returns the attached row; re-encode emits null with the same ID. Keep call/deps/resources. External `Nth`/field normalization returns absence. Drop ID or restore scalar-null encoding: fail. |
| Numbers | `9007199254740993`, signed integer bounds, ordinary floats and nested SDK/config JSON remain exact through two saves and SDK conversion. Old untyped decoding must fail the large-number assertion. Trailing JSON and out-of-range values remain errors. |
| Declared types | Empty/all-null nested arrays, optional list levels/elements and interface arrays keep full `Type()`, handle type, `Element` and child order. Compare fresh/restored nullable dereference and subsequent `NthValue`; first-item inference must fail even if bytes match. |
| Reference relocation | A→ordinal→B map fixtures with colliding local IDs cover self/list refs, frames, every lazy kind and private handle/recipe IDs. Preserve wrappers and shared call graphs. Ordinary scalar leaves and provider tool-call IDs remain unchanged. Omit a required mapping: return an error without dangling references. Live admission is tested in its owning batch. |
| Second save | Run both typed-read and untouched middle-process paths before a second restart. Re-export either form; no A ID survives. Assert actual child values, not just matching JSON. |
| Ownership/schema | A module object and action group share a Directory. After session release/pruning/restart, retained owners preserve it; final removal releases it once. Omit BoundWorkspace or defining module: action selection/schema assertion fails. Decode itself runs no action. |
| Data fidelity | Roundtrip every new family using meaningful combinations: nil/present Port description, Schema directives, signed thinking blocks, message/token order, migration changes and warnings. Drop Signature or an ordered member: observable assertion fails. |
| Actions | Pending/completed Check, including `Check.Run`'s failed value; each group with overlay workspace; ordinary/synthetic Generator comparison. Calling a restored action uses the saved workspace. |
| Resources/formats | Valid/missing fresh resource binding, raw host-client preservation, missing snapshot role, malformed current payload, previous-version reset and new-version clean reopen. Unknown codec family must not be silently accepted. |
| Remote Git | Restore exact socket/token/header/service refs and hostname/aliases; use controlled fresh handle fixtures to verify auth selection and service inputs. Omit each payload ref deliberately: detect the lost field even while its dependency survives. No real Git server is needed. |
| Mutable roles | Visitor classifies immutable output versus mutable backing; no A mutable key becomes B-local. Local missing-role controls still error. Description-batch acceptance proves ordinary B mirror selection and retained ownership before exposure. |

Extend existing meaningful module-object, import/ownership and Container persistence fixtures (`core/object_test.go:446`, `core/object_test.go:899`, `dagql/cache_persistence_import_test.go:388`, `dagql/cache_persistence_import_test.go:1367`, `core/container_persistence_test.go:300`, `core/container_persistence_test.go:548`). Include installed classes from every package, including ModuleConfigClient. Assert that each implemented family has its visitor, preserves its semantic fields and exercises the same declared references through encode/decode. Do not infer all-family coverage from one scalar fixture. **Required proof**.

No TLA extension is proposed for pure representation changes: existing ownership/capture/restart transitions remain unchanged (`dagql/tla/CacheLifecycle.tla:2578`, `dagql/tla/CacheLifecycle.tla:2636`). Focused Go checks cover JSON/type fidelity and newly expressed edges. Any later change to admission publication or ownership must be modeled in its owning batch before implementation. **Proposed**.

## 10. Evidence and remaining decisions

The prior isolated audit used actual module converters and local persistence under manually admitted calls at 45b88. Its tests passed because they asserted current faults: missing encoders, nullable decode failure, rounded large Int and lost list shape. They did not invoke an actual module or prove a repair. Evidence remains in `/tmp/local-persistence-audit-34811b57` (`run-45b88.log`, `extended-45b88.log`). No new run was performed for this plan. **Historical evidence**.

Routine engineering choices selected here are complete representations, explicit owned references, eager relocation, hard format cuts, existing all-or-nothing local errors and unchanged host-service limits. They are subject to council review under the Human's authorization. **Decision**.

**Human decisions required now: none.** Complete conversation configuration belongs to the separately commissioned plan. Unknown retained Void/inspection routes and cold enum behavior are bounded coverage obligations above, not permission to omit a newly demonstrated eligible value. All batch plans and holistic review must converge before implementation. **Open / Decision**.

## 11. Self-check and source index

- Can an attached absence become inline? No; retain its row and call before normalization. **Proposed**.
- Does a copied opaque string become a B result reference? Only a declared typed-ID position is translated; ordinary strings are unchanged. **Proposed**.
- Does successful decode authorize a host connection? No; original client identity and existing resource/use checks still apply. **Proposed**.
- Does this batch complete conversation persistence? No; it supplies message/skill data and the reference seams used by that later batch. **Proposed**.

| Implementation entry | Responsibility |
| --- | --- |
| `dagql/cache_persistence_self.go:25`, `dagql/cache_persistence_import.go:178` | Generic format, codecs, attached null import. |
| `dagql/cache_persistence_worker.go:470`, `dagql/cache_persistence_resolver.go:126` | Untouched saves and exact local loads. |
| `core/persisted_object.go:39`, `dagql/result_call_frame.go:63`, `dagql/call/id.go:598` | Numeric references, frames and typed IDs. |
| `core/object.go:745`, `core/schema_build.go:22` | Module fields and defining schema. |
| `core/modtree.go:1182`, `core/generators.go:637` | Shared node-table and bound-workspace patterns. |
| `core/service.go:102`, `dagql/cache_persistence_import.go:359` | Client-bound data and resource closure. |
