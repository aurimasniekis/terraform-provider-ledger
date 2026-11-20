#!/bin/sh
# Permission policy for GitHub workflow files (ADR-0007, rule 5). Run by `make workflow-policy`,
# which the workflow-lint job in .github/workflows/ci.yml also runs.
#
#   scripts/check-workflow-permissions.sh [DIR]   check every *.yml and *.yaml in DIR (default .github/workflows)
#   scripts/check-workflow-permissions.sh --self-test
#                                                 check that the negative fixtures are caught and the positive one passes
#
# This is a text check, not a YAML parser (ADR-0007, Consequences). It recognises a write scope as a value after a
# colon in block, flow ({contents: write}) or quoted ("write") form, and write-all.
set -eu

FIXTURES=scripts/testdata/permission-policy

# A write scope: a colon, blanks, an optional quote, write or write-all, an optional closing quote, then a blank,
# a comma, a closing brace or the end of the line.
WRITE_SCOPE=':[[:space:]]*["'"'"']?write(-all)?["'"'"']?([[:space:],}]|$)'

has_write_scope() {
  grep -qE "$WRITE_SCOPE" "$1"
}

top_level_read_only() {
  grep -A1 -x 'permissions:' "$1" | grep -qx '  contents: read'
}

# check_dir DIR: every workflow in DIR must request no write scope and a top-level `contents: read`.
check_dir() {
  status=0
  for f in "$1"/*.yml "$1"/*.yaml; do
    [ -e "$f" ] || continue
    if has_write_scope "$f"; then
      echo "::error file=$f::must stay read-only, but a write scope was requested"
      status=1
    fi
    if ! top_level_read_only "$f"; then
      echo "::error file=$f::top-level permissions must be exactly contents read"
      status=1
    fi
  done
  return "$status"
}

# self_test: each fail-* fixture must contain a write scope, and the pass-* fixtures must satisfy the policy.
self_test() {
  status=0
  for f in "$FIXTURES"/fail-*.yml; do
    if has_write_scope "$f"; then
      echo "ok: $f is rejected"
    else
      echo "::error file=$f::expected a write scope to be detected"
      status=1
    fi
  done
  for f in "$FIXTURES"/pass-*.yml; do
    if has_write_scope "$f" || ! top_level_read_only "$f"; then
      echo "::error file=$f::expected a read-only workflow to pass"
      status=1
    else
      echo "ok: $f passes"
    fi
  done
  return "$status"
}

case "${1:-}" in
  --self-test) self_test ;;
  *) check_dir "${1:-.github/workflows}" ;;
esac
