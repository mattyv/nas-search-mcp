#!/bin/sh
# Run with sudo on the NAS; the normal service and its index stay untouched.
set -eu
umask 077
fail() { printf '%s\n' "$*" >&2; exit 1; }
image= photos= install_dir=/volume1/docker/nas-search bind=192.168.1.4 dry=false
while [ "$#" -gt 0 ]; do
    case "$1" in
        --dry-run) dry=true; shift;;
        --image|--photos-path|--install-dir|--bind-ip)
            [ "$#" -ge 2 ] || fail "Missing value for $1"
            case "$1" in --image) image=$2;; --photos-path) photos=$2;; --install-dir) install_dir=$2;; --bind-ip) bind=$2;; esac
            shift 2;;
        *) fail "Unknown option: $1";;
    esac
done
printf '%s\n' "$image" | LC_ALL=C grep -Eq '^ghcr\.io/mattyv/nas-search-mcp(@sha256:[a-f0-9]{64}|:[a-f0-9]{40})$' || fail 'Specify the published ghcr.io/mattyv/nas-search-mcp image pinned by SHA256 digest or full commit tag'
printf '%s\n' "$bind" | awk -F. 'NF != 4 {exit 1} {for(i=1;i<=4;i++) if($i !~ /^[0-9]+$/ || $i>255) exit 1}' || fail 'Bind address must be an IPv4 address'
for path in "$photos" "$install_dir"; do
    case "$path" in /*) :;; *) fail 'Specify absolute paths';; esac
    case "$path" in *[!a-zA-Z0-9_./\ -]*|*/../*|*/..|*/./*|*/.) fail 'Paths contain unsupported characters or traversal';; esac
    [ -d "$path" ] || fail 'Photo folder and existing deployment directory must exist'
done
photos=$(readlink -f "$photos") || fail 'Cannot resolve photo folder'
install_dir=$(readlink -f "$install_dir") || fail 'Cannot resolve deployment directory'
case "$photos" in /volume1/homes/matt/Photos|/volume1/homes/matt/Photos/*|/volume1/Matthew|/volume1/Matthew/*) :;; *) fail 'Physical photo folder must be within /volume1/homes/matt/Photos or /volume1/Matthew';; esac
case "$install_dir/" in /volume1/homes/matt/Photos/*|/volume1/Matthew/*) fail 'Pilot state must be outside authorized source roots';; esac
secret=$install_dir/.env
pilot_env=$install_dir/.env-photo-pilot
data=$install_dir/data-photo-pilot
[ -f "$secret" ] && [ ! -L "$secret" ] || fail 'Existing private deployment .env is required'
for path in "$pilot_env" "$data"; do
    [ ! -e "$path" ] && [ ! -L "$path" ] || fail 'Pilot state already exists; refusing to overwrite it'
done
permissions=$(stat -c '%a' "$secret" 2>/dev/null || stat -f '%Lp' "$secret")
case "$permissions" in 400|600) :;; *) fail 'Deployment .env must have mode 600 or 400';; esac
# Read only known fields; deployment files are never executed as shell code.
field() { awk -v key="$1" 'index($0,key "=")==1 {print substr($0,length(key)+2); exit}' "$secret" | tr -d "\"'"; }
token=$(field NAS_TOKEN)
uid=$(field NAS_UID)
gid=$(field NAS_GID)
cpuset=$(field NAS_CPUSET)
denied=$(field NAS_DENIED_FOLDERS)
[ "${#token}" -ge 24 ] || fail 'Deployment token must have at least 24 characters'
case "$token" in *[!a-zA-Z0-9_-]*) fail 'Deployment token has unsupported characters';; esac
case "$uid:$gid" in *[!0-9:]*|:*|*:) fail 'Deployment UID/GID must be numeric';; esac
printf '%s\n' "$cpuset" | LC_ALL=C grep -Eq '^[0-9]+$' || fail 'Deployment CPU affinity must select one CPU'
[ "$(id -u)" = 0 ] || fail 'Run on the NAS with sudo/root Docker access'
PATH=/usr/local/bin:$PATH
export PATH
command -v docker >/dev/null 2>&1 || fail 'Install Synology Container Manager first'
info=$(docker info 2>&1) || fail 'Cannot access Docker daemon'
case "$info" in *'No memory limit support'*|*'No cpuset support'*) fail 'Kernel must support memory limits and CPU affinity';; esac
containers=$(docker ps -a --format '{{.Names}}') || fail 'Cannot check existing Docker containers'
if printf '%s\n' "$containers" | grep -qx nas-search-photo-pilot; then fail 'Photo pilot container already exists; refusing to replace it'; fi
available=$(cat /proc/meminfo | awk '$1=="MemAvailable:" {print $2}') || fail 'Cannot read NAS available memory'
case "$available" in ''|*[!0-9]*) fail 'Cannot determine NAS available memory';; esac
[ "$available" -ge 1310720 ] || fail 'Photo pilot needs at least 1280 MiB available RAM; normal service remains running. Do not launch both on insufficient memory.'
if [ "$dry" = true ]; then
    printf 'Dry run passed. Would start isolated photo pilot at http://%s:8081/mcp with %s mounted read-only. Choose a small sample folder first.\n' "$bind" "$photos"
    exit 0
fi
docker pull "$image"
docker run --rm --memory 1g --memory-swap 1g --cpuset-cpus "$cpuset" --entrypoint /bin/true "$image" || fail 'Memory/CPU-affinity probe failed; nothing deployed'
mkdir "$data"
chown "$uid:$gid" "$data"
docker run --rm --user "$uid:$gid" --memory 1g --memory-swap 1g --cpuset-cpus "$cpuset" \
    --read-only --cap-drop ALL --security-opt no-new-privileges:true \
    -v "$photos:/check-photos:ro" -v "$data:/check-data" --entrypoint python3 "$image" \
    -c 'import os,tempfile; assert os.access("/check-photos",os.R_OK|os.X_OK); f=tempfile.NamedTemporaryFile(dir="/check-data"); f.write(b"probe"); f.flush(); f.close()' \
    || fail 'Container user cannot read photos or write pilot data; fix DSM permissions. Normal service is untouched.'
{
    printf 'NAS_TOKEN=%s\n' "$token"
    printf '%s\n' NAS_ROOT=/files NAS_DATA=/data NAS_SEMANTIC=true NAS_PHOTOS=true NAS_OCR=false NAS_ALLOWED_FOLDERS=Photos NAS_SCAN_INTERVAL=300 NAS_VEC_EXTENSION=/opt/vec0.so NAS_WORKER=/app/worker/worker.py GOMEMLIMIT=128MiB GOMAXPROCS=1 OMP_THREAD_LIMIT=1 HOME=/data HF_HOME=/data/models/huggingface
} > "$pilot_env"
if [ -n "$denied" ]; then printf 'NAS_DENIED_FOLDERS=%s\n' "$denied" >> "$pilot_env"; fi
unset token
chmod 600 "$pilot_env"
docker run -d --name nas-search-photo-pilot --init \
    --user "$uid:$gid" --env-file "$pilot_env" --memory 1g --memory-swap 1g --cpuset-cpus "$cpuset" \
    --read-only --cap-drop ALL --security-opt no-new-privileges:true --tmpfs /tmp:size=64m,mode=1777 \
    -p "$bind:8081:8080" -v "$photos:/files/Photos:ro" -v "$data:/data" "$image" \
    || fail 'Photo pilot failed to start; normal service is untouched. Inspect Container Manager before removing pilot state.'
check='import json,os,urllib.request
assert os.environ["NAS_PHOTOS"]=="true"
assert urllib.request.urlopen("http://127.0.0.1:8080/healthz",timeout=5).status==200
body={"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"index_status","arguments":{}}}
r=urllib.request.Request("http://127.0.0.1:8080/mcp",data=json.dumps(body).encode(),headers={"Authorization":"Bearer "+os.environ["NAS_TOKEN"],"Content-Type":"application/json","Accept":"application/json, text/event-stream"})
reply=urllib.request.urlopen(r,timeout=15).read().decode()
if not reply.lstrip().startswith("{"): reply="\n".join(line[5:].strip() for line in reply.splitlines() if line.startswith("data:"))
result=json.loads(reply)
assert "result" in result and not result["result"].get("isError",False), "Authenticated index_status failed"
'
ready=false
for attempt in 1 2 3; do
    if docker exec nas-search-photo-pilot python3 -c "$check" >/dev/null 2>&1; then ready=true; break; fi
    sleep 2
done
[ "$ready" = true ] || fail 'Pilot started but authenticated status is unavailable; inspect pilot logs. Normal service is untouched.'
printf 'Photo pilot health and authenticated status passed at http://%s:8081/mcp. First use downloads local models; indexing may take time. Normal service is untouched.\n' "$bind"
printf '%s\n' 'Check progress: sudo docker logs --tail 80 nas-search-photo-pilot' 'Check limits: sudo docker stats --no-stream nas-search-photo-pilot'
