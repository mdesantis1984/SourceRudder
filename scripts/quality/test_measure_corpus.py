import contextlib
import copy
import io
import json
import tempfile
import unittest
from pathlib import Path

import measure_corpus as mc


def good_case(**overrides):
    case = {
        "id": "web-1",
        "tool": "search_web",
        "arguments": {"query": "go context", "maxResults": 10},
        "k": 3,
        "expect_any": ["pkg.go.dev/context"],
    }
    case.update(overrides)
    return case


def corpus(*cases):
    return {"schema_version": 1, "cases": list(cases) or [good_case()]}


def json_text(*urls, **extra):
    payload = {"results": [{"title": f"t{i}", "url": url, "snippet": "s"} for i, url in enumerate(urls)]}
    payload.update(extra)
    return json.dumps(payload)


def tool_result(text, is_error=False):
    return {"content": [{"type": "text", "text": text}], "isError": is_error}


class FakeCall:
    """Stands in for live_baseline.rpc bound to a token and deadline."""

    def __init__(self, texts, latency=10):
        self.texts = texts
        self.latency = latency
        self.calls = []

    def __call__(self, request_id, method, params):
        self.calls.append((request_id, method, params))
        if method != "tools/call":
            return {}, {"latency_ms": 1}
        text = self.texts[params["arguments"]["query"]]
        if isinstance(text, tuple):
            return None, {"status": "transport_failure", "error": text[0], "latency_ms": 5}
        return tool_result(text), {"latency_ms": self.latency}


class ValidateCorpusTests(unittest.TestCase):
    def assertInvalid(self, data, fragment):
        with self.assertRaises(mc.CorpusError) as caught:
            mc.validate_corpus(data)
        self.assertIn(fragment, str(caught.exception))

    def test_valid_corpus_passes(self):
        mc.validate_corpus(corpus())

    def test_shipped_corpus_is_valid_and_covers_every_tool(self):
        data = json.loads(mc.DEFAULT_CORPUS.read_text(encoding="utf-8"))
        mc.validate_corpus(data)
        self.assertEqual(set(mc.KNOWN_TOOLS), {case["tool"] for case in data["cases"]})
        self.assertGreaterEqual(len(data["cases"]), 18)

    def test_duplicate_ids_rejected(self):
        self.assertInvalid(corpus(good_case(), good_case()), "duplicate id 'web-1'")

    def test_unknown_tool_rejected(self):
        self.assertInvalid(corpus(good_case(tool="search_nothing")), "unknown tool 'search_nothing'")

    def test_k_must_be_positive_integer(self):
        for bad in (0, -1, 1.5, "3", True):
            with self.subTest(k=bad):
                self.assertInvalid(corpus(good_case(k=bad)), "k must be an integer >= 1")

    def test_k_cannot_exceed_max_results(self):
        self.assertInvalid(corpus(good_case(k=11)), "k (11) exceeds maxResults (10)")

    def test_expectations_must_be_non_empty_strings(self):
        for bad in ([], None, "pkg.go.dev", [""], [" "], [3]):
            with self.subTest(expect_any=bad):
                self.assertInvalid(corpus(good_case(expect_any=bad)), "expect_any must be a non-empty list of non-empty strings")

    def test_query_required(self):
        self.assertInvalid(corpus(good_case(arguments={"maxResults": 5})), "arguments.query must be a non-empty string")

    def test_max_results_must_be_positive_integer(self):
        self.assertInvalid(corpus(good_case(arguments={"query": "q", "maxResults": 0})), "arguments.maxResults must be an integer >= 1")

    def test_missing_id_and_wrong_schema_version(self):
        case = good_case()
        del case["id"]
        self.assertInvalid(corpus(case), "id must be a non-empty string")
        self.assertInvalid({"schema_version": 2, "cases": [good_case()]}, "unsupported schema_version 2")
        self.assertInvalid({"schema_version": 1, "cases": []}, "cases must be a non-empty list")

    def test_extra_arguments_allowed(self):
        mc.validate_corpus(corpus(good_case(arguments={"query": "q", "maxResults": 10, "format": "compact", "offset": 5})))


class MathTests(unittest.TestCase):
    def test_percentile_nearest_rank(self):
        values = [10, 20, 30, 40]
        self.assertEqual(20, mc.percentile(values, 50))
        self.assertEqual(40, mc.percentile(values, 95))
        self.assertEqual(10, mc.percentile(values, 0))
        self.assertEqual(7, mc.percentile([7], 95))
        self.assertEqual(95, mc.percentile(list(range(1, 101)), 95))

    def test_percentile_of_nothing_is_none(self):
        self.assertIsNone(mc.percentile([], 50))

    def test_token_estimate_is_ceil_chars_over_four(self):
        self.assertEqual(0, mc.estimate_tokens(""))
        self.assertEqual(1, mc.estimate_tokens("abcd"))
        self.assertEqual(2, mc.estimate_tokens("abcde"))
        self.assertEqual(2, mc.estimate_tokens("ñ" * 8))  # chars, not UTF-8 bytes

    def test_hit_at_k_and_first_rank(self):
        urls = ["https://a.example/x", "https://pkg.go.dev/context", "https://b.example"]
        self.assertEqual((True, 2), mc.hit_at_k(urls, ["PKG.GO.DEV/context"], 3))
        self.assertEqual((False, None), mc.hit_at_k(urls, ["pkg.go.dev/context"], 1))
        self.assertEqual((True, 1), mc.hit_at_k(urls, ["a.example", "pkg.go.dev"], 3))
        self.assertEqual((False, None), mc.hit_at_k([], ["x"], 5))


COMPACT = "\n".join([
    "1. Go context | https://pkg.go.dev/context | Package context defines the Context type",
    "2. Blog | https://go.dev/blog/context",
    "3. Title with \\| pipe | https://example.org/p | snippet",  # internal/compact escapes a literal pipe
    "warning: query relaxed: go context",
    "error: engine timeout",
])


class ParseTests(unittest.TestCase):
    def test_compact_parsing(self):
        parsed = mc.parse_compact(COMPACT)
        self.assertEqual(
            ["https://pkg.go.dev/context", "https://go.dev/blog/context", "https://example.org/p"],
            parsed["urls"],
        )
        self.assertEqual(3, parsed["result_count"])
        self.assertEqual(["query relaxed: go context"], parsed["warnings"])
        self.assertEqual(["engine timeout"], parsed["errors"])

    def test_compact_parsing_reads_partial_flag_from_header(self):
        # internal/compact prints the flag inside the header line, e.g. "query: q | cached=true | partial=true".
        parsed = mc.parse_compact("query: go context | cached=true | partial=true\n1. Go | https://go.dev | s")
        self.assertTrue(parsed["partial"])
        self.assertEqual(["https://go.dev"], parsed["urls"])
        self.assertFalse(mc.parse_compact("query: go context | cached=true\n1. Go | https://go.dev")["partial"])

    def test_compact_parsing_ignores_escaped_pipe_in_title(self):
        # internal/compact escapes a literal "|" inside a field as "\|"; only unescaped separators split fields.
        line = "1. Fake \\| https://evil.example title | https://real.example/page | snippet \\| https://evil2.example"
        parsed = mc.parse_compact(line)
        self.assertEqual(["https://real.example/page"], parsed["urls"])

    def test_compact_parsing_empty_text(self):
        parsed = mc.parse_compact("")
        self.assertEqual((0, [], [], []), (parsed["result_count"], parsed["urls"], parsed["warnings"], parsed["errors"]))

    def test_json_parsing(self):
        text = json_text("https://a.example", "https://b.example", partial=True, warnings=["w"], errors=["e"])
        parsed = mc.parse_json(text)
        self.assertEqual(["https://a.example", "https://b.example"], parsed["urls"])
        self.assertEqual((2, True, ["w"], ["e"]), (parsed["result_count"], parsed["partial"], parsed["warnings"], parsed["errors"]))

    def test_json_parsing_rejects_garbage(self):
        with self.assertRaises(mc.CorpusError):
            mc.parse_json("not json")


class RunCaseTests(unittest.TestCase):
    def test_json_case_records_cost_and_quality(self):
        text = json_text("https://x.example", "https://pkg.go.dev/context")
        call = FakeCall({"go context": text}, latency=42)
        record = mc.run_case(good_case(), call, "json", None, 1)
        self.assertEqual(len(text.encode()), record["bytes"])
        self.assertEqual(mc.estimate_tokens(text), record["tokens_estimate"])
        self.assertEqual(42, record["latency_ms"])
        self.assertEqual([42], record["latencies_ms"])
        self.assertEqual(2, record["result_count"])
        self.assertEqual((True, 2, "ok"), (record["hit"], record["first_hit_rank"], record["status"]))
        _, method, params = call.calls[0]
        self.assertEqual("tools/call", method)
        self.assertEqual("search_web", params["name"])
        self.assertNotIn("format", params["arguments"])

    def test_miss_when_expectation_is_below_k(self):
        text = json_text("https://x.example", "https://y.example", "https://z.example", "https://pkg.go.dev/context")
        record = mc.run_case(good_case(k=3), FakeCall({"go context": text}), "json", None, 1)
        self.assertEqual((False, None), (record["hit"], record["first_hit_rank"]))

    def test_compact_format_is_requested_and_parsed(self):
        call = FakeCall({"go context": COMPACT})
        record = mc.run_case(good_case(), call, "compact", "titles", 1)
        arguments = call.calls[0][2]["arguments"]
        self.assertEqual(("compact", "titles"), (arguments["format"], arguments["detail"]))
        self.assertEqual((3, True, 1), (record["result_count"], record["hit"], record["first_hit_rank"]))
        self.assertEqual(["query relaxed: go context"], record["warnings"])

    def test_transport_failure_is_a_recorded_miss(self):
        record = mc.run_case(good_case(), FakeCall({"go context": ("boom",)}), "json", None, 1)
        self.assertEqual("transport_failure", record["status"])
        self.assertEqual((False, 0, 0, None), (record["hit"], record["bytes"], record["result_count"], record["first_hit_rank"]))
        self.assertEqual(["boom"], record["errors"])

    def test_tool_error_keeps_visible_text_cost(self):
        def call(request_id, method, params):
            return tool_result("rate limited", True), {"latency_ms": 3}
        record = mc.run_case(good_case(), call, "json", None, 1)
        self.assertEqual(("tool_error", 12, False), (record["status"], record["bytes"], record["hit"]))

    def test_unparsable_json_is_protocol_failure(self):
        record = mc.run_case(good_case(), FakeCall({"go context": "oops"}), "json", None, 1)
        self.assertEqual(("protocol_failure", 4), (record["status"], record["bytes"]))

    def test_repeat_records_every_latency_and_keeps_first_as_cold(self):
        latencies = iter([100, 5, 6])
        def call(request_id, method, params):
            return tool_result(json_text("https://pkg.go.dev/context")), {"latency_ms": next(latencies)}
        record = mc.run_case(good_case(), call, "json", None, 3)
        self.assertEqual([100, 5, 6], record["latencies_ms"])
        self.assertEqual(100, record["latency_ms"])

    def test_cli_arguments_override_case_arguments(self):
        call = FakeCall({"go context": json_text("https://pkg.go.dev/context")})
        case = good_case(arguments={"query": "go context", "maxResults": 10, "detail": "full"})
        mc.run_case(case, call, "compact", "titles", 1)
        self.assertEqual("titles", call.calls[0][2]["arguments"]["detail"])


def record(case_id, bytes_, latency, hit, rank, tokens=None):
    return {"id": case_id, "tool": "search_web", "k": 3, "status": "ok", "bytes": bytes_,
            "tokens_estimate": tokens if tokens is not None else bytes_ // 4, "latency_ms": latency,
            "result_count": 3, "hit": hit, "first_hit_rank": rank, "warnings": [], "errors": [], "partial": False}


def evidence(cases, **extra):
    data = {"schema_version": mc.SCHEMA_VERSION, "cases": cases, "aggregate": mc.aggregate(cases)}
    data.update(extra)
    return data


class AggregateTests(unittest.TestCase):
    def test_aggregate_values(self):
        cases = [record("a", 400, 10, True, 1), record("b", 800, 20, True, 3), record("c", 1200, 100, False, None), record("d", 2000, 40, True, 2)]
        agg = mc.aggregate(cases)
        self.assertEqual(4, agg["cases"])
        self.assertEqual(4400, agg["total_bytes"])
        self.assertEqual(1000, agg["median_bytes"])
        self.assertEqual(1100, agg["total_tokens_estimate"])
        self.assertEqual(250, agg["median_tokens_estimate"])
        self.assertEqual((20, 100), (agg["latency_p50_ms"], agg["latency_p95_ms"]))
        self.assertEqual(0.75, agg["hit_rate"])
        self.assertEqual(2.0, agg["mean_first_hit_rank"])

    def test_aggregate_without_hits_has_no_mean_rank(self):
        agg = mc.aggregate([record("a", 4, 1, False, None)])
        self.assertEqual((0.0, None), (agg["hit_rate"], agg["mean_first_hit_rank"]))


class CompareTests(unittest.TestCase):
    def write(self, directory, name, data):
        path = Path(directory) / name
        path.write_text(json.dumps(data), encoding="utf-8")
        return str(path)

    def run_compare(self, before, after):
        with tempfile.TemporaryDirectory() as directory:
            paths = [self.write(directory, "b.json", before), self.write(directory, "a.json", after)]
            out, err = io.StringIO(), io.StringIO()
            with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
                code = mc.main(["compare", *paths])
        return code, out.getvalue(), err.getvalue()

    def test_deltas_and_exit_zero_when_quality_holds(self):
        before = evidence([record("a", 1000, 100, True, 2), record("b", 2000, 200, True, 1)])
        after = evidence([record("a", 500, 80, True, 1), record("b", 900, 150, True, 1)])
        delta = mc.compare_evidence(before, after)
        self.assertEqual(-1600, delta["aggregate"]["total_bytes"]["delta"])
        self.assertEqual((3000, 1400), (delta["aggregate"]["total_bytes"]["before"], delta["aggregate"]["total_bytes"]["after"]))
        code, out, _ = self.run_compare(before, after)
        self.assertEqual(0, code)
        self.assertIn("hit@K", out)
        self.assertIn("total_bytes", out)

    def test_exit_one_when_hit_rate_drops(self):
        before = evidence([record("a", 1000, 100, True, 1), record("b", 1000, 100, True, 1)])
        after = evidence([record("a", 100, 10, True, 1), record("b", 100, 10, False, None)])
        code, out, _ = self.run_compare(before, after)
        self.assertEqual(1, code)
        self.assertIn("QUALITY REGRESSION", out)

    def test_exit_zero_when_hit_rate_improves_even_if_bytes_grow(self):
        before = evidence([record("a", 100, 10, False, None)])
        after = evidence([record("a", 900, 50, True, 2)])
        self.assertEqual(0, self.run_compare(before, after)[0])

    def test_compares_only_common_cases_and_reports_the_rest(self):
        before = evidence([record("a", 100, 10, True, 1), record("only-before", 100, 10, True, 1)])
        after = evidence([record("a", 100, 10, True, 1), record("only-after", 100, 10, False, None)])
        delta = mc.compare_evidence(before, after)
        self.assertEqual(["a"], [row["id"] for row in delta["cases"]])
        self.assertEqual((["only-before"], ["only-after"]), (delta["only_before"], delta["only_after"]))
        self.assertEqual(0, self.run_compare(before, after)[0])

    def test_no_common_cases_is_usage_error(self):
        code, _, err = self.run_compare(evidence([record("a", 1, 1, True, 1)]), evidence([record("b", 1, 1, True, 1)]))
        self.assertEqual(2, code)
        self.assertIn("no common cases", err)

    def test_schema_version_mismatch_is_usage_error(self):
        bad = evidence([record("a", 1, 1, True, 1)])
        bad["schema_version"] = 99
        code, _, err = self.run_compare(bad, evidence([record("a", 1, 1, True, 1)]))
        self.assertEqual(2, code)
        self.assertIn("schema_version", err)


class RunCommandTests(unittest.TestCase):
    def test_run_corpus_writes_evidence_without_secrets(self):
        data = corpus(good_case(), good_case(id="web-2", arguments={"query": "docker", "maxResults": 10}, expect_any=["docs.docker.com"]))
        call = FakeCall({"go context": json_text("https://pkg.go.dev/context"), "docker": json_text("https://nope.example")})
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "out" / "run.json"
            result = mc.run_corpus(data, call, output, "json", None, 1, corpus_name="c.json")
            written = json.loads(output.read_text(encoding="utf-8"))
        self.assertEqual(written, json.loads(json.dumps(result)))
        self.assertEqual(mc.SCHEMA_VERSION, written["schema_version"])
        self.assertRegex(written["started_at_utc"], r"^\d{4}-\d\d-\d\dT.*Z$")
        self.assertEqual("ceil(characters / 4)", written["token_estimate_method"])
        self.assertEqual(0.5, written["aggregate"]["hit_rate"])
        self.assertEqual({"format": "json", "detail": None, "repeat": 1}, written["options"])
        self.assertNotIn("Authorization", json.dumps(written))

    def test_run_requires_auth_key_in_environment(self):
        err = io.StringIO()
        with tempfile.TemporaryDirectory() as directory, contextlib.redirect_stderr(err):
            code = mc.main(["run", "--output", str(Path(directory) / "o.json")], env={})
        self.assertEqual(2, code)
        self.assertIn(mc.TOKEN_ENV, err.getvalue())
        self.assertNotIn("secret", err.getvalue())

    def test_validate_command(self):
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            self.assertEqual(0, mc.main(["validate"]))
        self.assertIn("corpus ok", out.getvalue())
        with tempfile.TemporaryDirectory() as directory:
            bad = Path(directory) / "bad.json"
            bad.write_text(json.dumps(corpus(good_case(k=0))), encoding="utf-8")
            with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
                self.assertEqual(1, mc.main(["validate", "--corpus", str(bad)]))
        self.assertIn("k must be an integer >= 1", err.getvalue())

    def test_endpoint_is_reused_loopback_constant(self):
        import live_baseline
        self.assertTrue(live_baseline.ENDPOINT.startswith("http://127.0.0.1:"))
        self.assertIs(live_baseline.rpc, mc.live_baseline.rpc)


if __name__ == "__main__":
    unittest.main()
