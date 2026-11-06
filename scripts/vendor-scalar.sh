#!/usr/bin/env bash
# Re-vendor the Scalar API Reference bundle and its webfonts for /docs.
#   ./scripts/vendor-scalar.sh [version]
# Extracts dist/browser/standalone.js from the pinned npm tarball, downloads the
# fonts its stylesheet points at, rewrites those URLs to our own /docs/fonts and
# writes the result, gzipped, to internal/server/scalar.js.gz. Prints both the
# upstream and the vendored sha256 (they differ: the URL rewrite is part of the
# vendor step, so the artifact never matches the npm tarball byte-for-byte).
set -euo pipefail
cd "$(dirname "$0")/.."
version=${1:-1.77.0}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

curl -fsS -o "$work/pkg.tgz" "https://registry.npmjs.org/@scalar/api-reference/-/api-reference-${version}.tgz"
tar -xzf "$work/pkg.tgz" -O package/dist/browser/standalone.js > "$work/standalone.js"
upstream=$(sha256sum "$work/standalone.js" | cut -d" " -f1)

fonts=$(grep -o 'https://fonts\.scalar\.com/[a-zA-Z0-9._-]*' "$work/standalone.js" | sort -u)
[[ -n "$fonts" ]] || { echo "no font URLs found in standalone.js" >&2; exit 1; }

rm -rf internal/server/scalar/fonts
mkdir -p internal/server/scalar/fonts
while read -r url; do
  curl -fsS -o "internal/server/scalar/fonts/${url##*/}" "$url"
done <<< "$fonts"
sed -i 's#https://fonts\.scalar\.com/#/docs/fonts/#g' "$work/standalone.js"
gzip -9 -c "$work/standalone.js" > internal/server/scalar.js.gz

curl -fsSL -o "$work/ofl.txt" "https://raw.githubusercontent.com/rsms/inter/master/LICENSE.txt"
{
  printf '%s\n' \
    "Inter <https://github.com/rsms/inter> — Copyright (c) 2016 The Inter Project Authors" \
    "JetBrains Mono <https://github.com/JetBrains/JetBrainsMono> — Copyright (c) 2020 The JetBrains Mono Project Authors" \
    "" \
    "Both families are licensed under the SIL Open Font License 1.1, reproduced below." \
    "The files next to this notice are Scalar's subsets of them, re-downloaded by" \
    "scripts/vendor-scalar.sh together with the API Reference bundle." \
    ""
  cat "$work/ofl.txt"
} > internal/server/scalar/fonts/OFL.txt

echo "vendored @scalar/api-reference@${version} → internal/server/scalar.js.gz ($(du -h internal/server/scalar.js.gz | cut -f1))"
echo "upstream sha256: $upstream"
echo "vendored sha256: $(sha256sum internal/server/scalar.js.gz | cut -d" " -f1)"
echo "fonts: $(find internal/server/scalar/fonts -name '*.woff2' | wc -l) subsets, $(du -sh internal/server/scalar/fonts | cut -f1)"
