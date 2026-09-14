"""Format a Prometheus instant-query JSON (stdin) as "label/label=value, ..." on one line.
Used by scripts/prod_status.sh. `--keep-job` keeps the job label (dropped by default)."""
import json
import sys

keep_job = "--keep-job" in sys.argv[1:]
try:
    d = json.load(sys.stdin)
except ValueError:
    print("(no answer from Prometheus)")
    sys.exit(0)
if d.get("status") != "success":
    print("query error:", d.get("error", d))
    sys.exit(0)
skip = {"__name__", "instance", "project", "env", "scope", "host", "server"}
if not keep_job:
    skip.add("job")
out = []
for x in d["data"]["result"]:
    lbl = "/".join(v for k, v in sorted(x["metric"].items()) if k not in skip)
    val = x["value"][1]
    try:
        f = float(val)
        val = ("%.3g" % f) if abs(f) < 1000 else ("%.0f" % f)
    except ValueError:
        pass
    out.append(f"{lbl}={val}" if lbl else val)
print(", ".join(out) if out else "-")
