"""Read-only post-upgrade API checks using the container's existing admin login.

Run on the Docker host. Never prints passwords, tokens, or Cookie values.
"""
import json
import subprocess
import urllib.request

BASE = "http://127.0.0.1:9091/api/v1"
container = json.loads(subprocess.check_output([
    "docker", "inspect", "sub2api-piao-9091-app"
]))[0]
env = dict(item.split("=", 1) for item in container["Config"]["Env"] if "=" in item)


def request(path, token="", body=None):
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = "Bearer " + token
    req = urllib.request.Request(BASE + path, headers=headers,
                                 data=json.dumps(body).encode() if body else None)
    with urllib.request.urlopen(req, timeout=30) as response:
        result = json.load(response)
    assert result["code"] == 0, "API returned error: " + path
    return result["data"]


login = request("/auth/login", body={"email": env["ADMIN_EMAIL"], "password": env["ADMIN_PASSWORD"]})
token = login["access_token"]
settings = request("/admin/settings/cookie", token)
assert "ws_enabled" in settings and "interval_seconds" in settings
print("Cookie settings OK", json.dumps({key: settings[key] for key in (
    "enabled", "model", "interval_seconds", "ws_enabled", "ws_connections"
)}))
library = request("/admin/settings/openai-codex-cookie-library", token)
print("Cookie library OK, entries:", len(library))
logs = request("/admin/settings/cookie/logs", token)
print("Cookie acquisition logs OK, entries:", len(logs))
if logs:
    print("Latest acquisition:", json.dumps({key: logs[0].get(key) for key in (
        "created_at", "account_id", "success", "status_code", "host"
    )}))
accounts = request("/admin/accounts?platform=openai&lite=true&page_size=10", token)["items"]
for account in accounts:
    assert "ws_connections" in account, "Missing WS snapshot"
    ws = account["ws_connections"]
    assert ws["total"] == ws["idle"] + ws["in_use"], "Invalid WS totals"
    if account["type"] in ("oauth", "setup-token"):
        assert "cookie_binding" in account, "Missing Cookie binding"
    print("Account", account["id"], "binding:", json.dumps(account.get("cookie_binding")),
          "WS:", json.dumps(ws))
print("Authenticated deployment checks passed")
