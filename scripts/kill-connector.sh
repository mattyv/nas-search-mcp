#!/bin/sh
# Stop first so an in-flight login cannot issue tokens after revocation.
set -u
project_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd) || exit 1
cd "$project_dir" || exit 1
if ! docker compose stop nas-search; then
    echo 'Service stop failed; the connector may still be reachable. Do not restart.' >&2
    exit 1
fi
if ! docker compose run --rm --no-deps nas-search revoke-oauth; then
    echo 'OAuth token revocation failed; service stopped. Do not restart until revocation succeeds.' >&2
    exit 1
fi
echo 'Tokens revoked; nas-search stopped.'
