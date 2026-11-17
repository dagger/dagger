# dump-provenance

Provenance is the source dependency data for an artifact.

Build and test this client locally:

```sh
go build -o /tmp/dump-provenance ./cmd/dump-provenance
go test ./cmd/dump-provenance ./core/provenance
```

Live collection requires an engine with this branch's provenance API. The
default Cloud engine may not have this unmerged API. The examples below require
a compatible configured target. They have not been verified against a live
engine for this client.

Collect provenance in a new session:

```sh
DAGGER_ENGINE=cloud /tmp/dump-provenance \
  -workspace "$PWD" -workdir "$PWD" > /tmp/provenance.json
```

The client sends one public GraphQL request. It selects
`currentWorkspace.snapshot.artifacts`, filters for `Check` artifacts, and reads
each artifact's path, URI, selectors, and issues. The engine can load modules and
prepare inputs for this request. It does not run check bodies.

Use `-type Container` to select another artifact type. Use `-type ''` to select
all artifact types. An optional URI filter uses the public artifact address
syntax:

```sh
DAGGER_ENGINE=cloud /tmp/dump-provenance \
  -workspace "$PWD" -uri 'dag://?check=test' > /tmp/tests-provenance.json
```

To use an existing session, inherit it without Workspace or workdir overrides:

```sh
DAGGER_ENGINE=cloud /path/to/compatible/dagger --workspace "$PWD" api with-session -- \
  /tmp/dump-provenance -uri 'dag://?check=test'
```

The client rejects `-workspace` and `-workdir` when `DAGGER_SESSION_PORT` is set.
Those SDK options only apply when the SDK starts a new session. It never replaces
the caller's selected Workspace with an address loaded under another Workspace.

Inspect a saved report without an engine:

```sh
/tmp/dump-provenance -input /tmp/provenance.json \
  -match-file /.env -match-file /README.md -match-path /docs
cat /tmp/provenance.json | /tmp/dump-provenance -input - -match-file /new.txt
```

Paths use Workspace-root coordinates. `-match-file` tests one file, including an
added or deleted file. `-match-path` tests a subtree. Both flags can be repeated.
The `core/provenance` package performs these local selector matches. Saved input
does not trigger another collection. Its original `collection` metadata remains
unchanged. Added matches have `local-file` or `local-subtree` mode.

Output uses this report schema:

```json
{
  "collection": {
    "mode": "engine-backed",
    "snapshot": true,
    "workspace": "file:///workspace",
    "cwd": ".",
    "artifactType": "Check"
  },
  "artifacts": [
    {
      "path": ["test"],
      "uri": "dag://?check=test",
      "provenance": {
        "selectors": [],
        "issues": []
      }
    }
  ]
}
```

Missing or partial API data is an error. An explicit empty `items` array is a
valid empty selection. GraphQL errors remain errors, including partial responses.
The report preserves all public selectors and issues. Existing conservative root
selectors can cause every local path test to match.
A successful dump does not prove that artifact discovery is complete.

The client does not flatten input graphs. That code remains in the engine. A new
engine build is still required to test capture, rebinding, or graph collection
changes. Client output, JSON handling, and selector matching can be tested locally.
The tests use a local fake public API endpoint and saved Tiny09 data. They do not
build or start an engine.

`testdata/report.json` is a small sample from two Tiny09 artifacts. It retains
each artifact's Host.findUp selector and one conservative issue with its selector.
Workspace address and cwd are absent because the original requests did not
select those fields. Full Tiny09 data was used separately for a local smoke test.
