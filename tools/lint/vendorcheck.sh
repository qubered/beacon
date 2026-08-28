#!/usr/bin/env bash
# Design principle 1: no vendor names in the codebase.
#
# A vendor string outside a Pack, a fixture or a test means the abstraction has
# failed. Treat a failure here as a design review finding, not a lint nit.
set -euo pipefail
cd "$(dirname "$0")/../.."

denylist="tools/lint/vendor-denylist.txt"
terms=$(grep -vE '^\s*(#|$)' "$denylist" || true)

if [[ -z "$terms" ]]; then
  echo "vendorcheck: denylist is empty, nothing to check"
  exit 0
fi

# Packs and fixtures are where vendor names legitimately live. So are tests.
excludes=(
  ":(exclude)packs/**"
  ":(exclude)test/fixtures/**"
  ":(exclude)**/testdata/**"
  ":(exclude)**/*_test.go"
  ":(exclude)docs/spec/**"
  ":(exclude)$denylist"
  ":(exclude)LICENSE"
)

status=0
while IFS= read -r term; do
  [[ -z "$term" ]] && continue
  if git grep -Iin --fixed-strings -- "$term" -- "${excludes[@]}"; then
    echo "vendorcheck: FAIL — vendor name '$term' appears outside packs/ and fixtures." >&2
    status=1
  fi
done <<< "$terms"

if [[ $status -eq 0 ]]; then
  echo "vendorcheck: ok"
fi
exit $status
