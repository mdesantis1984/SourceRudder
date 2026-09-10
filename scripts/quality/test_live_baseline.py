import importlib.util
import io
import json
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

PATH = Path(__file__).with_name("live_baseline.py")
SPEC = importlib.util.spec_from_file_location("live_baseline", PATH)
baseline = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(baseline)


def wrapped(payload):
    return {"content": [{"type": "text", "text": json.dumps(payload)}]}


class LiveBaselineTests(unittest.TestCase):
    def test_deadlines_and_response_size_are_bounded(self):
        self.assertEqual(baseline.CALL_TIMEOUT, 70)
        self.assertEqual(baseline.GLOBAL_DEADLINE, 2700)
        self.assertEqual(baseline.MAX_RESPONSE_BYTES, 2 * 1024 * 1024)

    def test_rpc_rejects_oversized_responses(self):
        response = io.BytesIO(b"x" * (baseline.MAX_RESPONSE_BYTES + 1))
        with patch.object(baseline.urllib.request, "urlopen", return_value=response):
            result, metadata = baseline.rpc("id", "tools/list", {}, "token", baseline.time.monotonic() + 1)
        self.assertIsNone(result)
        self.assertEqual(metadata["status"], "protocol_failure")

    def test_search_builds_bounded_request_and_evidence(self):
        result = wrapped({"results": [], "strategy": "searxng"})
        with patch.object(baseline, "rpc", return_value=(result, {"latency_ms": 1})) as rpc:
            evidence = baseline.search(baseline.MANIFEST[0], "token", baseline.time.monotonic() + 1, "initial")
        arguments = rpc.call_args.args[2]["arguments"]
        self.assertEqual(arguments["maxResults"], 3)
        self.assertFalse(arguments["safeSearch"])
        self.assertEqual(evidence["status"], "healthy_empty")

    def test_source_identity_hashes_untracked_contents_without_exposing_them(self):
        original_root = baseline.ROOT
        try:
            with tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                baseline.ROOT = root
                subprocess.run(["git", "init", "--quiet"], cwd=root, check=True)
                subprocess.run(["git", "-c", "user.name=Test", "-c", "user.email=test@example.test", "commit", "--quiet", "--allow-empty", "-m", "initial"], cwd=root, check=True)
                (root / "candidate.txt").write_text("candidate bytes")
                identity = baseline.source_identity()
                self.assertEqual(identity["untracked_count"], 1)
                self.assertEqual(len(identity["tracked_diff_sha256"]), 64)
                self.assertEqual(len(identity["worktree_sha256"]), 64)
                self.assertNotIn("candidate.txt", json.dumps(identity))
        finally:
            baseline.ROOT = original_root

    def test_run_writes_evidence_and_cleans_up(self):
        compose_calls = []

        def fake_compose(args, _env, _timeout=None):
            compose_calls.append(args)
            return subprocess.CompletedProcess(args, 0, "", "")

        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "baseline.json"
            with patch.object(baseline, "compose", side_effect=fake_compose), \
                    patch.object(baseline, "ensure_fresh_resources"), \
                    patch.object(baseline, "source_revision", return_value="commit"), \
                    patch.object(baseline, "source_identity", return_value={}), \
                    patch.object(baseline, "running_metadata", return_value={}), \
                    patch.object(baseline, "engine_inventory", return_value={"missing": {"names": [], "categories": []}}), \
                    patch.object(baseline, "bootstrap", return_value={}), \
                    patch.object(baseline, "search", return_value={"status": "healthy_empty"}) as search, \
                    patch.object(baseline.secrets, "token_urlsafe", return_value="private-token"):
                self.assertEqual(baseline.run(output), 0)
            evidence = json.loads(output.read_text())
            self.assertEqual(len(evidence["calls"]), 28)
            self.assertEqual(search.call_count, 28)
            self.assertEqual(compose_calls[0][0], "up")
            self.assertEqual(compose_calls[-1][0], "down")
            self.assertNotIn("private-token", output.read_text())

    def test_run_redacts_failure_and_still_cleans_up(self):
        compose_calls = []

        def fake_compose(args, _env, _timeout=None):
            compose_calls.append(args)
            if args[0] == "up":
                raise RuntimeError("failed with private-token")
            return subprocess.CompletedProcess(args, 0, "", "")

        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "baseline.json"
            with patch.object(baseline, "compose", side_effect=fake_compose), \
                    patch.object(baseline, "ensure_fresh_resources"), \
                    patch.object(baseline, "source_revision", return_value="commit"), \
                    patch.object(baseline, "source_identity", return_value={}), \
                    patch.object(baseline.secrets, "token_urlsafe", return_value="private-token"):
                self.assertEqual(baseline.run(output), 1)
            self.assertEqual(compose_calls[-1][0], "down")
            self.assertNotIn("private-token", output.read_text())
            self.assertIn("[redacted]", output.read_text())

    def test_run_fails_when_cleanup_fails(self):
        def fake_compose(args, _env, _timeout=None):
            if args[0] == "down":
                raise RuntimeError("cleanup failed")
            return subprocess.CompletedProcess(args, 0, "", "")

        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "baseline.json"
            with patch.object(baseline, "compose", side_effect=fake_compose), \
                    patch.object(baseline, "ensure_fresh_resources"), \
                    patch.object(baseline, "source_revision", return_value="commit"), \
                    patch.object(baseline, "source_identity", return_value={}), \
                    patch.object(baseline, "running_metadata", return_value={}), \
                    patch.object(baseline, "engine_inventory", return_value={"missing": {"names": [], "categories": []}}), \
                    patch.object(baseline, "bootstrap", return_value={}), \
                    patch.object(baseline, "search", return_value={"status": "healthy_empty"}):
                self.assertEqual(baseline.run(output), 1)
            evidence = json.loads(output.read_text())
            self.assertEqual(evidence["cleanup_failure"]["message"], "cleanup failed")


if __name__ == "__main__":
    unittest.main()
