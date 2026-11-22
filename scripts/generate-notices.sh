#!/bin/sh
# Writes THIRD_PARTY_NOTICES.txt: the licence texts of every Go module compiled into the provider binary
# (ADR-0001 section 9, ADR-0003). GoReleaser runs it as a before hook and ships the file in every archive.
#
#   scripts/generate-notices.sh [OUTPUT]     (default: THIRD_PARTY_NOTICES.txt in the working directory)
#
# It fails when a module has no directory or no licence file, so an incomplete notices file is never written.
set -eu

OUT=${1:-THIRD_PARTY_NOTICES.txt}
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

# Modules in the main package's dependency graph: the ones linked into the binary (test-only modules are left out).
go list -deps -f '{{with .Module}}{{if not .Main}}{{.Path}} {{.Version}} {{.Dir}}{{end}}{{end}}' . > "$WORK/raw"
grep . "$WORK/raw" | sort -u > "$WORK/modules"

{
  echo "Third-party notices for terraform-provider-ledger"
  echo
  echo "The provider itself is licensed under the Apache License 2.0 (LICENSE). The Go modules listed below are"
  echo "compiled into the provider binary; each is reproduced here with its licence text, unmodified. Modules under"
  echo "the Mozilla Public License 2.0 (MPL-2.0) are included as required by ADR-0001 section 9. The source code of"
  echo "each module at the listed version is available from the Go module proxy (https://proxy.golang.org) and from"
  echo "the module's repository."
  echo
  while read -r path version dir; do
    if [ -z "$dir" ]; then
      echo "module $path $version has no directory in the module cache; run go mod download" >&2
      exit 1
    fi
    lic=$(find "$dir" -maxdepth 1 -type f \( -iname 'LICEN[CS]E*' -o -iname 'COPYING*' \) | sort | head -n 1)
    if [ -z "$lic" ]; then
      echo "module $path $version has no licence file in $dir" >&2
      exit 1
    fi
    echo "================================================================================"
    echo "$path $version ($(basename "$lic"))"
    echo "================================================================================"
    cat "$lic"
    echo
  done < "$WORK/modules"
} > "$WORK/notices"

mv "$WORK/notices" "$OUT"
echo "wrote $OUT ($(grep -c '^=====' "$OUT" | awk '{print $1/2}') modules)"
