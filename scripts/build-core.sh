#!/usr/bin/env bash
set -euo pipefail
project_root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$project_root"
dafny_bin="${DAFNY:-$project_root/.tools/dafny/dafny}"
export PATH="$project_root/.tools/bin:$PATH"
"$dafny_bin" verify core/Core.dfy core_tests/CoreTests.dfy
translation_dir="$(mktemp -d)"
trap 'rm -rf "$translation_dir"' EXIT
"$dafny_bin" translate go core_tests/CoreTests.dfy --include-runtime \
  --output "$translation_dir/tests" --go-module-name coretests
printf 'module coretests\n\ngo 1.25.0\n' > "$translation_dir/tests-go/go.mod"
# Dafny 4.11's System_ template omits --go-module-name for its runtime import.
sed -i 's|"dafny"|"coretests/dafny"|' "$translation_dir/tests-go/System_/System_.go"
(cd "$translation_dir/tests-go" && go run .)
"$dafny_bin" translate go core/Core.dfy --include-runtime \
  --output "$translation_dir/core" \
  --go-module-name github.com/mattyv/nas-search-mcp/generated/core
mkdir -p generated/core
cp -R "$translation_dir/core-go/." generated/core/
sed -i 's|"dafny"|"github.com/mattyv/nas-search-mcp/generated/core/dafny"|' generated/core/System_/System_.go
gofmt -w generated/core
