#!/usr/bin/env bash
# qa-prep.sh: the fixed steps of a QA test in one call, so a session
# spends no calls or tokens on them.
#
#   qa-prep.sh <PR number | maestro/T-<n> branch | T-<n>>
#
# It runs the maestro-code-review skill's review-prep.sh --qa, which
# holds the steps review and QA share; that file's header lists what it
# does and prints. Installed skills sit side by side, so it is found
# from this file's own path. Exit codes are review-prep.sh's; 1 with a
# reason when that skill isn't there.
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
prep=$here/../../maestro-code-review/scripts/review-prep.sh
[ -f "$prep" ] || { echo "qa-prep: no $prep: install the maestro-code-review skill next to this one" >&2; exit 1; }
exec bash "$prep" --qa "$@"
