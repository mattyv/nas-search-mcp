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

The authorized sources are `/volume1/shared` and `/volume1/shared-photos`. The running document pilot mounts only `Documents/Documents/Course/Week 10`; its client-visible prefix is `Documents/`. An empty pilot photo folder is mounted, and photo embeddings are disabled.

Expansion to the full authorized sources remains pending. Skip unsupported formats, hidden/dependency/build directories, symlinks, known Time Machine folders, and oversized files. All-format search is an aspiration, not current coverage. See [coverage](../README.md#coverage-and-limits).

## D009 — Scan periodically and replace changed index entries

**Date:** 2026-10-11 · **Status:** Accepted

Scan at startup and every five minutes. Use content fingerprints to detect changes and replace per-file index entries transactionally. Remove deleted files after a successful walk, and invalidate stale indexed content when a changed file fails extraction or exceeds the size limit.

Search reflects completed scans rather than immediate filesystem changes. Filesystem watchers and a whole-library scalability benchmark are deferred.

## D010 — Preserve work Tailscale and allow direct LAN access

**Date:** 2026-10-11 · **Status:** Accepted

The user chose to retain the computer's work Tailscale account and exit node. Enable persistent local-LAN access rather than switching accounts. This restored direct access to the NAS at `192.0.2.10`.

The NAS also has a personal Tailscale address, `100.64.0.10`. Remote cross-account access and ChatGPT cloud connectivity are not configured. Do not expose the pilot's HTTP port publicly. See [deployment](../docs/deployment.md).

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

**Date:** 2026-10-11 · **Status:** Accepted; document reading, original download, and OCR tested on the NAS

The user requires entire document contents, including scanned PDFs. Add bounded `read_document` continuation batches and `fetch_file` byte batches with source-version checks. Retain the 32 MiB source limit, distinguish OCR text from original bytes, and make client attachment limitations explicit.

Use free English Tesseract OCR with bounded Poppler page rendering when PDFs have no embedded text, both for indexing and reading. Keep OCR serialized and expose missing-text metadata/errors. The existing index extraction cap stays bounded; full-document reading bypasses it using pagination. Relevance cutoff calibration remains in [TODO.md](../TODO.md), rather than assigning an arbitrary threshold to rank-fusion scores.

## D016 — Hide obsolete browse paths and reject blocking file types

**Date:** 2026-10-11 · **Status:** Accepted; local regression tests pass, NAS rollout pending

Filter browse results through root-confined file opens, so paths retained from a previous mount no longer appear as readable files. Bound the candidate query to keep browsing inexpensive; many obsolete entries can temporarily shorten the result list until scanning reconciles the index. Reject negative `read_file` byte limits consistently with the other reading tools.

Open files without blocking before checking that they are regular files. This prevents a file replaced by a named pipe from hanging a browse or read request on the Linux NAS. Regression tests reproduced the failures before these fixes. Python extraction still needs the separate concurrent-replacement protection recorded in [TODO.md](../TODO.md).

## D017 — Retry unusable PDF text layers with OCR

**Date:** 2026-10-11 · **Status:** Accepted; local tests and the Melody PDF validation pass, NAS rollout pending

Add `force_ocr` to PDF document reading so a caller can bypass a broken embedded text layer. Require OCR to be enabled and keep the option unchanged across continuation calls. Mark suspicious glyph-heavy source text and automatically try OCR for those pages when enabled, both for indexing and reading. Preserve embedded text when automatic OCR provides no useful replacement.

The text-quality check is a conservative heuristic, not a guarantee of readable content. OCR can recover printed titles and labels; interpreting sheet music requires visual analysis. Parser and OCR behavior remain tested adapters outside the Dafny proof boundary. Updating extraction behavior refreshes the index and can prolong the initial scan.

Expose the current scan file, processing stage, and start times through `index_status`. Counts only increase after a complete file is committed; a long extraction or multiple embedding batches can leave them unchanged while work continues. These diagnostics do not estimate overall completion or distinguish active processing from waiting for the shared worker.

## D018 — Save originals through a client-local MCP tool

**Date:** 2026-10-12 · **Status:** Accepted; local download and installation validated

The user needs the assistant to download NAS files and store them locally. Expose `download_file` through the installed stdio bridge, reusing the original-byte downloader rather than sending base64 through the model. Save on the bridge host, require an absolute destination in an existing folder, and refuse overwrites. Keep private temporary files, atomic publication, source-version checks, bearer authentication, and the 32 MiB source bound.

The NAS remains read-only. Native HTTP clients retain `fetch_file` and the command-line helper; a NAS server cannot write to a client's disk. Installed bridges need reinstalling and clients need restarting. Local writes use the bridge user's filesystem permissions and require an explicit tool call; they are not confined to the NAS mounts. Filesystem and protocol adapters are tested Python outside the Dafny proof boundary; this change adds no verified application policy. Desktop conversation and remote-host behavior require separate validation.

Evidence: six new tests failed before implementation; nine downloader tests passed afterward. All four client installer runs passed the live NAS handshake. The installed bridge downloaded the 18,312-byte Melody PDF, verified private permissions, and rejected overwriting it; the temporary copy was removed. Dafny rebuild could not run because the local Dafny executable is absent; no Dafny sources or generated output changed. Desktop conversations remain untested.

## Open decisions

**2026-10-12 filename lookup:** Add `find_files` against the live authorized mounts. Some scanned identity documents were already indexed with OCR metadata, but content search missed them. Do not infer file absence from semantic search or extraction success. Match literal query terms against case-normalized relative filenames and folder names, using a verified Dafny matcher. Keep filesystem traversal and Unicode case normalization as tested Go adapters. Bound traversal time, visited entries, and results, and report partial searches. Include regular files whose content is unsupported or failed extraction; retain hidden-folder, symlink, and excluded-folder restrictions.

**2026-10-11 Desktop installation repair:** Claude Desktop's configuration had an empty `mcpServers` object. Restoring `nas-search` passed the live installer connection check; the cause of the missing entry is unknown. Detect an existing Linux config under `XDG_CONFIG_HOME` or `~/.config` automatically, preserve other settings, and print the destination so users can check it. Require `--config` when no known Linux config exists. This simplifies installation and repair; it does not prevent another application from changing the file.

**2026-10-11 ChatGPT Desktop installation:** Add `chatgpt-desktop` as an explicit installer client using the supported Codex CLI and shared config, including its backups and replacement checks. [Official documentation](https://learn.chatgpt.com/docs/extend/mcp) confirms sharing for the same local Codex host. The installed local entry is enabled; conversation use still needs checking after restart. Cloud Work uses a separate configuration and is outside this installer.

**2026-10-11 collection update:** The user selected Documents and Code together. The NAS now mounts `/volume1/shared-documents` as `Documents/` and `/volume1/shared-code` as `Code/`. The first scan is still running. Avoid mounting the complete Documents share alongside the separate Code mount, which would index code twice. Paths retained from the narrower pilot are obsolete; the new browse check hides inaccessible entries while the successful scan reconciles the index. A document formerly called `Documents/Melody.pdf` is now `Documents/Course/Week 10/Melody.pdf`.

- When to expand the pilot to the full authorized sources, based on indexing time and search latency.
- Whether CLIP photo inference fits comfortably on this NAS alongside its existing services.
- How to extend OCR languages, unsupported formats, archives, and backups within the resource budget.
- Which remote-access and ChatGPT connectivity route to validate.
- Whether scale measurements justify a different vector index or persistent model-worker strategy.
