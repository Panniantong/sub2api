"""Fail closed on invalid pnpm 9 audit output and unapproved high-risk findings."""
import argparse
import datetime as dt
import json
from pathlib import Path
import sys

import yaml


def check(audit, policy, today=None):
    today = today or dt.datetime.now(dt.timezone.utc).date()
    if not isinstance(audit, dict) or audit.get("error"):
        raise ValueError("audit failed or returned an invalid object")
    advisories = audit.get("advisories")
    metadata = audit.get("metadata")
    if not isinstance(metadata, dict):
        raise ValueError("missing or invalid audit metadata")
    counts = metadata.get("vulnerabilities")
    if not isinstance(advisories, dict) or not isinstance(counts, dict):
        raise ValueError("expected pnpm 9 advisories and vulnerability counts")
    for level in ("high", "critical"):
        if type(counts.get(level)) is not int or counts[level] < 0:
            raise ValueError("missing or invalid vulnerability count")
    if not isinstance(policy, dict) or policy.get("version") != 1 or not isinstance(policy.get("exceptions"), list):
        raise ValueError("invalid exception policy")
    allowed = {}
    for item in policy["exceptions"]:
        if not isinstance(item, dict):
            raise ValueError("invalid exception entry")
        for field in ("package", "advisory", "severity", "reason", "mitigation", "owner"):
            if not isinstance(item.get(field), str) or not item[field].strip():
                raise ValueError(f"exception missing {field}")
        key = (item["package"], item["advisory"])
        if key in allowed:
            raise ValueError("duplicate exception")
        expiry = dt.date.fromisoformat(str(item.get("expires_on", "")))
        allowed[key] = (expiry, item["severity"])
    blocked, observed = [], {"high": 0, "critical": 0}
    for advisory in advisories.values():
        if not isinstance(advisory, dict) or advisory.get("severity") not in {"info", "low", "moderate", "high", "critical"}:
            raise ValueError("invalid advisory")
        severity = advisory["severity"]
        if severity not in observed:
            continue
        observed[severity] += 1
        package = advisory.get("module_name")
        identifier = advisory.get("github_advisory_id")
        if not package or not identifier:
            raise ValueError("high-risk advisory has no package or identifier")
        exception = allowed.get((package, identifier))
        if exception is None or exception[0] < today or exception[1] != severity:
            blocked.append(f"{package}: {identifier} ({severity})")
    for level in observed:
        if counts[level] != observed[level]:
            raise ValueError("audit summary reports missing advisory details")
    return blocked


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--audit", type=Path, required=True)
    parser.add_argument("--exceptions", type=Path, required=True)
    args = parser.parse_args()
    try:
        audit = json.loads(args.audit.read_text(encoding="utf-8-sig"))
        policy = yaml.safe_load(args.exceptions.read_text(encoding="utf-8-sig"))
        blocked = check(audit, policy)
    except (OSError, ValueError, TypeError, yaml.YAMLError) as error:
        print(f"Audit validation failed: {error}", file=sys.stderr)
        return 2
    if blocked:
        print("Unapproved or expired high-risk findings:\n" + "\n".join(blocked), file=sys.stderr)
        return 1
    print("Audit accepted: no unapproved high/critical findings.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
