# Acquiring missing filesystem outputs

Status: this document restates the converged designs, including the Lazy-values rework; it adds no design requirements.

An imported result is an ordinary cached value whose pending parts carry operation data and optional download offers. Acquisition uses the existing Lazy operation and the same publication transaction as ready-output installation. A local stored snapshot without a foreign description keeps its existing missing-content error.

## Demand path

`DescribeParts` and `RouteParts` inspect encoded data without evaluating it. The demand path checks the receiver's available part, eligible equivalent local parts, admitted chains and equivalent pending operation routes. It then uses the receiver's recorded operation when available. The closed metadata-transform table additionally permits delegation to the exact recorded parent. Delegation carries its active path per demand in private context, never in shared demand state.

Directory and File have an operation route only with a registered `lazyKind` and valid `lazyJSON`. Container uses its existing field-indexed codec table and nonempty valid operation JSON. A field name alone supplies no execution authority. Private evaluation uses fresh operation state, exact saved inputs and the receiver's persisted platform.

Chain providers validate fixed-address reads and record exhaustion against the admitted offer revision. A replacement offer is not exhausted by an earlier one. Unavailable content may lead to another route; cancellation, local ownership failures and cleanup errors keep their classifications and accumulated causes.

## Publication and ownership

The writer gate excludes conflicting native bodies, imports and private evaluation. `PrepareReadyPart`, `CommitReadyPart` and `FinishReadyPart` preserve their transaction stages. Commit installs only the selected missing outputs, retaining the receiver's complete encoded view, operation identity, pending siblings and scoped snapshot links. Finish retains cleanup ownership through retries and acknowledges owner synchronization before settlement.

Each installed ref is independently owned. Inline list outputs use the `(result_id, output_path, role)` key, through encoding, visitors, typed collection, owner synchronization and boot restoration. Native completion also settles pending offers. Every initialized boot reconciles the desired lease set, including an empty store and an import-failure reset.

Private exec evaluation may create filesystem and exec metadata together. If filesystem content is already installed, publication keeps it and releases the redundant private ref. The release-observing fixture decorator applies only to that private filesystem handle. Fixture events are `lazy-enter`, `installed-lazy`, `lazy-ref-released` and `lazy-ref-release-error`; their counters retain the same exact row, group and part meanings. Delegation remains separately reported by its selected/installed events and gated source address.

## Limits and verification

No remote service lookup, offer-renewal RPC, background sharing implementation or cached-scalar backing check is added. Existing digest equivalence, resource admission and ownership rules continue to apply. See [Lazy values](lazy-values.md) for changed constructor timing, internal identities and the format cut.

Verification covers real local opens, admitted-chain import/failure, private operation fallback, scoped inline installation, cancellation, cleanup and synchronization retries, restart, mixed exec outputs and native cold/warm controls. The later sharing batches retain the preparation, revision, exact-input and publication interfaces described here.
