# Engine-persisted agent resume

This extraction implements bounded engine archives and local-first trace restore on the current agent lifecycle. It reworks the source design from `llm-workspace-dev-env` (`93a85205ee`), whose CLI hard cut-over is not adopted.

## User flow

`dagger agent` opts its session into telemetry archival. After graceful shutdown, the footer prints `dagger agent --trace=<trace-id>`. That command connects to the engine and first requests its archive. `dagger agent --trace=picker` selects a closed archive from this engine. Existing `--resume`/`-r`, JSON session saves, `.save`, and `.resume` remain available.

Only typed not-found or evicted responses fall back to Cloud. Active, interrupted, incomplete, corrupt, unauthorized, and I/O failures remain errors. Cloud retains its whole-trace startup path; its binary OTLP streams are fetched concurrently. Restore remains strict by default; `--partial` explicitly permits skipping agents whose recipes cannot be rebuilt.

## Publication and storage

Each agent publishes versioned, sequenced checkpoints with its identity, parent, state, and latest portable conversation recipe digest. Runtime publication uses a queue outside the agent mutex and a lossless telemetry processor. Final records form an authoritative registry roster and carry the expected final sequence, allowing the archive builder to detect missing records. Call recipes use independent per-client delivery claims.

The main session opts in with its canonical command trace ID. Ordinary telemetry still flows through the existing exporters. At teardown all producers stop, the providers flush and shut down, and the client store establishes immutable high-water cursors. Finalization verifies the trace, final roster, parent ancestry, and complete portable call closure before marking an archive closed. Commands with no agent identities do not create picker entries.

Client telemetry and archive manifests live outside disposable worker state. Archives survive a worker configuration reset. Startup marks previously active/finalizing archives interrupted; it does not claim crash recovery of an unfinished agent checkpoint. Defaults retain closed archives for seven days with a soft 10 GiB quota. The newest archive and leased readers are protected from quota eviction. Engine configuration exposes TTL and quota overrides.

## Restore protocol

The authenticated engine HTTP API provides listing, active-session title updates, bootstrap, and finite span/log/metric streams. A generation identifies an immutable cut. Bootstrap carries only resume-critical spans, final agent facts, and the transitive call-payload closure. Checksums, counts, generation, cursor bounds, and a required terminal frame prevent accepting truncated or mismatched data.

The frontend imports bootstrap and waits for its event loop to apply every batch before reading the restore plan. Parents are restored before children through `LLM.spawn(handle:, parentHandle:, state:, error:)`; no model process starts during restoration. The same importer streams remaining historical telemetry in the background against the fixed cut. It seals at the archived timestamp, keeps the live session primary, and records links to the source trace. Historical-stream errors warn without replacing the live prompt.

## Validation boundaries

Focused tests cover storage, retention, authorization, verified closure, framing, importer barriers, strict/partial restore, clean-miss fallback, and footer behavior. The extraction also includes a provider-free engine round trip that closes an opted-in session, loads its archive from another session, rebuilds the portable conversation recipe, and restores a paused agent. Live vendor harness calls and Cloud service deployments are separate from this protocol validation.
