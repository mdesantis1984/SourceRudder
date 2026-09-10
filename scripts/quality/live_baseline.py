#!/usr/bin/env python3
"""Run the isolated, human-reviewed live-search baseline."""
import argparse
import hashlib
import json
import os
import secrets
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
import uuid
from datetime import datetime, timezone
from pathlib import Path

from baseline_contract import (
    MANIFEST,
    MAX_RESULTS,
    SEARXNG_IMAGE,
    TOOLS,
    ProtocolFailure,
    decode_tool_result,
    parse_envelope,
    request_body,
    result_evidence,
    search_params,
    validate_manifest,
    warm_case,
)

ROOT = Path(__file__).resolve().parents[2]
COMPOSE = ROOT / "deploy/quality/docker-compose.yml"
ENDPOINT = "http://127.0.0.1:18080/mcp"
CALL_TIMEOUT, GLOBAL_DEADLINE = 70, 2700
MAX_RESPONSE_BYTES = 2 * 1024 * 1024


def utc_now():
    return datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")


def rpc(request_id, method, params, token, deadline):
    remaining = deadline - time.monotonic()
    if remaining <= 0:
        return None, {"status": "transport_failure", "error": "global deadline exceeded", "latency_ms": 0}
    request = urllib.request.Request(ENDPOINT, request_body(request_id, method, params), {"Content-Type": "application/json", "Authorization": "Bearer " + token})
    started = time.monotonic()
    try:
        with urllib.request.urlopen(request, timeout=min(CALL_TIMEOUT, remaining)) as response:
            raw = response.read(MAX_RESPONSE_BYTES + 1)
            if len(raw) > MAX_RESPONSE_BYTES:
                raise ProtocolFailure("response exceeds size limit")
            envelope = json.loads(raw)
    except (json.JSONDecodeError, ProtocolFailure) as error:
        return None, {"status": "protocol_failure", "error": str(error), "latency_ms": round((time.monotonic() - started) * 1000)}
    except (urllib.error.URLError, urllib.error.HTTPError, TimeoutError) as error:
        return None, {"status": "transport_failure", "error": str(error), "latency_ms": round((time.monotonic() - started) * 1000)}
    latency = round((time.monotonic() - started) * 1000)
    try:
        return parse_envelope(envelope, request_id), {"latency_ms": latency}
    except ProtocolFailure as error:
        return None, {"status": "protocol_failure", "error": str(error), "latency_ms": latency}


def search(case, token, deadline, phase):
    result, meta = rpc(case["id"], "tools/call", search_params(case), token, deadline)
    base = {**case, "phase": phase, "request": {"maxResults": MAX_RESULTS, "safeSearch": False}, **meta}
    if result is None:
        return base
    try:
        payload, tool_error = decode_tool_result(result)
        if tool_error:
            return {**base, "status": "provider_failure", "error": "MCP result.isError"}
        return {**base, **result_evidence(payload)}
    except (ProtocolFailure, json.JSONDecodeError) as error:
        return {**base, "status": "protocol_failure", "error": str(error)}


def compose(args, env, timeout=None):
    return subprocess.run(["docker", "compose", "-f", str(COMPOSE), "--project-name", "ia-buscar-quality", *args], cwd=ROOT, env=env, check=True, timeout=timeout, text=True, capture_output=True)


def safe_error(error, token):
    detail = getattr(error, "stderr", "") or ""
    return {"type": type(error).__name__, "message": str(error).replace(token, "[redacted]"),
            "detail": detail[-1000:].replace(token, "[redacted]")}


def source_revision():
    return subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip()


def source_identity():
    tracked = subprocess.check_output(["git", "diff", "--binary", "HEAD"], cwd=ROOT)
    untracked = subprocess.check_output(["git", "ls-files", "--others", "--exclude-standard"], cwd=ROOT, text=True).splitlines()
    digest = hashlib.sha256(tracked)
    for relative in untracked:
        digest.update(relative.encode() + b"\0")
        digest.update(hashlib.sha256((ROOT / relative).read_bytes()).digest())
    return {"tracked_diff_sha256": hashlib.sha256(tracked).hexdigest(), "untracked_count": len(untracked),
            "worktree_sha256": digest.hexdigest()}


def running_metadata(env):
    services = {}
    for service in ("app", "searxng"):
        container = compose(["ps", "-q", service], env, 20).stdout.strip()
        if not container:
            raise RuntimeError(f"missing {service} container")
        image_id = subprocess.check_output(["docker", "inspect", "--format", "{{.Image}}", container], text=True).strip()
        digests = subprocess.check_output(["docker", "image", "inspect", "--format", "{{json .RepoDigests}}", image_id], text=True).strip()
        services[service] = {"container_id": container, "image_id": image_id, "repo_digests": json.loads(digests or "[]")}
    return services


def engine_inventory(env):
    command = ["exec", "-T", "searxng", "wget", "-qO-", "http://127.0.0.1:8080/config"]
    raw = compose(command, env, 20).stdout
    config = json.loads(raw)
    engines = config.get("engines")
    if not isinstance(engines, list):
        raise RuntimeError("SearXNG /config did not expose engine inventory")
    safe = [{"name": item.get("name"), "categories": item.get("categories", []), "enabled": item.get("enabled", True)}
            for item in engines if isinstance(item, dict)]
    enabled = [item for item in safe if item["enabled"] and isinstance(item["name"], str) and isinstance(item["categories"], list)]
    names = {item["name"] for item in enabled}
    categories = {category for item in enabled for category in item["categories"]}
    missing = {"names": sorted({"arxiv", "youtube", "brave"} - names), "categories": sorted({"general", "news", "images"} - categories)}
    return {"engines": enabled, "missing": missing}


def ensure_fresh_resources():
    filters = ["docker", "ps", "-aq", "--filter", "label=com.docker.compose.project=ia-buscar-quality"]
    containers = subprocess.check_output(filters, text=True).split()
    networks = subprocess.check_output(["docker", "network", "ls", "-q", "--filter", "name=^ia-buscar-quality-net$"], text=True).split()
    if containers or networks:
        raise RuntimeError("refusing pre-existing ia-buscar-quality resources")
    with socket.socket() as probe:
        probe.bind(("127.0.0.1", 18080))


def bootstrap(token, deadline):
    initialize, meta = rpc("initialize", "initialize", {"protocolVersion": "2024-11-05", "clientId": "quality-baseline"}, token, deadline)
    if initialize is None:
        raise RuntimeError(meta)
    tools, meta = rpc("tools-list", "tools/list", {}, token, deadline)
    names = tools.get("tools") if isinstance(tools, dict) else None
    if not isinstance(names, list) or len(names) != 28:
        raise RuntimeError("tools/list did not return 28 tools")
    return {"initialize": initialize, "server_version": initialize.get("serverInfo", {}).get("version", ""), "tool_count": len(names), "tool_names": [tool.get("name") for tool in names if isinstance(tool, dict)]}


def run(output, warm_tool="search_web"):
    validate_manifest(MANIFEST)
    run_id, token, deadline = uuid.uuid4().hex, secrets.token_urlsafe(32), time.monotonic() + GLOBAL_DEADLINE
    env, owns_resources = os.environ.copy(), False
    env["IA_BUSCAR_QUALITY_AUTH_KEY"] = token
    evidence = {"run_id": run_id, "started_at_utc": utc_now(), "endpoint": ENDPOINT, "image": SEARXNG_IMAGE,
                "source_commit": source_revision(), "source_identity": source_identity(), "manifest": MANIFEST,
                "warm_tool": warm_tool, "calls": [],
                "global_deadline_seconds": GLOBAL_DEADLINE}
    primary = None
    try:
        ensure_fresh_resources()
        owns_resources = True
        compose(["up", "--build", "--wait", "--wait-timeout", "90"], env, max(1, int(deadline - time.monotonic())))
        evidence["running_metadata"] = running_metadata(env)
        evidence["engine_inventory"] = engine_inventory(env)
        if evidence["engine_inventory"]["missing"]["names"] or evidence["engine_inventory"]["missing"]["categories"]:
            raise RuntimeError("required SearXNG engine inventory is unavailable")
        evidence["capabilities"] = bootstrap(token, deadline)
        for case in MANIFEST:
            evidence["calls"].append(search(case, token, deadline, "initial"))
        initial = warm_case(warm_tool)
        warm = {**initial, "id": "warm-" + initial["id"]}
        evidence["calls"].append(search(warm, token, deadline, "warm_cache"))
    except Exception as error:
        primary = error
        evidence["primary_failure"] = safe_error(error, token)
    finally:
        if owns_resources:
            try:
                compose(["down", "--remove-orphans", "--timeout", "20"], env, 30)
            except Exception as error:
                evidence["cleanup_failure"] = safe_error(error, token)
        evidence["finished_at_utc"] = utc_now()
        output.parent.mkdir(parents=True, exist_ok=True)
        output.write_text(json.dumps(evidence, indent=2) + "\n")
    failed = any(call["status"] in {"transport_failure", "protocol_failure"} for call in evidence["calls"])
    return 1 if primary or failed or "cleanup_failure" in evidence else 0


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("command", choices=("manifest", "run"))
    parser.add_argument("--output", type=Path)
    parser.add_argument("--warm-tool", choices=TOOLS, default="search_web")
    args = parser.parse_args()
    validate_manifest(MANIFEST)
    if args.command == "manifest":
        print(json.dumps(MANIFEST, indent=2))
        return 0
    output = args.output or Path(tempfile.mkdtemp(prefix="ia-buscar-quality-")) / "baseline.json"
    return run(output, args.warm_tool)


if __name__ == "__main__":
    sys.exit(main())
