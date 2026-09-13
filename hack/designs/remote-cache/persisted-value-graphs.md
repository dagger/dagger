# Portable persisted values and references

Implemented in the retained codec foundation at a161ceb34c. This document replaces the earlier batch proposal and describes only the representation used by remote-cache transfer.

## Why it is needed

A cached module value may contain nullable lists, module enums, ordinary metadata objects and references to other cached objects. Another engine must receive the same value and type while replacing engine-local result IDs. Copying JSON without knowing its declared reference fields cannot do that.

## Existing implementation

PersistEncodeContext records exact attached result references and call IDs. PersistDecodeContext resolves those references through the cache's internal loader within the already admitted decode operation. It does not start a new query or run a public field to reconstruct a missing reference.

VisitEncodedReferences walks declared reference positions in object codecs, generic lists and recorded call frames. It rewrites result references and snapshot-role metadata without evaluating lazy parts, mounting filesystems or inspecting arbitrary numbers inside user JSON. Shared call subgraphs remain shared.

The generic representation preserves null separately from empty lists, exact JSON numbers and recursive declared types, including module enum and scalar elements. Object codecs retain ordinary payload fields and their attached children. Git values retain their actual mirror, authentication, socket and service references.

These types were already eligible for persistence through ordinary module returns. The codecs make that existing eligibility usable; they introduce no new public operation, persistence policy, service identity or cache matching rule. Pre-existing TokenUsage and Variable codecs have only mechanical interface changes.

## Transfer use

The live-description implementation assigns bundle-local IDs to selected rows and uses this visitor to translate every declared reference. On import it allocates B-local IDs and rewrites the payload before ordinary typed decoding. Dependencies and local mutable resources retain their existing ownership and resource rules.

The internal child loader is necessary for one admitted decode to finish after cache close starts. It uses the existing operation lifetime; it adds no nested-client participation or read recorder.

## Evidence and limits

The retained batch contains codec, relocation, typed restoration, Git reference and action/metadata round-trip tests, plus the decode-versus-close control. Earlier run evidence remains attributed to its original source. Current verification is recorded with the continuing stack.

This work represents values and references. It does not validate that a saved value's text agrees with the current bytes of a referenced File, and it does not change the cache's equivalence or pruning semantics.
