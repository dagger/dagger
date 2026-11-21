#!/bin/sh
# Workspace.glob on a workspace without a host path.
#
# Agents' workspaces (git checkouts, staff workers' copies) have no host
# path: Workspace.glob globs the workspace's in-engine rootfs, and every
# edit gives the workspace a new rootfs. An agent edits and globs over and
# over, so every glob runs against a tree no glob has seen before.
#
# Workload: in one session, build a FILES-file directory in an exec (DIRS =
# FILES/100 directories of 100 files), make it a workspace
# (Directory.asWorkspace, no host path), then ROUNDS rounds of one edit
# (Workspace.withNewFile, chained on the previous round's workspace) followed
# by a glob of everything ("**") and a narrower one ("d1/*"). Each round is
# its own query, as an agent's tool calls are. At the end, the dagql cache's
# results by field go to /out/cache-fields.json.
set -eu

FILES=${FILES:-5000}
ROUNDS=${ROUNDS:-100}
DIRS=$((FILES / 100))

vars=$(jq -n --rawfile s gen.sh --arg dirs "$DIRS" --arg nonce "${CAKE_NONCE:-}" \
  '{script: ("DIRS=" + $dirs + "\n# " + $nonce + "\n" + $s)}')

# shellcheck disable=SC2016 # GraphQL variables, not shell expansions
ws=$(echo 'query($script: String!) { container { from(address: "alpine:3.20") { withExec(args: ["sh", "-c", $script]) { directory(path: "/d") { asWorkspace { id } } } } } }' |
  dagger api query --no-load-module --var-json "$vars" |
  jq -er '.container.from.withExec.directory.asWorkspace.id')

k=0
while [ "$k" -lt "$ROUNDS" ]; do
  # shellcheck disable=SC2016
  ws=$(echo 'query($ws: ID!, $contents: String!) { node(id: $ws) { ... on Workspace { withNewFile(path: "d0/f0", contents: $contents) { id all: glob(pattern: "**") narrow: glob(pattern: "d1/*") } } } }' |
    dagger api query --no-load-module --var-json "$(jq -n --arg ws "$ws" --arg c "edit $k" '{ws: $ws, contents: $c}')" |
    jq -er '.node.withNewFile | if (.all | length) < 1 or (.narrow | length) < 1 then error("empty glob") else .id end')
  k=$((k + 1))
  if [ $((k % 20)) -eq 0 ]; then
    echo "rounds: $k/$ROUNDS"
  fi
done

curl -sSf --max-time 300 http://dagger-engine:6060/debug/dagql/cache |
  jq '[.results[] | ((.result_call.field // "?") + " -> " + (.type_name // "?"))]
      | group_by(.) | map({field: .[0], n: length}) | sort_by(-.n) | .[:40]' \
    >/out/cache-fields.json || true
