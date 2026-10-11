# Deploy with Synology Container Manager

Start with a small document/code folder, not your whole 2.3 TB library. This version has not yet run on the NAS.

## Prepare the project

Create `/volume1/docker/nas-search` using DSM File Station. Put the repository files there, preserving `worker/`, `generated/`, `internal/`, and `cmd/`. Either download the repository ZIP and extract its contents, or clone it using SSH if you already use SSH:

```sh
git clone https://github.com/mattyv/nas-search-mcp.git /volume1/docker/nas-search
```

Copy `.env.example` to `.env`. Set:

- `NAS_TOKEN`: generate a random token locally with `python3 -c 'import secrets; print(secrets.token_urlsafe(32))'`. Minimum 24 characters. Keep `.env` private.
- `NAS_MATTHEW_PATH`: an existing pilot subfolder under `/volume1/Matthew`, for example `/volume1/Matthew/SearchPilot`. Confirm the volume/share path first. The pilot folder still appears to clients as `Matthew/`.
- `NAS_PHOTOS_PATH`: confirm `/volume1/homes/matt/Photos` exists. It is mounted read-only; with photo embeddings disabled, image metadata is still catalogued. To keep the first pilot small, choose a small subfolder here too.
- `NAS_INDEX_PATH`: `/volume1/docker/nas-search/data`. Create this directory. It must be outside the source folders.
- `NAS_UID` and `NAS_GID`: numeric IDs of the DSM account used to read your sources and write the index. Defaults are placeholders. With SSH, `id matt` shows the actual IDs. In DSM grant this account read access to sources and write access to the data folder; verify permissions work for that numeric container user.
- `NAS_BIND_IP`: use `192.168.1.4` for home LAN testing. The default `127.0.0.1` only accepts connections on the NAS. Use `100.112.12.118` instead for the NAS's personal Tailscale address if that address can be published by Container Manager; test it before relying on remote access.
- `NAS_PHOTOS=false`: leave this until the document pilot is healthy.

Container Manager → **Project → Create**: name `nas-search`, path `/volume1/docker/nas-search`, use the existing `compose.yaml`, and build/start it. The Dockerfile builds Go and installs pinned Python libraries. Building on the NAS can be slow and is not covered by the running container's resource limits.

## Use a published image instead of building

After a successful main-branch CI run, the image is published at `ghcr.io/mattyv/nas-search-mcp:latest` and with a commit-SHA tag. Verify that the GitHub package is public; otherwise registry authentication is required. No published image is assumed available before CI succeeds.

For a pull-only Container Manager project, change the service's image line to:

```yaml
image: ghcr.io/mattyv/nas-search-mcp:latest
```

Remove `build: .` from that deployment copy. Keep the rest of `compose.yaml`. If the image is unavailable, build from the supplied Dockerfile. For a repeatable deployment, use the published commit-SHA tag instead of `latest`.

## Check the pilot

In Container Manager, inspect logs and memory. First model use requires outbound internet access. Look for scan/extraction/model errors and wait for the initial scan to finish. On a computer that can reach the NAS:

```sh
curl --fail http://192.168.1.4:8080/healthz
```

Expected: `ok`. This only confirms the HTTP process is alive, not that indexing succeeded. Connect an MCP client and call `index_status` to check scanning, indexed counts, and errors; then search for a phrase from a known pilot document. Check a result's path and content against the original file.

A 401 from `/mcp` means the bearer token is missing or incorrect. A refused/timed-out connection means check container status, the published bind address, DSM firewall, and network routing. Permission errors require checking source/data ACLs for `NAS_UID:NAS_GID`. Repeated restarts near 1 GiB indicate the pilot/model exceeds the cap; keep photos disabled and inspect logs.

## Expand carefully

After a successful pilot, point `NAS_MATTHEW_PATH` at the intended larger folder and restart the project. Changes are found by periodic scanning and content fingerprints. Keep a record of the source mounts; changing their physical source while keeping the same client prefix changes what that prefix means.

For photos, first choose a small photo folder, set `NAS_PHOTOS=true`, and use a **new** `NAS_INDEX_PATH` directory. The application rejects an existing index created with different pipeline settings; it must be rebuilt. Keep the old data directory until the new pilot works. Photo models use more memory than text models; their fit and performance on the NAS remain unmeasured.

The container uses CPU affinity (`NAS_CPUSET=0`) to restrict execution to one CPU; the Synology kernel does not support Docker's CFS CPU quota setting. It permits 1 GiB aggregate memory with no extra swap allowance and uses a read-only root filesystem. Authorized source mounts are read-only; only the index/model data folder is writable. Extraction/model workers have a ten-minute timeout. This bounds resource use but does not promise indexing speed or guarantee the models fit the DS220+.

For remote device access, use the existing personal Tailscale setup and an endpoint your device can reach. Preserve the work Tailscale account and enabled local-LAN access. Do not switch accounts to make the pilot work. LAN HTTP transmits the token without TLS; use a trusted LAN for the pilot, and Tailscale encryption or a correctly configured HTTPS reverse proxy for remote use. QuickConnect's DSM URL is not this MCP endpoint.
