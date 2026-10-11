# Project requirements

## Development

- Record material technical and product decisions in `decisions/README.md`, including reasons, status, consequences, and evidence. Preserve superseded choices and distinguish accepted choices from unvalidated assumptions.
- Use test-driven development for behavior changes: write a meaningful failing test, implement the behavior, and run the relevant tests. Record red-to-green evidence when practical. Do not claim TDD for tests written after implementation.
- Write application logic in Dafny wherever practical. Use as much formal verification as possible: specify preconditions, postconditions, invariants, termination, and relevant safety properties.
- Prefer compiled Dafny for performance-sensitive logic. Keep handwritten Go and Python focused on protocol, operating-system, database, extraction, and model adapters that Dafny cannot practically implement.
- Do not bypass verification with assumptions, axioms, or disabled checks. Explain the boundary between proved properties and unverified adapters; never describe the entire system as formally verified.
- Regenerate compiled Dafny output after source changes. Run `scripts/build-core.sh`, relevant Go tests with `sqlite_fts5`, and relevant Python tests before declaring work complete. Run the required CI checks before release.
- Use parallel agents for independent work when this speeds delivery. Give each agent clear file ownership and contracts. Integrate and review their work, including tests and proof results.

## Product and deployment

- Target a Synology DS220+ with an Intel Celeron J4025 and 2 GB RAM. Require no hardware upgrades and no second machine to run the service.
- Deliver a container for Synology Container Manager. Keep memory and CPU bounded; measure actual NAS compatibility and performance before promising either.
- Support read-only discovery, search, and reading of documents, code, and photos. Mount only authorized sources: the `Documents` share and `shared-photos`. Verify their physical paths at deployment.
- Use free local embedding models by default. External AI is acceptable when explicitly configured, but must not be required for ordinary local search. Document model downloads, unsupported formats, OCR gaps, and indexing freshness.
- Provide MCP access for Claude Desktop, Claude CLI, and Codex CLI. Document actual ChatGPT connectivity requirements and limitations.
- Preserve the user's work Tailscale connection. Do not switch accounts or exit nodes without authorization. Local LAN access is enabled and should remain enabled. Remote access through a free personal Tailscale setup is acceptable.
- Keep original files read-only, authenticate MCP requests, confine file access to mounted roots, and keep secrets out of source control.

## Communication

- Give concise progress updates during active work, at least once per minute when practical.
- Include an estimated completion percentage and remaining ETA in each substantive progress update. State the scope being measured, such as the first deployable version or NAS deployment.
- Estimates are approximate. Revise them when evidence changes, and identify dependencies or blockers instead of counting blocked work as complete.
- Report what works, what remains, verification evidence, and material limitations in the final handoff.
