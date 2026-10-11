# NAS Search MCP

Project choices and unresolved questions are recorded in the [decisions log](decisions/README.md).

Read-only keyword and semantic search for a Synology DS220+ with 2 GB RAM. The service, index, and free embedding models run on the NAS. Original files stay in their shared folders. No second computer or paid AI API is required for search.

**Status: document pilot running on the DS220+.** On 2026-10-11, text indexing and hybrid search passed on the NAS, with six indexed files and no reported indexing errors. Missing authentication returned HTTP 401. The kernel enforced the 1 GiB memory limit and CPU affinity to CPU 0. See [NAS validation evidence](docs/benchmarks/nas-pilot.json). Photo embeddings and desktop client connections remain untested on the NAS.

## Try it on your NAS

1. Follow [deployment instructions](docs/deployment.md) to create a Container Manager project. The authorized sources are the `Matthew` share and `homes/matt/Photos`; confirm their actual paths in DSM.
2. Keep `NAS_PHOTOS=false` and mount a small document/code subfolder first. The first scan downloads the free text embedding model.
3. Follow [client setup](docs/clients.md), then ask: **“Use nas-search to show index_status, then find documents about …”**
4. Check Container Manager memory and logs before adding more files or enabling photo embeddings.

The MCP tools are `search`, `browse`, `read_file`, `read_document`, `fetch_file`, and `index_status`. Search returns source paths, locations, and passages. `read_document` reads complete extracted PDF/Office/text contents in bounded batches, including English OCR for scanned PDF pages. `fetch_file` retrieves original bytes in batches; [client setup](docs/clients.md) includes a complete-file download helper. `read_file` reads a bounded text/code prefix. Scans run at startup and every five minutes; a large initial scan can take much longer.

## Coverage and limits

| Content | Current support |
|---|---|
| Text and common code formats | Keyword and semantic passage search |
| PDFs | Embedded text and optional English OCR, with page references |
| DOCX, XLSX/XLSM, PPTX | Paragraph/table, worksheet-row, and slide extraction |
| HTML | Visible text, excluding scripts/styles |
| JPEG, PNG, WebP, GIF, BMP, TIFF | Metadata; optional visual search using CLIP |
| Archives, backups, legacy Office, audio/video, RAW/HEIC | Not indexed |

Files above 32 MiB are skipped with an indexing error; indexing extraction stops at 200,000 characters. Full-document reading uses continuation offsets instead of this index cap. Hidden files/directories, symlinks, dependencies, build outputs, Synology recycle/thumbnail folders, and known Time Machine folders are skipped. Binary files disguised as text are rejected. Transcription, photo OCR, and automatic backup deduplication are not implemented. Blank or unreadable PDF pages can still be flagged as needing OCR. This is not an all-format search engine.

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
