#!/bin/sh
# Run on the NAS with sudo. Original files and existing secrets/index are preserved.
set -eu
umask 077
fail() { printf '%s\n' "$*" >&2; exit 1; }
image= install_dir=/volume1/docker/nas-search documents= photos= code=/volume1/shared-code data= bind=127.0.0.1 dry=false
uid=${SUDO_UID:-$(id -u)}
gid=${SUDO_GID:-$(id -g)}
while [ "$#" -gt 0 ]; do
    case "$1" in
        --dry-run) dry=true; shift ;;
        --image|--install-dir|--documents-path|--photos-path|--code-path|--data-path|--bind-ip|--uid|--gid)
            [ "$#" -ge 2 ] || fail "Missing value for $1"
            case "$1" in
                --image) image=$2;; --install-dir) install_dir=$2;; --documents-path) documents=$2;;
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
case "$bind" in ''|*[!0-9.]* ) fail 'Bind address must be an explicit IPv4 address';; esac
for path in "$install_dir" "$documents" "$photos" "$code" "$data"; do
    case "$path" in /*) :;; *) fail 'All source/install/data paths must be explicit absolute paths';; esac
    case "$path" in */../*|*/..|*/./*|*/.) fail 'Paths must not contain dot traversal segments';; esac
    case "$path" in *[!a-zA-Z0-9_./\ -]*) fail 'Paths must use letters, digits, spaces, underscores, dots, slashes, or hyphens';; esac
done
[ -d "$documents" ] && [ -d "$photos" ] && [ -d "$code" ] || fail 'Selected source directories must already exist'
# Resolve symlinked existing ancestors without creating anything during preflight.
canonical() {
    if [ -e "$1" ] || [ -L "$1" ]; then
        readlink -f "$1" || return 1
    else
        parent=$(canonical "$(dirname "$1")") || return 1
        printf '%s/%s\n' "$parent" "$(basename "$1")"
    fi
}
install_dir=$(canonical "$install_dir") || fail 'Cannot resolve install path'
existing_env=false
[ ! -L "$install_dir/.env" ] || fail 'Refusing symlink .env; credentials must be a regular private file'
if [ -f "$install_dir/.env" ]; then
    existing_env=true
    # Parse only deployment fields, never the token. Do not source arbitrary shell.
    field() { awk -v key="$1" 'index($0,key "=")==1 {print substr($0,length(key)+2); exit}' "$install_dir/.env" | tr -d "\"'"; }
    documents=$(field NAS_DOCUMENTS_PATH)
    photos=$(field NAS_PHOTOS_PATH)
    saved_code=$(field NAS_CODE_PATH)
    code=${saved_code:-/volume1/shared-code}
    data=$(field NAS_INDEX_PATH)
    allowed=$(field NAS_ALLOWED_FOLDERS)
    uid=$(field NAS_UID)
    gid=$(field NAS_GID)
    [ -d "$documents" ] && [ -d "$photos" ] && [ -d "$code" ] && [ -d "$data" ] || fail 'Existing .env source and index directories must exist; refusing automatic recreation'
    case "$uid:$gid" in *[!0-9:]*|:*|*:) fail 'Existing .env UID/GID must be numeric';; esac
fi
documents=$(canonical "$documents") || fail 'Cannot resolve Documents source'
photos=$(canonical "$photos") || fail 'Cannot resolve Photos source'
code=$(canonical "$code") || fail 'Cannot resolve Code source'
data=$(canonical "$data") || fail 'Cannot resolve index path'
install_dir=$(canonical "$install_dir") || fail 'Cannot resolve install path'
for source in "$documents" "$photos" "$code"; do
    case "$data/" in "$source/"*) fail 'Index data must be outside source directories';; esac
    case "$install_dir/" in "$source/"*) fail 'Installer state must be outside source directories';; esac
done
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
    if [ -z "$allowed" ]; then
        awk 'BEGIN {value="NAS_ALLOWED_FOLDERS=Documents,Code,Photos"} /^NAS_ALLOWED_FOLDERS=/ {if (!found++) print value; next} {print} END {if (!found) print value}' "$install_dir/.env" > "$install_dir/.env.tmp" || fail 'Cannot set source access scope'
        chmod 600 "$install_dir/.env.tmp" && chown 0:0 "$install_dir/.env.tmp" && mv "$install_dir/.env.tmp" "$install_dir/.env" || fail 'Cannot save source access scope'
    fi
fi
# No deployment/index state is changed before image/actual kernel limit validation.
docker pull "$image"
docker run --rm --memory 1g --memory-swap 1g --cpuset-cpus 0 --entrypoint /bin/true "$image" || fail 'Memory/CPU-affinity probe failed; nothing deployed'
mkdir -p "$install_dir"
if [ ! -f "$install_dir/.env" ]; then
    token=$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')
    [ "${#token}" -eq 64 ] || fail 'Unable to generate a private random token'
    {
        printf 'NAS_TOKEN=%s\n' "$token"
        printf "NAS_DOCUMENTS_PATH='%s'\nNAS_PHOTOS_PATH='%s'\nNAS_CODE_PATH='%s'\nNAS_INDEX_PATH='%s'\n" "$documents" "$photos" "$code" "$data"
        printf 'NAS_UID=%s\nNAS_GID=%s\nNAS_BIND_IP=%s\nNAS_CPUSET=0\nNAS_PHOTOS=false\nNAS_ALLOWED_FOLDERS=Documents,Code,Photos\n' "$uid" "$gid" "$bind"
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
docker run --rm --user "$uid:$gid" --memory 1g --memory-swap 1g --cpuset-cpus 0 \
    -v "$documents:/check-documents:ro" -v "$photos:/check-photos:ro" -v "$code:/check-code:ro" -v "$data:/check-data" \
    --entrypoint python3 "$image" -c 'import os,tempfile; assert os.access("/check-documents",os.R_OK|os.X_OK); assert os.access("/check-photos",os.R_OK|os.X_OK); assert os.access("/check-code",os.R_OK|os.X_OK); f=tempfile.NamedTemporaryFile(dir="/check-data"); f.write(b"probe"); f.flush(); f.close()' \
    || fail 'Container user cannot read selected sources or write index data; fix DSM permissions before deployment'
# The deployment copy is pull-only; upstream resource and security defaults remain.
previous_compose=
if [ -f "$install_dir/compose.yaml" ]; then
    previous_compose=$install_dir/compose.yaml.previous
    cp "$install_dir/compose.yaml" "$previous_compose"
fi
awk -v image="$image" '/^[ ]*build:/ {next} /^[ ]*image:/ {print "    image: " image; next} /^[ ]*cpus:/ {next} /^[ ]*pids_limit:/ {next} {print}' "$project_dir/compose.yaml" > "$install_dir/compose.yaml.tmp"
mv "$install_dir/compose.yaml.tmp" "$install_dir/compose.yaml"
previous=
[ ! -f "$install_dir/current-image" ] || previous=$(cat "$install_dir/current-image")
compose() { docker compose --project-name nas-search --project-directory "$install_dir" -f "$install_dir/compose.yaml" "$@"; }
rollback() {
    if [ -n "$previous" ]; then
        printf 'Deployment checks failed; rolling back to the previous image.\n' >&2
        if [ -n "$previous_compose" ]; then
            cp "$previous_compose" "$install_dir/compose.yaml"
        else
            sed "s|^    image:.*|    image: $previous|" "$install_dir/compose.yaml" > "$install_dir/compose.yaml.tmp"
            mv "$install_dir/compose.yaml.tmp" "$install_dir/compose.yaml"
        fi
        compose up -d --no-build || printf 'Rollback failed; inspect Container Manager.\n' >&2
    else
        printf 'Deployment checks failed; stopping the new service. Index and .env are preserved.\n' >&2
        compose stop || true
    fi
    exit 1
}
compose up -d --no-build || rollback
# Check authenticated MCP status inside the container, without exposing tokens in args.
check='import json,os,urllib.request
url="http://127.0.0.1:8080"
assert urllib.request.urlopen(url+"/healthz",timeout=5).status==200
body={"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"index_status","arguments":{}}}
r=urllib.request.Request(url+"/mcp",data=json.dumps(body).encode(),headers={"Authorization":"Bearer "+os.environ["NAS_TOKEN"],"Content-Type":"application/json","Accept":"application/json, text/event-stream"})
reply=urllib.request.urlopen(r,timeout=15).read().decode()
if not reply.lstrip().startswith("{"): reply="\n".join(line[5:].strip() for line in reply.splitlines() if line.startswith("data:"))
result=json.loads(reply)
assert "result" in result and not result["result"].get("isError",False), "Authenticated index_status failed"
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
