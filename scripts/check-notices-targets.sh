#!/bin/sh
# Drift check between the notices TARGETS list (scripts/generate-notices.sh) and the binaries GoReleaser builds from
# .goreleaser.yml (its goos/goarch matrix and ignore rules). Run by `make release-targets-check` after
# `make release-snapshot`, which writes dist/artifacts.json. TARGETS is a copy of the matrix, so a change to one that is
# not mirrored in the other fails here.
#
#   scripts/check-notices-targets.sh [ARTIFACTS]  compare TARGETS with the Binary entries of ARTIFACTS (default dist/artifacts.json)
#   scripts/check-notices-targets.sh --self-test  check the comparison: a snapshot-shaped list of TARGETS passes, and the same list
#                                                 minus one platform is rejected
set -eu

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

# compare ARTIFACTS: 0 when the two lists are equal; 1 when they differ (the difference goes to stderr); 2 when
# ARTIFACTS is missing or holds no binaries.
compare() {
  if [ ! -f "$1" ]; then
    echo "$1 not found: run make release-snapshot first" >&2
    return 2
  fi
  sh scripts/generate-notices.sh --targets | sort -u > "$WORK/targets"
  jq -r '.[] | select(.type == "Binary") | "\(.goos)/\(.goarch)"' "$1" | sort -u > "$WORK/built"
  if [ ! -s "$WORK/built" ]; then
    echo "$1 lists no binaries" >&2
    return 2
  fi
  if diff "$WORK/targets" "$WORK/built" > "$WORK/diff"; then
    echo "ok: TARGETS match the $(wc -l < "$WORK/built" | tr -d ' ') binaries in $1"
    return 0
  fi
  echo "::error::TARGETS in scripts/generate-notices.sh differ from the binaries GoReleaser built (< only in TARGETS, > only in the build):" >&2
  cat "$WORK/diff" >&2
  return 1
}

# self_test: built from the live TARGETS list, so it stays valid when the list changes. The list as snapshot-shaped
# Binary entries must pass; the same list without its first platform must be rejected with the drift exit code (1).
self_test() {
  sh scripts/generate-notices.sh --targets | jq -R -s '[split("\n")[] | select(. != "") | split("/") | {type: "Binary", goos: .[0], goarch: .[1]}]' > "$WORK/match.json"
  jq '.[1:]' "$WORK/match.json" > "$WORK/drift.json"
  status=0
  if compare "$WORK/match.json" > /dev/null; then
    echo "ok: the TARGETS list passes"
  else
    echo "::error::expected the TARGETS list to match itself"
    status=1
  fi
  rc=0
  compare "$WORK/drift.json" > /dev/null 2>&1 || rc=$?
  if [ "$rc" -eq 1 ]; then
    echo "ok: the TARGETS list without its first platform is rejected"
  else
    echo "::error::expected drift to be detected (exit 1), got exit $rc"
    status=1
  fi
  return "$status"
}

case "${1:-}" in
  --self-test) self_test ;;
  *) compare "${1:-dist/artifacts.json}" ;;
esac
