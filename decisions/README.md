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

**Status:** Superseded by D040; original behavior deployed, successful browser upload unvalidated

Offer an optional `upload_document` MCP tool that creates a short-lived, single-use browser upload page. Require a fresh OAuth browser sign-in, then an explicit file selection and upload click. Accept one PDF, TXT, Markdown, or DOCX file up to 32 MiB. Store it only in a dedicated `Documents/Uploads` directory mounted writable; keep all existing source mounts read-only. Queue targeted indexing after publication, and let the periodic full scan recover when targeted indexing cannot run. Keep the feature disabled by default and require private deployment settings to identify the verified folder and enable it.

This gives MCP users a controlled path to add documents without asking an assistant to handle the file or a password. It adds a writable location inside the authorized Documents tree and requires browser reachability of the OAuth issuer. ChatGPT's documented file handoff provides a temporary `download_url` without a guaranteed host, so server-side fetching would introduce an unbounded SSRF target; the owner chose the browser page for both ChatGPT and Claude pending a separate design. A bounded queue indexes new files without waiting for a full paced walk. A queue overflow, extraction error, or canceled scan can leave indexing until a later full pass. Implementation evidence is in `internal/upload` tests, `cmd/nas-search/upload_page_test.go`, `cmd/nas-search/upload_integration_test.go`, `internal/app/upload_index_test.go`, and `tests_install/test_nas_installer.py`; live NAS permissions, browser sign-in, and indexing freshness still require deployment validation. Upload storage, HTTP handling, filesystem checks, and index adapters are Go code outside Dafny verification; only the separately specified core properties are proved.

The NAS now advertises the upload tool, but Claude grouped it under read-only tools because its annotation described the link-creation call rather than the write flow it starts. Classify it as a non-destructive write action so clients can require approval, while retaining fresh browser sign-in and an explicit upload click. The annotation regression failed before this change and passed afterward. Keep per-call approval as the default for sensitive documents; batch approval is a separate product decision.

## D038 — List direct child folders from authorized roots

**Status:** Accepted locally; NAS deployment behavior pending

Provide a read-only `list_folders` tool for live folder navigation. Each call lists only direct child folders and returns at most 100 results; bound both elapsed time and entries examined. Show empty directories. Default to deny access unless the requested path is within an owner-authorized root, and omit hidden folders, excluded folders, and symlinks. Keep `find_files` for query-based matching of live file paths and `browse` for indexed files.

Folder navigation helps users discover sources and empty locations without pretending that the content index is a complete directory listing. Live filesystem access avoids waiting for indexing, while direct-child and resource limits keep each request focused on a small amount of work. Root confinement and filtering preserve the existing source-access boundary. Tests first failed because the application method and MCP tool did not exist, then passed with the new behavior. The focused and full Go suites and installer Python tests pass locally. The tool implementation, its limits, and path checks are filesystem adapters outside Dafny verification; target-NAS behavior remains unvalidated.

## D039 — Preserve browser origin and independent sign-in sessions

**Status:** Accepted locally; live Safari upload unvalidated

Set the upload form's referrer policy to `strict-origin`. Under the [Fetch standard](https://fetch.spec.whatwg.org/#append-a-request-origin-header), the prior `no-referrer` policy makes ordinary form POSTs send `Origin: null`, while the upload handler correctly requires the issuer origin. `strict-origin` sends only the site origin, not the private upload URL. Keep rejecting missing, null, or foreign origins and keep the browser session, CSRF, size, type, and single-use checks. Give each OAuth login page a cookie name derived from its random session, so another login page cannot overwrite or delete that page's cookie. Keep the same five-minute expiry and exact cookie/session comparison.

This restores ordinary browser uploads without accepting cross-site posts and makes concurrent connector or browser sign-ins independent. The upload header and interleaved-login regressions failed before the changes and passed afterward. HTTP header and cookie handling remain unverified Go adapters; live Safari submission and target-NAS deployment still need confirmation.

## D040 — Accept any regular file in browser uploads

**Status:** Accepted, locally tested, and user-reported live browser upload

Allow one regular file of any type, including photos and extensionless files, up to 32 MiB in the dedicated `Documents/Uploads` directory. Keep the existing short-lived single-use link, fresh OAuth browser sign-in, explicit upload click, and no-overwrite behavior. Retain content validation for PDF, TXT, Markdown, and DOCX, including rejection of malformed files in those formats. Other types can be stored and found by filename, but content indexing remains limited to supported formats. Photo visual indexing continues to follow the existing photo-search setting. This replaces D037's upload type allowlist while preserving its access and publication safeguards.

This lets users add originals that the content index cannot process and makes them discoverable by filename without implying that every file can be searched by contents. Existing scanner validation protects supported document formats, while the size limit and isolated writable folder bound the upload path. The new file-type test failed before implementation and passed afterward; the browser flow also accepts a photo in local tests. A duplicate-name regression failed before the clearer conflict response and passed afterward. The owner subsequently reported a successful live file save. Upload storage, type checks, filesystem access, and indexing are Go adapters outside Dafny verification. D041 adds an optional broader placement mode; this fixed-folder mode remains the default.

## D041 — Offer indexed-source upload destinations through an isolated writer

**Status:** Provisional; local tests passed, NAS deployment unvalidated

The owner wants the assistant to choose an upload destination inside any folder already available through the configured searchable sources. Define “indexed destination” as an existing, authorized folder under a source root, including empty folders; the index stores files rather than a complete folder inventory. The assistant supplies a logical relative folder path from `browse_folders`. The MCP tool binds that path to a short-lived grant, and the fresh OAuth browser page displays the destination before the owner chooses a file and clicks Upload. The submitted form cannot change the destination. Recheck authorization at submission and in a separate writer process. An allowlisted folder's navigation ancestors do not grant upload placement. Denied, hidden, excluded, missing, and symlink folders remain unavailable. Publishing a file must not replace an existing name; moving and deleting files remain out of scope.

Keep the search/extraction container's source mounts read-only. An optional small writer container receives writable mounts only for the configured source roots and listens on a private Unix socket with no network interface. This broadens write exposure compared with D040's one-folder mount. A compromised writer could alter originals within its writable mounts despite the append-only upload API; this residual risk and the owner-confirmed destination justify keeping the mode opt-in. The container UID must have write permission in a chosen destination; the installer will not change source-folder permissions. Existing fixed-folder uploads remain available when this mode is off. Go filesystem and protocol adapters are tested but not formally verified; Dafny's relative-path predicate covers lexical validity only.

The destination-authorization test first failed because the method was absent, then passed. Writer policy, one-active-upload, and health tests also failed before their behavior was implemented, then passed. Installer tests first exposed the missing isolated-writer mount and overlapping-source preflight checks, then passed. The changed-package Go race suite, full installer suite, and Dafny build passed locally. CI and NAS behavior remain unvalidated.

## D042 — Recover host dependencies after reboot and network loss

**Status:** Accepted; adapter tests and required deployment checks apply

Container restart policies do not cover a late tunnel device or a VPN that becomes
healthy after the initial dependency wait. Provide a root boot and scheduled
recovery check for the explicitly identified VPN/Tailscale stack, search service,
upgrade worker and optional Photos broker. Restore only the standard tunnel
device, let the VPN perform its own connection retries and start its dependent
Tailscale service after health succeeds. Preserve account state, unrelated services,
images, credentials and original data. Limit automatic container interventions to
three per subsystem per rolling hour. A healthy service is not restarted.

Use protected root installations, exact project/service identities and independent
subsystem checks. Search recovery and upgrades share a deployment lock inherited
by the installer child; a crashed parent does not release exclusion while its
installer remains alive. Identify workers by root credentials, exact arguments
and process start identity before maintenance. Do not use persistent status as a
lock. Keep partial setup diagnostics for inspection. Daemon liveness is distinct
from backend readiness; scheduled checks do not establish client egress or prove
reboot/outage recovery without an authorized target-device test. OS/process/Docker
adapters remain tested, not formally proved. See [recovery](../docs/nas-recovery.md).

## D043 — Reuse Synology Photos People albums through a restricted host broker

**Status:** Accepted and locally implemented; combined CI and deployment validation pending

Prefer existing Synology Photos names, person assignments and thumbnails for
named-person lookup, avoiding a second recognition model on the 2 GB NAS. Retain
descriptive CLIP search as a separate optional feature and preserve its current
setting. Add optional MCP people lookup, person-assigned photo/video metadata and
small JPEG previews. Do not expose album mutations, database access, original
paths or public sharing links.

Go communicates over a private Unix HTTP socket; the container mounts only its
run directory read-only. The isolated upload writer does not receive this socket.
The root broker accepts only fixed read operations, typed bounded IDs and paging,
and a local account frozen from private app settings. Requests cannot choose an
account, API, method, command, path or credential. Account identity and physical
paths stay in private deployment settings. A new account is not assumed to inherit
another user's personal People library. Shared Space is outside this first scope.

Require an explicit whole Photos grant and reject any denied Photos subtree.
Compiled Dafny policy proves grant/deny requirements, denial monotonicity and
integer bounds. The host independently rereads root-private scope settings before
data requests, including after revocation. Native results must explicitly contain
the requested person. Preview reads repeat that membership check, require typed
matching photo/unit IDs and ready metadata, and return only bounded JPEG data.
Bound image dimensions before decoding and re-encode previews without embedded
metadata before returning them to MCP clients.
Face-group cover identifiers are not photo identifiers. Saved face assignments
may be incomplete or wrong; assistants must not infer identity from the preview.

Native probes establish personal People listing, explicit photo membership and
JPEG thumbnail delivery on Photos 1.9.1. The root CLI can emit successful CGI
content followed by wrapper error117; accept only the observed framed combination
and reject ambiguous/HTML responses. Temporary preview bytes stay private and are
removed after the response. Setup coordinates with upgrades, preserves environment
and source/index state, checks authenticated MCP access, and attempts prior-state
rollback. Reboot, resource use and final client display remain deployment checks.

Tests first reproduced missing tools, unsafe bounds, redirects/oversized replies,
denied-scope broker access, incorrect person membership, malformed previews,
wrapper handling, and bool/float identity aliases. Tests also cover actual Unix
HTTP, setup rollback and worker resume. Independent review found no critical
issue in those boundaries. The proof does not cover mutexes, Unicode matching,
JSON/HTTP, peer credentials, native permissions, processes, Synology responses or
JPEG decoding; those remain tested adapters. Original image data and credentials
are not test fixtures. Combined-main verification follows separately. See
[Photos setup](../docs/synology-photos.md).

Combined-current-main local verification passes 332 Python tests plus two
subtests. Eight real-Compose contracts await CI. Dafny rebuild verifies 48 combined
obligations with zero errors and executable tests; the Go race suite covers the
existing upload/approval/OAuth behavior alongside Photos. A native preview probe
confirmed bounded JPEG delivery. Final installed MCP access and client display
remain deployment checks.

## Open decisions

- When measured indexing time and latency justify expanding to larger source collections.
- Whether photo inference fits beside other workloads on the target device.
- Which additional formats, OCR languages, archives, and backups fit the resource budget.
- Which remote-access and ChatGPT cloud-connectivity routes to validate.
- Whether collection scale justifies a different vector index.


## D044 — Require owner opt-in when Photos setup widens access

Status: accepted.

The first NAS setup stopped before deployment because the existing allowlist did
not satisfy the full Photos policy. The native People API cannot enforce our
photo subfolder exclusions. Setup therefore keeps its default refusal and adds
`--grant-photos-access` as an explicit owner request to allow the whole Personal
Photos library. The flag preserves every existing denial and all other grants;
a Photos root or subtree denial still blocks setup. This extends D043 without
weakening its request-time policy.

The candidate grant becomes visible before the host readiness probe, whose
request-time policy reads the private settings. A failure before container
installation restores the original settings without restarting the healthy
service. Later failures use the existing image and settings rollback.

Evidence: three meaningful opt-in success tests first failed with the original
full-scope refusal, then passed after implementation. The helper tests also
cover denied subfolders, default refusal, dry runs, raw settings preservation,
readiness policy, and early and late rollback. The OS/settings adapter is tested,
not formally proved; the existing compiled Dafny grant/deny policy is unchanged.


## D045 — Support bounded registration for fresh OAuth MCP clients

Status: accepted; live ChatGPT reconnect validation pending.

A fresh MCP SDK client reproduced the connection failure: protected MCP returned
401, resource and authorization discovery returned 200, then `/register` returned
404 before the password page. Saved connections could continue using the
pre-registered client. The Photos upgrade did not change the OAuth handler, and
the NAS comparison confirmed its saved login settings were preserved. This
evidence identifies a fresh-client interoperability gap; it does not prove which
request caused the ChatGPT UI spinner.

Add advertised public-client registration for supported callback destinations,
with exact per-client callback binding, owner password consent, S256 PKCE and
`files:read`. Preserve existing pre-registered clients and isolate first-party
browser actions. Registration itself grants no file access and performs no
remote metadata fetch. Request size, callback count, pending lifetime, rate and
registry capacity are bounded; owner-authorized registrations persist until revocation so token expiry does
not break a saved connection. Unapproved registrations expire after fifteen
minutes. Anonymous requests can still temporarily exhaust
new-client capacity. Revoking all access removes registrations too.

The compiled Dafny policy proves required validation conditions and capacity
bounds. URL parsing, HTTP, SQLite transactions, cryptography and client UI remain
tested adapters outside the proof. A real Python MCP SDK regression failed at
registration with the old implementation before the fix was written, then passed
registration, password consent, PKCE exchange and authenticated access after the
fix. Validation: full Go race tests, 347 Python tests plus two subtests, and 54
combined Dafny verification obligations passed; eight local Compose checks were
skipped for CI. Independent review also checked wrong-password handling, client
and callback binding, browser-action isolation, old-database revocation and
reauthentication after all tokens expire.

## D046 — Reduce indexing and search cost on the DS220+

Status: provisional; measured in development only, NAS measurements pending.

A review of the hot paths found that each five-minute scan copied and hashed
every eligible file even when nothing changed, that each indexed file paid an
interpreter start plus an embedding-model reload because extraction killed the
resident worker, that the semantic query re-parsed the query vector from JSON
for every stored vector and joined every row before ranking, and that the
container was pinned to one of the two Celeron J4025 cores. An approximate
vector index was considered and rejected: an exact scan of a few hundred
thousand 384-float vectors fits this device, and the cost was in the query shape,
not the algorithm. D004 stands.

Decisions:

- **Skip unchanged files by size and modification time.** Stamps live in their own
  `stamps` table (superseded: a `stamp` column on `files`, see D047). A file whose
  stamp and photo flavour match the stored row is skipped without a copy or hash;
  otherwise the SHA-256 path runs as before and the stamp is refreshed. Files
  without a stamp are hashed once. This trusts size plus mtime as a change
  signal, which a same-size, same-mtime rewrite would defeat; `NAS_SCAN_FILE_DELAY_SECONDS`
  stays at its default until scan contention is measured again. The skip rule and
  the photo fingerprint flavour are Dafny functions (`CanSkipUnchanged`,
  `IndexedFingerprint`) with a proved property: skipping agrees with what the
  hash path would decide for the same bytes.
- **Extract through the resident worker.** When model keepalive is on, `extract`
  and `read_document` reuse the warm Python process, and the text model stays
  loaded. The worker reports its peak RSS and is recycled above
  `NAS_WORKER_MAX_RSS_MIB` (default 600), checked only after extraction replies so
  a photo run keeps CLIP resident and the first extraction afterwards recycles the
  process once. Embedding batches are passed to the model whole.
- **Run the vector query as a two-stage KNN.** Vectors are stored and queried as
  float32 blobs, the vec0 KNN path ranks on the vector table alone, and only the
  top rows are joined to `chunks` and prefix-filtered. New tables use the cosine
  metric; older tables keep L2 and the store converts the distance, which is
  identical for normalised embeddings. A narrow prefix in a large index can
  return fewer than the requested semantic hits. Prefix filters use index ranges,
  new FTS5 tables are external-content, and search checks file existence only for
  the ranked page. Existing keyword tables keep their format (superseded: an
  on-open rebuild, see D047).
- **Use both cores with low CPU shares.** Compose defaults to `NAS_CPUSET=0-1`
  and `NAS_CPU_SHARES=512`; DSM keeps priority under contention. `NAS_CPUSET=0`
  restores single-core pinning. Existing `.env` files keep their value.

Apart from the two core functions above, this is adapter code outside the Dafny
core. Evidence: 58 verification obligations and the executable core tests pass; new Go tests for stamp skipping, warm-session extraction, KNN
ordering and legacy migrations, new installer and compose tests, and query plans
showing the `files` primary key and chunk rowid lookups in use. Nothing here has
been timed on the NAS; measure scan duration, search latency and DSM
responsiveness before treating the gains as real.

## D047 — Keep the index usable by the previous release

Status: accepted.

The first NAS upgrade to the D046 build failed and rolled back. The installer log is
discarded by design, so the cause is inferred, not observed. On open, that build
rebuilt the whole keyword index and added a `stamp` column before the HTTP listener
started. The installer allowed about fifteen seconds for health before rolling back.
A rebuild of 100,000 passages took 1.5 seconds on a fast development server. A
J4025 with spinning disks and a larger index can plausibly exceed the window.

The rollback exposed a second defect. The installer restores the previous program
but not the database. The previous release writes file rows without naming columns,
so an added column makes every one of its re-index writes fail. A test reproduced
this before the fix.

Decisions:

- **Schema changes stay readable and writable by the previous release.** Add tables,
  never columns, and never rewrite an existing table on startup. Stamps move to a
  separate `stamps` table, which the previous release ignores. Existing keyword tables
  keep their format. Only new databases get the external-content table. Both formats
  work for both releases.
- **The installer waits about two minutes for the new service to answer** before
  rolling back, instead of three short tries.

Consequences: existing installs keep the larger keyword table until the index is
rebuilt in a new data directory. A database left half-migrated by the failed upgrade
still works with this release.

Evidence: new store tests run the previous release's own writes against fresh,
previous-release and half-migrated databases. The first failed before the fix with
"table files has 5 columns but 4 values were supplied". A new installer test shows a
start that answers on the twenty-first health check now succeeds. It failed before
the wait was extended. Go, Python and Dafny checks pass.

## D048 — Add shared memory, a private editor, passkeys and Jev review

Status: provisional; built and tested in development, not yet tried on the NAS, in a
real browser, with a real passkey, or against the live Jev API.

Claude web, desktop and mobile, ChatGPT and local CLIs should share project context:
what happened, where work stands, and what the owner has decided. The design is in
[shared-memory design](../docs/shared-memory-design.md); its section 0 lists the
choices below.

Decisions:

- **Journal first.** Journal entries are append-only history. Checkpoints are versioned
  snapshots of where a project stands. Memories are curated statements derived from
  entries. A memory is current only when accepted.
- **Chat-first capture.** The assistant writes when asked, and opens context at the start
  of a conversation because the owner's Claude instructions say so. No client hooks.
- **Separate container.** `nas-memory` owns `memory.sqlite3` on its own mount and has no
  access to searchable folders. The search process reaches it through a private socket
  in `<index>/memory-ipc`, as the upload writer does. Memory is off by default.
- **Assistants never confirm.** Anything an assistant writes is unverified, proposed or a
  candidate. Assistants may only dispute a memory, may not supersede an entry the owner
  confirmed, and may not write checkpoint entries through the journal. Accepting,
  rejecting, retracting and forgetting happen only in the editor. The `memory_forget`
  tool returns a link to the item in the editor and erases nothing. (Superseded during
  review: forgetting from a chat through a browser approval page on the public connector
  and directly on the LAN connector. Injected text could have driven both, so they were
  removed.)
- **The socket never trusts its caller.** The search container also runs document
  extractors, so a compromised extractor could reach the memory socket. The socket treats
  every caller as an assistant, ignores claims to be the owner, and refuses to forget.
  The editor reaches the store in-process, inside the memory container.
- **Private editor.** The editor is served only on the tailnet through `tailscale serve`,
  bound to `127.0.0.1` on the NAS, and the memory container sits on its own Compose
  network so the search container cannot reach it. Tailscale's login header identifies
  the owner, so there is no editor password; requests that arrive through Funnel are
  refused. Any process on the NAS host that can reach `127.0.0.1:8083` could present the
  header, so the host itself is trusted. POSTs need an exact Origin match. It installs to
  a phone's home screen, keeps unsent notes on the device, and keeps at most 30 recently
  viewed pages for offline reading, for up to 7 days.
- **Passkeys on the public side.** The public OAuth sign-in asks for a passkey after the
  password. `NAS_OAUTH_PASSKEYS` defaults to `enroll`: sign-in offers enrollment until the
  first passkey exists, then always asks for one. `required` forces enrollment; `off`
  disables passkeys. An IP-address issuer defaults to `off` because passkeys need a host
  name. Recovery is the host-only `reset-passkeys` command. Until the first passkey is
  registered, anyone with the password can register one, so register promptly. After a
  rollback to an earlier release, sign-in returns to password only, because that release
  ignores the passkey tables.
- **Jev gates, it does not extract.** TypeSafe's Jev scores whether an entry records
  something the owner decided or confirmed, and suggests a category. Scores at or above
  `NAS_MEMORY_ACCEPT_AT` are accepted, at or below `NAS_MEMORY_REJECT_AT` rejected, and
  the rest wait for review. Unscored candidates always wait. Only entries in
  `NAS_JEV_SCOPES` are sent (`none` sends nothing), and the owner's own entries never
  are. While Jev is unavailable, entries that need it wait and everything else proceeds.
- **No embeddings yet.** Memory and journal search are full-text. Vector search is
  deferred until measured need.

Verified core: scope visibility and boundaries, the memory status set and lifecycle
(terminal states are final), assistant limits (dispute only, no confirming, no
superseding the owner's confirmations, no checkpoint entries), socket methods (never
forget), checkpoint versions, context packing (bounded, ordered, everything kept when it
fits, the first item kept whenever it fits) and budget bounds, which project-less
memories join a context, review routing (unscored goes to review), the
external-classification gate (the owner's entries never leave), forget orphaning,
corrections, passkey modes, defaults, steps and removal (a registered passkey is always
asked for outside `off`; `required` keeps one), passkey sign counts and capacity, and
editor access. Packing does not prove the greedy choice is optimal beyond those
properties.

Accepted adapter-level rules, recorded rather than proved: the priority order of a
context package and its 14-day window for recent entries are written in the store's
queries; the editor's "edit and accept" proposes the edited memory and then rejects the
original in two steps, so a failure between them leaves both visible for the owner to
resolve; secret redaction is a pattern list that keeps commit SHAs, digests, URLs and
paths but can miss unusual secrets. Unverified adapters: SQLite storage and FTS,
the socket client and server, MCP tool handling, the editor's HTTP and JavaScript, the
WebAuthn library and ceremonies, the Jev HTTP client, secret redaction, input-format
checks, Compose and the installer.

Schema: `memory.sqlite3` is new; every table has an `extra` JSON column so later fields
need no new columns. Checkpoints are keyed by scope, project and version, and retry keys
are unique per scope, so one scope cannot detect or block another's entries. `oauth.sqlite3` gains two tables only (`oauth_passkeys`,
`oauth_passkey_flows`); the previous release ignores them.

Dependency: `github.com/go-webauthn/webauthn` v0.18.0, pinned because later releases
need Go 1.26 and the image builds with Go 1.25.

Delivery: the verified rules and the shared contract were written first; six parallel
agents built the store and socket, the MCP tools and forget approval, the editor,
passkeys, the Jev client and consolidation, and deployment, each test-first within its
own files. Decision rules the agents left inline were moved into the core during
integration. Integration wiring was written before its tests: end-to-end tests run two
assistants against the real store over the real socket.

Review: three independent reviews checked security, correctness and these project rules.
They found an assistant could un-confirm the owner's entries; the socket trusted callers'
claims to be the owner; forgetting from a chat could be driven by injected text; small
cross-scope leaks through checkpoints, retry keys and forgetting; redaction erased commit
SHAs, URLs and paths; a reject threshold of 0 was silently ignored; tool limits disagreed
with the store; consolidation stalled while Jev was down; the IP-issuer passkey default
never ran under Compose; and several decisions were inline. Each was reproduced with a
failing test and fixed by three parallel agents, with the new rules added to the core
first. Validation after fixes: 93 Dafny obligations and the executable core tests, all Go
packages with `sqlite_fts5`, the race detector on the memory, socket, consolidation,
editor and OAuth packages, and 420 Python tests (the bridge test that also fails on main
excepted). A final review confirmed each finding fixed and found three more,
fixed test-first where testable: an assistant's dispute with suggested wording was always
refused (the wording now travels in the reason), saving a checkpoint without a scope used
the default scope instead of the project's, and the editor's outbox retried refused notes
forever (now only network failures, timeouts and rate limits retry; this change has no
browser test). The socket no longer contains forget handlers at all.

Limits: no automatic capture of chats; keyword search only; nothing measured on the NAS;
the editor's offline behaviour and passkey pages are untested in real browsers.
