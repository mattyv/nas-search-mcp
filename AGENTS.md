# Project requirements

## Development

- Record material technical and product decisions in `decisions/README.md`, including reasons, status, consequences, and evidence. Preserve superseded choices and distinguish accepted choices from unvalidated assumptions.
- Use test-driven development for behavior changes: write a meaningful failing test, implement the behavior, and run the relevant tests. Record red-to-green evidence when practical. Do not claim TDD for tests written after implementation.
- Write application logic in Dafny wherever practical. Use as much formal verification as possible: specify preconditions, postconditions, invariants, termination, and relevant safety properties.
- Treat any pure decision rule as application logic, even when it appears mid-adapter: a comparison that decides whether to skip, replace, rank, or accept something belongs in `core/Core.dfy` with a proved property, bridged through `internal/policy`. Adapters call the core; they do not re-implement it inline in Go or Python.
- Prefer compiled Dafny for performance-sensitive logic. Keep handwritten Go and Python focused on protocol, operating-system, database, extraction, and model adapters that Dafny cannot practically implement.
- Do not bypass verification with assumptions, axioms, or disabled checks. Explain the boundary between proved properties and unverified adapters; never describe the entire system as formally verified.
- Regenerate compiled Dafny output after source changes. Run `scripts/build-core.sh`, relevant Go tests with `sqlite_fts5`, and relevant Python tests before declaring work complete. Run the required CI checks before release.
- Use parallel agents for independent work when this speeds delivery. Give each agent clear file ownership and contracts, and tell each one which decisions belong in Dafny. Delegated work defaults to adapter code, so when integrating, read each diff for inline decision logic and move it into the core before merging. Review tests and proof results as well as code.

## Product and deployment

- Target a Synology DS220+ with an Intel Celeron J4025 and 2 GB RAM. Require no hardware upgrades and no second machine to run the service.
- Deliver a container for Synology Container Manager. Keep memory and CPU bounded; measure actual NAS compatibility and performance before promising either.
- Support read-only discovery, search, and reading of documents, code, and photos. Mount only owner-authorized sources. Verify their physical paths at deployment; keep site-specific paths in private deployment settings.
- Use free local embedding models by default. External AI is acceptable when explicitly configured, but must not be required for ordinary local search. Document model downloads, unsupported formats, OCR gaps, and indexing freshness.
- Provide MCP access for Claude Desktop, Claude CLI, and Codex CLI. Document actual ChatGPT connectivity requirements and limitations.
- Preserve the user's work Tailscale connection. Do not switch accounts or exit nodes without authorization. Local LAN access is enabled and should remain enabled. Remote access through a free personal Tailscale setup is acceptable.
- Keep original files read-only, authenticate MCP requests, confine file access to mounted roots, and keep secrets out of source control.
- Keep the index database usable by the previous release, because a failed upgrade rolls back the program but not the database. Add tables rather than columns, and never rebuild or rewrite existing tables during startup.

## Communication

- Apply the `vibe-coding` skill throughout this project. The user describes desired behavior and tries the result; do not assume they read the code.
- Apply `writing-clearly-and-concisely` to documentation and other prose. Lead with the useful outcome, use active voice and concrete language, remove needless words, and keep instructions easy to follow.
- Explain outcomes, where to try them, and one clear next action. Keep code internals, architecture terminology, and lists of touched files out of routine updates unless explicitly requested.
- Make routine technical decisions independently. Ask only product questions the user can answer from using the system. Explain trade-offs in terms of cost, speed, access, and user-visible limitations.
- When something fails, describe what is broken for the user and what is being done to fix it. Do not use stack traces or code snippets as explanations; operational commands are appropriate when needed to try or deploy the result.
- Give concise progress updates during active work, at least once per minute when practical.
- Include an estimated completion percentage and remaining ETA in each substantive progress update. State the scope being measured, such as the first deployable version or NAS deployment.
- Estimates are approximate. Revise them when evidence changes, and identify dependencies or blockers instead of counting blocked work as complete.
- Report what works, what remains, verification evidence, and material limitations in the final handoff.
