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

Expose Streamable HTTP tools and require authentication for MCP requests. The original pilot used a static bearer token. Keep original files read-only and index/model data in a separate writable directory. The unauthenticated health endpoint reports process health only. D021 adds a separate OAuth route for Claude web/mobile; the private static-token route remains available to installed local clients.

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

The NAS also has a personal Tailscale address, `100.112.12.118`. Remote cross-account access and ChatGPT cloud connectivity are not configured. The original pilot HTTP listener remains private; D021 records a separate OAuth listener that may be exposed only after the documented checks and explicit approval. See [deployment](../docs/deployment.md).

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

## D019 — Validate visual search in an isolated NAS pilot

**Date:** 2026-10-12 · **Status:** Accepted pilot approach; NAS fit and search quality unvalidated

The user requested image search. Reuse existing local CLIP image and text embeddings, photo metadata, and `search` with `photos=true`. Start a separate photo-only pilot on port 8081 with its own data directory, a selected read-only source folder, one CPU, and a 1 GiB memory limit without extra swap. Preserve the running document service, its index, credentials, and source configuration. Do not turn on photos in the existing index: its pipeline signature requires a fresh index when this setting changes.

Measure container memory, model failures, indexing time, and descriptive search quality on the DS220+ before enabling the full library. Models download on first use, then inference runs locally. The existing development smoke test is evidence that the models run on that development machine; it does not establish NAS compatibility or retrieval quality. Initial scope includes supported image formats, not face recognition, image OCR, or HEIC/RAW conversion. The pilot launcher is an unproved operating-system/container adapter; existing Dafny search policy remains unchanged. Require at least 1280 MiB available host memory before starting the separate 1 GiB pilot and disable automatic restarts, so model failures do not create a restart loop. Available memory can still change after preflight; monitor DSM during the run.

Evidence: the first two launcher tests failed because the script did not exist; all seven pilot tests then passed, including resource/security options, source confinement, credential handling, dry-run, and failure paths. Shell syntax validation passed. These use a fake Docker adapter and establish launcher behavior, not NAS compatibility. The published image e3ae38e passed full Linux CI before this pilot work. Local Dafny is absent and local Go linking fails against the installed macOS SDK, so fresh proof/Go verification remains assigned to CI.

## D020 — Retain embedding models for a configurable idle period

**Date:** 2026-10-14 · **Status:** Accepted; local tests and Dafny regeneration pass, NAS rollout and measurements pending

Retain a serialized embedding worker for five idle minutes by default, configurable through `NAS_MODEL_KEEPALIVE_MINUTES`; accept fractional minutes and use `0` to disable reuse. Repeated searches previously paid model loading costs on every request. Restart the idle countdown after embedding requests, release the worker on idle expiry and service shutdown, and terminate it when a request is cancelled or its protocol fails. Do not replay failed requests automatically.

Retain only one model in the worker. Release it before switching model types; stop the worker before document extraction so a warm model does not overlap extraction or OCR memory. Indexing can renew the timeout or interrupt model reuse. Keep the existing container memory and CPU limits. This supersedes the short-lived embedding-worker choice while preserving one-shot extraction and the zero-timeout fallback.

Red-to-green evidence: two Python tests first failed because model retention and the persistent protocol were absent, then passed after implementation. The Python suite passes 75 tests plus two subtests. The Go lifecycle tests first failed because retention support was absent; the complete Go suite passes with race detection and `sqlite_fts5`. `scripts/build-core.sh` verifies and regenerates the unchanged core without stale generated output.

The process lifecycle, timer, and model cache belong to Go/Python operating-system and model adapters outside the Dafny proof boundary. Do not claim that retention, runtime memory reclamation, or faster NAS searches are formally proved. NAS latency and resident-memory benefits remain unvalidated until measured on the DS220+.

## D023 — Request checked main upgrades through authenticated MCP

**Date:** 2026-10-15 · **Status:** Accepted implementation; NAS enablement and reboot recovery unvalidated

Expose optional `upgrade_from_main` and `upgrade_status` tools through the existing bearer and OAuth authentication boundaries. Require an explicit user upgrade request in the tool description. Enabling the feature grants the capability to every authenticated client, including existing OAuth grants; there is no separate administrator role. Disclose service upgrades in OAuth consent. Original document access stays read-only. The user specifically requested upgrade commands through MCP.

Keep Docker and root execution outside the search container. Publish one complete, exclusive fixed-action request into a separate root-controlled sticky queue. A host worker locks deployment, resolves the exact current main commit, checks successful push-main verification and publication jobs, fetches a bounded source archive, rejects unsafe archive entries, and invokes the existing pinned-image installer. Retain the request until terminal status is written. Wait for installer completion so slow Docker operations cannot outlive a falsely released upgrade lock; stalled installations require operator inspection. Preserve existing secrets, mounts, index and models. Startup rollback covers image/configuration, not database schemas.

One-time sudo setup creates `/volume1/@nas-search-upgrader`, rather than trusting the existing user-writable deployment tree. Protect all ancestors and check DSM ACLs at deployment. Copy private settings, install trusted worker code, and mount only its request queue into the service. A root DSM boot task is required for reboot recovery. The worker itself does not self-update. This adds Python/Go operating-system, filesystem and protocol adapters; no new application policy requires Dafny, and these adapters are not formally verified.

Evidence: initial queue tests failed because the queue was absent; the MCP test then failed with an unknown upgrade tool. Both passed after implementation, including concurrent exclusive publication and rejection of unauthenticated calls. Host and setup tests began with missing-file failures and passed after implementation; further review added archive bounds, CI publication, privacy, permission disclosure and failure handling checks. Verification: current-main Dafny rebuild passed 37 obligations with zero errors and executable core tests; generated output remains unchanged. All Go tests pass with race detection and `sqlite_fts5`. Python worker and installer tests pass 125 tests plus two subtests. OAuth upgrade and consent checks pass. Docker is unavailable locally, so the feature image build and CI publication remain pending. Latest main b332152 was installed on the NAS by the user through the staged sudo helper; its installer reported HTTP health and authenticated index_status passed. The new MCP upgrade feature still requires publication and protected host setup.

## Open decisions

**2026-10-12 filename lookup:** Add `find_files` against the live authorized mounts. The children's passport scans were already indexed with OCR metadata, but content search missed them. Do not infer file absence from semantic search or extraction success. Match literal query terms against case-normalized relative filenames and folder names, using a verified Dafny matcher. Keep filesystem traversal and Unicode case normalization as tested Go adapters. Bound traversal time, visited entries, and results, and report partial searches. Include regular files whose content is unsupported or failed extraction; retain hidden-folder, symlink, and excluded-folder restrictions.

**2026-10-11 Desktop installation repair:** Claude Desktop's configuration had an empty `mcpServers` object. Restoring `nas-search` passed the live installer connection check; the cause of the missing entry is unknown. Detect an existing Linux config under `XDG_CONFIG_HOME` or `~/.config` automatically, preserve other settings, and print the destination so users can check it. Require `--config` when no known Linux config exists. This simplifies installation and repair; it does not prevent another application from changing the file.

**2026-10-11 ChatGPT Desktop installation:** Add `chatgpt-desktop` as an explicit installer client using the supported Codex CLI and shared config, including its backups and replacement checks. [Official documentation](https://learn.chatgpt.com/docs/extend/mcp) confirms sharing for the same local Codex host. The installed local entry is enabled; conversation use still needs checking after restart. Cloud Work uses a separate configuration and is outside this installer.

**2026-10-11 collection update:** The user selected Documents and Code together. The NAS now mounts `/volume1/Matthew/Documents` as `Matthew/` and `/volume1/Matthew/Code` as `Code/`. The first scan is still running. Avoid mounting the complete Matthew share alongside the separate Code mount, which would index code twice. Paths retained from the narrower pilot are obsolete; the new browse check hides inaccessible entries while the successful scan reconciles the index. A document formerly called `Matthew/Melody.pdf` is now `Matthew/Berklee/Week 10/Melody.pdf`.

## D021 — Add a separate OAuth connector for Claude web and mobile

**Date:** 2026-10-12 · **Status:** Accepted; controlled NAS deployment, public endpoint checks, Claude web login/search, and mobile visibility passed; mobile tool calls remain unvalidated

Keep the existing static-bearer MCP listener private on `8080`, bound to host loopback and the existing NAS LAN IP. Add an OAuth-only listener on `8082` for Claude web/mobile, bound to host loopback in Docker. Use the pre-registered public client ID `nas-claude` with no default client secret, PKCE S256, bcrypt password verification, explicit login consent and CSRF protection, throttling, short one-use authorization codes, short access tokens, and rotating refresh tokens with replay revocation. Use a private SQLite token store and bounded pending-login, grant, body, response, and concurrency limits. Optional stdio remains available for local use.

Limit file access through `NAS_ALLOWED_FOLDERS`; an empty value denies access, and denied folders override allowed folders. Normalize selected paths under `/files`, reject symlinks throughout descriptor-based opens, and retain the 32 MiB bounded private temporary snapshot for reads. This reuses the existing Dafny relative-path validation; OAuth, filesystem, database, and transport adapters remain unproved Go code and require tests. The selected connector scope is the existing Documents and Code mounts plus the existing pilot photo mount, including sensitive personal documents; photo search stays disabled. OAuth authorization allows Claude to receive returned file contents.

The user selected this scope and authorized project deployment. The host has private Tailscale Serve forwarding for `8080`; Funnel currently forwards public `443` to `127.0.0.1:8082`. Confirm physical NAS mount paths and environment-file ownership before any future deployment. Ask before changing Funnel or Tailscale ACLs, restarting anything outside this project, or pushing to git. The detailed procedure and safety limits are in [the connector guide](../docs/oauth-connector.md).

For unattended SSH deployment, the NAS's existing sudo rule authorizes only an unrelated Nord helper. Use the fixed root-owned `scripts/nas-connector-admin.py` helper. Copy the reviewed helper into root-owned mode-`700` `/volume1/nas-search-connector`, then grant only its exact `deploy` and `cleanup` argument forms through a temporary `NOPASSWD` rule. The rule must include the operation because sudo matches the Python script path as an argument. This avoids repeated SSH sudo prompts while limiting root access to the helper's fixed operations; `cleanup` removes only that exact rule. Keep the secure project on the NAS data volume, outside a DSM shared folder; retain this checkout as the recovery source if DSM removes it. The controlled project-only deployment passed after an earlier attempt rolled back. A parallel diagnostic container was skipped because this 2 GB NAS had about 457 MB available and 1.3 GB swap in use; the controlled deploy uses startup checks and rolls back on failure.

The login password minimum is 12 bytes at the user's request, with no composition rules; bcrypt cost 12, one attempt per second, and a five-failure lockout remain. A short common phrase is easier to guess than a unique longer phrase, so the deployment guide recommends a unique phrase. This choice supersedes the original 16-byte minimum before NAS deployment. The boundary test failed at 12 bytes before the change and passed afterward.

Local evidence: Dafny core verification and executable tests passed without a source change; the Go race suite passed, including the complete OAuth login, tool listing, and search flow; Python adapter and installer tests passed. Official MCP Inspector v2.10.1 completed OAuth login, listed all seven tools, and searched a synthetic file through the local fixture. Folder denial, token-store privacy, consent, request bounds, and kill ordering have recorded behavioral red-to-green tests. Initial OAuth tests were written first, but initial failures were dependency/link errors rather than behavioral failures, so do not claim full TDD for that work. The Inspector flow has not been repeated against the NAS. Claude web login and search now work, and the connector appears in Claude mobile; a mobile tool call remains untested.

**2026-10-14 deployment and access evidence:** After the first installer attempt rolled back, a corrected glibc-linked binary (SHA-256 prefix `f2cdf4…`) passed an offline Docker build and isolated `vec0` startup smoke test. The controlled project-only NAS deploy then passed. Read-only checks confirmed private health `200`, anonymous loopback `/mcp` `401` with resource metadata, port `8082` bound only to `127.0.0.1`, and port `8080` bound to loopback and `192.168.1.4`. The host Tailscale was upgraded from 1.58.2 to 1.104.1 using the official signed package after DNS publication failed; tailnet UserID `7023865582684939` and the private `8080` Serve route remained intact. Funnel currently forwards public `443` to `127.0.0.1:8082`. From a public relay, `/mcp` returned `401` with `WWW-Authenticate`, OAuth metadata returned `200`, `/`, `/webman/`, and `/healthz` returned `404`, and public port `8080` timed out. DNS resolved to `103.84.155.153` and `103.84.155.217`. The temporary sudo rule was removed. These checks establish the observed endpoint responses, not Claude compatibility or NAS Inspector compatibility. The root-owned secure project retains `.env` with the password hash. Require explicit approval for future Funnel changes, ACL changes, pushes, and restarts outside this project.

**2026-10-14 login repair:** The first Claude web login reached the NAS consent page but its form returned `invalid_request`. The [Fetch specification](https://fetch.spec.whatwg.org/#append-a-request-origin-header) says `no-referrer` can make a form submission carry `Origin: null`, which the consent guard rejects. Serve only the valid login HTML with `Referrer-Policy: strict-origin`, which sends the origin without the OAuth query, and retain `no-referrer` on all other responses. Preserve the exact-origin, cookie, session, and password checks. The login-policy test failed before implementation and passed after it; the end-to-end local OAuth test also passed with an explicit issuer Origin. An offline NAS build and isolated startup passed; the project-only controlled deploy passed. Public `/authorize` now returns `200` with `strict-origin`, anonymous `/mcp` still returns `401`, and `/` returns `404`. A fresh Claude web sign-in then succeeded, confirming the browser issue was resolved. Funnel remains on and the temporary sudo rule and staging files are absent. The deployed helper still prints the obsolete message “Funnel remains off”; its local copy now says “Funnel state unchanged,” but that wording update was not deployed.

- When to expand the pilot to the full authorized sources, based on indexing time and search latency.
- Whether CLIP photo inference fits comfortably on this NAS alongside its existing services.
- How to extend OCR languages, unsupported formats, archives, and backups within the resource budget.
- Which remote-access and ChatGPT connectivity route to validate.
- Whether scale measurements justify a different vector index; measure the accepted persistent model-worker strategy on the NAS.

## D022 — Show originals through expiring download links

**Date:** 2026-10-15 · **Status:** Accepted; local tests and Dafny verification pass; NAS rollout and Claude web/mobile use unvalidated

The user reported that Claude could fetch a document's bytes but could not show the document to them. `fetch_file` returns base64 to the model; MCP clients do not turn that into a chat attachment, and Claude web and mobile cannot save it locally. Add a `download_link` tool that returns a short-lived URL. Claude posts it as a link; the user taps it and the browser opens or saves the original. This works in every client whose browser can reach the link origin, without client changes.

Each link is 32 random bytes, kept in memory only as a SHA-256 hash, bound to one path and its source version, and valid for `NAS_DOWNLOAD_LINK_MINUTES` (default 10, range 1–60). At most 200 links stay active. Serving reopens the file through the current folder policy, rejects changed files with `410`, and streams up to the 32 MiB source limit with range support, outside the 2 MiB buffered MCP response cap. Downloads have their own rate and concurrency bounds; logs omit the URL. Responses are `private, no-store`, `nosniff`, and `no-referrer`. Only PDF, JPEG, PNG, GIF, WebP, and plain text render inline; all other formats download as `application/octet-stream` under a sandbox policy, so HTML or SVG on the NAS cannot run script on the connector's origin.

Links use `NAS_DOWNLOAD_URL`, falling back to `NAS_OAUTH_ISSUER`; with neither, the tool is hidden. On the current deployment that means the public Funnel origin: anyone holding an unexpired link can open that file without logging in, and the link passes through Anthropic's service in the tool result, as file contents already do. Setting `NAS_DOWNLOAD_URL` to a private address keeps links off the public route at the cost of needing Tailscale on mobile. Restarting cancels all links. Alternatives rejected: MCP image or embedded-resource results (client support varies, PDFs are not shown to the user, and responses exceed the public cap); one-use links (browser PDF viewers issue several range requests).

Proof boundary: the Dafny core proves the inline allowlist excludes HTML, XHTML, SVG, XML, JavaScript, and unknown types, and defines link expiry as strictly before the expiry instant. Token generation, the link table, HTTP serving, rate limiting, filesystem access, and MIME detection are tested Go adapters outside the proof. MIME detection (file extension, else content sniffing, then normalization) decides what the verified allowlist sees, so it is the most security-relevant unproved step.

Red-to-green evidence: new Dafny executable tests failed to resolve before `ShowInline` and `LinkActive` existed. With empty Go stubs, all eight new Go tests failed on behavior (stub errors, accepted invalid base, missing tool, no rate limit), then passed after implementation. `scripts/build-core.sh` verified and regenerated the core; the Go race suite with `sqlite_fts5` and 104 Python tests pass.

**2026-10-15 review follow-up:** An independent review found no blockers. Fixed three issues. A handler panic leaked a download concurrency slot permanently; slots are now released on panic, in both the download and MCP gates. One shared rate limit let junk requests starve real links, and a PDF viewer's burst of range requests hit `429`. Requests for active links now get a separate, larger budget (20/s, burst 100), unknown links keep the strict budget, and full concurrency slots queue for up to 10 seconds instead of failing. `fetch_file` mentioned `download_link` even when links were disabled; the hint now appears only when the tool exists. Each fix had a test that failed on behavior with signature-only stubs, then passed. A reported markdown break on filenames with parentheses did not reproduce: Go's path escaping already encodes them; a regression test now covers it. Docs now mark Claude app use as unconfirmed, the private-address setup as untested, and note that `revoke-oauth` alone leaves links active until expiry.

## D024 — Accept Synology upgrade paths and resume interrupted setup

**Date:** 2026-10-15 · **Status:** Accepted fix; NAS recovery pending

The first live setup created the protected deployment snapshots, then failed because the installer rejected the `@` in its own default `/volume1/@nas-search-upgrader` path. Live HTTP health still passed and MCP discovery still showed the earlier seven tools. A regression reproduced the exact path rejection before the fix, then passed initial installation and a subsequent upgrade after allowing literal at signs. Other path restrictions remain in place.

Add explicit `--resume` for an existing protected setup. Require trusted ancestors and complete root-owned snapshots, worker and queue; preserve the target's token and configuration. Default setup continues to refuse an existing target. This avoids deleting saved deployment state and supports recovery after a failed attempt. These are tested shell/Python deployment adapters outside the Dafny proof boundary.

Evidence: eight resume tests failed before implementation and 19 bootstrap tests passed afterward. The complete Python suite passes 136 tests plus two subtests; the Go suite with race detection and sqlite_fts5 passes. Dafny rebuild verifies 37 obligations with zero errors and executable tests, with unchanged generated output. Live recovery is still pending.
