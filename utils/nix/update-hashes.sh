#!/usr/bin/env bash
# Regenerate vendorHashes in utils/nix/services.nix.
#
# For each service: set its hash to lib.fakeHash, run `nix build .#<svc>`
# (which fails with the actual hash in the error), then write that hash back.

set -euo pipefail

SERVICES=(bank exchange gateway notification user)
NIX_FILE="utils/nix/services.nix"

if [ ! -f "$NIX_FILE" ]; then
  echo "error: $NIX_FILE not found — run from repo root" >&2
  exit 1
fi

for svc in "${SERVICES[@]}"; do
  echo ">>> $svc"

  # Replace either a quoted hash or an existing lib.fakeHash with lib.fakeHash.
  sed -i.bak -E "s|^(    $svc = )(\"[^\"]*\"|lib\\.fakeHash);|\\1lib.fakeHash;|" "$NIX_FILE"
  rm -f "$NIX_FILE.bak"

  # Expect failure with hash mismatch.
  if output=$(nix build ".#$svc" --no-link 2>&1); then
    echo "error: nix build .#$svc unexpectedly succeeded with fakeHash" >&2
    exit 1
  fi

  # Pull the actual hash from the "got: sha256-..." line.
  hash=$(printf '%s\n' "$output" | sed -nE 's/.*got:[[:space:]]+(sha256-[A-Za-z0-9+/=]+).*/\1/p' | head -1)
  if [ -z "$hash" ]; then
    echo "error: could not parse hash from nix output for $svc:" >&2
    echo "$output" >&2
    exit 1
  fi

  sed -i.bak -E "s|^(    $svc = )lib\\.fakeHash;|\\1\"$hash\";|" "$NIX_FILE"
  rm -f "$NIX_FILE.bak"

  echo "    $svc = $hash"
done

echo "done — review the diff in $NIX_FILE"
