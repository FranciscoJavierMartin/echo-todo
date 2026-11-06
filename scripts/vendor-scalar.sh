#!/usr/bin/env bash
# Re-vendor the Scalar API Reference bundle served at /docs/scalar.js.
#   ./scripts/vendor-scalar.sh [version]
# Writes internal/server/scalar.js.gz from the pinned npm tarball
# (dist/browser/standalone.js, self-contained: no chunk imports) and prints
# its sha256 for the commit message.
set -euo pipefail
cd "$(dirname "$0")/.."
version=${1:-1.77.0}
tarball=$(mktemp -t scalar-api-reference-XXXX.tgz)
trap 'rm -f "$tarball"' EXIT
curl -fsS -o "$tarball" "https://registry.npmjs.org/@scalar/api-reference/-/api-reference-${version}.tgz"
tar -xzf "$tarball" -O package/dist/browser/standalone.js | gzip -9 > internal/server/scalar.js.gz
echo "vendored @scalar/api-reference@${version} → internal/server/scalar.js.gz ($(du -h internal/server/scalar.js.gz | cut -f1))"
echo "sha256: $(sha256sum internal/server/scalar.js.gz | cut -d' ' -f1)"
