# NAS Search MCP

Project choices and unresolved questions are recorded in the [decisions log](decisions/README.md).

Read-only keyword and semantic search for a Synology DS220+ with 2 GB RAM. The service, index, and free embedding models run on the NAS. Original files stay in their shared folders. No second computer or paid AI API is required for search.

**Status: running on the DS220+.** NAS checks passed for text indexing, hybrid search, authenticated MCP access, complete PDF text retrieval, original-file download, and a one-page scanned-PDF OCR fixture. Documents and Code are mounted separately. Initial indexing is still running; photo embeddings remain disabled. Client configurations are installed; GUI conversations still need testing. An indexed C++ source file also passed a text-read check. See [current NAS validation](docs/benchmarks/nas-update.json) and [initial pilot evidence](docs/benchmarks/nas-pilot.json).

## Install

**Connect an MCP client** from this checkout on your computer:

```sh
python3 scripts/install-mcp.py --client codex \
  --url http://192.0.2.10:8080/mcp \
  --token-file build/nas-deployment/.env
```

Replace `codex` with `claude-code`, `claude-desktop`, or `chatgpt-desktop`. ChatGPT Desktop uses the shared Codex config for local Codex connections; its cloud chats require a separate integration. On Linux, the installer detects an existing `Claude/claude_desktop_config.json` under `XDG_CONFIG_HOME`, or `~/.config` when unset. Use `--config` for another location. Fully quit and reopen Claude Desktop after installation. The token source is a private local file, not included in the repository; it can also be a token-only file. The installer preserves existing settings and keeps the token out of command arguments. See [client installation](docs/clients.md#install-the-mcp-connection).

**Install or upgrade the NAS service** from a complete checkout on the NAS:

```sh
sudo sh scripts/install-nas.sh \
  --image ghcr.io/mattyv/nas-search-mcp:COMMIT_SHA \
  --documents-path /volume1/shared-documents \
  --code-path /volume1/shared-code \
  --photos-path /volume1/shared-photos \
  --data-path /volume1/docker/nas-search/data \
  --bind-ip 192.0.2.10
```

Replace `COMMIT_SHA` with a published version whose CI passed; `latest` is deliberately rejected. Existing NAS settings take precedence, so edit `.env` explicitly when changing collection mounts. The installer checks permissions and kernel support, preserves credentials/index data, and checks the started service. See [NAS installation and upgrades](docs/deployment.md#scripted-installation-and-upgrades).

**Download an entire original document** to your computer:

```sh
python3 scripts/fetch-file.py --url http://192.0.2.10:8080/mcp \
  --token-file build/nas-deployment/.env \
  --path 'Documents/Course/Week 10/Melody.pdf' --output /tmp/nas-melody.pdf
```

The helper reassembles exact original bytes into a private local file. For extracted text, ask your assistant to use `read_document` and follow `next_offset` until complete. Scanned PDF pages use English OCR when enabled.

If a PDF's text layer contains unreadable glyphs, ask your assistant to retry `read_document` with `force_ocr: true`. Keep that setting on every continuation request. Forced OCR requires OCR to be enabled and applies only to PDFs. It can recover printed titles and labels, but does not interpret musical notation. Clearly suspicious text layers also trigger automatic OCR when enabled; metadata reports suspect pages and OCR use. This heuristic can miss poor text layers, and OCR can still return incomplete or unusable text.

## Use it

Ask your assistant: **“Use nas-search to check index_status, then find documents about …”** Results include source paths, page or section locations, and passages. Documents appear under `Documents/`; code appears under `Code/`.

Search returns ranked candidates, including possible unrelated matches. Scores describe ranking, not confidence or relevance probabilities. Ask your assistant to read the passages and source document before drawing conclusions. A calibrated relevance cutoff remains unfinished.

The seven MCP tools are `search`, `find_files`, `browse`, `read_file`, `read_document`, `fetch_file`, and `index_status`. Use `read_document` for extracted text and `fetch_file` for original bytes. Scans run at startup and every five minutes. During initial indexing, semantic search and document reading can wait behind the worker queue; status checks, filename lookup, and original-file retrieval avoid that queue.

Use `find_files` when you know a filename or folder name, or content search misses a document. It checks the live mounted files, including files that failed extraction or have unsupported content formats. Query words must all occur somewhere in the relative path, ignoring case; punctuation is literal, not a wildcard or regular expression. For example, query `passport` with prefix `Documents/Personal Documents/`. A warning or truncated result means the search stopped early; narrow the prefix before concluding a file is missing. Hidden files, symlinks, and excluded folders are omitted.

If indexed counts stop changing, check `index_status.current_file`, `scan_stage`, `file_started_at`, and `scan_started_at`. A file appears in the index only after its extraction and embedding finish, so counts can remain unchanged during a large file. Stages can include waiting for the shared worker; they are not a completion percentage or proof of a stall. This OCR update refreshes existing entries, so the first scan can take longer.

## Coverage and limits

| Content | Current support |
|---|---|
| Text and common code formats | Keyword and semantic passage search |
| PDFs | Embedded text and optional English OCR, with page references |
| DOCX, XLSX/XLSM, PPTX | Paragraph/table, worksheet-row, and slide extraction |
| HTML | Visible text, excluding scripts/styles |
| JPEG, PNG, WebP, GIF, BMP, TIFF | Metadata; optional visual search using CLIP |
| Archives, backups, legacy Office, audio/video, RAW/HEIC | Not indexed |

Files above 32 MiB are skipped with an indexing error; indexing extraction stops at 200,000 characters. Full-document reading uses continuation offsets instead of this index cap. Hidden files/directories, symlinks, dependencies, build outputs, Synology recycle/thumbnail folders, and known Time Machine folders are skipped. Binary files disguised as text are rejected. Transcription, photo OCR, and automatic backup deduplication are not implemented. Blank or unreadable PDF pages can still be flagged as needing OCR. Support depends on the file format; the 32 MiB source limit also applies to reading and downloading.

Text search uses BGE-small English embeddings plus SQLite FTS5, with SQLite `sqlite-vec` for vectors. Photo search uses CLIP vision/text models when enabled. Models download from Hugging Face on first use and are cached in the persistent data folder. Content is processed locally; search passages sent to your Claude/ChatGPT client are handled under that client's data policy. Photo indexing is disabled by default because it uses more memory.

## Verification and performance

The application core is written in [Dafny](core/Core.dfy) and compiled to Go. It specifies bounded chunking and reconstruction, lexical relative-path validation, result-limit clamping, deterministic ordering/top-k, ranking properties, and abstract replacement/removal properties. These proofs do **not** prove filesystem confinement, SQLite transactions, MCP transport/authentication, document parsers, embeddings, or deployment. Those Go/Python boundaries are tested separately. See [AGENTS.md](AGENTS.md) for development rules.

Real embedding smoke tests passed on the development computer, using serialized CPU workers. The text model also passed the NAS document pilot; photo models remain untested there. The container caps aggregate memory at 1 GiB and restricts CPU affinity to one CPU; exceeding the memory cap can terminate it. Models load in short-lived processes, so each request includes loading overhead.

The [recorded development benchmark](docs/benchmarks/development.json) includes machine identity, dependency versions, timestamp, and per-process measurements. Reproduce it with the project's Python environment:

```sh
python3 scripts/benchmark-worker.py --data /path/to/data --output benchmark.json --label development
```

The data directory contains `models/`; missing weights download automatically. Use `--label nas` only when running on the actual NAS. These single-operation smoke measurements are not throughput estimates.

Developer checks:

```sh
scripts/build-core.sh
go test -tags sqlite_fts5 ./...
python3 -m pytest tests_worker tests_install -q
```

Use the project's Python virtual environment for worker tests. Dafny is expected at `.tools/dafny/dafny`, or set `DAFNY` to an installed binary. Compiled Go output is committed so container builds do not need Dafny. CI verifies and tests before publishing the image.
