import collections
import datetime
import heapq
import json
import sys

import ijson

path = sys.argv[1]
with open(path, "rb") as f:
    for prefix, event, value in ijson.parse(f):
        if prefix == "captured_at_time":
            captured = value
            break
cap = datetime.datetime.fromisoformat(captured.replace("Z", "+00:00")).timestamp() * 1e9

sessions = {}
with open(path, "rb") as f:
    for s in ijson.items(f, "session_results.item"):
        sessions[s["session_id"]] = set(s.get("shared_result_ids") or [])
owner = {}
for sid, ids in sessions.items():
    for i in ids:
        owner.setdefault(i, []).append(sid[:12])

top = []
bytes_by_type = collections.Counter()
bytes_by_field = collections.Counter()
age_buckets = collections.Counter()
age_bytes = collections.Counter()
limits = [(60, "a<1m"), (300, "b<5m"), (900, "c<15m"), (1800, "d<30m"), (3600, "e<1h"), (7200, "f<2h"), (4 * 3600, "g<4h")]
nested_inline = 0
args_bytes_total = 0
total = 0
with open(path, "rb") as f:
    for r in ijson.items(f, "results.item"):
        rid = r["shared_result_id"]
        rc = r.get("result_call") or {}
        s = json.dumps(rc, default=str) if rc else ""
        cb = len(s)
        total += cb
        t = r.get("type_name") or ""
        fld = rc.get("field") or rc.get("syntheticOp") or "?"
        bytes_by_type[t] += cb
        bytes_by_field[fld] += cb
        if '"call": {' in s or '"call":{' in s:
            nested_inline += 1
        item = (cb, rid, t, fld, ",".join(owner.get(rid, [])), r.get("incoming_ownership_count"))
        if len(top) < 15:
            heapq.heappush(top, item)
        else:
            heapq.heappushpop(top, item)
        if rid in owner:
            lu = int(r.get("last_used_at_unix_nano") or 0)
            ca = int(r.get("created_at_unix_nano") or 0)
            age = (cap - lu) / 1e9 if lu else None
            name = "never"
            if age is not None:
                for lim, nm in limits:
                    if age < lim:
                        name = nm
                        break
                else:
                    name = "h>=4h"
            age_buckets[name] += 1
            age_bytes[name] += cb
            if ca:
                cage = (cap - ca) / 1e9
                for lim, nm in limits:
                    if cage < lim:
                        age_buckets["created " + nm] += 1
                        break
                else:
                    age_buckets["created h>=4h"] += 1

print("total result_call json bytes", total, "frames with inline nested call:", nested_inline)
print("largest frames:")
for item in sorted(top, reverse=True):
    print("  ", item)
print("bytes by type", bytes_by_type.most_common(15))
print("bytes by field", bytes_by_field.most_common(15))
print("session-owned last-used / created age:")
for k in sorted(age_buckets):
    print("  ", k, age_buckets[k], age_bytes.get(k, ""))
