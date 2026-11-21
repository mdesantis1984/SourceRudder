#!/usr/bin/env python3
"""Measure what each search response costs and whether its top results are good (S11).

Commands:
  validate                      check the fixed corpus schema
  run --output FILE             call every corpus case over MCP and write JSON evidence
  compare BEFORE.json AFTER.json  print deltas; exit 1 if the hit@K rate drops

Transport is reused from live_baseline.py (loopback-only ENDPOINT, size cap, timeouts); this
module adds no HTTP code. Run it against a throwaway stack from deploy/quality, never production.
Token counts are ESTIMATES: ceil(characters / 4) over the model-visible text, stdlib only.
"""
import argparse
import copy
import hashlib
import json
import math
import os
import re
import statistics
import sys
import time
from datetime import datetime, timezone
from pathlib import Path

import live_baseline

SCHEMA_VERSION = 1
TOKEN_ENV = "SOURCERUDDER_QUALITY_AUTH_KEY"
TOKEN_ESTIMATE_METHOD = "ceil(characters / 4)"
DEFAULT_CORPUS = Path(__file__).resolve().parent / "corpus" / "search_quality.json"
KNOWN_TOOLS = (
    "search_web",
    "search_github",
    "search_stackoverflow",
    "search_npm",
    "search_pypi",
    "search_doc_oficial",
    "search_news",
)
AGGREGATE_METRICS = (
    "total_bytes",
    "median_bytes",
    "total_tokens_estimate",
    "median_tokens_estimate",
    "latency_p50_ms",
    "latency_p95_ms",
    "hit_rate",
    "mean_first_hit_rank",
)
NUMBERED_LINE = re.compile(r"^\s*(\d+)\.\s+(.*)$")
URL_FIELD = re.compile(r"^https?://\S+$")
# internal/compact escapes a literal "|" inside a field as "\|"; only unescaped pipes separate fields.
FIELD_SEPARATOR = re.compile(r"(?<!\\)\|")
HEADER_LINE = re.compile(r"^\s*query:")
NOTE_LINE = re.compile(r"^\s*(warnings?|errors?)\s*:\s*(.*)$", re.IGNORECASE)
PARTIAL_LINE = re.compile(r"^\s*partial\s*:\s*true\b", re.IGNORECASE)


class CorpusError(Exception):
    """The corpus or a response does not satisfy the expected shape."""


def is_int(value):
    return isinstance(value, int) and not isinstance(value, bool)


def validate_case(case, seen):
    if not isinstance(case, dict):
        raise CorpusError("each case must be an object")
    case_id = case.get("id")
    if not isinstance(case_id, str) or not case_id.strip():
        raise CorpusError("id must be a non-empty string")
    label = f"case '{case_id}'"
    if case_id in seen:
        raise CorpusError(f"duplicate id '{case_id}'")
    seen.add(case_id)
    if case.get("tool") not in KNOWN_TOOLS:
        raise CorpusError(f"{label}: unknown tool '{case.get('tool')}' (known: {', '.join(KNOWN_TOOLS)})")
    arguments = case.get("arguments")
    if not isinstance(arguments, dict):
        raise CorpusError(f"{label}: arguments must be an object")
    query = arguments.get("query")
    if not isinstance(query, str) or not query.strip():
        raise CorpusError(f"{label}: arguments.query must be a non-empty string")
    max_results = arguments.get("maxResults")
    if max_results is not None and (not is_int(max_results) or max_results < 1):
        raise CorpusError(f"{label}: arguments.maxResults must be an integer >= 1")
    k = case.get("k")
    if not is_int(k) or k < 1:
        raise CorpusError(f"{label}: k must be an integer >= 1")
    if max_results is not None and k > max_results:
        raise CorpusError(f"{label}: k ({k}) exceeds maxResults ({max_results})")
    expect = case.get("expect_any")
    if not isinstance(expect, list) or not expect or not all(isinstance(item, str) and item.strip() for item in expect):
        raise CorpusError(f"{label}: expect_any must be a non-empty list of non-empty strings")


def validate_corpus(data):
    if not isinstance(data, dict):
        raise CorpusError("corpus must be a JSON object")
    if data.get("schema_version") != SCHEMA_VERSION:
        raise CorpusError(f"unsupported schema_version {data.get('schema_version')} (expected {SCHEMA_VERSION})")
    cases = data.get("cases")
    if not isinstance(cases, list) or not cases:
        raise CorpusError("cases must be a non-empty list")
    seen = set()
    for case in cases:
        validate_case(case, seen)


def percentile(values, p):
    """Nearest-rank percentile; None for an empty list."""
    if not values:
        return None
    ordered = sorted(values)
    rank = max(math.ceil(p / 100 * len(ordered)), 1)
    return ordered[rank - 1]


def estimate_tokens(text):
    return math.ceil(len(text) / 4)


def hit_at_k(urls, expect_any, k):
    """Return (hit, 1-based rank of the first matching URL within the top k)."""
    needles = [item.lower() for item in expect_any]
    for rank, url in enumerate(urls[:k], start=1):
        if any(needle in url.lower() for needle in needles):
            return True, rank
    return False, None


def parse_json(text):
    try:
        payload = json.loads(text)
    except json.JSONDecodeError as error:
        raise CorpusError(f"response text is not JSON: {error}") from error
    results = payload.get("results") if isinstance(payload, dict) else None
    if not isinstance(results, list):
        raise CorpusError("response JSON has no results list")
    return {
        "urls": [item.get("url") if isinstance(item, dict) and isinstance(item.get("url"), str) else "" for item in results],
        "result_count": len(results),
        "partial": bool(payload.get("partial")),
        "warnings": [str(item) for item in payload.get("warnings") or []],
        "errors": [str(item) for item in payload.get("errors") or []],
    }


def parse_compact(text):
    """Parse `<n>. <title> | <url> | <snippet>` lines plus `warning:`/`error:`/`partial: true` notes."""
    parsed = {"urls": [], "result_count": 0, "partial": False, "warnings": [], "errors": []}
    for line in text.splitlines():
        numbered = NUMBERED_LINE.match(line)
        if numbered:
            fields = [field.strip() for field in FIELD_SEPARATOR.split(numbered.group(2))]
            # Field 0 is the title and field 1 the URL; later fields are snippet and key=value pairs.
            url = fields[1] if len(fields) > 1 and URL_FIELD.match(fields[1]) else ""
            parsed["urls"].append(url)
            parsed["result_count"] += 1
            continue
        if HEADER_LINE.match(line):
            header_fields = [field.strip() for field in FIELD_SEPARATOR.split(line)]
            if "partial=true" in header_fields:
                parsed["partial"] = True
            continue
        note = NOTE_LINE.match(line)
        if note:
            parsed["warnings" if note.group(1).lower().startswith("warning") else "errors"].append(note.group(2).strip())
        elif PARTIAL_LINE.match(line):
            parsed["partial"] = True
    return parsed


def call_arguments(case, fmt, detail):
    arguments = copy.deepcopy(case["arguments"])
    if fmt == "compact":
        arguments["format"] = "compact"
    if detail:
        arguments["detail"] = detail
    return arguments


def failure_record(base, status, error, latencies):
    return {**base, "status": status, "bytes": 0, "tokens_estimate": 0, "latency_ms": latencies[0],
            "latencies_ms": latencies, "result_count": 0, "partial": False, "warnings": [], "errors": [error],
            "top_urls": [], "hit": False, "first_hit_rank": None}


def run_case(case, call, fmt, detail, repeat):
    """Call one case `repeat` times; quality and cost come from the first (cold) call."""
    arguments = call_arguments(case, fmt, detail)
    effective_format = arguments.get("format", "json")
    params = {"name": case["tool"], "arguments": arguments}
    base = {"id": case["id"], "tool": case["tool"], "k": case["k"], "format": effective_format, "detail": arguments.get("detail")}
    latencies, first = [], None
    for attempt in range(1, repeat + 1):
        result, meta = call(f"{case['id']}#{attempt}", "tools/call", params)
        latencies.append(meta.get("latency_ms", 0))
        if attempt == 1:
            first = (result, meta)
            if result is None:
                return failure_record(base, meta.get("status", "transport_failure"), str(meta.get("error", "")), latencies)
    result = first[0]
    content = result.get("content") if isinstance(result, dict) else None
    text = content[0].get("text") if isinstance(content, list) and content and isinstance(content[0], dict) else None
    if not isinstance(text, str):
        return failure_record(base, "protocol_failure", "missing result.content[0].text", latencies)
    record = {**base, "bytes": len(text.encode("utf-8")), "tokens_estimate": estimate_tokens(text),
              "latency_ms": latencies[0], "latencies_ms": latencies}
    if result.get("isError"):
        return {**record, "status": "tool_error", "result_count": 0, "partial": False, "warnings": [], "errors": [text[:200]],
                "top_urls": [], "hit": False, "first_hit_rank": None}
    try:
        parsed = parse_compact(text) if effective_format == "compact" else parse_json(text)
    except CorpusError as error:
        return {**record, "status": "protocol_failure", "result_count": 0, "partial": False, "warnings": [],
                "errors": [str(error)], "top_urls": [], "hit": False, "first_hit_rank": None}
    hit, rank = hit_at_k(parsed["urls"], case["expect_any"], case["k"])
    return {**record, "status": "ok", "result_count": parsed["result_count"], "partial": parsed["partial"],
            "warnings": parsed["warnings"], "errors": parsed["errors"], "top_urls": parsed["urls"][:case["k"]],
            "hit": hit, "first_hit_rank": rank}


def round_or_none(value, digits):
    return None if value is None else round(value, digits)


def aggregate(cases):
    sizes = [case["bytes"] for case in cases]
    tokens = [case["tokens_estimate"] for case in cases]
    latencies = [case["latency_ms"] for case in cases]
    ranks = [case["first_hit_rank"] for case in cases if case["hit"] and case["first_hit_rank"] is not None]
    return {
        "cases": len(cases),
        "total_bytes": sum(sizes),
        "median_bytes": statistics.median(sizes) if sizes else None,
        "total_tokens_estimate": sum(tokens),
        "median_tokens_estimate": statistics.median(tokens) if tokens else None,
        "latency_p50_ms": percentile(latencies, 50),
        "latency_p95_ms": percentile(latencies, 95),
        "hit_rate": round(sum(1 for case in cases if case["hit"]) / len(cases), 4) if cases else None,
        "mean_first_hit_rank": round_or_none(statistics.mean(ranks) if ranks else None, 3),
    }


def utc_now():
    return datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")


def run_corpus(data, call, output, fmt, detail, repeat, corpus_name="", progress=None):
    validate_corpus(data)
    evidence = {
        "schema_version": SCHEMA_VERSION,
        "started_at_utc": utc_now(),
        "endpoint": live_baseline.ENDPOINT,
        "corpus": {"name": corpus_name, "sha256": hashlib.sha256(json.dumps(data, sort_keys=True).encode()).hexdigest()},
        "options": {"format": fmt or "json", "detail": detail, "repeat": repeat},
        "token_estimate_method": TOKEN_ESTIMATE_METHOD,
        "cases": [],
    }
    for case in data["cases"]:
        record = run_case(case, call, fmt, detail, repeat)
        evidence["cases"].append(record)
        if progress:
            progress(record)
    evidence["aggregate"] = aggregate(evidence["cases"])
    evidence["finished_at_utc"] = utc_now()
    output = Path(output)
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(json.dumps(evidence, indent=2) + "\n", encoding="utf-8")
    return evidence


def delta(before, after):
    return {"before": before, "after": after,
            "delta": round(after - before, 4) if before is not None and after is not None else None}


def compare_evidence(before, after):
    for label, data in (("before", before), ("after", after)):
        if data.get("schema_version") != SCHEMA_VERSION:
            raise CorpusError(f"{label} file has unsupported schema_version {data.get('schema_version')} (expected {SCHEMA_VERSION})")
    old = {case["id"]: case for case in before["cases"]}
    new = {case["id"]: case for case in after["cases"]}
    common = [case_id for case_id in old if case_id in new]
    if not common:
        raise CorpusError("no common cases between the two files")
    old_agg = aggregate([old[i] for i in common])
    new_agg = aggregate([new[i] for i in common])
    rows = [{"id": i, "tool": new[i]["tool"],
             "bytes": delta(old[i]["bytes"], new[i]["bytes"]),
             "tokens_estimate": delta(old[i]["tokens_estimate"], new[i]["tokens_estimate"]),
             "latency_ms": delta(old[i]["latency_ms"], new[i]["latency_ms"]),
             "hit": {"before": old[i]["hit"], "after": new[i]["hit"]},
             "first_hit_rank": {"before": old[i]["first_hit_rank"], "after": new[i]["first_hit_rank"]}} for i in common]
    aggregate_delta = {metric: delta(old_agg[metric], new_agg[metric]) for metric in AGGREGATE_METRICS}
    return {"cases": rows, "aggregate": aggregate_delta,
            "only_before": [i for i in old if i not in new], "only_after": [i for i in new if i not in old],
            "quality_regression": new_agg["hit_rate"] < old_agg["hit_rate"]}


def cell(value):
    return "-" if value is None else (f"{value:g}" if isinstance(value, float) else str(value))


def signed(value):
    return "-" if value is None else f"{value:+g}"


def print_comparison(result, out):
    out.write(f"{'case':<28}{'bytes':>16}{'tokens~':>14}{'latency ms':>18}  hit(rank)\n")
    for row in result["cases"]:
        hit = f"{'Y' if row['hit']['before'] else 'N'}({cell(row['first_hit_rank']['before'])})->{'Y' if row['hit']['after'] else 'N'}({cell(row['first_hit_rank']['after'])})"
        out.write(f"{row['id']:<28}{cell(row['bytes']['before']) + '->' + cell(row['bytes']['after']):>16}"
                  f"{cell(row['tokens_estimate']['before']) + '->' + cell(row['tokens_estimate']['after']):>14}"
                  f"{cell(row['latency_ms']['before']) + '->' + cell(row['latency_ms']['after']):>18}  {hit}\n")
    out.write(f"\n{'aggregate':<26}{'before':>12}{'after':>12}{'delta':>12}\n")
    for metric in AGGREGATE_METRICS:
        values = result["aggregate"][metric]
        name = f"{metric} (hit@K)" if metric == "hit_rate" else metric
        out.write(f"{name:<26}{cell(values['before']):>12}{cell(values['after']):>12}{signed(values['delta']):>12}\n")
    if result["only_before"] or result["only_after"]:
        out.write(f"\nnot compared: only before={result['only_before']} only after={result['only_after']}\n")
    out.write("\ntoken counts are estimates: " + TOKEN_ESTIMATE_METHOD + "\n")
    if result["quality_regression"]:
        out.write("QUALITY REGRESSION: hit@K rate dropped\n")


def load_json(path):
    try:
        return json.loads(Path(path).read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise CorpusError(f"cannot read {path}: {error}") from error


def build_parser():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    commands = parser.add_subparsers(dest="command", required=True)
    validate = commands.add_parser("validate", help="check the corpus schema")
    validate.add_argument("--corpus", type=Path, default=DEFAULT_CORPUS)
    run = commands.add_parser("run", help="call every case against the local quality stack")
    run.add_argument("--corpus", type=Path, default=DEFAULT_CORPUS)
    run.add_argument("--output", type=Path, required=True)
    run.add_argument("--format", choices=("json", "compact"), default="json")
    run.add_argument("--detail", help="detail level sent as the `detail` argument (for example titles, snippets, full)")
    run.add_argument("--repeat", type=int, default=1, help="calls per case; latency uses the first (cold) call")
    compare = commands.add_parser("compare", help="compare two run files; exit 1 if hit@K drops")
    compare.add_argument("before", type=Path)
    compare.add_argument("after", type=Path)
    return parser


def main(argv=None, env=None):
    args = build_parser().parse_args(argv)
    env = os.environ if env is None else env
    try:
        if args.command == "validate":
            data = load_json(args.corpus)
            validate_corpus(data)
            print(f"corpus ok: {len(data['cases'])} cases, tools: {', '.join(sorted({c['tool'] for c in data['cases']}))}")
            return 0
        if args.command == "compare":
            result = compare_evidence(load_json(args.before), load_json(args.after))
            print_comparison(result, sys.stdout)
            return 1 if result["quality_regression"] else 0
        if args.repeat < 1:
            print("--repeat must be >= 1", file=sys.stderr)
            return 2
        token = env.get(TOKEN_ENV)
        if not token:
            print(f"{TOKEN_ENV} must be set to the auth key of the local quality stack", file=sys.stderr)
            return 2
        data = load_json(args.corpus)
        validate_corpus(data)
        deadline = time.monotonic() + live_baseline.GLOBAL_DEADLINE

        def call(request_id, method, params):
            return live_baseline.rpc(request_id, method, params, token, deadline)

        init, meta = call("initialize", "initialize", {"protocolVersion": "2024-11-05", "clientId": "quality-corpus"})
        if init is None:
            print(f"initialize failed: {str(meta.get('error', ''))}".replace(token, "[redacted]"), file=sys.stderr)
            return 1

        def progress(record):
            print(f"{record['id']}: {record['status']} hit={record['hit']} bytes={record['bytes']} {record['latency_ms']}ms", file=sys.stderr)

        evidence = run_corpus(data, call, args.output, args.format, args.detail, args.repeat, args.corpus.name, progress)
        print(f"wrote {args.output} ({evidence['aggregate']['cases']} cases, hit@K {evidence['aggregate']['hit_rate']})")
        return 0
    except CorpusError as error:
        print(str(error), file=sys.stderr)
        return 2 if args.command == "compare" else 1


if __name__ == "__main__":
    sys.exit(main())
