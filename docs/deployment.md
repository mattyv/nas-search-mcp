# Deploy with Synology Container Manager

Use a small authorized document/code folder first. Confirm the source folders' physical paths and permissions in DSM before mounting them. A pilot does not establish full-library performance or photo-model compatibility.

For the existing NAS deployment, configure [boot and network-outage recovery](nas-recovery.md)
alongside installation. Container restart policies alone do not handle a late TUN
device or a VPN that becomes healthy after the first startup wait.


## Scripted installation and upgrades

From a complete project checkout on the NAS, after CI publishes the selected image:

```sh
sudo sh scripts/install-nas.sh \
  --image ghcr.io/OWNER/nas-search-mcp:COMMIT_SHA
```

Replace placeholders with your repository and published commit. The installer rejects `latest`; use a complete published commit SHA. Configure source mounts, private data storage, and listener address in the private `.env` after verifying local paths. The installer preserves existing credentials and index data, checks source and writable-path separation, starts the service, and verifies health and authenticated MCP access. If OAuth is configured, it also checks issuer metadata and the authentication challenge. A failed upgrade restores the previous image/configuration where possible, but does not roll back database changes. Existing settings take precedence over command-line source values; deliberately update the private `.env` when changing mounts.

Keep original sources read-only and disjoint from writable index and upgrade-queue directories. Keep secrets out of source control. English PDF OCR is enabled by default; it uses Poppler and Tesseract. Other OCR languages and photo OCR are not implemented. Unsupported formats are listed in the [coverage table](../README.md#coverage-and-limits).

## Upgrade through MCP

After one-time setup, a private bearer client can call `upgrade_from_main` to queue an upgrade. A public OAuth client receives a short-lived approval link; the owner must complete fresh browser sign-in and click Approve before the upgrade is queued. Call `upgrade_status` after reconnecting. The host worker resolves the current `main` commit, requires successful verification and image publication for that exact commit, and installs its pinned image. If verification has not passed, the request fails rather than selecting an older commit.

On a public OAuth connection, an upgrade request returns an approval link. Open it in your browser and enter the NAS password on the NAS page; never put the password in chat. The upgrade is queued only after approval. Private bearer clients retain direct queuing. Search may disconnect briefly during an upgrade.

Enabling this feature grants upgrade capability to all authenticated MCP clients; there is no separate administrator role. Keep it disabled if any authenticated client should have search-only access. The OAuth consent page should disclose service upgrades. Original source files remain read-only.

Use a reviewed checkout containing the feature, merged to `main` and published by CI:

```sh
sudo python3 scripts/enable-upgrades.py \
  --image ghcr.io/OWNER/nas-search-mcp:COMMIT_SHA
```

The setup creates a protected host directory and copies the active private settings there. Protect the directory and every ancestor from writes by other accounts, and check DSM ACLs. The host worker performs only the fixed upgrade operation; it does not receive a Docker socket or host shell from the search container. A root boot task keeps the worker available after NAS restarts. Service upgrades do not replace the host-worker code; update that code through a separately reviewed administrator process.

If setup stops after creating the protected directory, preserve it and resume from an updated checkout:

```sh
sudo python3 scripts/enable-upgrades.py --resume \
  --image ghcr.io/OWNER/nas-search-mcp:COMMIT_SHA
```

Resume requires complete root-owned settings, a protected worker, and the expected queue. It keeps the saved credentials and configuration. Do not delete the protected folder to retry. Inspect its private setup log if recovery fails.

In DSM Task Scheduler, create a root **Triggered Task → User-defined script** for the **Boot-up** event. Use the protected worker path shown by setup:

```sh
/usr/bin/python3 PROTECTED_DIR/upgrade-host.py \
  --install-dir PROTECTED_DIR \
  >> PROTECTED_DIR/upgrade-host.log 2>&1
```

The worker serializes upgrades with a host lock. Requests remain queued if it is stopped. `upgrade_status` reports `queued`, `running`, `succeeded`, or `failed`; after failure, inspect the host's private current-image record to identify the running image. A stuck installer can remain running; inspect the host and Docker before intervening.

## Photo search

Photo search can find visible subjects in JPEG, PNG, WebP, GIF, BMP, TIFF, and HEIC/HEIF images. Camera RAW, face recognition, and image OCR are unsupported. Each search returns ranked candidates, including when none are a good match. Public result links require fresh browser OAuth sign-in.

Photo embeddings are disabled by default. To enable them, first verify the authorized photo folder's physical path, then update the existing values in the protected deployment `.env`:

```sh
NAS_PHOTOS=true
NAS_PHOTOS_PATH=/path/to/authorized-photos
```

Do not add duplicate setting lines. Apply the settings through the normal deployment upgrade. Check `index_status`; photos become searchable as indexing completes. Enabling photos keeps the document index. To turn photo embeddings off, set `NAS_PHOTOS=false` and redeploy.

The service uses both CPU cores by default, with low CPU shares so DSM keeps priority when the NAS is busy. Synology's kernel cannot cap CPU time, so shares limit contention instead. Set `NAS_CPUSET=0` in the protected `.env` to pin the service to one core, or `NAS_CPU_SHARES` to change the share (default 512; Docker's default is 1024). Measure DSM responsiveness during a scan before relying on this.

Photo indexing uses local CLIP models, downloaded on first use, and can keep one CPU busy. Measure memory, indexing time, and search quality on the target NAS before expanding the collection. Development measurements are not NAS performance guarantees.

For named-person lookup, prefer the optional [Synology Photos connector](synology-photos.md),
which reuses existing People albums and thumbnails. This experimental visual
index is a separate feature and does not enable that connector. Do not change
the existing photo-index setting solely to enable people lookup.

For an isolated pilot, start with a small read-only folder and keep the document service running:

```sh
sudo sh scripts/photo-pilot.sh \
  --image ghcr.io/OWNER/nas-search-mcp:COMMIT_SHA \
  --photos-path /path/to/small-photo-folder \
  --dry-run
```

Remove `--dry-run` to start it. The pilot uses a separate container, port, and data directory. Monitor DSM and the container during model download, indexing, and search. Stop the pilot if it affects NAS responsiveness. Do not expand the collection until you have checked memory, descriptive search, and continued document-service availability.

## Manual Container Manager setup

If you prefer DSM to the installer, create a private deployment folder and copy the repository there, preserving its directory structure. Copy `.env.example` to `.env`. Set a random `NAS_TOKEN` of at least 24 characters; keep `.env` private. Configure the source folders, index directory, container user/group, and bind address using the setting names shown in `.env.example`. Grant the container user read access to source folders and write access to the index directory. Mount sources read-only and keep the index outside them.

In Container Manager, create a Project using the deployment folder and `compose.yaml`, then build and start it. Building on the NAS can be slow and is not covered by the running container's resource limits. For a published image, use its full commit-SHA tag and remove `build: .` from the deployment copy.

## Check the deployment

Check container logs for scan, extraction, and model errors. First model use requires outbound internet access. On a computer that can reach the NAS, check the health endpoint:

```sh
curl --fail http://NAS_LAN_ADDRESS:8080/healthz
```

This confirms only that the HTTP process is alive. Connect an MCP client and call `index_status` to check indexing and errors; then search for a phrase from a known pilot document and compare the result with its source.

A 401 from `/mcp` means the bearer token is missing or incorrect. A refused or timed-out connection suggests checking container status, the published bind address, DSM firewall, and network routing. Permission errors require checking source/data ACLs for the configured numeric user and group. Repeated restarts near the memory cap indicate the workload exceeds its limit; keep photo embeddings disabled and inspect logs.

For remote access, use an endpoint your device can reach through your existing personal Tailscale setup or a correctly configured HTTPS proxy. Preserve existing work Tailscale access and local-LAN access. Do not switch accounts or exit nodes to make the service work. LAN HTTP sends the bearer token without TLS; use it only on a trusted LAN. QuickConnect's DSM URL is not the MCP endpoint.
