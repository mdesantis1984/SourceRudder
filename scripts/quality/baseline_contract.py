"""Pure manifest and protocol rules for the live-search baseline."""

import json
from collections import Counter

SEARXNG_IMAGE = "searxng/searxng:2026.9.3-a1144dda3@sha256:0b8a200aaa0ec63e6595e18816c19f12d0b9d38326b9fc4123fc1fa80a438776"
TOOLS = (
    "search_web",
    "search_doc_oficial",
    "search_news",
    "search_academic",
    "search_images",
    "search_youtube",
    "search_local_index",
    "search_reddit",
    "search_stackoverflow",
)
MAX_RESULTS = 3
SNIPPET_LIMIT = 240
QUERIES = {
    "search_web": [("Go context cancellation documentation", "general technical documentation"), ("Docker Compose healthcheck", "container-operation documentation"), ("OpenAPI authentication specification", "API specification documentation")],
    "search_doc_oficial": [("Python json module", "official Python documentation candidates"), ("Go net http package", "official Go documentation candidates"), ("Docker Compose reference", "official Docker documentation candidates")],
    "search_news": [("Kubernetes release", "recent Kubernetes reporting"), ("PostgreSQL release", "recent PostgreSQL reporting"), ("Linux kernel release", "recent Linux reporting")],
    "search_academic": [("retrieval augmented generation evaluation", "academic research papers"), ("transformer attention paper", "academic research papers"), ("software supply chain security research", "academic research papers")],
    "search_images": [("solar eclipse", "image results"), ("container architecture diagram", "image results"), ("aurora borealis", "image results")],
    "search_youtube": [("Git rebase tutorial", "video tutorials"), ("Kubernetes tutorial", "video tutorials"), ("Python async tutorial", "video tutorials")],
    "search_local_index": [("workspace architecture", "local workspace index results or explicit unavailability"), ("authentication middleware", "local workspace index results or explicit unavailability"), ("Docker configuration", "local workspace index results or explicit unavailability")],
    "search_reddit": [("Python packaging", "community discussion"), ("Docker Compose", "community discussion"), ("Kubernetes debugging", "community discussion")],
    "search_stackoverflow": [("Python json decode error", "technical Q&A"), ("Go context cancellation", "technical Q&A"), ("Docker Compose healthcheck", "technical Q&A")],
}
MANIFEST = [
    {"id": f"{tool}-{index + 1}", "tool": tool, "query": query, "intent": intent}
    for tool in TOOLS
    for index, (query, intent) in enumerate(QUERIES[tool])
]


class ProtocolFailure(Exception):
    """The server response does not satisfy the expected MCP contract."""


def validate_manifest(rows):
    if len(rows) != 27 or tuple(dict.fromkeys(row["tool"] for row in rows)) != TOOLS:
        raise ValueError("manifest must contain the required nine tools")
    if Counter(row["tool"] for row in rows) != Counter({tool: 3 for tool in TOOLS}):
        raise ValueError("manifest must contain exactly three cases per tool")
    if len({row["id"] for row in rows}) != len(rows):
        raise ValueError("manifest IDs must be unique")
    for row in rows:
        if set(row) != {"id", "tool", "query", "intent"} or not all(row.values()):
            raise ValueError("each case needs id, tool, query, and expected intent")


def request_body(request_id, method, params):
    return json.dumps({"jsonrpc": "2.0", "id": request_id, "method": method, "params": params}).encode()


def parse_envelope(envelope, request_id):
    if not isinstance(envelope, dict):
        raise ProtocolFailure("response is not JSON object")
    if envelope.get("jsonrpc") != "2.0" or envelope.get("id") != request_id:
        raise ProtocolFailure("invalid JSON-RPC version or id")
    if "error" in envelope:
        raise ProtocolFailure("JSON-RPC error envelope")
    if not isinstance(envelope.get("result"), dict):
        raise ProtocolFailure("missing JSON-RPC result")
    return envelope["result"]


def decode_tool_result(result):
    if result.get("isError"):
        return None, "tool_error"
    content = result.get("content")
    if not isinstance(content, list) or len(content) != 1 or not isinstance(content[0], dict) or content[0].get("type") != "text":
        raise ProtocolFailure("expected result.content[0] text wrapper")
    text = content[0].get("text")
    if not isinstance(text, str):
        raise ProtocolFailure("tool text content is not a string")
    payload = json.loads(text)
    if not isinstance(payload, dict) or not isinstance(payload.get("results"), list):
        raise ProtocolFailure("tool text is not a SearchResponse JSON object")
    if any(not isinstance(item, dict) for item in payload["results"]) or any(not isinstance(payload.get(name, []), list) for name in ("warnings", "errors")):
        raise ProtocolFailure("SearchResponse contains malformed fields")
    return payload, None


def classify(payload):
    strategy, errors, results = str(payload.get("strategy", "")), payload.get("errors", []), payload["results"]
    warnings = payload.get("warnings", [])
    if strategy == "local_index_unavailable":
        return "capability_unavailable"
    if "fallback" in strategy or any("fallback" in str(item) for item in warnings):
        return "fallback"
    if payload.get("partial"):
        return "usable_partial" if results else "provider_failure"
    if errors:
        return "provider_failure"
    return "healthy_results" if results else "healthy_empty"


def result_evidence(payload):
    items = payload["results"]
    urls = list(dict.fromkeys(item.get("url") for item in items if isinstance(item, dict) and isinstance(item.get("url"), str)))
    sources = list(dict.fromkeys(item.get("source") for item in items if isinstance(item, dict) and isinstance(item.get("source"), str)))
    snippets = [
        {"title": str(item.get("title", ""))[:SNIPPET_LIMIT], "snippet": str(item.get("snippet", ""))[:SNIPPET_LIMIT]}
        for item in items[:MAX_RESULTS]
    ]
    return {
        "status": classify(payload),
        "strategy": payload.get("strategy", ""),
        "cached": bool(payload.get("cached")),
        "partial": bool(payload.get("partial")),
        "result_count": len(items),
        "urls": urls,
        "sources": sources,
        "warnings": payload.get("warnings", []),
        "errors": payload.get("errors", []),
        "snippets": snippets,
        "relevance": "human_review_required",
    }


def search_params(case):
    return {"name": case["tool"], "arguments": {"query": case["query"], "maxResults": MAX_RESULTS, "safeSearch": False}}


def warm_case(tool):
    if tool not in TOOLS:
        raise ValueError("warm tool must be one of the manifest tools")
    return next(case for case in MANIFEST if case["tool"] == tool)
