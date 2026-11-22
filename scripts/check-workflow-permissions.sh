#!/bin/sh
# Permission policy for GitHub workflow files (ADR-0001 section 8). Run by `make workflow-policy`,
# which the workflow-lint job in .github/workflows/ci.yml also runs.
#
#   scripts/check-workflow-permissions.sh [DIR]   check every *.yml and *.yaml in DIR (default .github/workflows)
#   scripts/check-workflow-permissions.sh --self-test
#                                                 check that the negative fixtures are caught and the positive one passes
#
# release.yml alone may hold one job-level `contents: write` line; see has_write_scope.
# This is a text check, not a YAML parser (ADR-0001, Consequences). It recognises a write scope as a value after a
# colon in block, flow ({contents: write}) or quoted ("write") form, and write-all.
set -eu

FIXTURES=scripts/testdata/permission-policy

# A write scope: a colon, blanks, an optional quote, write or write-all, an optional closing quote, then a blank,
# a comma, a closing brace or the end of the line.
WRITE_SCOPE=':[[:space:]]*["'"'"']?write(-all)?["'"'"']?([[:space:],}]|$)'

# The one allowed exception (ADR-0003): release.yml may grant exactly `contents: write`, as a job-level permission
# (six spaces of indent), to create the GitHub release. Any other write scope in it is still rejected. A file that is
# not a release workflow gets no exception. has_write_scope FILE [release]: pass "release" to apply the exception.
has_write_scope() {
  if [ "${2:-}" = release ]; then
    grep -vxE '      contents: write' "$1" | grep -qE "$WRITE_SCOPE"
  else
    grep -qE "$WRITE_SCOPE" "$1"
  fi
}

# kind_of FILE: "release" for release.yml and for the release fixtures, empty otherwise.
kind_of() {
  case "$(basename "$1")" in
    release.yml | pass-release*.yml | fail-release*.yml) echo release ;;
    *) echo "" ;;
  esac
}

# Top-level permissions must stay read-only, and release.yml may request the write scope on exactly one line.
release_scope_ok() {
  [ "$(grep -cxE '      contents: write' "$1")" -le 1 ]
}

top_level_read_only() {
  grep -A1 -x 'permissions:' "$1" | grep -qx '  contents: read'
}

# check_dir DIR: every workflow in DIR must request no write scope and a top-level `contents: read`.
check_dir() {
  status=0
  for f in "$1"/*.yml "$1"/*.yaml; do
    [ -e "$f" ] || continue
    if has_write_scope "$f" "$(kind_of "$f")" || ! release_scope_ok "$f"; then
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
    if has_write_scope "$f" "$(kind_of "$f")" || ! release_scope_ok "$f"; then
      echo "ok: $f is rejected"
    else
      echo "::error file=$f::expected a write scope to be detected"
      status=1
    fi
  done
  for f in "$FIXTURES"/pass-*.yml; do
    if has_write_scope "$f" "$(kind_of "$f")" || ! release_scope_ok "$f" || ! top_level_read_only "$f"; then
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
