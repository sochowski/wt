#!/usr/bin/env bash
# Run ONLY in a disposable source copy: installer rebuilds bin/wt-state.
set -euo pipefail
[[ "${WT_NATIVE_INSTALL_TEST_ISOLATED:-}" == 1 ]] || { echo 'Requires WT_NATIVE_INSTALL_TEST_ISOLATED=1 and a disposable source copy'; exit 1; }
source_dir="$(cd "$(dirname "$0")" && pwd)"
root="$(mktemp -d)"
trap 'rm -rf "$root"' EXIT
mkdir -p "$root/stubs" "$root/missing" "$root/old" "$root/installed" "$root/package/node_modules/jiti"
printf '#!/bin/sh\nexit 0\n' > "$root/stubs/pi"
chmod +x "$root/stubs/pi"
printf '{}' > "$root/package/node_modules/jiti/package.json"
printf 'export default function () {}\n' > "$root/package/index.ts"
printf '{"name":"pi-subagents","pi":{"extensions":["./index.ts"]}}\n' > "$root/package/package.json"
run_install() {
  local home="$1"; shift
  env -i PATH="$root/stubs:$PATH" HOME="$home" GOMODCACHE="${GOMODCACHE:-$(go env GOMODCACHE)}" GOCACHE="${GOCACHE:-$(go env GOCACHE)}" \
    WT_STATUS_DIR="$home/.local/state/wt" WT_DB="$home/.local/state/wt/wt.db" WT_BASE_DIR="$home/worktrees" WT_CONFIG_DIR="$home/.config/wt" \
    "$@" bash "$source_dir/install.sh"
}
if run_install "$root/missing" > "$root/missing.log" 2>&1; then echo 'missing dependency unexpectedly installed'; exit 1; fi
grep -q 'requires a compatible local' "$root/missing.log"
[[ ! -e "$root/missing/bin" && ! -e "$root/missing/.local/state/wt" ]]
if run_install "$root/old" WT_PI_SUBAGENTS_SOURCE="$root/package" > "$root/old.log" 2>&1; then echo 'old dependency unexpectedly installed'; exit 1; fi
grep -q 'does not declare' "$root/old.log"
[[ ! -e "$root/old/bin" && ! -e "$root/old/.local/state/wt" ]]
printf '{"name":"pi-subagents","wtNativeProviderContract":1,"pi":{"extensions":["./index.ts"]}}\n' > "$root/package/package.json"
compatible_source="${WT_NATIVE_INSTALL_TEST_PACKAGE:-$root/package}"
for position in 0 1 2; do
  for form in string object; do
    duplicate="$root/duplicate-$position-$form"
    mkdir -p "$duplicate/.pi/agent"
    jq -n --argjson position "$position" --arg form "$form" '
      (if $form == "object" then {source:"npm:pi-subagents",extensions:[]} else "npm:pi-subagents@0.23.0" end) as $old |
      ["npm:unrelated-a", "/unrelated/b"] | {packages:(.[:$position] + [$old] + .[$position:]),theme:"fixture"}' > "$duplicate/.pi/agent/settings.json"
    cp "$duplicate/.pi/agent/settings.json" "$duplicate/before.json"
    if run_install "$duplicate" WT_PI_SUBAGENTS_SOURCE="$compatible_source" > "$duplicate/install.log" 2>&1; then echo 'duplicate package unexpectedly installed'; exit 1; fi
    grep -q 'remove the old npm pi-subagents entry' "$duplicate/install.log"
    cmp "$duplicate/before.json" "$duplicate/.pi/agent/settings.json"
    [[ ! -e "$duplicate/bin" && ! -e "$duplicate/.local/state/wt" ]]
  done
done
mkdir -p "$root/installed/.pi/agent"
printf '{"theme":"fixture","packages":["/unrelated/local-package"]}\n' > "$root/installed/.pi/agent/settings.json"
run_install "$root/installed" WT_PI_SUBAGENTS_SOURCE="$compatible_source" > "$root/installed.log" 2>&1
run_install "$root/installed" WT_PI_SUBAGENTS_SOURCE="$compatible_source" >> "$root/installed.log" 2>&1
jq -e --arg source "$compatible_source" '.theme == "fixture" and .packages == ["/unrelated/local-package", $source]' "$root/installed/.pi/agent/settings.json" >/dev/null
[[ "$(readlink -f "$root/installed/.pi/agent/extensions/wt")" == "$source_dir/config/pi-wt" ]]
grep -q native-bootstrap "$root/installed/.pi/agent/extensions/wt/extension.js"
! grep -q '/tmp\|staging' "$root/installed/.pi/agent/extensions/wt/native-bootstrap.js"
printf 'PASS: missing/old dependency and all duplicate positions/forms refuse before installation; ordinary paired-source install idempotent, preserves settings and loads independent bootstrap\n'
