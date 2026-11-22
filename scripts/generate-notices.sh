#!/bin/sh
# Writes THIRD_PARTY_NOTICES.txt: the licence texts of every Go module compiled into the provider binary, plus the
# licence and patent grant of the Go standard library and runtime (ADR-0001 section 9, ADR-0003). GoReleaser runs it
# as a before hook and ships the file in every archive.
#
#   scripts/generate-notices.sh [OUTPUT]     (default: THIRD_PARTY_NOTICES.txt in the working directory)
#   scripts/generate-notices.sh --targets    (print the TARGETS below, one goos/goarch per line, and exit)
#
# It fails when a module has no directory or no licence file, or when the Go distribution has no LICENSE, so an
# incomplete notices file is never written. A module's NOTICE files (NOTICE, NOTICE.txt or NOTICE.md) are included
# after its licence file, because some licences require them to be passed on.
set -eu

OUT=${1:-THIRD_PARTY_NOTICES.txt}
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

# The module list is the union over every GoReleaser target (goos/goarch matrix and ignore rules in .goreleaser.yml),
# not just the host platform, so a dependency that only some targets compile in is still listed. Today the lists are
# identical for every target. Keep this list in step with .goreleaser.yml: make release-targets-check fails when the two
# differ. darwin/386, darwin/arm and windows/arm are not Go targets in the toolchain go.mod pins (go tool dist list), so
# this list omits them and .goreleaser.yml excludes them.
TARGETS="freebsd/386 freebsd/amd64 freebsd/arm freebsd/arm64 \
linux/386 linux/amd64 linux/arm linux/arm64 \
darwin/amd64 darwin/arm64 \
windows/386 windows/amd64 windows/arm64"

if [ "${1:-}" = "--targets" ]; then
  for target in $TARGETS; do
    echo "$target"
  done
  exit 0
fi

# Modules in the main package's dependency graph: the ones linked into the binary (test-only modules are left out).
# CGO_ENABLED=0 matches the build (see the build target in .goreleaser.yml and the Makefile).
: > "$WORK/raw"
for target in $TARGETS; do
  GOOS=${target%/*} GOARCH=${target#*/} CGO_ENABLED=0 \
    go list -deps -f '{{with .Module}}{{if not .Main}}{{.Path}} {{.Version}} {{.Dir}}{{end}}{{end}}' . >> "$WORK/raw"
done
grep . "$WORK/raw" | sort -u > "$WORK/modules"

GOVERSION=$(go env GOVERSION)
GOROOT_DIR=$(go env GOROOT)
if [ -z "$GOVERSION" ] || [ ! -f "$GOROOT_DIR/LICENSE" ]; then
  echo "the Go distribution at '$GOROOT_DIR' has no LICENSE (or go env GOVERSION is empty)" >&2
  exit 1
fi

# section TITLE FILE: a titled block holding the file, unmodified.
section() {
  echo "================================================================================"
  echo "$1"
  echo "================================================================================"
  cat "$2"
  echo
}

{
  echo "Third-party notices for terraform-provider-ledger"
  echo
  echo "The provider itself is licensed under the Apache License 2.0 (LICENSE). The Go standard library and runtime"
  echo "($GOVERSION) are compiled into the provider binary as well; their licence (LICENSE) and patent grant (PATENTS)"
  echo "from the Go distribution are reproduced first. The Go modules listed below are compiled into the provider binary"
  echo "too; each is reproduced here with its licence text, and with its NOTICE files where it has them, unmodified."
  echo "Modules under the Mozilla Public License 2.0 (MPL-2.0) are included as required by ADR-0001 section 9. The source"
  echo "code of each module at the listed version is available from the Go module proxy (https://proxy.golang.org) and"
  echo "from the module's repository."
  echo
  section "Go standard library and runtime ($GOVERSION): LICENSE" "$GOROOT_DIR/LICENSE"
  if [ -f "$GOROOT_DIR/PATENTS" ]; then
    section "Go standard library and runtime ($GOVERSION): PATENTS" "$GOROOT_DIR/PATENTS"
  else
    echo "the Go distribution at '$GOROOT_DIR' has no PATENTS file" >&2
    exit 1
  fi

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
    section "$path $version ($(basename "$lic"))" "$lic"

    find "$dir" -maxdepth 1 -type f \( -iname NOTICE -o -iname NOTICE.txt -o -iname NOTICE.md \) | sort > "$WORK/notice-files"
    while read -r notice; do
      section "$path $version ($(basename "$notice"))" "$notice"
    done < "$WORK/notice-files"
  done < "$WORK/modules"
} > "$WORK/notices"

mv "$WORK/notices" "$OUT"
echo "wrote $OUT ($GOVERSION runtime and $(wc -l < "$WORK/modules" | tr -d ' ') modules)"
