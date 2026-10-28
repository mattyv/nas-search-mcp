# Shared LLM Memory and Journal

**Design proposal — v0.2**  
**Date:** 28 October 2026 (v0.2 records the choices made before implementation; see section 0)  
**Target:** ChatGPT, Claude, Codex and other MCP-capable clients  
**Existing foundation:** [`mattyv/nas-search-mcp`](https://github.com/mattyv/nas-search-mcp) on a Synology DS220+

## 0. Choices made for the first implementation

These override the sections below where they differ. The decision log (D048) records the reasons.

- **Chat-first capture.** The main clients are Claude web, desktop and mobile. The assistant writes when the owner asks, and calls `context_open` at the start of a conversation because the owner's Claude instructions say so. Client hooks are not built.
- **Separate memory container.** The memory service runs in its own container with its own database and no access to the searchable folders. The search process reaches it through a private socket, as the upload writer already does.
- **Editor on Tailscale only.** The owner's editor is a web app served only on the private tailnet through `tailscale serve`. Tailscale identifies the owner, so the editor has no password. It installs to a phone's home screen and keeps drafts while offline.
- **Passkeys on the public side.** The public OAuth sign-in used by Claude and ChatGPT gains passkeys as a second factor, enrolled on first sign-in. Forgetting a memory from a chat needs a fresh browser sign-in and approval, like service upgrades.
- **Jev as a gate, not an extractor.** Jev is TypeSafe's hosted decision model, called with the owner's API key. It returns calibrated probabilities for typed questions and generates no text. It scores whether a journal entry records something the owner decided or confirmed. Confident scores are accepted or rejected automatically and the rest wait in the editor's review queue. Only entries in configured scopes are sent, and the owner's own entries are never sent. Jev cannot run on the DS220+.
- **Verified rules.** Scope visibility, the memory status lifecycle, assistant limits, checkpoint versions, context packing, review routing, passkey policy and editor access are Dafny functions with proofs.
- **No embeddings yet.** Memory and journal search use full-text search. Vector search over memory is deferred until measured need.

## 1. Executive summary

Build a **shared, journal-first context service** that allows different LLM clients to remember useful information, record progress, and resume work performed in another client.

The system is *not* intended to synchronise entire chat windows or give every model an indiscriminate copy of all prior conversations. Instead, it preserves a chronological record of meaningful events and derives three more useful views:

1. **Memory:** facts, preferences, accepted decisions and reusable knowledge.
2. **Current state:** active projects, tasks, blockers and next actions.
3. **Knowledge graph:** entities and relationships, if graph retrieval proves useful.

The **journal is the durable record of what happened**. Memory and state are maintained projections. A knowledge graph is optional and derived, not the starting point.

**Recommended MVP:** extend the existing Go MCP codebase with a separately permissioned journal/memory module and a separate SQLite database. Reuse FTS5, the existing BGE-small embedding pipeline, and sqlite-vec. Use external inference selectively for extraction and consolidation. Do not introduce PostgreSQL, Neo4j or a full GraphRAG framework until testing demonstrates a need.

## 2. Goals and non-goals

### Goals

- Continue work across ChatGPT, Claude, Codex and desktop coding assistants without manually reconstructing the context.
- Maintain a readable personal/work journal: **what happened, when, why, and what comes next**.
- Retrieve prior decisions with their rationale and original supporting evidence.
- Distinguish **proposals, accepted decisions, completed work, unverified claims and superseded information**.
- Retain context at personal, project and session scopes without leaking unrelated or confidential information.
- Remain usable on a constrained Synology DS220+; allow cloud migration later without redesigning the MCP API.
- Permit explicit correction, retraction, deletion and rebuilding of derived representations.

### Non-goals for v1

- Automatic access to all native chat histories: MCP alone does not provide this.
- Full-fidelity replay of a conversation across different model providers.
- Automatic acceptance of every LLM-generated assertion as truth.
- A heavyweight, continuously running local generative model.
- Comprehensive entity extraction and GraphRAG before shared handoff works.

## 3. User workflows

### A. Start or resume a project

**Example:** Open Codex and say, “Resume `nas-search-mcp` from where Claude left off.”

1. Client calls `context_open(project="nas-search-mcp", intent="resume")`.
2. Service selects the latest relevant checkpoint, accepted decisions, current tasks, recent journal events and supporting references.
3. It returns a **bounded handoff package**, not the whole history.
4. The client explicitly separates confirmed state from proposed or unverified work.

**Design consequence:** Maintain fast project-centric state snapshots alongside chronological history. Retrieval must be scoped and token-budgeted.

### B. Record progress during a session

**Example:** Claude Code fixes indexing contention and commits a change.

1. Client writes a journal event describing the work, motivation and outcome.
2. The event links to the repository, commit, test results and any relevant prior decision.
3. The entry remains historically available even if later work reverses the change.
4. A background processor proposes updates to current project state and long-term memory.

**Design consequence:** Append-oriented events, strong provenance, idempotent writes and distinct **reported** versus **verified** completion states.

### C. Explore ideas without promoting them to facts

**Example:** ChatGPT discusses replacing SQLite with PostgreSQL but makes no decision.

Record the discussion as an **exploration**. Do **not** change the current architectural decision merely because a model suggested something.

**Design consequence:** Each assertion needs a type and status. A model-generated proposal is not an accepted user decision.

### D. Produce a chronological journal

**Example:** “What did I work on last week, across all assistants?”

1. Search events by time range, scope and project.
2. Group related events into human-readable daily summaries.
3. Link each summary to its source events.
4. Preserve the difference between the *time an event happened* and *the time it was recorded*.

**Design consequence:** Time-series access is a first-class feature; semantic search alone is inadequate. Store UTC timestamps and display in a selected local timezone.

### E. Correct or retract memory

**Example:** “We decided against Jev for the first release.”

1. Create a new event recording the correction/decision.
2. Supersede any conflicting *current* memory or project-state records.
3. Preserve ordinary historical events to explain the change.
4. If the user explicitly requests erasure, delete the designated content and invalidate dependent indexes, caches and summaries according to the retention policy.

**Design consequence:** Historical supersession and explicit deletion are different operations.

### F. Save a manual journal note

The user can record a personal observation or project note **without an active LLM coding session**. The journal must not depend on a client deciding that a message is worth remembering.

## 4. Information model

The conceptual model is:

```text
Conversation / coding session / manual note / imported document
                         |
                         v
               Journal events + sources
                  (durable history)
                         |
          +--------------+---------------+
          |              |               |
          v              v               v
       Memories      Project state    Graph projection
    (what matters)  (current view)   (relationships)
          \              |               /
           +-------------+--------------+
                         |
                         v
             Scoped, evidence-backed
                 context packages
```

### 4.1 Journal events — what happened

Journal events are append-oriented and should support at least:

- `observation`, `discussion`, `exploration`, `decision`, `action`, `result`, `correction`, `checkpoint` and `manual_note`.
- Event ID and idempotency key.
- Actor/client, project, session and access scope.
- `occurred_at` and `recorded_at` timestamps.
- Human-readable title/summary and optional structured detail.
- Status (`proposed`, `confirmed`, `unverified`, `superseded`, etc.) where relevant.
- References to original sources, evidence and related event IDs.

Journal entries should be readable on their own. A terse vector or graph edge is not an adequate substitute.

### 4.2 Sources — where the information came from

Store stable references to conversations, messages, files, Git commits, PRs and documents when available. Preserve source type, URI/locator, content fingerprint, timestamps and visibility scope.

Prefer a **pointer plus a bounded evidence excerpt** to storing every full conversation by default. A source pointer may become inaccessible, so mark whether evidence is actually available rather than implying it can always be fetched.

### 4.3 Memories — what is currently believed

Memories are curated, mutable projections, each with:

- A canonical statement and memory category (`fact`, `preference`, `decision`, `constraint`, `lesson`).
- Scope, subject/project and validity interval where relevant.
- Evidence links to journal events or sources.
- Status (`candidate`, `accepted`, `disputed`, `superseded`, `retracted`).
- Extraction/model version and last verification time.

The confidence of an extraction model is **not** the same as evidence that the statement is true.

### 4.4 Checkpoints and current state — where work stands

A checkpoint is a resumable snapshot containing:

- Goal and current branch/commit if applicable.
- Completed and verified work.
- Accepted decisions and constraints.
- Blockers, unresolved questions and next actions.
- Source links and an optional parent checkpoint/version.

Maintain the latest accepted checkpoint per project, with older versions retained. Use version checks to prevent two clients from silently overwriting each other's state.

### 4.5 Optional knowledge graph — how things relate

Later, derive entities, aliases and source-backed relationships, e.g.:

```text
(nas-search-mcp) --uses--> (SQLite)
(nas-search-mcp) --embeds_with--> (BGE-small-en-v1.5)
(design decision #42) --supersedes--> (design proposal #17)
```

Store both the assertion and the original text that supports it. Avoid requiring a fixed predicate ontology at ingestion time; normalisation can happen progressively.

## 5. Example journal record

```json
{
  "event_id": "evt_01...",
  "event_type": "exploration",
  "title": "Shared-memory architecture options",
  "summary": "Considered journal-first storage and optional GraphRAG.",
  "project": "nas-search-mcp",
  "scope": "personal/projects/nas-search-mcp",
  "client": "chatgpt",
  "session_id": "session_01...",
  "occurred_at": "2026-10-28T11:00:00Z",
  "recorded_at": "2026-10-28T11:05:00Z",
  "assertion_status": "proposed",
  "source_ids": ["src_01..."],
  "next_actions": ["Prototype cross-client checkpoint retrieval"]
}
```

This example is illustrative; it is **not** a claim that a decision or event was already written to the NAS.

## 6. Capture and consolidation workflow

### Capture modes

| Mode | Trigger | Reliability | Recommendation |
|---|---|---|---|
| Explicit journal entry | User says “remember/record this” | High intent clarity | Required in MVP |
| Explicit handoff | User says “save checkpoint” | Good if invoked | Required in MVP |
| Client hook | Commit, tool event, session lifecycle | Client-specific | Add where supported |
| Periodic checkpoint | Scheduled or client-driven | Helps with interrupted sessions | Add later |
| Chat export/import | User-authorised archive | Broader historical coverage | Optional ingestion path |

**Critical constraint:** installing an MCP server does not mean ChatGPT, Claude or Codex will automatically send it all messages. Each client needs a supported write path, hooks/instructions or a separate import process. Design for missed session-end events.

### Consolidation

Perform extraction out of band:

1. Read new events and candidate supporting passages.
2. Propose memory entries, entity mentions, relationships and state changes.
3. Deduplicate against accepted memories and aliases.
4. Detect contradictions, uncertain claims and potential supersession.
5. Auto-apply only low-risk, policy-permitted updates; queue significant or ambiguous changes for confirmation.
6. Persist provenance and extraction version so projections can be rebuilt.

Jev, TypeSafe's hosted decision model, classifies *candidate* statements (e.g. proposed vs decided) and returns calibrated probabilities that can gate automatic acceptance. It cannot extract open-ended entities or relationships, so a generative extractor is still needed for that. Neither is an authority on what actually happened.

## 7. Retrieval and handoff design

A single vector search is insufficient. Use a query planner with several retrieval modes:

| Request | Retrieval strategy |
|---|---|
| “What did I do yesterday?” | Time-ordered journal query |
| “Resume project X” | Latest checkpoint + accepted decisions + recent events |
| “Why did we reject Y?” | Decision lookup + earlier proposals + evidence |
| “Where have we discussed Z?” | FTS5 + vector search + source links |
| “What depends on component A?” | Graph traversal, when graph projection exists |

`context_open` should apply access scope and a token budget *before* returning data. Suggested package structure:

```text
Project / goal
Current accepted state
Recent verified events
Key decisions and rationale
Open questions and next actions
Relevant sources and evidence
Uncertainties / conflicts / missing context
```

Do not silently collapse an unresolved dispute into one answer. Return conflicts explicitly.

## 8. Proposed MCP API

Keep tools task-oriented rather than exposing raw SQL:

| Tool | Purpose |
|---|---|
| `context_open` | Return bounded, scoped context for starting/resuming work |
| `journal_append` | Record a meaningful event or manual note |
| `journal_search` | Search chronological events and produce timelines |
| `checkpoint_save` | Save a versioned handoff/current-state snapshot |
| `memory_search` | Retrieve curated memory and evidence |
| `memory_correct` | Supersede, dispute or retract a memory |
| `memory_forget` | Explicitly erase selected memory and dependent projections |

Optional later tools: `entity_lookup`, `graph_traverse`, `consolidation_review`.

All writes should accept a client-generated idempotency key. Checkpoint updates should use optimistic concurrency, returning a conflict rather than silently discarding another client's work. Tool responses should include IDs and commit/confirmation outcomes; the assistant must not claim a write succeeded until it receives that response.

## 9. Technology stack for the DS220+

The existing repository targets an **Intel Celeron J4025 DS220+ with 2 GB RAM** and already contains:

- Go MCP transport and authentication.
- SQLite with FTS5 and `sqlite-vec`.
- BGE-small-en-v1.5 embeddings through FastEmbed/ONNX (384 dimensions).
- An optional CLIP image embedding path.
- A worker process for extraction and embedding.

### Recommended additions

| Layer | Choice | Reason |
|---|---|---|
| MCP/API | Existing Go implementation | Reuse authentication and client integration |
| Journal/memory storage | **Separate SQLite database** | Prevent document-index rebuilds from destroying memories |
| Chronological search | Indexed time, project and scope fields | Efficient journal queries without inference |
| Text search | FTS5 | Already available |
| Semantic search | Existing BGE-small + sqlite-vec | Reuse model and integration |
| Current-state materialisation | Go + SQLite | Cheap and deterministic |
| Extraction/consolidation | Asynchronous external model/service | Avoid CPU/RAM contention on the NAS |
| Graph storage (later) | SQLite relationship tables + recursive CTEs | Adequate for a small personal graph |
| Deployment | Existing Docker Compose, isolated memory writes | Low operational overhead |

Keep the original document/file-search permissions and read-only source mounts intact. Memory writes should touch a dedicated writable data path, not arbitrary files on the NAS. Ideally, give the new memory API separate credentials or authorization scopes.

### Performance precautions

The main container is limited to **1 GB RAM**, uses both cores with low CPU priority, and serialises the embedding worker behind one warm process. Vector search uses sqlite-vec's nearest-neighbour query before joining passages (decision D046). The memory container is limited separately and runs no Python or models.

Prioritise in this order:

1. Benchmark interactive search and memory operations at idle and during indexing.
2. Separate background ingestion from latency-sensitive reads.
3. Index journal and graph fields with ordinary SQLite B-tree indexes.
4. Cache repeated query embeddings if measurements justify it.
5. Consider alternative vector indexes or hosting only after measuring a real bottleneck.

Do **not** run a local generative extractor on the 2 GB DS220+ as part of the request path.

### Hosting boundary

The API should not assume that the database must forever live on the NAS. If remote connectivity or performance is unsatisfactory, move the service/storage to a personal cloud account (e.g. Cloud Run with an appropriate managed data store) while retaining the MCP contract and export format.

## 10. Security, privacy and trust

Shared memory is likely to contain material from several trust domains. Adopt these rules from the start:

- **Scopes:** `personal`, named projects and `work/<organisation>`; deny cross-scope retrieval by default.
- **Per-client rights:** distinguish reader, journal writer and memory administrator. A coding agent should not automatically have broad delete rights.
- **Provenance:** assistant-generated statements are attributed to the assistant, not automatically to the user.
- **Untrusted inputs:** retrieved journal/document text is evidence, never authority to execute instructions or change permissions.
- **Secret filtering:** exclude credentials and high-risk tokens from memory and logs.
- **Retention:** support explicit export, correction and deletion; invalidate embeddings, derived graph edges, cached summaries and backups according to documented policy.
- **Transport:** authenticated HTTPS for remote MCP; do not expose SQLite or unauthenticated admin endpoints.
- **Auditing:** record who changed curated memory, when, why, and which source supported it.

## 11. MVP and implementation phases

### Phase 1 — Working continuity (highest priority)

Build journal entry storage, manual `journal_append`, `journal_search`, `checkpoint_save` and `context_open`. Use SQLite and deterministic retrieval. Do not add a graph extractor yet.

**Acceptance test:** complete a task in Claude, save a checkpoint, then open Codex or ChatGPT and resume accurately without re-explaining the task. Verify that the returned context distinguishes completed work, plans and open issues.

### Phase 2 — Automatic useful capture

Add supported client hooks, event deduplication, source references, periodic/manual checkpoints and a daily journal view. Ensure abrupt session endings do not corrupt the timeline.

**Acceptance test:** generate an accurate chronological report of a week's activity across multiple clients, with navigable evidence.

### Phase 3 — Curated long-term memory

Add extraction/consolidation, memory candidates, duplicate detection, conflict handling, correction and retraction. Evaluate Jev as a classifier only if it improves accuracy or cost against simpler alternatives.

**Acceptance test:** a superseded decision is retained in history but not returned as the currently accepted decision.

### Phase 4 — Graph-enhanced retrieval

Derive entities and source-backed edges from journal/events. Test one- and two-hop graph traversal before adopting Graphiti, LightRAG or another framework. Use the same BGE model for relevant text search.

**Acceptance test:** graph retrieval improves answers to multi-hop questions over the current FTS5 + vector baseline, with acceptable indexing time and memory use.

## 12. Evaluation metrics

Track at least:

- **Cross-client resumption accuracy:** whether the receiving client correctly identifies goal, completed work, constraints and next action.
- **Unsupported-memory rate:** fraction of returned claims lacking accessible supporting evidence.
- **State freshness:** delay between a confirmed event and its appearance in resumed context.
- **Recall quality:** relevance of retrieved memories and journal events for a representative test set.
- **Correction behaviour:** whether superseded/retracted information stops appearing as current truth.
- **Performance:** median/p95 context-open latency, peak NAS memory, CPU usage and indexing interference.
- **Capture coverage:** fraction of meaningful sessions with at least one recoverable journal event or checkpoint.
- **Privacy:** no cross-scope disclosure in adversarial access tests.

Use the same evaluation corpus to compare baseline vector/FTS retrieval against any later graph or Jev augmentation.

## 13. Open design questions

1. Which clients should support automatic capture first: Claude Code, Codex, Claude Desktop or ChatGPT? Their hook/tool behaviours differ.
2. Should the journal store original conversation excerpts, source links only, or opt-in full transcripts?
3. Which changes can be applied automatically, and which require explicit user confirmation?
4. Should a journal entry be considered authoritative only when human-attested, or may verified tool evidence also confirm actions?
5. What are the default scope boundaries between personal projects and work-confidential material?
6. Will the NAS meet remote latency and uptime requirements, or should the service eventually move to Google Cloud?
7. Does graph retrieval materially outperform hybrid search for the actual questions the user asks?

## 14. Recommended immediate next step

Build the smallest cross-client demo without GraphRAG:

1. Add a separate `memory.sqlite3` and a small journal/checkpoint schema.
2. Implement `journal_append`, `checkpoint_save` and `context_open` in the existing Go repo.
3. Configure one writing client and one different reading client.
4. Run a realistic **Claude → ChatGPT/Codex handoff** with a deliberately unfinished task.
5. Measure correctness, latency, provenance and the number of manual steps required.

Only then decide whether automatic extraction, Jev or a dedicated GraphRAG framework actually solves the next observed problem.

---

**Governing principle:** **Preserve what happened, maintain what is currently believed, and supply only the context needed for the next task.**
