"""Bounded two-turn forwarding smoke test. Never print credentials."""
import collections
import json
import subprocess
import sys
import time
import urllib.request
import urllib.error
import uuid

base = "http://127.0.0.1:9091"
container = json.loads(subprocess.check_output(["docker", "inspect", "sub2api-piao-9091-app"]))[0]
env = dict(x.split("=", 1) for x in container["Config"]["Env"] if "=" in x)


def api(path, token="", body=None):
    req = urllib.request.Request(base + path, data=json.dumps(body).encode() if body else None,
                                 headers={"Content-Type": "application/json", "Authorization": "Bearer " + token})
    return json.load(urllib.request.urlopen(req, timeout=20))["data"]


login = api("/api/v1/auth/login", body={"email": env["ADMIN_EMAIL"], "password": env["ADMIN_PASSWORD"]})
keys = api("/api/v1/admin/users/1/api-keys?page_size=100", login["access_token"])["items"]
key = next(x["key"] for x in keys if x["id"] == 1)
session = str(uuid.uuid4())


def turn(label, body):
    req = urllib.request.Request(base + "/v1/responses", data=json.dumps(body).encode(), headers={
        "Authorization": "Bearer " + key, "Content-Type": "application/json",
        "session_id": session, "User-Agent": "codex_cli_rs/0.153.4", "originator": "codex_cli_rs"})
    counts = collections.Counter()
    started = time.monotonic()
    response = None
    output_items = []
    try:
        stream = urllib.request.urlopen(req, timeout=35)
    except urllib.error.HTTPError as error:
        print(label, "HTTP_ERROR", error.code, error.read().decode(), flush=True)
        raise
    with stream:
        for line in stream:
            if time.monotonic() - started > 40:
                raise TimeoutError("turn exceeded 40 seconds")
            if not line.startswith(b"data: "):
                continue
            if line.strip() == b"data: [DONE]":
                break
            event = json.loads(line[6:])
            kind = event.get("type", "unknown")
            counts[kind] += 1
            if kind == "response.output_item.done" and isinstance(event.get("item"), dict):
                output_items.append(event["item"])
            if kind in ("response.completed", "response.failed", "response.incomplete", "response.cancelled", "error"):
                response = event.get("response", {})
                print(label, "terminal", kind, "status", response.get("status"),
                      "seconds", round(time.monotonic() - started, 2), "events", dict(counts), flush=True)
                if kind != "response.completed":
                    print("upstream error:", event.get("error", response.get("error")), flush=True)
                    raise RuntimeError("turn failed: " + kind)
                break
    if response is None:
        print(label, "NO TERMINAL", dict(counts), flush=True)
        raise RuntimeError("no completed response")
    response["_stream_output"] = output_items
    return response


common = {"model": "gpt-6-astra", "stream": True, "store": False,
          "instructions": "Reply with exactly pong. Do not use tools.", "prompt_cache_key": session}
if "--tool-only" not in sys.argv:
    first = turn("first", {**common, "input": [{"role": "user", "content": "ping"}]})
    second = turn("second", {**common, "previous_response_id": first["id"],
                              "input": [{"role": "user", "content": "ping again"}]})
    print("TWO_TURN_OK", bool(second.get("id")), flush=True)

tool = {"type": "custom", "name": "echo", "description": "Echo the short input text."}
tool_response = turn("tool_call", {**common,
    "instructions": "Call echo exactly once with the input ping. Do not write anything else.",
    "tools": [tool], "tool_choice": "required",
    "input": [{"role": "user", "content": "Call echo with ping."}]})
items = tool_response.get("output", []) + tool_response["_stream_output"]
print("TOOL_OUTPUT_TYPES", [x.get("type") for x in items], flush=True)
call = next(x for x in items if x.get("type") in ("custom_tool_call", "function_call"))
tool_done = turn("tool_result", {**common,
    "previous_response_id": tool_response["id"], "tools": [tool], "tool_choice": "none",
    "input": [{"type": call["type"] + "_output", "call_id": call["call_id"], "output": "pong"}]})
print("TOOL_CONTINUATION_OK", bool(tool_done.get("id")), flush=True)
