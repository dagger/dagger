import json
import sys

import ijson

# usage: frame_shape.py <cache.json> <result id>...
path = sys.argv[1]
want = {int(a) for a in sys.argv[2:]}


def shape(v, depth=0, maxdepth=4):
    if isinstance(v, dict):
        if depth >= maxdepth:
            return "{...%d}" % len(json.dumps(v, default=str))
        return {k: shape(x, depth + 1, maxdepth) for k, x in v.items()}
    if isinstance(v, list):
        if not v:
            return []
        return ["list[%d] total=%d" % (len(v), len(json.dumps(v, default=str))), shape(v[0], depth + 1, maxdepth)]
    if isinstance(v, str) and len(v) > 80:
        return "str[%d]" % len(v)
    return v


with open(path, "rb") as f:
    for r in ijson.items(f, "results.item"):
        if r["shared_result_id"] not in want:
            continue
        rc = r.get("result_call") or {}
        print("== result", r["shared_result_id"], r.get("type_name"), "deps", len(r.get("explicit_dep_ids") or []))
        for a in rc.get("args") or []:
            print(" arg", a.get("name"), len(json.dumps(a, default=str)))
            print("   ", json.dumps(shape(a.get("value"), 0, 6), default=str)[:3000])
        for a in rc.get("implicitInputs") or []:
            print(" implicit", a.get("name"), len(json.dumps(a, default=str)))
        print(" receiver", json.dumps(shape(rc.get("receiver"), 0, 3), default=str)[:1500])
        print(" value_state", json.dumps(shape(r.get("value_state"), 0, 3), default=str)[:1500])
