# Decisions log

This log records project choices and their reasons. Entries reflect the conversation and implementation as of 2026-10-11. **Accepted** means chosen; **provisional** means chosen for the pilot and subject to validation; **deferred** means not implemented. Acceptance does not imply every requirement is complete.

Add a dated entry when a material choice changes. Keep previous entries and mark them superseded, linking to the replacement. Record the context, decision, consequence, and evidence. Keep credentials and private file contents out of this log.

## D001 — Run the service on the existing NAS

**Date:** 2026-10-11 · **Status:** Accepted

The target is a Synology DS220+ with an Intel Celeron J4025 and 2 GB RAM. The user ruled out hardware upgrades and running the service on another machine. Run the server, index, extraction, and embeddings on the NAS in a Container Manager container. Development and container builds may run elsewhere.

This constrains memory, concurrency, and model size. A successful small pilot does not establish full-library performance.

## D002 — Build a custom service

**Date:** 2026-10-11 · **Status:** Accepted

After considering existing products, the user authorized a custom implementation. No evaluated option was accepted as meeting the combined hardware, search, and MCP requirements. GraphRAG and LightRAG were discussed without being selected.

Build a focused file-search service. This gives control over resource use and verification, but makes maintenance our responsibility.

## D003 — Use Dafny, TDD, and parallel agents

**Date:** 2026-10-11 · **Status:** Accepted

The user requested Dafny, as much formal verification as practical, test-driven development, and agents for speed. Prefer compiled Dafny for performance-sensitive application logic. Use Go for MCP/database/OS integration and Python for extraction/model adapters where practical Dafny implementations are unavailable.

Prove the core's specified properties and test the adapters separately. Do not claim that filesystem access, parsers, models, or the entire service are formally verified. Development rules live in [AGENTS.md](../AGENTS.md); the core and TDD evidence live in [core/](../core/) and [core_tests/](../core_tests/).

## D004 — Use SQLite FTS5 and sqlite-vec

**Date:** 2026-10-11 · **Status:** Accepted

Use one SQLite database for metadata, chunks, keyword search, and vectors. Use sqlite-vec for vector operations rather than a separate database service. The Rust SQLite/vector project mentioned in discussion was not positively identified and was not selected.

This keeps deployment small. Exact vector scans trade simple, bounded operation for increasing search latency as the collection grows. Revisit indexing strategy after measuring real collection sizes and latency.

## D005 — Use free local embeddings by default

**Date:** 2026-10-11 · **Status:** Accepted for text; provisional for photos

Use BGE-small English embeddings for documents/code and CLIP vision/text embeddings for optional visual photo search. External AI is acceptable to the user, but paid APIs must not be necessary for ordinary search.

Initial downloads require internet access; cached models process content locally. Text inference passed on the NAS. Photo inference passed on the development computer only. Assistants may send returned excerpts to their own providers. Evidence: [NAS pilot](../docs/benchmarks/nas-pilot.json) and [development benchmark](../docs/benchmarks/development.json).

## D006 — Serialize extraction and model workers

**Date:** 2026-10-11 · **Status:** Provisional

Use serialized, short-lived Python workers with CPU inference and small batches. Avoid keeping text and photo models resident together on the 2 GB NAS.

This reduces concurrent memory demand but adds model-loading overhead. Keep photo embeddings disabled until their memory use and compatibility are tested on the NAS.

## D007 — Expose read-only, authenticated MCP tools

**Date:** 2026-10-11 · **Status:** Accepted

Expose Streamable HTTP tools: `search`, `browse`, `read_file`, and `index_status`. Require a static bearer token for MCP requests. Keep original files read-only and index/model data in a separate writable directory. The unauthenticated health endpoint reports process health only.

Target Claude Desktop, Claude CLI, Codex CLI, and ChatGPT Desktop where supported. Client and cloud connectivity require separate validation; server compatibility alone does not prove desktop integration. See [client setup](../docs/clients.md).

## D008 — Limit searchable sources and begin with a pilot

**Date:** 2026-10-11 · **Status:** Accepted

The authorized sources are `/volume1/Matthew` and `/volume1/homes/matt/Photos`. The running document pilot mounts only `Matthew/Documents/Berklee/Week 10`; its client-visible prefix is `Matthew/`. An empty pilot photo folder is mounted, and photo embeddings are disabled.

Expansion to the full authorized sources remains pending. Skip unsupported formats, hidden/dependency/build directories, symlinks, known Time Machine folders, and oversized files. All-format search is an aspiration, not current coverage. See [coverage](../README.md#coverage-and-limits).

## D009 — Scan periodically and replace changed index entries

**Date:** 2026-10-11 · **Status:** Accepted

Scan at startup and every five minutes. Use content fingerprints to detect changes and replace per-file index entries transactionally. Remove deleted files after a successful walk, and invalidate stale indexed content when a changed file fails extraction or exceeds the size limit.

Search reflects completed scans rather than immediate filesystem changes. Filesystem watchers and a whole-library scalability benchmark are deferred.

## D010 — Preserve work Tailscale and allow direct LAN access

**Date:** 2026-10-11 · **Status:** Accepted

The user chose to retain the computer's work Tailscale account and exit node. Enable persistent local-LAN access rather than switching accounts. This restored direct access to the NAS at `192.168.1.4`.

The NAS also has a personal Tailscale address, `100.112.12.118`. Remote cross-account access and ChatGPT cloud connectivity are not configured. Do not expose the pilot's HTTP port publicly. See [deployment](../docs/deployment.md).

## D011 — Use Synology-supported container limits

**Date:** 2026-10-11 · **Status:** Accepted

Limit aggregate container memory to 1 GiB with no additional swap allowance. An attempted Docker CFS CPU quota failed on the NAS kernel; use CPU affinity to CPU 0 instead. The kernel also discarded the process-count limit, so remove that unsupported option.

The pilot verified memory and CPU-affinity enforcement. Its measured peak is evidence for that workload only. Retain a read-only root filesystem, read-only sources, dropped capabilities, and no-new-privileges. See [compose.yaml](../compose.yaml) and [NAS evidence](../docs/benchmarks/nas-pilot.json).

## D012 — Verify and publish through GitHub CI

**Date:** 2026-10-11 · **Status:** Accepted

GitHub CI verifies Dafny, regenerates compiled output, rejects stale generated code, runs Go race/integration and Python tests, and builds the container. Successful main-branch verification permits publishing the image to GHCR. Pin Dafny and Python dependencies; keep generated Go available for container builds without Dafny.

The user explicitly waived commit signing for this repository. See [workflow](../.github/workflows/verify.yml).

## D013 — Report progress and preserve measurement evidence

**Date:** 2026-10-11 · **Status:** Accepted

The user requested progress percentages and ETAs, then challenged unsupported-looking README numbers. State the scope and uncertainty of estimates. Distinguish development measurements from NAS measurements and save reproducible benchmark evidence where possible.

Do not describe estimated completion percentages as measured facts, or extrapolate pilot resource use into whole-library guarantees. See [benchmark evidence](../docs/benchmarks/).

## D014 — Install MCP clients without shell token exports

**Date:** 2026-10-11 · **Status:** Accepted

Provide separate installers for clients and NAS deployment. Register a private, local stdio bridge for Codex, Claude Code, and Claude Desktop. Keep bearer tokens in protected files rather than command arguments or desktop environment assumptions. Preserve existing configuration, make replacements explicit, retain backups, and test rollback. The bridge serves this stateless NAS service, not every MCP streaming capability.

## D015 — Read complete documents, retrieve originals, and index scans with OCR

**Date:** 2026-10-11 · **Status:** Accepted; new features await NAS rollout

The user requires entire document contents, including scanned PDFs. Add bounded `read_document` continuation batches and `fetch_file` byte batches with source-version checks. Retain the 32 MiB source limit, distinguish OCR text from original bytes, and make client attachment limitations explicit.

Use free English Tesseract OCR with bounded Poppler page rendering when PDFs have no embedded text, both for indexing and reading. Keep OCR serialized and expose missing-text metadata/errors. The existing index extraction cap stays bounded; full-document reading bypasses it using pagination. Relevance cutoff calibration remains in [TODO.md](../TODO.md), rather than assigning an arbitrary threshold to rank-fusion scores.

## Open decisions

- When to expand the pilot to the full authorized sources, based on indexing time and search latency.
- Whether CLIP photo inference fits comfortably on this NAS alongside its existing services.
- How to extend OCR languages, unsupported formats, archives, and backups within the resource budget.
- Which remote-access and ChatGPT connectivity route to validate.
- Whether scale measurements justify a different vector index or persistent model-worker strategy.
