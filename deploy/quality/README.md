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

## Search-result cost and quality corpus

`scripts/quality/measure_corpus.py` measures what each search response costs and
whether its top results are good, so a change (for example the compact format or
result-set paging) can be compared with the code before it. The fixed corpus is
`scripts/quality/corpus/search_quality.json`: 20 cases over `search_web`,
`search_github`, `search_stackoverflow`, `search_npm`, `search_pypi`,
`search_doc_oficial` and `search_news`. Each case has `arguments`, a `k` and
`expect_any` URL substrings; it counts as a hit when any of the top `k` result
URLs contains one (case-insensitive). Expectations are human-auditable; the news
cases are volatile, so audit a miss before treating it as a regression.

```bash
python3 scripts/quality/measure_corpus.py validate
```

The runner reuses the loopback-only transport of `live_baseline.py` and needs a
stack that is already running with a key you choose. Never put the key in a
file or in a report; the evidence files do not contain it.

```bash
export SOURCERUDDER_QUALITY_AUTH_KEY="$(openssl rand -hex 32)"
docker compose -f deploy/quality/docker-compose.yml --project-name sourcerudder-quality up --build --wait
python3 scripts/quality/measure_corpus.py run --output /tmp/sourcerudder-quality/before.json
python3 scripts/quality/measure_corpus.py run --format compact --detail snippets --output /tmp/sourcerudder-quality/after.json
docker compose -f deploy/quality/docker-compose.yml --project-name sourcerudder-quality down --remove-orphans
python3 scripts/quality/measure_corpus.py compare /tmp/sourcerudder-quality/before.json /tmp/sourcerudder-quality/after.json
```

Do not run it while `live_baseline.py run` is active: both use project
`sourcerudder-quality` and port 18080. Per case the evidence records the bytes of
the model-visible text, a token **estimate** (`ceil(characters / 4)`, not a real
tokenizer), latency, result count, partial/warnings/errors, hit@K and the rank of
the first hit. `--repeat N` repeats each call; latency and quality come from the
first (cold) call because later calls may be served from cache. With
`--format compact` results are parsed from `<n>. <title> | <url> ...` lines, and
`warning:`/`error:`/`partial: true` lines. `compare` reports total and median
bytes and tokens, p50/p95 latency (nearest rank), hit@K rate and mean first-hit
rank, and exits 1 when the hit@K rate drops (quality first), 0 otherwise, 2 on
unusable input.
