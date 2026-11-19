| PR | family | workload | live before | live after | focus before | focus after | Δ focus | ratio |
|---|---|---|---:|---:|---:|---:|---:|---:|
| #15202 core: dedupe and bound schema builder memos | schema | 1 session loads 300 distinct Dang modules (Directory.asModule), 10 per query | 183.2 MiB | 76.5 MiB | 104.1 MiB | 2.4 MiB | −101.7 MiB | 43.9× |
| #15215 core: share module schemas across client roots | schema | 300 nested-client calls of a Go module function that calls a Dang dependency | 758.4 MiB | 601.4 MiB | 215.4 MiB | 107.7 MiB | −107.7 MiB | 2.0× |
| #15216 core: share artifact module servers across clients | schema | 40 concurrent nested clients run the checks of a 10-module Dang workspace | 318.9 MiB | 154.2 MiB | 78.7 MiB | 1.0 MiB | −77.7 MiB | 78.1× |
| #15213 engine: keep contenthash trees on disk | contenthash | 1 session hashes a 20k-file directory and 200 one-file edits of it | 585.9 MiB | 236.1 MiB | 354.7 MiB | 0.0 MiB | −354.7 MiB | →0 |
| #15212 engine: forget contenthash trees of collected snapshots | contenthash | 200 one-file edits of a 20k-file directory hashed in separate sessions, then a cache prune | 599.8 MiB | 227.8 MiB | 352.9 MiB | 0.0 MiB | −352.9 MiB | →0 |
| #15232 engine/snapshots: one ancestry lease for dagql owners | contenthash | 16 withNewFile chains of 1000 synced results each, each owning its snapshot | 449.6 MiB | 256.0 MiB | 249.9 MiB | 54.6 MiB | −195.2 MiB | 4.6× |
| #15223 dang: bound prepared programs kept per session | dang | 40 comment-only variants of a 300-function Dang module, one call each | 723.2 MiB | 447.7 MiB | 351.2 MiB | 32.0 MiB | −319.2 MiB | 11.0× |
| #15224 dang: bound received values kept per session | dang | 250 chained calls on a Dang object whose list state grows by 256 per call | 688.5 MiB | 411.7 MiB | 307.7 MiB | 35.0 MiB | −272.7 MiB | 8.8× |
| #15226 core: bound receivers a session keeps converted | dang | 200 distinct results of a 1 MiB Dang object, one function call each | 479.5 MiB | 294.2 MiB | 209.3 MiB | 40.7 MiB | −168.6 MiB | 5.1× |
| #15220 clientdb: intern the span index's span IDs | telemetry | 60k cheap uncached API calls with the session's telemetry index open | 1120.7 MiB | 996.9 MiB | 146.0 MiB | 44.8 MiB | −101.2 MiB | 3.3× |
| #15221 clientdb: intern the log index, pack row metadata | telemetry | 60k cheap uncached API calls, each logging its payload, log index open | 1146.8 MiB | 1011.1 MiB | 17.5 MiB | 12.0 MiB | −5.5 MiB | 1.5× |
| #15227 dagui: cut CLI telemetry memory (CLI heap) | telemetry | a long agent session's telemetry streamed into a headless-TUI `dagger run`: 100 metric exports x 200 calls x 8 gauges, exemplars | 1627.9 MiB | 644.3 MiB | 66.0 MiB | 0.5 MiB | −65.5 MiB | 132.0× |
| #15227 alloc focus (churn, alloc_space) | telemetry | same run | 18404.8 MiB | 18039.3 MiB | 337.3 MiB | 0.0 MiB | −337.3 MiB | →0 |
| #15228 idtui: stop pruned span trees pinning walkers (CLI heap) | telemetry | 300k spans of a long agent session (tests, logs) streamed into a headless TUI rendering frames | 1504.9 MiB | 690.4 MiB | 660.2 MiB | 27.9 MiB | −632.3 MiB | 23.6× |
| #15229 dagui: intern span digests and share attributes (CLI heap) | telemetry | 300k spans, mostly dagql calls with frames, inputs, outputs, cache attrs, into a headless TUI | 1586.7 MiB | 717.6 MiB | 114.0 MiB | 62.4 MiB | −51.6 MiB | 1.8× |

| trace tool | alloc/search before | alloc/search after | live peak before | live peak after |
|---|---:|---:|---:|---:|
| ListServices | 9.2 MiB | 9.1 MiB | +10.0 MiB | +1.3 MiB |
| FindSpans | 1568.1 MiB | 57.2 MiB | +462.6 MiB | +45.0 MiB |
| FindCalls | 932.3 MiB | 785.9 MiB | +209.5 MiB | +27.5 MiB |
