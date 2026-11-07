#!/usr/bin/env bash
# Regenerate THIRD_PARTY_NOTICES.md from the modules the shipped binaries
# (./cmd/...) actually link, with one verbatim license text per module.
# Apache-2.0 modules are grouped under a single copy of the Apache text, and
# each upstream NOTICE file is reproduced verbatim next to it (Apache-2.0
# §4(d) demands that on binary redistribution).
set -euo pipefail
cd "$(dirname "$0")/.."

out=THIRD_PARTY_NOTICES.md
tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT

classify() {
  h=$(head -c 3000 "$1" | tr '\n' ' ')
  case "$h" in
    *"GNU LESSER"*) echo LGPL ;;
    *"GNU GENERAL"*) echo GPL ;;
    *"Mozilla Public License Version 2.0"*) echo MPL-2.0 ;;
    *"Apache License"*) echo Apache-2.0 ;;
    *"Permission is hereby granted, free of charge"*) echo MIT ;;
    *"Redistribution and use in source and binary forms"*) echo BSD-3-Clause ;;
    *) echo UNKNOWN ;;
  esac
}

go list -deps -f '{{if .Module}}{{.Module.Path}}|{{.Module.Version}}{{end}}' ./cmd/... \
  | sort -u | grep -v '^$' | grep -v '^echo-todo-2' | while IFS='|' read -r p v; do
    d=$(go list -m -f '{{.Dir}}' "$p")
    lic=$(find "$d" -maxdepth 1 -iname 'LICENSE*' -o -maxdepth 1 -iname 'COPYING*' -o -maxdepth 1 -iname 'Licence*' 2>/dev/null | sort | head -1)
    notice=$(find "$d" -maxdepth 1 -iname 'NOTICE*' 2>/dev/null | sort | head -1)
    [ -n "$lic" ] || lic=/dev/null
    echo "$(classify "$lic")|$p|$v|$lic|$notice"
  done > "$tmp"

{
  cat <<'HDR'
# Third-party notices

Third-party software this project bundles and redistributes. This project's
own code is MIT-licensed (see LICENSE); everything below keeps its own license.

## Go dependencies

Modules linked into the shipped binaries (`./cmd/...`). Regenerate with
`make licenses`. Apache-2.0 modules share one copy of the license text;
every upstream NOTICE file is reproduced verbatim after it, as Apache-2.0 §4(d)
requires on redistribution. MIT and BSD-3-Clause modules each carry their own
text, which contains their copyright notice.

HDR

  if grep -q '^Apache-2.0|' "$tmp"; then
    count=$(grep -c '^Apache-2.0|' "$tmp")
    echo "### Apache License 2.0 (${count} modules)"
    echo
    grep '^Apache-2.0|' "$tmp" | sort -t'|' -k2 | while IFS='|' read -r _ p v _ _; do
      echo "- ${p} ${v}"
    done
    echo
    echo '```text'
    curl -fsSL https://www.apache.org/licenses/LICENSE-2.0.txt
    echo '```'
    echo
    echo '---'
    echo
    grep '^Apache-2.0|' "$tmp" | while IFS='|' read -r _ p v _ n; do
      [ -n "$n" ] || continue
      echo "#### NOTICE — ${p} ${v}"
      echo
      echo '```text'
      cat "$n"
      echo '```'
      echo
    done
    echo '---'
    echo
  fi

  grep -v '^Apache-2.0|' "$tmp" | sort -t'|' -k2 | while IFS='|' read -r l p v lic _; do
    echo "### ${l} — ${p} ${v}"
    echo
    echo '```text'
    cat "$lic"
    echo '```'
    echo
  done

  cat <<'ASSETS'

## Vendored web bundle — Scalar API reference

`internal/server/scalar.js.gz` vendors the built `dist/browser/standalone.js`
from `@scalar/api-reference@1.77.0`:

* Scalar (@scalar/api-reference) — MIT License, Copyright (c) 2023-present Scalar
  <https://github.com/scalar/scalar/blob/main/LICENSE>

and upstream bundles in these packages (versions as found in the bundle):

* vue 3.5.44 (@vue/shared) — MIT, Copyright (c) 2018-present Yuxi (Evan) You and Vue contributors
* tailwindcss 4.3.3 — MIT, Copyright (c) Adam Wathan
* focus-trap 8.2.3 — MIT
* tabbable 6.5.0 — MIT

Both focus-trap and tabbable retain their license banners in the bundle; the
rest are reproduced here. Full license texts are at the URLs above and in the
upstream packages <https://github.com/scalar/scalar>.

Note: this script does not change the vendored bundle; to pull in a new
Scalar version and re-vendor, run `./scripts/vendor-scalar.sh`.

ASSETS

  echo '## Fonts'
  echo
  echo 'The fonts served at `/docs/fonts/` are the subsets Scalar ships for'
  echo '`@scalar/api-reference`: Inter and JetBrains Mono, both under the SIL Open'
  echo 'Font License 1.1 (not MIT). They are bundled into the binary together with'
  echo 'their license text, reproduced at'
  echo '`internal/server/scalar/fonts/OFL.txt` and served at `/docs/fonts/OFL.txt`.'
  echo
  echo '---'
  echo
  echo 'Everything above was generated from the licenses of the respective upstream'
  echo 'projects; report inaccuracies upstream of the projects involved.'
} > "$out"
