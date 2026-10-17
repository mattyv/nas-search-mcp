# Decisions log

This log records accepted choices, provisional choices, and their reasons. **Accepted** means chosen; **provisional** means chosen for a pilot and still subject to validation; **deferred** means not implemented. Acceptance does not imply that every requirement is complete.

When a material choice changes, add an entry that explains the context, decision, consequence, and available evidence. Preserve superseded choices and link them to their replacements. This public log records product and technical decisions, not private deployment details or operational history.

Evidence should point to reproducible tests, Dafny proofs, CI checks, or clearly labeled synthetic/development benchmarks. Measure the target device privately before making deployment or performance claims.

## D001 — Run the service on the target NAS

**Status:** Accepted

The target is a Synology DS220+ with an Intel Celeron J4025 and 2 GB RAM. The service, index, extraction, and embeddings should run on this device without hardware upgrades or a second machine. This constrains memory, concurrency, and model size; small-pilot results cannot establish full-library performance.

## D002 — Build a focused custom service

**Status:** Accepted

No evaluated product met the combined hardware, search, and MCP requirements. A focused implementation gives control over resource use and verification, with the consequence that the project owns ongoing maintenance.

## D003 — Use Dafny, TDD, and parallel work

**Status:** Accepted

Use compiled Dafny for application logic where practical, test behavior changes before implementation, and delegate independent tasks when useful. Use Go and Python for protocol, database, operating-system, extraction, and model adapters where Dafny is impractical. Prove the core's specified properties and test adapters separately; do not describe the entire system as formally verified. See [AGENTS.md](../AGENTS.md), [core](../core/), and [core tests](../core_tests/).

## D004 — Use SQLite FTS5 and sqlite-vec

**Status:** Accepted

Use one SQLite database for metadata, chunks, keyword search, and vectors. This keeps deployment small. Exact vector scans trade simplicity for increasing latency as collections grow; revisit the index after measuring real workloads.

## D005 — Use free local embeddings by default

**Status:** Accepted for text; provisional for photos

Use BGE-small English embeddings for documents/code and optional CLIP vision/text embeddings for photos. Paid APIs must not be required for ordinary search. Initial model downloads need internet access; cached models process content locally. Assistant providers may receive passages returned by search.

## D006 — Serialize extraction and model workers

**Status:** Provisional

Use serialized CPU workers and small batches to limit peak memory. Avoid keeping text and photo models resident together. This reduces concurrent memory demand but adds model-loading overhead; validate compatibility on the target hardware.

## D007 — Expose read-only, authenticated MCP tools

**Status:** Accepted

Require authentication for MCP requests, keep original files read-only, and store writable index/model data separately. A health endpoint reports process health only. Support local bearer-token clients and an optional separate OAuth listener. Client and cloud connectivity need validation independently of server compatibility. See [client setup](../docs/clients.md).

## D008 — Limit searchable sources and begin with a pilot

**Status:** Accepted

Mount only source folders authorized by the owner, verify their physical paths during deployment, and begin with a small pilot. Skip unsupported formats, hidden/dependency/build directories, symlinks, known backup folders, and oversized files. All-format search is an aspiration, not current coverage. See [coverage](../README.md#coverage-and-limits).

## D009 — Scan periodically and replace changed index entries

**Status:** Accepted

Scan at startup and periodically. Use content fingerprints to detect changes, replace per-file entries transactionally, and remove deleted files after a successful walk. Search reflects completed scans, not immediate filesystem changes; watchers and whole-library scalability testing are deferred.

## D010 — Preserve existing network access

**Status:** Accepted

Preserve the owner's existing work Tailscale connection, routing, and local-LAN access. Do not change accounts or exit nodes to make the service work. Configure remote access separately and test it without disrupting other services.

## D011 — Bound container resources

**Status:** Accepted

Configure a 1 GiB aggregate memory limit, no additional swap allowance, and one-CPU affinity for the target environment. Retain a read-only root filesystem, read-only source mounts, dropped capabilities, and no-new-privileges. These settings bound use but do not promise performance or guarantee that every model fits.

## D012 — Verify and publish through CI

**Status:** Accepted

CI verifies Dafny, checks generated code, runs Go and Python tests, and builds the container. Publish an image only after successful verification. Pin dependencies and retain generated Go so container builds do not require Dafny.

## D013 — Report progress and preserve measurement evidence

**Status:** Accepted

State the scope and uncertainty of estimates. Distinguish development measurements from target-device measurements, preserve reproducible evidence, and do not extrapolate pilot measurements into whole-library guarantees.

## D014 — Install clients without shell token exports

**Status:** Accepted

Provide client and NAS installers. Keep bearer tokens in protected files, preserve existing client settings, make replacements explicit, retain backups, and test rollback. The stdio bridge serves this stateless MCP service; it is not a general bridge for every MCP transport feature.

## D015 — Read complete documents and retrieve originals

**Status:** Accepted

Provide bounded `read_document` continuation batches and `fetch_file` byte batches with source-version checks. Retain the source-size limit and distinguish OCR text from original bytes. Use free English OCR for PDFs when needed. OCR is a parser adapter, not a guarantee of complete or accurate text. Client attachment support varies.

## D016 — Hide obsolete browse paths and reject blocking file types

**Status:** Accepted

Filter browse results through root-confined opens and reject files replaced by blocking special files before reading. Bound candidate queries. Tests cover the regressions; Python extraction still requires separate concurrent-replacement protections.

## D017 — Retry unusable PDF text layers with OCR

**Status:** Accepted

Allow callers to force OCR for PDFs and retry suspicious embedded text when OCR is enabled. Preserve embedded text if OCR gives no useful replacement. The quality check is a heuristic; OCR can recover printed words but does not interpret sheet music. Expose scan stage and current-file diagnostics without presenting them as a completion estimate.

## D018 — Save originals through a client-local tool

**Status:** Accepted

Expose `download_file` through the stdio bridge. Save on the bridge host, require an absolute path in an existing directory, refuse overwrites, and use private temporary files with atomic publication and source-version checks. This writes on the client computer; the NAS remains read-only and local writes use the bridge user's permissions.

## D019 — Isolate photo-search experiments

**Status:** Accepted pilot approach; target fit and search quality provisional

Reuse local CLIP models through an isolated photo-only pilot with its own data directory and read-only source. Preserve the document service and index. Measure memory, indexing time, model failures, and descriptive quality before expanding. Image search does not provide face recognition or photo OCR. Development smoke tests do not establish target-device compatibility.

## D020 — Retain embedding models for a configurable idle period

**Status:** Accepted

Retain a serialized worker for five idle minutes by default, configurable through `NAS_MODEL_KEEPALIVE_MINUTES`; zero disables reuse. Keep one model resident at a time and release it before switching model types or extracting documents. This can reduce repeated load time but may increase memory residency; target-device benefits remain unvalidated.

## D021 — Add a separate OAuth connector

**Status:** Accepted

Keep the bearer-token listener private and provide a separate OAuth listener for Claude web/mobile. Use PKCE, password verification, explicit consent, CSRF protection, bounded sessions/tokens/requests, and revocation. Limit file access to explicitly configured folders and reject symlinks. OAuth and filesystem adapters are tested Go code outside the Dafny proof boundary.

## D022 — Show originals through expiring links

**Status:** Accepted

Offer links only when a link origin is configured. Bind each random link to one path and source version, expire it, enforce size/rate/concurrency limits, and serve only safe inline MIME types. Links avoid sending full originals through the model but require careful browser and filesystem handling. The initial capability-link-only public access was superseded by D030.

## D023 — Request checked main upgrades through MCP

**Status:** Accepted; superseded in part by D030

Expose optional upgrade tools to authenticated clients. Keep Docker and root execution outside the search container; use a fixed-action queue and a protected host worker. The worker verifies the exact current commit before installing its pinned image. Enabling upgrades grants the capability to every authenticated client, so disclose it and keep it disabled when clients need search-only access. Rollback covers image/configuration, not database schemas.

## D024 — Resume interrupted protected setup

**Status:** Accepted

Create protected host state rather than trusting a user-writable deployment tree. Resume only when trusted ancestors, root-owned settings, worker, and queue are complete. Preserve saved configuration; default setup refuses an existing protected target. The host worker does not self-update and requires separate administrator maintenance.

## D025 — Preserve active connector settings during upgrades

**Status:** Accepted

Discover the active Compose directory when no source is specified, preserve explicit overrides, and verify OAuth metadata and anonymous MCP challenges when OAuth is configured. Provide a root-only repair path with a private backup and rollback. These Docker, filesystem, and HTTP operations are adapters outside the proof boundary.

## D026 — Preserve access restrictions and writable-mount separation

**Status:** Accepted

Keep empty/missing allowlists deny-all, authorize only deliberately selected roots, and require source roots to be disjoint from writable index and queue directories. Neutralize inherited environment overrides so the settings validated are the settings deployed. Preserve LAN access while configuring bind addresses.

## D027 — Isolate login failures and request budgets

**Status:** Accepted

Track password failures per bounded login session rather than globally. Separate anonymous OAuth admission from authenticated MCP budgets, validate credentials before charging the authenticated budget, and honor cancellation while waiting. This reduces one source of starvation but cannot guarantee availability under sustained resource attacks.

After iPhone reconnects still failed on the first call, a regression reproduced authenticated request bursts exhausting a shared protocol budget. The accepted fix charges only tool calls and invalid methods against a per-token 5/s, burst-60 budget; initialize, ping, tool discovery, and notifications are exempt. The user confirmed an iPhone status call and search worked without 429 after release. These rate and identity checks are tested Go adapters outside the Dafny proof boundary.

## D028 — Keep the host worker alive after malformed requests

**Status:** Accepted

Reject symlinks, special files, oversized requests, and invalid JSON without following targets or terminating the worker. Quarantine malformed directories opaquely rather than traversing untrusted contents. This improves resilience; a queue writer can still cause availability problems. Worker fixes require a separate administrator update.

Regression tests reproduced worker termination and unsafe cleanup before the fix. The current runner adds separate installed and target commits, timestamps, and a fixed failure reason; it rejects duplicate deployment settings before attempting an upgrade. Replacing the root-owned runner requires an idle queue, a checked candidate, a private backup, and restart verification. Its update tests failed before the two-second stability and safe-log checks and passed afterward. A published image alone does not update a running host worker.

## D029 — Enable photo search without rebuilding document content

**Status:** Accepted; target-device performance provisional

Track photo-vector state per image so changing `NAS_PHOTOS` re-indexes photos without reprocessing documents. Keep text-model and source-root changes subject to the existing index rules. Decode HEIC/HEIF with a small upright model input. Photo search remains optional because resource use and quality depend on the target workload.

The owner chose a full authorized photo source. Its path and scope live only in protected NAS settings; enabling the setting did not restart the service. A development benchmark found peak photo-indexing memory below the 1 GiB container limit and ranked the expected image first for 17 of 18 test queries. That does not establish indexing time or memory headroom for the full collection on the NAS. Earlier photo-only pilot remains available for a smaller measurement.

## D030 — Require owner sign-in for public links and upgrades

**Status:** Accepted

Public upgrade requests return a short-lived approval link. Fresh browser OAuth sign-in and an explicit approval click queue the fixed upgrade; the password never goes through model arguments. Public file links require separate browser OAuth sign-in scoped to each link. Browser grants use a distinct resource and are checked for revocation on every request. Private bearer clients retain their existing behavior. This supersedes D023's tool-description-only confirmation and D022's unauthenticated public links. The OAuth and browser handlers are tested Go adapters outside the Dafny proof boundary.

Browser integration tests cover authorization, target binding, revocation, forged approvals, CSRF, cross-origin requests, expiry, and replay. The Go race suite passes locally. Public NAS behavior remains unvalidated until this version is deployed.

## D031 — Keep deployment identity outside the public checkout

**Status:** Accepted; public history rewritten 2026-10-16

Use generic paths, folder labels, and network examples in tracked configuration, tests, and documentation. Keep actual source paths, public issuer, numeric user, and credentials in the root-only NAS settings. Preserve the existing logical folder label privately during the migration so the index does not need rebuilding. The owner chose to keep the repository public so credential-free verified upgrades remain available. Current checkout scans and installer tests support the new generic configuration; old public commits require a separately reviewed history rewrite. Rewriting Git history changes commit IDs and does not erase third-party caches or forks.

**2026-10-16 history rewrite:** With the owner's approval, every branch was rewritten to replace the Tailscale hostname, tailnet and public addresses, Tailscale user ID, physical source paths, personal folder label, DSM user ID, VPN handoff reference, a personal document anecdote and a course name with generic values, then force-pushed with leases on the previous commits. A scan of all rewritten commits and messages found none of those values; no credentials had been committed. The rewritten main differs from the previous main only in a renamed test folder; Dafny verification, the Go race suite and 207 Python tests pass on it. Limits: GitHub keeps the closed pull requests' original commits, which only GitHub Support can remove; the `v0.1.0` tag still points to an original commit because the session could not update tags; existing clones must be reset to the new history.

## D032 — Register only an explicit ChatGPT callback

**Status:** Provisional; live ChatGPT connection unvalidated

The ChatGPT plugin builder can use the existing pre-registered OAuth client, but its callback differs from Claude's. Accept one exact ChatGPT callback only when its URL is set in private deployment settings. Keep authorization-code PKCE and the existing protected-resource challenge; do not add open dynamic registration merely to satisfy the builder's DCR option. This adds a second trusted redirect to the same public client ID, so the owner must review that exact callback before deployment. Configuration and OAuth tests establish local behavior; live sign-in and tool calls remain to be checked.

## D033 — Measure file-fetch latency without logging file identity

**Status:** Provisional; diagnostic release not deployed

An active full-library scan caused high disk wait, and one public MCP request took about three minutes, but current logs do not identify its tool or slow step. Add timing for file open, stat, MIME detection, range read, and final stat without logging paths, content, versions, or credentials. This distinguishes filesystem delay from transport delay before changing scan pacing. The local regression test checks timing fields and log privacy; actual target-device latency remains unmeasured with this change.

## D034 — Reserve public MCP capacity for control calls

**Status:** Accepted locally; NAS deployment unvalidated

The public connector returned `work_busy` while two long requests occupied both admission slots; this blocked `upgrade_status` and `upgrade_from_main` even though neither needs the model worker. Keep the total active limit at two, with one slot for ordinary work and one for MCP setup, `index_status`, and upgrade calls. Classify only bounded, parsed JSON-RPC requests after OAuth and body-size checks. The control slot guarantees admission under ordinary-work saturation, not completion when the NAS disk or database is slow. A regression test holds normal work open and confirms those calls proceed; the previous implementation failed that test.

## D035 — Pace file checks during full scans

**Status:** Accepted locally; target-device latency unvalidated

Pause before each authorized, supported file is read during a scan, including unchanged files. The Synology container defaults to two seconds per file; a private setting can change it or set zero to disable pacing. This reduces bursts of source reads, snapshots, and hashing that coincide with slow connector responses on a 2 GB NAS. The wait respects cancellation and does not hold the model worker gate. It preserves already indexed files when a scan stops during a wait, and does not promote an incomplete extraction revision. At two seconds, 10,000 eligible files add about 5½ hours to a complete scan. The choice favors connector availability over freshness; only target-device measurement can establish whether it resolves the public HTTPS failures. The timer and filesystem behavior are operating-system adapters outside Dafny verification.

## D036 — Accept a bounded OAuth display-language hint

**Status:** Accepted locally; live ChatGPT sign-in unvalidated

ChatGPT may add the standard `ui_locales` authorization parameter. The strict OAuth parameter list previously rejected the request before login. Accept one value of at most 128 bytes and ignore it because the login page is English-only. Keep exact redirect, resource, client, scope, and PKCE checks, and reject unknown parameters. A ChatGPT-style authorization and token-flow test failed before the change and passed after it; live connector sign-in still needs confirmation. This OAuth adapter is outside Dafny verification.

## D037 — Upload documents through a fresh browser session

**Status:** Accepted locally; live NAS upload and indexing unvalidated

Offer an optional `upload_document` MCP tool that creates a short-lived, single-use browser upload page. Require a fresh OAuth browser sign-in, then an explicit file selection and upload click. Accept one PDF, TXT, Markdown, or DOCX file up to 32 MiB. Store it only in a dedicated `Documents/Uploads` directory mounted writable; keep all existing source mounts read-only. Queue targeted indexing after publication, and let the periodic full scan recover when targeted indexing cannot run. Keep the feature disabled by default and require private deployment settings to identify the verified folder and enable it.

This gives MCP users a controlled path to add documents without asking an assistant to handle the file or a password. It adds a writable location inside the authorized Documents tree and requires browser reachability of the OAuth issuer. ChatGPT's documented file handoff provides a temporary `download_url` without a guaranteed host, so server-side fetching would introduce an unbounded SSRF target; the owner chose the browser page for both ChatGPT and Claude pending a separate design. A bounded queue indexes new files without waiting for a full paced walk. A queue overflow, extraction error, or canceled scan can leave indexing until a later full pass. Implementation evidence is in `internal/upload` tests, `cmd/nas-search/upload_page_test.go`, `cmd/nas-search/upload_integration_test.go`, `internal/app/upload_index_test.go`, and `tests_install/test_nas_installer.py`; live NAS permissions, browser sign-in, and indexing freshness still require deployment validation. Upload storage, HTTP handling, filesystem checks, and index adapters are Go code outside Dafny verification; only the separately specified core properties are proved.

## Open decisions

- When measured indexing time and latency justify expanding to larger source collections.
- Whether photo inference fits beside other workloads on the target device.
- Which additional formats, OCR languages, archives, and backups fit the resource budget.
- Which remote-access and ChatGPT cloud-connectivity routes to validate.
- Whether collection scale justifies a different vector index.
