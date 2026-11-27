#!/bin/sh
# Pathological searches: an agent searching for the regexp "." (match
# anything) over a whole workspace OOMs the engine.
#
# The cost is TRANSIENT (nothing stays allocated once the search returns), so
# like fixtures/15218 this workload measures the searches themselves, from
# the engine's /debug/vars memstats: per search, the bytes allocated
# (TotalAlloc), the peak live heap (a poller forces a GC with
# /debug/pprof/heap?gc=1 and reads HeapAlloc right after, over and over), the
# peak heap footprint (HeapSys - HeapReleased, which is what the kernel sees),
# the wall time, the response size, and the searching CLI's max RSS. It
# writes one line per search to /out/search.txt, and keeps the heap profile
# taken at each search's peak (/out/peak-<mode>-<n>.pb.gz) plus the profiles
# just before and after it (alloc_space diffs attribute the allocation).
#
# Tree: FILES files (default 200) of LINES lines (default 200) of ~64 bytes,
# ~2.5 MiB of text, generated in an exec; LONG > 0 adds one file holding a
# single LONG-byte line, like minified JS.
#
# Searches: for each mode in MODES and each pattern in PATTERNS (default ". e"):
#
#   dir   Directory.search on the generated tree
#   ws    Workspace.search on the tree as a workspace (Directory.asWorkspace):
#         the rootfs path, no host
#   host  Workspace.search on currentWorkspace, the session's cwd (/fixture,
#         holding a copy of the tree in tree/), so ripgrep runs in the
#         `dagger run` CLI and the results come back over the session; the
#         CLI's peak RSS is recorded before and after (session_cli_hwm_kb)
#
# Each search selects SELECT (default "filePath lineNumber matchedLines").
# Every search gets a fresh nonce (a file in the tree, or a fresh client for
# host), so none is a cache hit. Searches that fail (an engine-side budget
# tripping, say) are recorded with their error, not fatal.
set -eu

FILES=${FILES:-200}
LINES=${LINES:-200}
LONG=${LONG:-0}
MODES=${MODES:-dir ws host}
PATTERNS=${PATTERNS:-. e}
SELECT=${SELECT:-filePath lineNumber matchedLines}
TIMEOUT=${TIMEOUT:-900}

ENGINE=http://dagger-engine:6060
tmp=$(mktemp -d)
: >/out/search.txt

# gen.sh: the tree, under $ROOT.
cat >"$tmp/gen.sh" <<'EOF'
set -eu
mkdir -p "$ROOT"
awk -v root="$ROOT" -v files="$FILES" -v lines="$LINES" -v long="$LONG" 'BEGIN {
  for (f = 0; f < files; f++) {
    dir = root "/d" int(f / 50)
    if (f % 50 == 0) system("mkdir -p " dir)
    path = dir "/f" f ".txt"
    for (l = 0; l < lines; l++)
      printf "line %05d of file %05d: the quick brown fox jumps over it\n", l, f > path
    close(path)
  }
  if (long > 0) {
    path = root "/min.js"
    s = "var a=function(b){return b+1};"
    while (length(s) < long) s = s s
    printf "%s\n", substr(s, 1, long) > path
    close(path)
  }
}'
EOF

# The host workspace is the session's: `dagger run`'s cwd, /fixture
# (session.sh), so the host copy of the tree goes there.
host=/fixture
if echo " $MODES " | grep -q ' host '; then
  apk add --no-cache ripgrep >/dev/null
  ROOT=$host/tree FILES=$FILES LINES=$LINES LONG=$LONG sh "$tmp/gen.sh"
  echo "host tree: $(du -sk "$host/tree" | cut -f1) KiB, rg $(rg --version | head -n 1)"
fi

# The `dagger run` CLI holding the session (session.sh's parent), whose
# peak RSS (VmHWM) is recorded too: it may be the client a host search runs
# in.
sessionCLI=$(awk '/^PPid:/ {print $2}' "/proc/$PPID/status")
hwm() { awk '/^VmHWM:/ {print $2}' "/proc/$sessionCLI/status" 2>/dev/null || echo 0; }

script="ROOT=/d FILES=$FILES LINES=$LINES LONG=$LONG
$(cat "$tmp/gen.sh")"
vars=$(jq -n --arg s "$script" '{script: $s}')
# shellcheck disable=SC2016 # GraphQL variables, not shell expansions
tree='container { from(address: "alpine:3.20") { withExec(args: ["sh", "-c", $script]) { directory(path: "/d")'

# Build the tree once, so the searches only search.
echo "query(\$script: String!) { $tree { entries } } } } }" |
  dagger api query --no-load-module --var-json "$vars" >/dev/null

memstat() { curl -sSf --max-time 60 "$ENGINE/debug/vars" | jq -r '.memstats | "\(.HeapAlloc) \(.HeapSys - .HeapReleased) \(.TotalAlloc)"'; }

# poll N: until $tmp/polling goes away, force a GC (keeping that profile)
# and read the live heap; keep the profile of the highest one.
poll() {
  best=0
  while [ -f "$tmp/polling" ]; do
    curl -sSf --max-time 300 "$ENGINE/debug/pprof/heap?gc=1" -o "$tmp/poll.pb.gz" || continue
    stat=$(memstat) || continue
    read -r live foot _ <<EOF
$stat
EOF
    echo "$live $foot" >>"$tmp/polled"
    if [ "$live" -gt "$best" ]; then
      best=$live
      mv "$tmp/poll.pb.gz" "$tmp/peak.pb.gz"
    fi
  done
}

# timed CMD...: run CMD for at most TIMEOUT seconds, recording its max RSS
# in $tmp/time where busybox has time(1).
if busybox time -v -o "$tmp/time" true 2>/dev/null; then
  timed() { timeout "$TIMEOUT" busybox time -v -o "$tmp/time" "$@"; }
else
  timed() { timeout "$TIMEOUT" "$@"; }
fi

n=0
for mode in $MODES; do
  for pattern in $PATTERNS; do
    n=$((n + 1))
    # shellcheck disable=SC2016 # a jq variable, not a shell expansion
    pat=$(jq -n --arg p "$pattern" '$p')
    case $mode in
    dir) q="query(\$script: String!) { $tree { withNewFile(path: \".nonce\", contents: \"$CAKE_NONCE $n\") { search(pattern: $pat) { $SELECT } } } } } } }" ;;
    ws) q="query(\$script: String!) { $tree { withNewFile(path: \".nonce\", contents: \"$CAKE_NONCE $n\") { asWorkspace { search(pattern: $pat) { $SELECT } } } } } } } }" ;;
    host) q="{ currentWorkspace { search(pattern: $pat) { $SELECT } } }" ;;
    *) echo "unknown mode $mode" >&2; exit 1 ;;
    esac

    curl -sSf --max-time 300 "$ENGINE/debug/pprof/heap?gc=1" -o "/out/pre-$mode-$n.pb.gz"
    read -r live0 foot0 alloc0 <<EOF
$(memstat)
EOF
    : >"$tmp/polled"
    rm -f "$tmp/peak.pb.gz"
    touch "$tmp/polling"
    poll &
    poller=$!

    hwm0=$(hwm)
    start=$(date +%s)
    dir=/fixture qvars=$vars
    if [ "$mode" = host ]; then
      dir=$host qvars='{}'
    fi
    rc=0
    : >"$tmp/time"
    (cd "$dir" && echo "$q" | timed \
      dagger api query --no-load-module --var-json "$qvars" >"$tmp/out.json" 2>"$tmp/err.txt") || rc=$?
    secs=$(($(date +%s) - start))

    rm -f "$tmp/polling"
    wait "$poller" || true
    read -r _ _ alloc1 <<EOF
$(memstat)
EOF
    curl -sSf --max-time 300 "$ENGINE/debug/pprof/heap?gc=1" -o "/out/post-$mode-$n.pb.gz"
    [ -f "$tmp/peak.pb.gz" ] && mv "$tmp/peak.pb.gz" "/out/peak-$mode-$n.pb.gz"

    peaklive=$(cut -d' ' -f1 "$tmp/polled" | sort -n | tail -n 1)
    peakfoot=$(cut -d' ' -f2 "$tmp/polled" | sort -n | tail -n 1)
    cliRSS=$(sed -n 's/.*Maximum resident set size (kbytes): *//p' "$tmp/time" 2>/dev/null || true)
    results=$(jq '[.. | objects | select(has("filePath"))] | length' "$tmp/out.json" 2>/dev/null || echo 0)
    line="search $n mode $mode pattern $pattern rc $rc seconds $secs results $results response_bytes $(wc -c <"$tmp/out.json") alloc $((alloc1 - alloc0)) live_start $live0 peak_live ${peaklive:-0} foot_start $foot0 peak_foot ${peakfoot:-0} polls $(wc -l <"$tmp/polled") cli_rss_kb ${cliRSS:-0} session_cli_hwm_kb $hwm0 $(hwm)"
    echo "$line"
    echo "$line" >>/out/search.txt
    if [ "$rc" != 0 ]; then
      err=$(tail -c 600 "$tmp/err.txt" | tr '\n' ' ')
      echo "  error: $err"
      echo "  error: $err" >>/out/search.txt
    fi
  done
done
