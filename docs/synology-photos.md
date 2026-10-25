# Find people with Synology Photos

Use the NAS's existing Synology Photos People albums through MCP. This avoids a
second face-recognition model or CLIP photo index. Document semantic search stays
as it is. Names and group membership come from Synology Photos and can be wrong
or incomplete; the assistant does not identify people from image pixels.

The first integration supports the configured local account's **Personal Space**.
Setup derives that account from the existing private app UID and freezes its
identity in root-only settings. Requests cannot choose another account. It does
not expose Shared Space or other users' libraries. A new separate DSM account
does not automatically inherit another user's personal People albums.

## Tools

- `photos_people`: find existing named People albums. `query` matches label text,
  ignoring case. Hidden and unnamed people are omitted.
- `photos_for_person`: list matching photo/video IDs, filenames and capture times.
  Each result must explicitly include the requested person in Synology's returned
  membership; a potentially ignored person filter fails instead of inventing a
  match.
- `photos_preview`: return a medium JPEG thumbnail as MCP image content. It checks
  the photo's person membership again, then asks Synology for a ready thumbnail.
  A People album's cover ID is a face identifier; use a photo ID from the second
  tool, not the album's cover.

Ask: **“Find photos of Alex and show three previews.”** The assistant first
resolves the label, requests a small page of matches, then requests previews.
Client support determines how images appear. Original downloads and public photo
links are not part of these tools. HEIC originals can be listed; previews depend
on Synology having generated its JPEG thumbnail.

Paging defaults to 20 items, up to 100 per request. `next_offset` advances through
the underlying Synology list, including entries excluded by a name query. An empty
filtered page does not prove the person is absent if `next_offset` is present.
The maximum offset is 100000. Capture times are Unix seconds and can be negative
for photographs dated before 1970. Recognition and indexing freshness follow
Synology Photos; changing a label there is reflected by the next lookup.

## Host access and installation

The service does not receive a DSM password, session cookie, Docker socket or
general SSH/shell access. A small root host broker calls only fixed read-only
Synology API methods as the privately configured account. Requests cannot select an account, API,
method, command or path. This restriction is the privilege boundary; the root
API utility itself is not a general read-only credential.

The broker lives in `/volume1/@nas-photos-broker`, outside DSM shared folders.
Its Unix socket is in `run/photos.sock`, owned by the app UID with mode 600.
Only root and that UID can connect. The container mounts the `run` directory
read-only at `/synology-photos`, so replacing the socket after a restart works.
A read-only bind mount does not itself make socket operations read-only; the
broker's operation allowlist enforces that rule. There is no new TCP listener,
public route or Tailscale change.

The current access policy must explicitly allow the whole `Photos` root. Empty
access, subfolder-only access, or any denied Photos subfolder blocks this backend;
we cannot yet map native People groups onto file-subfolder restrictions. Both
the compiled application policy and the broker's per-request private settings
check enforce this. Setup does not widen an existing allowlist.

From a complete reviewed checkout, after the selected commit's image publication
and required CI pass:

```sh
sudo /usr/bin/python3 scripts/enable-synology-photos.py \
  --image ghcr.io/mattyv/nas-search-mcp:COMMIT_SHA
```

Use `--dry-run` first to validate setup without changing it. The helper protects
the code snapshots, preserves existing settings and index, and enables only the
Photos socket settings. It pauses an idle upgrade worker and uses the shared
deployment lock during service replacement. An active/pending upgrade blocks
setup. Review root commands before execution, and never paste passwords into
chat. A failed first setup leaves its protected directory for inspection; do
not delete deployment settings or an index to retry.

The existing root recovery tasks must be installed as described in
[NAS recovery](nas-recovery.md). The updated recovery script also checks the
Photos broker and starts it after reboot or process exit. A broker that starts
before the Photos package is ready can return an unavailable error; a later
request retries the local API. Container startup tolerates an unavailable socket
so normal document/mobile access can recover independently.

An installation without host setup leaves all three tools absent. The normal NAS
installer preserves these settings and their optional mount during later service
upgrades. Direct Compose users must add the read-only directory bind explicitly:
the base file only passes `NAS_SYNOLOGY_PHOTOS_SOCKET` through and does not create
an optional host directory by default.

## Bounds and verification

The broker serializes API calls, limits requests to 20 per minute, uses bounded
request/output sizes, caps child runtime and temporary output, and limits previews
to 512 KiB JPEGs. Temporary image data stays private and is deleted after the
response. The Go client bounds image dimensions and re-encodes previews to remove
embedded metadata before returning MCP image content. It does not rename, merge,
edit, delete or share anything. Access to the
same app UID permits denial of service against its local socket; this is not a
security guarantee against a compromised service account.

Dafny proves the integer paging/identifier bounds and explicit Photos grant/deny
policy used by the compiled Go adapter. Adding denials cannot restore access.
HTTP/JSON, Unicode matching, peer credentials, processes, files, Synology
permissions, saved face assignments and image parsing remain tested adapters
outside that proof. The standard JPEG decoder and re-encoder are not formally proved.

Returned previews and metadata become available to the MCP client and its AI
provider under that client's data policy. Removing temporary NAS bytes does not
remove copies already returned to a chat.

Native probes established Photos 1.9.1 support for personal People listing,
matching items with explicit person membership, and a small JPEG preview.
This Photos version emits a successful CGI response followed
by wrapper error 117 through `synowebapi`; the broker accepts only that observed
combination. The complete deployed MCP flow, resource use and automatic
reboot/outage recovery need their own installation-specific validation. Future
DSM/Photos updates may change these internal interfaces; unsupported responses
fail with a fixed error rather than falling back to database access or credentials.
