import collections
import datetime
import json
import sys

import ijson

# Streams an engine's /debug/dagql/cache dump (too large for jq) and
# summarizes, per session, what its results are and what owns them.
# usage: pip install ijson; sessions.py <cache.json>
path = sys.argv[1]

sessions = {}
with open(path, "rb") as f:
    captured = None
    for prefix, event, value in ijson.parse(f):
        if prefix == "captured_at_time":
            captured = value
            break
with open(path, "rb") as f:
    for s in ijson.items(f, "session_results.item"):
        sessions[s["session_id"]] = set(s.get("shared_result_ids") or [])

owner = {}
for sid, ids in sessions.items():
    for i in ids:
        owner.setdefault(i, []).append(sid)

print("captured_at", captured)
print("sessions", {k[:12]: len(v) for k, v in sessions.items()})

cap = None
try:
    cap = datetime.datetime.fromisoformat(captured.replace("Z", "+00:00")).timestamp() * 1e9
except Exception:
    pass

by_type = collections.Counter()
by_field = collections.Counter()
payload = collections.Counter()
incoming = collections.Counter()
dep_parents = collections.Counter()
rows = {}
call_bytes_total = 0
ages = []
n = 0
with open(path, "rb") as f:
    for r in ijson.items(f, "results.item"):
        n += 1
        rid = r["shared_result_id"]
        deps = r.get("explicit_dep_ids") or []
        for d in deps:
            dep_parents[d] += 1
        rc = r.get("result_call") or {}
        fname = rc.get("field") or rc.get("syntheticOp") or "?"
        cb = len(json.dumps(rc, default=str)) if rc else 0
        call_bytes_total += cb
        rows[rid] = (
            r.get("type_name") or "",
            fname,
            r.get("payload_state") or "",
            int(r.get("incoming_ownership_count") or 0),
            bool(r.get("has_persisted_edge")),
            int(r.get("expires_at_unix") or 0),
            int(r.get("last_used_at_unix_nano") or 0),
            len(deps),
            r.get("persisted_envelope_kind") or "",
            cb,
        )

print("results", n, "avg result_call json bytes", call_bytes_total // max(n, 1))

def summarize(label, ids):
    ids = [i for i in ids if i in rows]
    print("==", label, len(ids))
    tc = collections.Counter(rows[i][0] for i in ids)
    print(" top types", tc.most_common(25))
    fc = collections.Counter(rows[i][1] for i in ids)
    print(" top fields", fc.most_common(25))
    print(" payload_state", collections.Counter(rows[i][2] for i in ids).most_common())
    print(" envelope_kind", collections.Counter(rows[i][8] for i in ids).most_common())
    print(" persisted_edge", sum(1 for i in ids if rows[i][4]), "expiry", sum(1 for i in ids if rows[i][5]))
    inc = collections.Counter(min(rows[i][3], 5) for i in ids)
    print(" incoming_ownership (5=5+)", sorted(inc.items()))
    nsess = collections.Counter(len(owner.get(i, [])) for i in ids)
    print(" owning sessions", sorted(nsess.items()))
    par = collections.Counter(min(dep_parents.get(i, 0), 5) for i in ids)
    print(" dep parents (5=5+)", sorted(par.items()))
    # session-only: incoming == number of owning sessions (no dep parents, no persisted edge)
    sess_only = [i for i in ids if rows[i][3] == len(owner.get(i, [])) and not rows[i][4]]
    print(" owned ONLY by sessions (no dep parents, no edge):", len(sess_only))
    print("   their top types", collections.Counter(rows[i][0] for i in sess_only).most_common(15))
    if cap:
        buckets = collections.Counter()
        for i in ids:
            lu = rows[i][6]
            if not lu:
                buckets["never"] += 1
                continue
            age = (cap - lu) / 1e9
            for lim, name in ((60, "<1m"), (600, "<10m"), (1800, "<30m"), (3600, "<1h"), (4 * 3600, "<4h")):
                if age < lim:
                    buckets[name] += 1
                    break
            else:
                buckets[">=4h"] += 1
        print(" last-used age", sorted(buckets.items()))
    cb = sorted((rows[i][9] for i in ids))
    if cb:
        print(" result_call json bytes p50/p90/max", cb[len(cb) // 2], cb[int(len(cb) * 0.9)], cb[-1])

for sid, ids in sorted(sessions.items(), key=lambda kv: -len(kv[1]))[:3]:
    summarize("session " + sid[:12], ids)
summarize("not session-owned", [i for i in rows if i not in owner])
