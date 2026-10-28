#!/bin/sh
# Run on the NAS with sudo. Original files and existing secrets/index are preserved.
set -eu
umask 077
fail() { printf '%s\n' "$*" >&2; exit 1; }
image= install_dir= documents= photos= code= data= bind=127.0.0.1 documents_label=Documents dry=false upload_enabled=false upload_indexed=false upload_path=
synology_path= synology_socket=
uid=${SUDO_UID:-$(id -u)}
gid=${SUDO_GID:-$(id -g)}
while [ "$#" -gt 0 ]; do
    case "$1" in
        --dry-run) dry=true; shift ;;
        --image|--install-dir|--documents-path|--documents-label|--photos-path|--code-path|--data-path|--bind-ip|--uid|--gid)
            [ "$#" -ge 2 ] || fail "Missing value for $1"
            case "$1" in
                --image) image=$2;; --install-dir) install_dir=$2;; --documents-path) documents=$2;; --documents-label) documents_label=$2;;
                --photos-path) photos=$2;; --code-path) code=$2;; --data-path) data=$2;; --bind-ip) bind=$2;; --uid) uid=$2;; --gid) gid=$2;;
            esac
            shift 2 ;;
        *) fail "Unknown option: $1" ;;
    esac
done
[ -n "$image" ] || fail 'Specify --image with a versioned tag or SHA digest'
case "$image" in *:latest|*[!a-zA-Z0-9._/:@-]*|*:|*@) fail 'Use an explicit versioned image tag or SHA digest; latest is rejected';; esac
case "${image##*/}" in *:*|*@sha256:*) :;; *) fail 'Image needs an explicit versioned tag or digest';; esac
case "$uid:$gid" in *[!0-9:]*|:*|*:) fail 'UID/GID must be numeric';; esac
case "$documents_label" in ''|*[!a-zA-Z0-9_-]*) fail 'Document label must use letters, digits, underscores, or hyphens';; esac
case "$bind" in ''|*[!0-9.]* ) fail 'Bind address must be an explicit IPv4 address';; esac
# Discover the running deployment before selecting any configuration or credentials.
[ "$(id -u)" = 0 ] || fail 'Run on the NAS with sudo/root Docker access'
PATH=/usr/local/bin:$PATH
export PATH
command -v docker >/dev/null 2>&1 || fail 'Docker is required; install Synology Container Manager'
if [ -z "$install_dir" ]; then
    if detected=$(docker inspect --format '{{ index .Config.Labels "com.docker.compose.project.working_dir" }}' nas-search-nas-search-1 2>/dev/null); then
        [ -n "$detected" ] || fail 'Active deployment has no working-directory label; specify --install-dir'
        install_dir=$detected
    else
        containers=$(docker ps -a --filter 'name=^/nas-search-nas-search-1$' --format '{{.Names}}') || fail 'Cannot discover active deployment'
        [ -z "$containers" ] || fail 'Cannot inspect active deployment; refusing default configuration'
        fail 'No active deployment found; specify --install-dir'
    fi
fi
for path in "$install_dir" "$documents" "$photos" "$code" "$data"; do
    [ -n "$path" ] || continue
    case "$path" in /*) :;; *) fail 'All source/install/data paths must be explicit absolute paths';; esac
    case "$path" in */../*|*/..|*/./*|*/.) fail 'Paths must not contain dot traversal segments';; esac
    case "$path" in *[!a-zA-Z0-9_./@\ -]*) fail 'Paths must use letters, digits, spaces, underscores, dots, slashes, at signs, or hyphens';; esac
done
# Resolve symlinked existing ancestors without creating anything during preflight.
canonical() {
    if [ -e "$1" ] || [ -L "$1" ]; then
        readlink -f "$1" || return 1
    else
        parent=$(canonical "$(dirname "$1")") || return 1
        printf '%s/%s\n' "${parent%/}" "$(basename "$1")"
    fi
}
install_dir=$(canonical "$install_dir") || fail 'Cannot resolve install path'
existing_env=false
cpuset=0-1
cpu_shares=512
[ ! -L "$install_dir/.env" ] || fail 'Refusing symlink .env; credentials must be a regular private file'
if [ -f "$install_dir/.env" ]; then
    existing_env=true
    # Restrict NAS assignments to one canonical spelling. Compose accepts aliases
    # and duplicate last values that would disagree with our field reader.
    awk '
        /^[[:space:]]*(export[[:space:]]+)?NAS_[A-Za-z0-9_]+[[:space:]]*=/ {
            if ($0 !~ /^NAS_[A-Za-z0-9_]+=/) exit 1
            key=$0; sub(/=.*/, "", key)
            if (seen[key]++) exit 1
        }
    ' "$install_dir/.env" || fail 'Existing .env has duplicate or unsupported NAS assignments; use one NAS_KEY=value line per setting'
    # Parse only deployment fields, never the token. Do not source arbitrary shell.
    field() {
        awk -v key="$1" -v sq="'" '
            index($0,key "=")==1 {
                value=substr($0,length(key)+2)
                sub(/^[[:space:]]+/, "", value); sub(/[[:space:]]+$/, "", value)
                first=substr(value,1,1)
                if (first == sq || first == "\"") {
                    if (length(value)<2 || substr(value,length(value),1)!=first) exit 1
                    value=substr(value,2,length(value)-2)
                }
                if (index(value,sq) || index(value,"\"")) exit 1
                print value; exit
            }
        ' "$install_dir/.env" || fail 'Unsupported quoting in deployment setting'
    }
    saved_documents=$(field NAS_DOCUMENTS_PATH)
    documents=${saved_documents:-$documents}
    photos=$(field NAS_PHOTOS_PATH)
    saved_code=$(field NAS_CODE_PATH)
    code=${saved_code:-$code}
    saved_label=$(field NAS_DOCUMENTS_LABEL)
    documents_label=${saved_label:-$documents_label}
    data=$(field NAS_INDEX_PATH)
    if grep -q '^NAS_BIND_IP=' "$install_dir/.env"; then bind=$(field NAS_BIND_IP)
    else bind=$(field NAS_LAN_IP); bind=${bind:-127.0.0.1}; fi
    queue=$(field NAS_UPGRADE_PATH)
    if grep -q '^NAS_CPUSET=' "$install_dir/.env"; then cpuset=$(field NAS_CPUSET); fi
    if grep -q '^NAS_CPU_SHARES=' "$install_dir/.env"; then cpu_shares=$(field NAS_CPU_SHARES); fi
    upload_enabled=$(field NAS_UPLOAD_ENABLED)
    upload_indexed=$(field NAS_UPLOAD_INDEXED_DESTINATIONS)
    upload_path=$(field NAS_UPLOAD_PATH)
    synology_path=$(field NAS_SYNOLOGY_PHOTOS_HOST_PATH)
    synology_socket=$(field NAS_SYNOLOGY_PHOTOS_SOCKET)
    uid=$(field NAS_UID)
    gid=$(field NAS_GID)
    [ -d "$documents" ] && [ -d "$photos" ] && [ -d "$code" ] && [ -d "$data" ] || fail 'Existing .env source and index directories must exist; refusing automatic recreation'
    case "$uid:$gid" in *[!0-9:]*|:*|*:) fail 'Existing .env UID/GID must be numeric';; esac
fi
[ -d "$documents" ] && [ -d "$photos" ] && [ -d "$code" ] || fail 'Selected source directories must already exist'
case "$documents_label" in ''|*[!a-zA-Z0-9_-]*) fail 'Document label must use letters, digits, underscores, or hyphens';; esac
# Validate effective loaded paths as well as command-line paths.
queue=${queue:-$install_dir/upgrades}
upload_enabled=${upload_enabled:-false}
upload_indexed=${upload_indexed:-false}
case "$upload_enabled" in true|false) :;; *) fail 'NAS_UPLOAD_ENABLED must be true or false';; esac
case "$upload_indexed" in true|false) :;; *) fail 'NAS_UPLOAD_INDEXED_DESTINATIONS must be true or false';; esac
[ "$upload_indexed" != true ] || [ "$upload_enabled" = true ] || fail 'Indexed upload destinations require uploads to be enabled'
for path in "$documents" "$photos" "$code" "$data" "$queue" "$upload_path"; do
    [ -n "$path" ] || continue
    case "$path" in /*) :;; *) fail 'Effective mount paths must be absolute';; esac
    case "$path" in */../*|*/..|*/./*|*/.|*[!a-zA-Z0-9_./@\ -]*) fail 'Invalid effective mount path';; esac
done
printf '%s\n' "$cpuset" | LC_ALL=C grep -Eq '^[0-9]+(-[0-9]+)?$' || fail 'NAS_CPUSET must be one CPU (0) or a range (0-1)'
case "$cpuset" in *-*) [ "${cpuset%-*}" -le "${cpuset#*-}" ] || fail 'NAS_CPUSET range must run low to high';; esac
printf '%s\n' "$cpu_shares" | LC_ALL=C grep -Eq '^[0-9]{1,5}$' && [ "$cpu_shares" -ge 2 ] || fail 'NAS_CPU_SHARES must be a number from 2 to 99999'
printf '%s\n' "$bind" | awk -F. 'NF != 4 {exit 1} {for(i=1;i<=4;i++) if ($i !~ /^[0-9]+$/ || length($i)>3 || $i+0>255 || (length($i)>1 && substr($i,1,1)=="0")) exit 1}' || fail 'Bind address must be a valid explicit IPv4 address'
queue=$(canonical "$queue") || fail 'Cannot resolve upgrade queue'
documents=$(canonical "$documents") || fail 'Cannot resolve selected source'
if [ "$upload_enabled" = true ] && [ "$upload_indexed" != true ]; then
    [ -n "$upload_path" ] && [ -d "$upload_path" ] && [ ! -L "$upload_path" ] || fail 'Upload folder must already exist as a real directory'
    upload_path=$(canonical "$upload_path") || fail 'Cannot resolve upload folder'
    [ "$upload_path" = "$documents/Uploads" ] || fail 'Upload folder must be the Documents/Uploads subfolder'
fi
photos=$(canonical "$photos") || fail 'Cannot resolve Photos source'
code=$(canonical "$code") || fail 'Cannot resolve Code source'
data=$(canonical "$data") || fail 'Cannot resolve index path'
install_dir=$(canonical "$install_dir") || fail 'Cannot resolve install path'
overlap() { [ "$1" = / ] || [ "$2" = / ] && return 0; case "$1/" in "$2/"*) return 0;; esac; case "$2/" in "$1/"*) return 0;; esac; return 1; }
if [ "$upload_indexed" = true ]; then
    overlap "$documents" "$photos" && fail 'Indexed upload source roots must not overlap'
    overlap "$documents" "$code" && fail 'Indexed upload source roots must not overlap'
    overlap "$photos" "$code" && fail 'Indexed upload source roots must not overlap'
fi
for source in "$documents" "$photos" "$code"; do
    overlap "$data" "$source" && fail 'Index data must not overlap source directories'
    overlap "$queue" "$source" && fail 'Upgrade queue must not overlap source directories'
    case "$install_dir/" in "$source/"*) fail 'Installer state must be outside source directories';; esac
done
overlap "$data" "$queue" && fail 'Index data and upgrade queue must not overlap'
case "$install_dir/" in "$data/"*) fail 'Index data must not contain installer state';; esac
case "$install_dir/" in "$queue/"*) fail 'Upgrade queue must not contain installer state';; esac
if [ -n "$synology_path" ] || [ -n "$synology_socket" ]; then
    [ -n "$synology_path" ] && [ "$synology_socket" = /synology-photos/photos.sock ] || fail 'Photos broker host path and exact socket setting must be configured together'
    case "$synology_path" in /*) :;; *) fail 'Photos broker path must be absolute';; esac
    case "$synology_path" in */../*|*/..|*/./*|*/.|*[!a-zA-Z0-9_./@\ -]*) fail 'Invalid Photos broker mount path';; esac
    [ -d "$synology_path" ] && [ ! -L "$synology_path" ] || fail 'Photos broker directory must already exist without symlinks'
    physical=$(canonical "$synology_path") || fail 'Cannot resolve Photos broker path'
    [ "$physical" = "$synology_path" ] || fail 'Photos broker path must be normalized and physical'
    for other in "$documents" "$photos" "$code" "$data" "$queue"; do
        overlap "$synology_path" "$other" && fail 'Photos broker socket directory must not overlap sources, data or upgrade queue'
    done
    case "$install_dir/" in "$synology_path/"*) fail 'Photos broker directory must not contain installer state';; esac
fi
[ "$(id -u)" = 0 ] || fail 'Run on the NAS with sudo/root Docker access'
PATH=/usr/local/bin:$PATH
export PATH
command -v docker >/dev/null 2>&1 || fail 'Docker is required; install Synology Container Manager'
info=$(docker info 2>&1) || fail 'Cannot access Docker daemon'
case "$info" in *'No memory limit support'*|*'No cpuset support'*) fail 'Kernel must support memory limits and CPU affinity';; esac
docker compose version >/dev/null 2>&1 || fail 'Docker Compose v2 is required'
project_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
[ -f "$project_dir/compose.yaml" ] || fail 'Run installer from the complete project checkout'
if [ "$dry" = true ]; then
    printf 'Dry run: Docker reachable. Would pull %s, validate memory/CPU affinity, then deploy in %s. Existing .env and index will be preserved.\n' "$image" "$install_dir"
    exit 0
fi
# Existing credential contents are preserved; restrict access before deployment.
if [ "$existing_env" = true ]; then
    chmod 600 "$install_dir/.env" || fail 'Cannot secure existing .env permissions'
    chown 0:0 "$install_dir/.env" || fail 'Cannot secure existing .env ownership'
fi
# No deployment/index state is changed before image/actual kernel limit validation.
docker pull "$image"
docker run --rm --memory 1g --memory-swap 1g --cpuset-cpus "$cpuset" --cpu-shares "$cpu_shares" --entrypoint /bin/true "$image" || fail 'Memory/CPU-affinity probe failed; nothing deployed'
mkdir -p "$install_dir"
if [ ! -f "$install_dir/.env" ]; then
    token=$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')
    [ "${#token}" -eq 64 ] || fail 'Unable to generate a private random token'
    {
        printf 'NAS_TOKEN=%s\n' "$token"
        printf "NAS_DOCUMENTS_PATH='%s'\nNAS_DOCUMENTS_LABEL=%s\nNAS_PHOTOS_PATH='%s'\nNAS_CODE_PATH='%s'\nNAS_INDEX_PATH='%s'\n" "$documents" "$documents_label" "$photos" "$code" "$data"
        printf 'NAS_UID=%s\nNAS_GID=%s\nNAS_BIND_IP=%s\nNAS_CPUSET=%s\nNAS_CPU_SHARES=%s\nNAS_PHOTOS=false\nNAS_ALLOWED_FOLDERS=%s,Code,Photos\n' "$uid" "$gid" "$bind" "$cpuset" "$cpu_shares" "$documents_label"
    } > "$install_dir/.env"
    unset token
    chmod 600 "$install_dir/.env"
    chown 0:0 "$install_dir/.env" || fail 'Cannot secure new .env ownership'
    if [ ! -d "$data" ]; then
        mkdir -p "$data"
        chown "$uid:$gid" "$data"
    fi
else
    printf 'Keeping existing .env unchanged; its paths, user, and bind address take precedence.\n'
fi
# Verify mount access as the actual container user. Probe only the index directory
# using a temporary file that is removed immediately; original sources stay read-only.
docker run --rm --user "$uid:$gid" --memory 1g --memory-swap 1g --cpuset-cpus "$cpuset" --cpu-shares "$cpu_shares" \
    -v "$documents:/check-documents:ro" -v "$photos:/check-photos:ro" -v "$code:/check-code:ro" -v "$data:/check-data" \
    --entrypoint python3 "$image" -c 'import os,tempfile; assert os.access("/check-documents",os.R_OK|os.X_OK); assert os.access("/check-photos",os.R_OK|os.X_OK); assert os.access("/check-code",os.R_OK|os.X_OK); f=tempfile.NamedTemporaryFile(dir="/check-data"); f.write(b"probe"); f.flush(); f.close()' \
    || fail 'Container user cannot read selected sources or write index data; fix DSM permissions before deployment'
if [ "$upload_enabled" = true ] && [ "$upload_indexed" != true ]; then
    docker run --rm --user "$uid:$gid" --memory 1g --memory-swap 1g --cpuset-cpus "$cpuset" --cpu-shares "$cpu_shares" \
        -v "$upload_path:/check-upload" --entrypoint python3 "$image" \
        -c 'import tempfile; f=tempfile.NamedTemporaryFile(dir="/check-upload"); f.write(b"probe"); f.flush(); f.close()' \
        || fail 'Container user cannot write the dedicated upload folder; fix DSM permissions before deployment'
fi
if [ "$upload_indexed" = true ]; then
    [ ! -L "$data/upload-ipc" ] || fail 'Upload IPC directory must not be a symlink'
    if [ ! -e "$data/upload-ipc" ]; then
        mkdir -m 700 "$data/upload-ipc" || fail 'Cannot create upload IPC directory'
        chown "$uid:$gid" "$data/upload-ipc" || fail 'Cannot assign upload IPC directory'
    fi
    [ -d "$data/upload-ipc" ] || fail 'Upload IPC path must be a directory'
    python3 -c 'import os,stat,sys; sys.exit(0 if stat.S_IMODE(os.stat(sys.argv[1],follow_symlinks=False).st_mode)==0o700 else 1)' "$data/upload-ipc" || fail 'Upload IPC directory must have mode 700'
    docker run --rm --user "$uid:$gid" --memory 96m --memory-swap 96m --cpuset-cpus "$cpuset" --cpu-shares "$cpu_shares" \
        -v "$data/upload-ipc:/check-ipc" --entrypoint python3 "$image" \
        -c 'import tempfile; f=tempfile.NamedTemporaryFile(dir="/check-ipc"); f.write(b"probe"); f.flush(); f.close()' \
        || fail 'Container user cannot write the upload IPC directory'
fi
# The deployment copy is pull-only; upstream resource and security defaults remain.
previous_compose=
if [ -f "$install_dir/compose.yaml" ]; then
    previous_compose=$install_dir/compose.yaml.previous
    cp "$install_dir/compose.yaml" "$previous_compose"
fi
awk -v image="$image" -v bind="$bind" -v uploads="$upload_enabled" -v indexed="$upload_indexed" -v photos_socket_dir="$synology_path" '
    /^  [A-Za-z0-9_-]+:/ {service=$1; sub(/:$/, "", service)}
    /NAS_BIND_IP.*8080:8080/ {print "      - \"" bind ":8080:8080\""; if (bind != "127.0.0.1" && bind != "0.0.0.0") print "      - \"127.0.0.1:8080:8080\""; next}
    /^[ ]*build:/ {next}
    /^[ ]*image:/ {print "    image: " image; next}
    /^[ ]*cpus:/ {next}
    /^[ ]*pids_limit:/ {next}
    /^    volumes:/ {print; if (service == "nas-search" && photos_socket_dir != "") print "      - \"" photos_socket_dir ":/synology-photos:ro\""; next}
    {print; if (uploads == "true" && indexed != "true" && /:\/data"$/) print "      - \"${NAS_UPLOAD_PATH}:/uploads:rw\""}
' "$project_dir/compose.yaml" > "$install_dir/compose.yaml.tmp"
mv "$install_dir/compose.yaml.tmp" "$install_dir/compose.yaml"
previous=
[ ! -f "$install_dir/current-image" ] || previous=$(cat "$install_dir/current-image")
compose() (
    # The checked private file is authoritative. Ambient overrides could otherwise
    # reopen denied folders, change credentials, or bypass mount-path validation.
    for name in $(env | sed -n 's/^\(NAS_[A-Za-z0-9_]*\)=.*/\1/p'); do
        unset "$name"
    done
    if [ "$upload_indexed" = true ]; then
        docker compose --profile indexed-upload --env-file "$install_dir/.env" --project-name nas-search --project-directory "$install_dir" -f "$install_dir/compose.yaml" "$@"
    else
        docker compose --env-file "$install_dir/.env" --project-name nas-search --project-directory "$install_dir" -f "$install_dir/compose.yaml" "$@"
    fi
)
rollback() {
    if [ -n "$previous" ]; then
        printf 'Deployment checks failed; rolling back to the previous image.\n' >&2
        if [ -n "$previous_compose" ]; then
            cp "$previous_compose" "$install_dir/compose.yaml"
        else
            sed "s|^    image:.*|    image: $previous|" "$install_dir/compose.yaml" > "$install_dir/compose.yaml.tmp"
            mv "$install_dir/compose.yaml.tmp" "$install_dir/compose.yaml"
        fi
        compose up -d --no-build --remove-orphans || printf 'Rollback failed; inspect Container Manager.\n' >&2
    else
        printf 'Deployment checks failed; stopping the new service. Index and .env are preserved.\n' >&2
        compose stop || true
    fi
    exit 1
}
compose up -d --no-build --remove-orphans || rollback
if [ "$upload_indexed" = true ]; then
    writer_ready=false
    writer_attempt=0
    while [ "$writer_attempt" -lt 20 ]; do
        if compose exec -T nas-upload-writer /app/nas-search upload-writer-health >/dev/null 2>&1; then writer_ready=true; break; fi
        writer_attempt=$((writer_attempt + 1))
        sleep 1
    done
    [ "$writer_ready" = true ] || rollback
else
    # A previously enabled writer must not retain writable source mounts.
    old_writer=$(docker ps -q --filter 'name=^/nas-search-nas-upload-writer-1$') || rollback
    [ -z "$old_writer" ] || docker stop "$old_writer" >/dev/null || rollback
fi
# Check authenticated MCP status inside the container, without exposing tokens in args.
check='import json,os,urllib.request,urllib.error
url="http://127.0.0.1:8080"
assert urllib.request.urlopen(url+"/healthz",timeout=5).status==200
body={"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"index_status","arguments":{}}}
r=urllib.request.Request(url+"/mcp",data=json.dumps(body).encode(),headers={"Authorization":"Bearer "+os.environ["NAS_TOKEN"],"Content-Type":"application/json","Accept":"application/json, text/event-stream"})
reply=urllib.request.urlopen(r,timeout=15).read().decode()
if not reply.lstrip().startswith("{"): reply="\n".join(line[5:].strip() for line in reply.splitlines() if line.startswith("data:"))
result=json.loads(reply)
assert "result" in result and not result["result"].get("isError",False), "Authenticated index_status failed"
issuer=os.environ.get("NAS_OAUTH_ISSUER", "")
if issuer:
    oauth="http://127.0.0.1:8082"
    metadata=urllib.request.urlopen(oauth+"/.well-known/oauth-authorization-server",timeout=5)
    assert metadata.status==200
    assert json.loads(metadata.read())["issuer"]==issuer, "OAuth issuer changed"
    try:
        urllib.request.urlopen(oauth+"/mcp",timeout=5)
    except urllib.error.HTTPError as error:
        assert error.code==401 and "resource_metadata" in error.headers.get("WWW-Authenticate", ""), "OAuth discovery challenge missing"
    else:
        raise AssertionError("Anonymous OAuth MCP must require authentication")
'
ready=false
for attempt in 1 2 3; do
    if compose exec -T nas-search python3 -c "$check" >/dev/null 2>&1; then ready=true; break; fi
    sleep 2
done
[ "$ready" = true ] || rollback
[ -z "$previous_compose" ] || rm -f "$previous_compose"
printf '%s\n' "$image" > "$install_dir/current-image.tmp"
mv "$install_dir/current-image.tmp" "$install_dir/current-image"
printf 'Installed %s. HTTP health and authenticated index_status passed. Initial indexing may still be running; inspect status/logs. Credentials remain private in %s/.env.\n' "$image" "$install_dir"
