#!/bin/sh
# PR #15216 "core: share artifact module servers across clients".
#
# Artifact discovery, evaluation and checks select through a standalone
# server per module (dagqlServerForModule): a fork of the full, unversioned
# core schema with the module installed. Main kept it in each client's own
# memo, so every client of a session that runs a module's checks builds and
# holds its own, for as long as the client lives; with the fix the session
# keeps one per module for all its clients.
#
# Workload: a workspace of MODS small Dang modules with two checks each,
# built in the API (Directory.asWorkspace), so its discovery is keyed by
# content and every client gets the same modules. CLIENTS concurrent nested
# clients each run every check (Check.pass is never cached, so each goes
# through the client's module server), then stay connected (a sleeping exec
# in the same query) while the live heap is taken.
set -eu

CLIENTS=${CLIENTS:-40}
MODS=${MODS:-10}
HOLD=${HOLD:-900}
TIMEOUT=${TIMEOUT:-300}

# The workspace directory: dagger.toml naming every module, and each module's
# config and source, as nested withNewFile selections.
toml=""
open=""
close=""
m=0
while [ "$m" -lt "$MODS" ]; do
  toml="$toml[modules.mod$m]\\nsource = \\\"modules/mod$m\\\"\\n"
  cfg="name = \\\"mod$m\\\"\\nengineVersion = \\\"latest\\\"\\n\\n[runtime]\\nsource = \\\"dang\\\"\\n"
  src="type Mod$m {\\n  pub ok: Void @check { null }\\n  pub alsoOk: Void @check { null }\\n}\\n"
  open="$open withNewFile(path: \"modules/mod$m/dagger-module.toml\", contents: \"$cfg\") { withNewFile(path: \"modules/mod$m/main.dang\", contents: \"$src\") {"
  close="$close } }"
  m=$((m + 1))
done
ws="directory { withNewFile(path: \"dagger.toml\", contents: \"$toml\") { $open asWorkspace { artifacts { asChecks { pass } } } $close } }"

logs=/tmp/clients
mkdir -p "$logs"
i=0
while [ "$i" -lt "$CLIENTS" ]; do
  echo "{ $ws container { from(address: \"alpine:3.20\") { withEnvVariable(name: \"CAKE_CLIENT\", value: \"$i\") { withExec(args: [\"sleep\", \"$HOLD\"]) { exitCode } } } } }" |
    dagger --progress=plain api query --no-load-module >"$logs/$i.log" 2>&1 &
  i=$((i + 1))
done

# A client is ready once all its checks ran: the plain progress reports each
# Check.pass done.
checks=$((MODS * 2))
waited=0
while :; do
  ready=0
  for f in "$logs"/*.log; do
    if [ "$(grep -c 'pass.*DONE' "$f" || true)" -ge "$checks" ]; then
      ready=$((ready + 1))
    fi
  done
  echo "checked: $ready/$CLIENTS (${waited}s)"
  [ "$ready" -ge "$CLIENTS" ] && break
  if [ "$waited" -ge "$TIMEOUT" ] || grep -q ' ERROR' "$logs"/*.log; then
    tail -n 30 "$logs/0.log"
    exit 1
  fi
  sleep 2
  waited=$((waited + 2))
done
curl -sf http://dagger-engine:6060/debug/vars | grep -o '"schemaBuilds": *{[^}]*}' || true
