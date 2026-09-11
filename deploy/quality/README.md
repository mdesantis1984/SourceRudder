[English](README.md) | [Español](README.es.md)

# SourceRudder 2.0 local live-search baseline

SourceRudder 2.0 is **unreleased**. This isolated quality workflow collects reviewable live-search evidence; it is not a production deployment or an automatic relevance grade.

Inspect the fixed nine-case manifest before any live run:

```bash
python3 scripts/quality/live_baseline.py manifest
```

Run the isolated baseline and write evidence outside the repository:

```bash
python3 scripts/quality/live_baseline.py run --output /tmp/sourcerudder-quality/baseline.json
```

To warm the first manifest case for a specific tool while retaining all 27
initial calls, pass `--warm-tool`; the default remains `search_web`:

```bash
python3 scripts/quality/live_baseline.py run --warm-tool search_reddit --output /tmp/sourcerudder-quality/reddit.json
```

The runner generates an in-memory local auth token, refuses pre-existing
`sourcerudder-quality` resources, then starts and removes only that project. It
never grades relevance automatically: URLs are preserved and only snippets are
bounded. Partial responses are evidence, not automatic failures.

The JSON report is written even when startup, execution, or cleanup fails. The
runner exits non-zero for lifecycle, transport, protocol, or cleanup failures;
provider fallback, partial results, and unavailable optional capabilities remain
reviewable evidence. Error text is token-redacted, response bodies are capped at
2 MiB, and source identity records hashes plus an untracked-file count without
recording local filenames or contents.

The normal bridge intentionally gives both services public egress for direct
Reddit and StackOverflow calls; it does not claim LAN blocking. It mounts no
production configuration and makes no production probe. This baseline uses the
pinned release's default SearXNG settings, not a proven production configuration.
The pinned amd64 image is `2026.9.3-a1144dda3@sha256:0b8a200aaa0ec63e6595e18816c19f12d0b9d38326b9fc4123fc1fa80a438776`.
Docker Hub's official tags API reported that tag and digest on 2026-09-05.
