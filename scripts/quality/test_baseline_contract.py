import importlib.util
import json
import unittest
from collections import Counter
from pathlib import Path

PATH = Path(__file__).with_name("baseline_contract.py")
SPEC = importlib.util.spec_from_file_location("baseline_contract", PATH)
contract = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(contract)


def wrapped(payload, is_error=False):
    return {"content": [{"type": "text", "text": json.dumps(payload)}], "isError": is_error}


class LiveBaselineTests(unittest.TestCase):
    def test_manifest_has_exact_tools_and_three_queries_each(self):
        contract.validate_manifest(contract.MANIFEST)
        self.assertEqual({row["tool"] for row in contract.MANIFEST}, set(contract.TOOLS))
        self.assertEqual(Counter(row["tool"] for row in contract.MANIFEST), Counter({tool: 3 for tool in contract.TOOLS}))
        self.assertEqual(len(contract.MANIFEST) + 1, 28)

    def test_manifest_rejects_wrong_tool_coverage(self):
        rows = [row for row in contract.MANIFEST if row["tool"] != "search_reddit"]
        with self.assertRaises(ValueError):
            contract.validate_manifest(rows)

    def test_request_limits_and_safe_search_are_fixed(self):
        request = json.loads(contract.request_body("id", "tools/call", {"name": "search_web"}))
        self.assertEqual(request["id"], "id")
        self.assertEqual(request["jsonrpc"], "2.0")
        params = contract.search_params(contract.MANIFEST[0])
        self.assertEqual(params["arguments"]["maxResults"], 3)
        self.assertFalse(params["arguments"]["safeSearch"])

    def test_settings_use_pinned_release_defaults_without_keep_only(self):
        root = Path(__file__).resolve().parents[2]
        settings = (root / "deploy/quality/searxng/settings.yml").read_text()
        self.assertIn("use_default_settings: true", settings)
        self.assertNotIn("keep_only", settings)

    def test_decodes_real_mcp_text_wrapper_and_rejects_errors(self):
        payload, error = contract.decode_tool_result(wrapped({"results": [], "partial": False}))
        self.assertEqual(payload["results"], [])
        self.assertIsNone(error)
        self.assertEqual(contract.decode_tool_result(wrapped({}, True))[1], "tool_error")
        with self.assertRaises(contract.ProtocolFailure):
            contract.decode_tool_result({"content": []})
        with self.assertRaises(contract.ProtocolFailure):
            contract.decode_tool_result(wrapped({"results": ["not-an-object"]}))

    def test_rejects_jsonrpc_error_and_mismatched_ids(self):
        good = {"jsonrpc": "2.0", "id": "one", "result": {}}
        self.assertEqual(contract.parse_envelope(good, "one"), {})
        for envelope in ({"jsonrpc": "2.0", "id": "two", "result": {}}, {"jsonrpc": "2.0", "id": "one", "error": {"code": -1}}):
            with self.assertRaises(contract.ProtocolFailure):
                contract.parse_envelope(envelope, "one")

    def test_classifies_all_outcomes_without_treating_partial_as_failure(self):
        cases = [
            ({"results": [], "strategy": "local_index_unavailable"}, "capability_unavailable"),
            ({"results": [], "strategy": "official_doc_web_fallback"}, "fallback"),
            ({"results": [{}], "partial": True}, "usable_partial"),
            ({"results": [], "partial": True}, "provider_failure"),
            ({"results": [], "errors": ["upstream"]}, "provider_failure"),
            ({"results": []}, "healthy_empty"), ({"results": [{}]}, "healthy_results"),
        ]
        for payload, expected in cases:
            self.assertEqual(contract.classify(payload), expected)

    def test_result_evidence_keeps_full_urls_and_bounds_only_snippets(self):
        url = "https://example.test/" + "u" * 500
        evidence = contract.result_evidence({"results": [{"url": url, "source": "engine", "title": "t" * 300, "snippet": "s" * 300}] * 4})
        self.assertEqual(evidence["urls"], [url])
        self.assertEqual(len(evidence["snippets"]), 3)
        self.assertEqual(len(evidence["snippets"][0]["snippet"]), contract.SNIPPET_LIMIT)
        self.assertEqual(evidence["relevance"], "human_review_required")

    def test_normalizers_and_manifest_are_deterministic(self):
        self.assertEqual(json.dumps(contract.MANIFEST, sort_keys=True), json.dumps(contract.MANIFEST, sort_keys=True))
        self.assertEqual(contract.result_evidence({"results": []})["urls"], [])

    def test_warm_case_uses_first_case_for_the_requested_manifest_tool(self):
        reddit = contract.warm_case("search_reddit")
        self.assertEqual(reddit, next(case for case in contract.MANIFEST if case["tool"] == "search_reddit"))
        with self.assertRaises(ValueError):
            contract.warm_case("search_unknown")


if __name__ == "__main__":
    unittest.main()
