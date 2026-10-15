# Deploy with Synology Container Manager

Start with a small document/code folder, not your whole 2.3 TB library. A small document pilot has passed on the DS220+; photo models and larger collections still need validation.

## Scripted installation and upgrades

From a complete project checkout on the NAS, after the desired commit's CI image publication succeeds:

```sh
sudo sh scripts/install-nas.sh \
  --image ghcr.io/mattyv/nas-search-mcp:COMMIT_SHA \
  --documents-path '/volume1/shared-documents/Course/Week 10' \
  --code-path /volume1/shared-code \
  --photos-path /volume1/docker/nas-search/pilot-photos \
  --data-path /volume1/docker/nas-search/data \
  --bind-ip 192.0.2.10
```

Replace `COMMIT_SHA` with the complete published commit SHA. The installer rejects `latest`. Source folders must exist. `--install-dir` defaults to `/volume1/docker/nas-search`; UID/GID default to the user invoking `sudo`, with explicit `--uid`/`--gid` overrides. `--dry-run` inspects prerequisites without pulling an image or writing files; the actual install also probes kernel limits and mount permissions as the container user.

Existing `.env` contents and index data are preserved, and existing deployment settings take precedence over command-line source settings. The installer secures `.env` permissions, writes pull-only Compose with a persisted image version, starts it, and checks HTTP health and authenticated MCP status. If OAuth is configured, its issuer metadata and authentication challenge must also pass before success. It does not claim initial indexing is finished. On a failed upgrade it restores the previous Compose configuration and restarts that image. This is an image/configuration rollback, not a database snapshot or schema rollback.

To expand the collection, deliberately edit the existing `.env` mount paths before running the installer. Do not put index data inside a source mount. The existing pilot can keep the same token and database during this update; extraction-version changes trigger a refresh of unchanged source files.

English PDF OCR is enabled by default (`NAS_OCR=true`) and uses Poppler plus Tesseract in the serialized worker queue. Set it to false to disable OCR. Photo OCR and additional recognition languages are not implemented. A one-page scanned-PDF OCR fixture and complete PDF text retrieval have passed on the NAS. Larger scans still need testing.

## Upgrade through MCP

After one-time setup, an authenticated assistant can call `upgrade_from_main` without arguments, then `upgrade_status`. Ask: “Upgrade NAS search from main and report the result.” The request returns immediately. The host worker resolves current `main`, requires successful verification and image publication for that exact commit, and installs its pinned image. If current main has not passed CI, the request fails; it does not silently select an older commit. Search may disconnect briefly; reconnect before checking status.

This is an owner capability. Enabling it grants upgrade access to **all existing authenticated MCP clients**, including OAuth clients. There is no separate administrator role. The OAuth login page discloses service upgrades; original source files remain read-only. Keep upgrades disabled if any authenticated client should have search-only access. Tool descriptions require an explicit user upgrade request, but that instruction is not a technical authorization boundary against an authenticated client.

Use a complete reviewed checkout containing this feature on the NAS, after that commit is merged to main and its CI image publication succeeds:

```sh
sudo python3 scripts/enable-upgrades.py \
  --image ghcr.io/mattyv/nas-search-mcp:COMMIT_SHA
```

Replace `COMMIT_SHA` with the full published commit SHA containing these tools. This command cannot enable them in an older image. Python 3.8 or newer is required. Defaults discover the active Compose deployment and create `/volume1/@nas-search-upgrader`. Fresh installations with no existing container fall back to `/volume1/docker/nas-search`. Explicit `--source-install-dir` overrides discovery. The target and every ancestor must be root-owned and writable only by root; also verify DSM ACLs do not grant another account write/delete permission. The script refuses an existing target. Source paths, token, index and model cache are preserved. It copies private deployment settings into the protected location, deploys the selected image using the existing installer, and starts a host worker. The active Compose project remains `nas-search`; future deployment settings belong in the protected target's `.env`.

If setup failed after creating the protected directory, preserve it and resume from an updated checkout:

```sh
sudo python3 scripts/enable-upgrades.py --resume \
  --image ghcr.io/mattyv/nas-search-mcp:COMMIT_SHA
```

Resume requires complete root-owned settings, a protected worker and the expected queue. It keeps the saved token and configuration instead of copying the old deployment again. Inspect the private setup log if it fails; do not delete the protected folder to retry.

In DSM **Control Panel → Task Scheduler**, create a **Triggered Task → User-defined script** for user `root`, event **Boot-up**, with this command:

```sh
/usr/bin/python3 /volume1/@nas-search-upgrader/upgrade-host.py \
  --install-dir /volume1/@nas-search-upgrader \
  >> /volume1/@nas-search-upgrader/upgrade-host.log 2>&1
```

The setup starts the worker now; the boot task keeps it available after NAS restarts. A host lock prevents duplicate workers. Reconnect your MCP client to refresh its tool list. If the worker is stopped, requests remain queued until it starts. `upgrade_status` reports `queued`, `running`, `succeeded`, or `failed`, with the attempted commit and a safe failure stage. A stuck installer can remain running indefinitely; inspect the host and Docker before intervening. The worker waits for actual completion rather than releasing the lock while Docker might still be deploying.

The search container receives only a fixed-operation request queue, without a Docker socket or host shell. The root worker downloads bounded source archives from this repository, rejects links and escaping paths, and invokes the existing health-checked installer. The installer attempts image/configuration rollback after failed startup; it does not snapshot or roll back database changes. A failed setup may already have changed the running deployment: inspect health and the private `enable-upgrades.log` before retrying. Do not rerun the old deployment helper after migration without deliberately choosing which protected configuration is active.

For manual inspection on the NAS:

```sh
sudo cat /volume1/@nas-search-upgrader/current-image
sudo tail -n 30 /volume1/@nas-search-upgrader/upgrade-host.log
```

These are tested operating-system and MCP adapters outside the Dafny proof boundary. A live same-version upgrade through MCP passed on the DS220+ on 2026-10-15, including health and authenticated status checks. Reboot recovery and a future version-changing MCP upgrade still need validation. The privileged host worker stays at the version installed during setup; service upgrades do not replace its code. Updating that worker needs a separately reviewed administrator action.

## Start a small visual image-search pilot

Visual search is implemented but has not been measured on the DS220+. Start with 20–50 JPEG, PNG, WebP, GIF, BMP, or TIFF images in an existing subfolder under `/volume1/shared-photos`. Images must be at most 32 MiB and 25 megapixels. HEIC, camera RAW, face recognition, and image OCR are outside this pilot. Searches describe visible content, such as “a beach at sunset”; results are candidates to inspect, not confidence scores.

Run the pilot launcher from an updated checkout on the NAS:

```sh
sudo sh scripts/photo-pilot.sh \
  --image ghcr.io/mattyv/nas-search-mcp:e3ae38e943786aa2e3d6a2ac017b05e6099b55dc \
  --photos-path '/volume1/shared-photos/YOUR_SMALL_FOLDER' \
  --dry-run
```

Replace the folder with its actual physical path. The image above passed CI and includes the existing visual-search support. A successful dry run checks prerequisites without downloading models or starting a container. Remove `--dry-run` to start the pilot. Keep the main document service running: this uses a separate container, port 8081, and data directory. The launcher refuses to replace an existing pilot or reuse an existing pilot data directory. It checks available NAS memory before starting; insufficient memory leaves the document service alone.

The pilot mounts only the selected source folder, read-only, as `Photos/`. It uses the existing protected NAS token. Models download on first use, which can take several minutes and require internet access. The container is limited to one CPU and 1 GiB memory without extra swap. These limits do not prove compatibility: monitor the actual container and DSM while indexing.

On the NAS, inspect the pilot:

```sh
sudo docker stats --no-stream nas-search-photo-pilot
sudo docker logs --tail 40 nas-search-photo-pilot
sudo docker inspect --format '{{.State.Status}} OOMKilled={{.State.OOMKilled}}' nas-search-photo-pilot
```

Record memory during indexing and during the first descriptive search, not just when idle. An OOM-killed container, unexpected stop, model error, or keyword-only fallback means the visual-search check has not passed. Stop the pilot if DSM slows down:

```sh
sudo docker stop nas-search-photo-pilot
```

Connect a separate local MCP entry so ordinary document search stays available. On your computer, from this checkout:

```sh
python3 scripts/install-mcp.py --client codex --name nas-photos \
  --url http://192.0.2.10:8081/mcp \
  --token-file ~/.config/nas-search/token
```

Use the other supported `--client` choices to add the same entry to Claude CLI, Claude Desktop, or ChatGPT Desktop's local Codex connection. Restart the client. Ask it to call `nas-photos` `index_status`, confirm `photos=true`, and report errors. When indexing finishes, ask it to search with `photos=true` for three descriptions of known images and one unrelated description. Confirm that results use visual search (`mode=hybrid`, without a model/vector fallback warning), inspect the returned images, and record elapsed time. Download a result through this connection's `download_file` tool to inspect the original locally. Unrelated queries can still return ranked images; relevance filtering remains uncalibrated.

Do not expand the collection until indexing, descriptive search quality, memory use, and continued document-service availability have been checked on the NAS. The pilot uses a fresh index because changing photo settings on the existing document index requires rebuilding it.

## Manual Container Manager setup

Use this route if you prefer DSM to the installer. Create `/volume1/docker/nas-search` using DSM File Station. Put the repository files there, preserving `worker/`, `generated/`, `internal/`, and `cmd/`. Either download the repository ZIP and extract its contents, or clone it using SSH if you already use SSH:

```sh
git clone https://github.com/mattyv/nas-search-mcp.git /volume1/docker/nas-search
```

Copy `.env.example` to `.env`. Set:

- `NAS_TOKEN`: generate a random token locally with `python3 -c 'import secrets; print(secrets.token_urlsafe(32))'`. Minimum 24 characters. Keep `.env` private.
- `NAS_DOCUMENTS_PATH`: an existing pilot subfolder under `/volume1/shared`, for example `/volume1/shared/SearchPilot`. Confirm the volume/share path first. The pilot folder still appears to clients as `Documents/`.
- `NAS_CODE_PATH`: `/volume1/shared-code`, mounted separately as `Code/`. When searching Documents and Code together, set `NAS_DOCUMENTS_PATH=/volume1/shared-documents` so code is not mounted twice under the full Documents share.
- `NAS_PHOTOS_PATH`: confirm `/volume1/shared-photos` exists. It is mounted read-only; with photo embeddings disabled, image metadata is still catalogued. To keep the first pilot small, choose a small subfolder here too.
- `NAS_INDEX_PATH`: `/volume1/docker/nas-search/data`. Create this directory. It must be outside the source folders.
- `NAS_UID` and `NAS_GID`: numeric IDs of the DSM account used to read your sources and write the index. Defaults are placeholders. With SSH, `id owner` shows the actual IDs. In DSM grant this account read access to sources and write access to the data folder; verify permissions work for that numeric container user.
- `NAS_BIND_IP`: use `192.0.2.10` for home LAN testing. The default `127.0.0.1` only accepts connections on the NAS. Use `100.64.0.10` instead for the NAS's personal Tailscale address if that address can be published by Container Manager; test it before relying on remote access.
- `NAS_PHOTOS=false`: leave this until the document pilot is healthy.

Container Manager → **Project → Create**: name `nas-search`, path `/volume1/docker/nas-search`, use the existing `compose.yaml`, and build/start it. The Dockerfile builds Go and installs pinned Python libraries. Building on the NAS can be slow and is not covered by the running container's resource limits.

## Use a published image instead of building

After a successful main-branch CI run, the image is published at `ghcr.io/mattyv/nas-search-mcp:latest` and with a commit-SHA tag. Verify that the GitHub package is public; otherwise registry authentication is required. No published image is assumed available before CI succeeds.

For a pull-only Container Manager project, change the service's image line to:

```yaml
image: ghcr.io/mattyv/nas-search-mcp:COMMIT_SHA
```

Replace `COMMIT_SHA` with a published commit whose CI passed and remove `build: .` from that deployment copy. Keep the remaining settings. If the image is unavailable, build from the supplied Dockerfile.

## Check the pilot

In Container Manager, inspect logs and memory. First model use requires outbound internet access. Check scan, extraction, and model errors. Initial indexing can queue semantic queries and full-document reads; status checks and original downloads remain available. On a computer that can reach the NAS:

```sh
curl --fail http://192.0.2.10:8080/healthz
```

Expected: `ok`. This only confirms the HTTP process is alive, not that indexing succeeded. Connect an MCP client and call `index_status` to check scanning, indexed counts, and errors; then search for a phrase from a known pilot document. Check a result's path and content against the original file.

A 401 from `/mcp` means the bearer token is missing or incorrect. A refused/timed-out connection means check container status, the published bind address, DSM firewall, and network routing. Permission errors require checking source/data ACLs for `NAS_UID:NAS_GID`. Repeated restarts near 1 GiB indicate the pilot/model exceeds the cap; keep photos disabled and inspect logs.

## Expand carefully

After a successful pilot, point `NAS_DOCUMENTS_PATH` at the intended larger folder and restart the project. Changes are found by periodic scanning and content fingerprints. Keep a record of the source mounts; changing their physical source while keeping the same client prefix changes what that prefix means.

For photos, first choose a small photo folder, set `NAS_PHOTOS=true`, and use a **new** `NAS_INDEX_PATH` directory. The application rejects an existing index created with different pipeline settings; it must be rebuilt. Keep the old data directory until the new pilot works. Photo models use more memory than text models; their fit and performance on the NAS remain unmeasured.

The container uses CPU affinity (`NAS_CPUSET=0`) to restrict execution to one CPU; the Synology kernel does not support Docker's CFS CPU quota setting. It permits 1 GiB aggregate memory with no extra swap allowance and uses a read-only root filesystem. Authorized source mounts are read-only; only the index/model data folder is writable. Extraction/model workers have a ten-minute timeout. This bounds resource use but does not promise indexing speed or guarantee the models fit the DS220+.

For remote device access, use the existing personal Tailscale setup and an endpoint your device can reach. Preserve the work Tailscale account and enabled local-LAN access. Do not switch accounts to make the pilot work. LAN HTTP transmits the token without TLS; use a trusted LAN for the pilot, and Tailscale encryption or a correctly configured HTTPS reverse proxy for remote use. QuickConnect's DSM URL is not this MCP endpoint.

## Restore mobile login after the earlier upgrade configuration loss

The first upgrade migration copied an older deployment configuration instead of the active mobile connector settings. Private MCP continued working, but the OAuth listener was disabled. Current installer defaults discover the active Compose directory, preserve its settings, and verify configured OAuth before reporting success.

The saved original settings remain in `/volume1/nas-search-connector/.env`. From a reviewed complete checkout staged in a root-owned directory with trusted ancestors, run `scripts/restore-mobile.py` as root. `--dry-run` validates readiness without changing settings. The repair requires matching token, index and source roots, preserves upgrade queue settings, saves a private backup, restores the original OAuth/download/access configuration, and checks metadata plus anonymous OAuth rejection. On failure it restores the prior configuration and attempts to restart it. It does not alter Funnel or Tailscale. The repair pauses the matching root upgrade worker while changing settings and resumes it afterward, including on failure. An already queued or running upgrade blocks the repair.

A user-writable checkout cannot be used directly for this privileged repair. Copy the reviewed checkout into a new root-owned mode-700 directory beneath `/volume1` first. Never print either deployment's environment file. Mobile login must be checked again after repair; private MCP health alone does not establish mobile access.
