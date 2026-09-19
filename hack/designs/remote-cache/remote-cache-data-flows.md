# Remote-cache engine foundations

This is the continuing implementation scope after the September 13 audit and removal. It replaces the autonomous eight-document plan. The engine uses its existing cache, digest equivalence, lazy evaluation, ownership and pruning behavior.

## What another engine receives

Engine A describes selected cached results: their encoded values, declared types, call frames, transferable extra digests, dependency references and immutable snapshot chains. Engine B translates A's local references into its own rows and admits those rows into the ordinary cache. It does not import A's equivalence-class numbers; its existing egraph derives equivalence from the imported digests.

A lookup or a metadata read does not download filesystem contents. When an existing lazy part needs bytes, it opens an available local snapshot, tries its supplied remote chain, then executes the original computation if the download cannot supply the part. No remote service lookup is added to ordinary cache misses.

## Implementation order

1. Retain the implemented value codecs and declared-reference visitor described in [persisted-value-graphs.md](persisted-value-graphs.md).
2. Preserve original filesystem-producing inputs after completion and replace the synthetic Changeset output with an ordinary named producer. See [saved-filesystem-producers.md](saved-filesystem-producers.md).
3. Describe selected live rows and admit the relocated metadata into another live cache. See [live-cache-descriptions.md](live-cache-descriptions.md).
4. Use supplied snapshot chains from existing lazy evaluation, falling back to the original producer. See [remote-cache-acquisition.md](remote-cache-acquisition.md).
5. Complete the originally requested from-image metadata path without unpacking layers. Check existing implementation first. Lookup preference is a separate unresolved choice: the original D15 records a lean toward available, then downloadable, then runnable results. Do not change selection policy without resolving that choice with Erik.

Extra-digest transfer uses the small annotation Erik requested: keep safe useful extras such as pinned image identities; omit content-derived extras from transfer. Describing a chain does not establish that its bytes are available on B. Attaching an identity after downloading actual content is outside this initial work. Local digest merging and teaching keep their current meaning. A layer digest is not the existing tree-content digest.

An unevaluated result can also travel: it carries its original inputs and pending parts without invented output snapshots.

## Limits that remain part of the task

Nondeterministic computations and independently acquired exec outputs retain the existing behavior. A saved function result does not gain a new dependency on the continued presence or byte agreement of a referenced File. Nested clients use ordinary calls and the same cache.

Export takes the holds needed for that actual operation; it does not extend retention while waiting for a service to request data. Import preserves ordinary dependencies, resource checks and cleanup. Descriptions are trusted inside the existing scope. No remote cache service, background availability leases or new durable queues are part of this implementation.

The equality redesign, byte-read conditions, nested observation forwarding, value-owned validation and LLM/MCP expansion have been removed from this effort, including their models and future implementation obligations. Historical commits and evidence are not a queue to resume.

## Verification

Use concrete cross-engine value and filesystem operations: metadata hits without downloads, relocation with different row IDs, lazy chain download, failed-download computation, restart and ordinary cleanup. Exercise the existing race-sensitive admission and attachment paths when changing them. Do not run or maintain the removed policy tests.

Status: source cleanup complete; remaining foundations proceed privately from the retained codec implementation. No public PR or push is authorized.
