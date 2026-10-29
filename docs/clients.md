# Connect your MCP clients

The service uses Streamable HTTP at `/mcp` with a static bearer token. Use `http://NAS_LAN_ADDRESS:8080/mcp` for a trusted local network, or the private HTTPS/Tailscale address configured for your own service. The server, index, and models remain on the NAS; the desktop bridge forwards requests and can save downloaded originals on its own computer.

Claude web and mobile can use a separate OAuth-protected listener on port `8082`; they do not use the static token or private listener. Configure its public endpoint and access scope for your own service as described in [Claude web and mobile connector](oauth-connector.md).

## Install the MCP connection

Fully quit Claude Desktop before changing its configuration, then reopen it after installation. If the NAS tools disappear later, check whether the `nas-search` entry is still present in the config file printed by the installer. Rerunning the installer restores a missing entry; use `--replace` only when replacing an existing entry.

Run from the project checkout on your client computer (Python 3.10 or newer):

```sh
python3 scripts/install-mcp.py --client codex \
  --url http://NAS_LAN_ADDRESS:8080/mcp \
  --token-file build/nas-deployment/.env
```

Use `--client claude-code` for Claude CLI or `--client claude-desktop` for Claude Desktop. On Linux, the installer uses an existing `Claude/claude_desktop_config.json` under `XDG_CONFIG_HOME`, or `~/.config` when unset. If that file does not exist, specify your client's path with `--config`; the installer does not create a guessed Linux config. Automatic desktop paths also cover macOS and Windows. The `build/nas-deployment/.env` path is a private local deployment file; it is not included in the repository. Substitute your own NAS `.env` copy or a token-only file. Keep it outside source control and omit the token value from commands.

The installer checks the MCP handshake and tools before writing settings. It copies a dependency-free stdio bridge and token into `~/.config/nas-search`, protects them with owner-only permissions, and uses the CLI's supported registration command or merges desktop JSON. Existing entries with the same name are refused unless `--replace` is supplied. Backups are retained: `.nas-search.bak` for CLI configuration and `.bak` for desktop JSON. Failed configuration changes restore the prior configuration and credential files. `--dry-run` makes no changes or network requests; `--skip-check` is only for deliberately offline setup.

Start a new CLI session or fully restart the desktop app. Config installation and SDK tool calls have passed; conversations inside the client GUIs still need testing. The installed bridge supports this project's stateless HTTP server; it is not a general bridge for server-initiated requests or resumable event streams. You do not need Node.js or a shell-exported token with this installer. Manual native HTTP alternatives follow.

## Shared memory

When [shared memory](memory.md) is on, Claude, Codex, and other clients also get `context_open`, `journal_append`, and related tools. See [shared memory](memory.md) for the full set. The guide shows the one line to add to Claude's preferences so it uses them.

## Open originals in your browser

Ask: **“Give me a link to example.pdf.”** Claude replies with a link; tap it and complete a fresh browser OAuth sign-in to open the original. Never enter the NAS password in Claude chat. PDFs, JPEG/PNG/GIF/WebP photos, and plain text display directly. Word, Excel, HTML, SVG, and other formats download instead, so they cannot run anything on the NAS's web address.

This is the way to *see* a file. `fetch_file` only hands raw bytes to the model, which cannot turn them into an attachment for you.

- Links work wherever your browser can reach the configured address. Confirm access from each client and network you plan to use.
- Each link opens one file and expires after 10 minutes (`NAS_DOWNLOAD_LINK_MINUTES`, 1–60). Restarting the service cancels all links. Ask again for a fresh one. If a long PDF stops loading pages partway through, the link has expired; ask for a new one.
- Public links require fresh browser OAuth sign-in and unlock only that file for at most ten minutes in the signed-in browser. Private links on the LAN or tailnet still use the unguessable URL as their credential; don't forward them to sensitive documents.
- A link stops working if the file changes, or its folder leaves `NAS_ALLOWED_FOLDERS`.
- The limit is 32 MiB per file and 200 active links.

Links use `NAS_DOWNLOAD_URL`, or the configured `NAS_OAUTH_ISSUER` if that is empty. With neither set, the tool is hidden. To keep links private, set `NAS_DOWNLOAD_URL` to an address your devices can reach without public exposure, such as `http://NAS_LAN_ADDRESS:8080` at home or a private Tailscale address away from home. Check that the address opens in your browser before relying on it.

## Read complete documents and retrieve originals

`read_document` returns extracted text and page/section locations. Continue with its `next_offset` until `complete=true`; offsets count Unicode characters. This bypasses the search index's 200,000-character extraction cap, but source files still have a 32 MiB safety limit. English OCR is used for PDF pages without embedded text when `NAS_OCR=true`. Check OCR metadata; an unreadable scan can still contain missing text. Long scanned documents may repeat OCR of earlier pages during pagination, so reading can be slow.

With the installed stdio bridge, ask your assistant: **“Download this NAS file to /tmp/nas-original.pdf using download_file.”** Supply the exact NAS `path` and an absolute local `output` path. The destination folder must already exist. The tool saves the complete original and returns its local path and byte count; it refuses to overwrite existing files. Downloads retain the 32 MiB source limit and source-version checks.

The file is saved on the computer running the bridge. A bridge running on a remote host saves there. Native HTTP connections do not offer `download_file`; use the helper below or reconnect through the installed bridge. A saved file does not automatically become a chat attachment.

After updating this checkout, rerun the client installation command with `--replace`, then start a new CLI session or fully quit and reopen the desktop app to load the new tool.

`fetch_file` returns original bytes in base64 batches, without conversion. For continuation calls, supply the first response's `version` as `expected_version`. A client must reassemble the bytes; receiving them does not establish that its model can interpret PDF/image attachments. To download the entire original to your computer:

```sh
python3 scripts/fetch-file.py --url http://NAS_LAN_ADDRESS:8080/mcp \
  --token-file build/nas-deployment/.env \
  --path 'Documents/example.pdf' --output /tmp/nas-original.pdf
```

The helper uses authenticated MCP and writes a private local file. Existing output files are preserved unless `--replace` is explicit. PDFs are not converted to Markdown, and original NAS files stay read-only.

## Manual native HTTP setup

Use these alternatives if you prefer native HTTP to the bundled bridge.

### Claude CLI / Claude Code

Set `NAS_SEARCH_TOKEN` to the same token as the NAS `.env`, then run:

```sh
claude mcp add --transport http --scope user nas-search \
  http://NAS_LAN_ADDRESS:8080/mcp \
  --header "Authorization: Bearer ${NAS_SEARCH_TOKEN}"
claude mcp list
```

Restart Claude Code and check `/mcp`. This command stores the configured credential in your client configuration; protect that file and avoid exposing the token in screenshots or shell history. These flags follow [official Claude Code documentation](https://code.claude.com/docs/en/mcp).

### Codex CLI

Add this table to your existing `~/.codex/config.toml`; keep existing entries:

```toml
[mcp_servers.nas_search]
url = "http://NAS_LAN_ADDRESS:8080/mcp"
bearer_token_env_var = "NAS_SEARCH_TOKEN"
startup_timeout_sec = 30
tool_timeout_sec = 600
```

Export `NAS_SEARCH_TOKEN` in the environment that launches Codex, then restart it and run `codex mcp list`. The token value belongs in your environment, not in this TOML snippet. This follows [official OpenAI MCP documentation](https://learn.chatgpt.com/docs/extend/mcp?surface=cli).

## ChatGPT Desktop and ChatGPT cloud

For ChatGPT Desktop's local Codex connection, run the client installer with `--client chatgpt-desktop`. This is an explicit alias for installing into the shared Codex configuration through `codex mcp`; the Codex CLI must be available. It uses the protected token file and stdio bridge, so no shell token export is needed. Existing entries require `--replace` to change them.

[Official OpenAI documentation](https://learn.chatgpt.com/docs/extend/mcp) confirms that ChatGPT Desktop, Codex CLI, and the IDE extension share MCP configuration for the same Codex host. Restart the connection from Settings → MCP servers, start a new local Codex chat, and use `/mcp` to check availability. Cloud Work chats do not load this local configuration, even when opened in the desktop app; see [developer settings](https://learn.chatgpt.com/docs/developer-settings).

ChatGPT's **cloud** custom-MCP connection cannot directly reach a LAN or private Tailscale address. OpenAI documents [Secure MCP Tunnel](https://developers.openai.com/api/docs/guides/secure-mcp-tunnels), which could run beside the server and make an outbound connection. It requires a Platform tunnel identity, runtime API key, and relevant organization/workspace permissions. A publicly reachable HTTPS service is another route. ChatGPT's plugin builder can use the OAuth listener with a pre-registered client and its exact callback URL; see the [OAuth connector guide](oauth-connector.md#chatgpt-plugin-setup). This path still needs an end-to-end ChatGPT connection test.

Do not expose the private bearer listener to the public internet to bypass that limitation. Verify the tunnel/authentication path and account support as a separate integration.

## First requests

Ask your client to:

1. Call `index_status` and report scan progress and errors.
2. Search for a known phrase in a pilot document and show its source path/location.
3. Browse `Documents/` and read a known small text/code file.
4. After photo indexing is enabled, search with `photos=true` for a description of a known image.

Initial indexing shares its worker with semantic search and document extraction, so those calls can wait. Status checks and original-file retrieval avoid the worker queue. A health check confirms only that HTTP is running. Search results come from a periodically updated index; validate important answers against the source file.

Before reporting a named document missing, call `find_files`. This searches live filenames and folder names, independently of OCR and indexing. Use a known folder as `prefix`, then pass the returned exact path to `read_document` or `fetch_file`. All whitespace-separated query terms must appear in the path, ignoring case. Check warnings and `truncated`: a partial search does not prove absence. Symlinks, hidden files, and excluded folders are omitted; matching by name does not guarantee the content can be extracted.

For a PDF with unreadable extracted glyphs, call `read_document` with `force_ocr: true`, and use the same setting with every `next_offset`. OCR must be enabled on the NAS. Check `metadata.text_quality_suspect`, `suspect_pages`, and the OCR fields before treating extracted text as reliable. The suspect flag describes the original text layer, even when OCR replaces it. Sheet music needs visual interpretation to understand the notation; OCR may recover only printed words.
