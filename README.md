# NAS Search MCP

Project decisions and open questions are recorded in the [decisions log](decisions/README.md).

NAS Search MCP provides read-only keyword and semantic search over authorized folders. It is designed for a Synology DS220+ with an Intel Celeron J4025 and 2 GB RAM. The service, index, and free embedding models run on the NAS; ordinary search does not require a second computer or a paid AI service.

Treat this repository as a deployable project, not evidence that a particular installation is running or validated. Check your own deployment's health, permissions, indexing status, and resource use before relying on it.

## Install

Connect a local MCP client from this checkout:

```sh
python3 scripts/install-mcp.py --client codex \
  --url http://NAS_LAN_ADDRESS:8080/mcp \
  --token-file /path/to/private/token-file
```

Use `claude-code`, `claude-desktop`, or `chatgpt-desktop` for the other supported local clients. ChatGPT cloud chats need a separate integration. See [client setup](docs/clients.md).

Install or upgrade the NAS service from a complete checkout on the NAS:

```sh
sudo sh scripts/install-nas.sh \
  --image ghcr.io/OWNER/nas-search-mcp:COMMIT_SHA
```

Use a published commit whose CI passed; `latest` is deliberately rejected. Configure authorized source mounts, private data storage, and the listener address in the private `.env`, after verifying the physical paths on your NAS. Existing NAS settings take precedence. See [deployment guidance](docs/deployment.md).

## Use it

Ask your assistant to check `index_status`, then search for a topic. Results include source paths, page or section locations, and passages. Ask it to read the source before relying on a result; ranking scores are not confidence probabilities.

The NAS provides `search`, `find_files`, `list_folders`, `browse`, `read_file`, `read_document`, `fetch_file`, and `index_status`. A configured download-link origin adds `download_link`; public links require fresh browser OAuth sign-in. The local stdio bridge also provides `download_file` to save an original on the computer running the bridge. See [complete documents and original files](docs/clients.md#read-complete-documents-and-retrieve-originals).

When browser uploads are enabled, `upload_document` gives you a short-lived page for one file of any type, including photos and files without an extension, up to 32 MiB. Open the original link yourself in Safari or Chrome within five minutes, complete a fresh OAuth sign-in, choose the file, and click **Upload**. If an assistant's embedded browser shows `invalid_request`, request a new link and open the original `/upload/` link in your own browser; an `/authorize` page cannot be reused. By default, the service saves the file under `Documents/Uploads`. With indexed-destination uploads enabled, the assistant chooses an existing folder within the configured searchable sources, and the page shows that folder before you click Upload. Ask the assistant to use `browse_folders` to find a valid destination. The destination must be inside an allowed source folder; denied, hidden, excluded, missing, and symlink folders are rejected. Existing filenames are never overwritten, and the service never moves or deletes existing files. Ask the assistant to use `find_files` to locate a saved file by filename. Content search is available only for supported formats. PDF, TXT, Markdown, and DOCX uploads still undergo format checks, so malformed files in those formats can be rejected. Photo visual indexing follows the photo-search setting. The page never asks you to put a password or file into chat. This MCP-initiated browser flow does not provide direct ChatGPT file-attachment support.

To enable uploads, create the `Uploads` folder inside the authorized Documents source on the NAS. Give the container's configured UID/GID write permission to that folder only. In the private NAS `.env`, set `NAS_UPLOAD_ENABLED=true` and `NAS_UPLOAD_PATH` to the verified physical path of that folder, then run the NAS installer to deploy. The installer checks that the path is exactly the `Uploads` subfolder of Documents and that the container user can write there. The normal Documents mount stays read-only; only this separate mount is writable. Uploads require the OAuth listener and a browser address that can reach its issuer. Leave uploads disabled when that access is not configured.

To allow placement in any existing authorized searchable folder, set `NAS_UPLOAD_ENABLED=true` and `NAS_UPLOAD_INDEXED_DESTINATIONS=true` in the private `.env` and redeploy. `NAS_UPLOAD_PATH` is used only by fixed-folder mode and is not required here. This starts a small isolated writer in the same project. Search and extraction retain read-only source mounts; only the writer receives writable mounts for the configured source roots, and it has no network connection. The destination folder must permit the configured container UID/GID to create files; the installer does not change permissions on source folders. A folder can be searchable yet still refuse uploads if NAS permissions deny writing. This mode permits new files across the authorized sources, so review `NAS_ALLOWED_FOLDERS` and `NAS_DENIED_FOLDERS` first. The browser page confirms each destination, while the writer independently rejects traversal, symlinks, excluded folders, and overwrites. A compromised writer would still have write access to its mounted roots; use the fixed-folder mode when broad placement is unnecessary.

Use `list_folders` to navigate one level at a time through authorized source folders. It reads the mounted folders live, so empty directories appear even when they contain no indexed files. Each call returns direct child folders only, with at most 100 results and bounded time and entries examined. Hidden folders, excluded folders, and symlinks are omitted. The tool defaults to denying access unless the requested path is inside an authorized root.

Use `find_files` when you know a filename or folder name, or when content search misses a document. It requires a query and matches live file paths independently of extraction. `browse` lists indexed files. Hidden files, symlinks, and excluded folders are omitted from live path results.

Scans run at startup and periodically. An item enters the index after extraction and embedding finish. During indexing, semantic search and document reading may wait behind the worker queue; status checks and original-file retrieval remain available. The container pauses for two seconds before checking each eligible file, including unchanged files, to reduce disk contention on small NAS devices. Set `NAS_SCAN_FILE_DELAY_SECONDS=0` in the private deployment settings to disable pacing. At two seconds, 10,000 eligible files add about 5½ hours to a full scan; files already indexed remain searchable while it runs. Measure connector latency and scan progress on your NAS before changing this setting.

For finding photos of people, the chosen next step is a read-only Synology Photos
integration that reuses its named People albums. The optional `photos_people`,
`photos_for_person` and `photos_preview` tools provide named-person lookup, matching
item metadata and small JPEG previews after [host setup](docs/synology-photos.md).
These tools do not require photo embeddings. The [visual-search pilot](docs/deployment.md#start-a-small-visual-image-search-pilot)
remains a separate experimental option for descriptive image search.

## Coverage and limits

| Content | Support |
|---|---|
| Text and common code formats | Keyword and semantic passage search |
| PDFs | Embedded text and optional English OCR, with page references |
| DOCX, XLSX/XLSM, PPTX | Paragraph/table, worksheet-row, and slide extraction |
| HTML | Visible text, excluding scripts/styles |
| JPEG, PNG, WebP, GIF, BMP, TIFF, HEIC/HEIF | Metadata; optional visual search using CLIP |
| Archives, backups, legacy Office, audio/video, camera RAW | Not indexed |

Files above 32 MiB are skipped; index extraction stops at 200,000 characters. Full-document reading uses continuation offsets but retains the source-size limit. Transcription, photo OCR, independent face recognition, and automatic backup deduplication are not implemented. The optional Photos connector reuses Synology's existing People assignments. Search coverage depends on file format and extraction quality.

Text search uses BGE-small English embeddings and SQLite FTS5 with sqlite-vec. Optional photo search uses CLIP. Models download from Hugging Face on first use and are cached in the persistent data folder. Content processing is local; excerpts sent to an assistant are handled under that provider's data policy. Photo indexing is disabled by default because it uses more memory.

## Verification and performance

The application core is written in [Dafny](core/Core.dfy) and compiled to Go. Its proofs cover specified core properties; they do not prove filesystem confinement, SQLite transactions, MCP transport/authentication, document parsers, embeddings, or deployment. Go and Python adapters are tested separately. See [AGENTS.md](AGENTS.md) for development checks.

The container is configured with bounded memory and CPU use. Actual NAS compatibility, indexing speed, search latency, and memory use must be measured on the target device before making performance claims. Development smoke measurements are not throughput estimates.

## Claude connector cannot find the server on a Mac

If Claude's sign-in opens Safari and Safari says it cannot find the server, check DNS before changing the connector. A Mac connected to a different Tailscale tailnet may send the connector's public `*.ts.net` name to that tailnet's MagicDNS resolver, which cannot resolve names in your NAS tailnet. Compare `dscacheutil -q host -a name NAS.YOUR_TAILNET.ts.net` with `dig @1.1.1.1 NAS.YOUR_TAILNET.ts.net`. If only the public lookup succeeds, add a macOS resolver for your NAS tailnet domain:

```sh
sudo mkdir -p /etc/resolver
printf 'nameserver 1.1.1.1\nnameserver 1.0.0.1\n' | sudo tee /etc/resolver/YOUR_TAILNET.ts.net >/dev/null
dscacheutil -q host -a name NAS.YOUR_TAILNET.ts.net
```

Replace the placeholders with your own public Funnel hostname and its tailnet domain. This changes DNS only for that tailnet domain; it does not switch Tailscale accounts or change DNS for the work tailnet. Retry Claude's **Reconnect** button after the hostname resolves. Remove the rule with `sudo rm /etc/resolver/YOUR_TAILNET.ts.net` if you no longer need it. Names that exist only inside the NAS tailnet will not resolve through this public resolver.
