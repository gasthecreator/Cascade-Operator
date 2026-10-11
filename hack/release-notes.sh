#!/usr/bin/env bash
# Print the CHANGELOG.md section for one version, for use as GitHub Release
# notes. Usage: hack/release-notes.sh 0.2.0   (a leading "v" is accepted)
# Fails if the section is missing or empty, so a release can never ship with
# blank notes.
set -euo pipefail

version="${1:?usage: release-notes.sh <version>}"
version="${version#v}"
changelog="${CHANGELOG:-$(dirname "$0")/../CHANGELOG.md}"

notes="$(awk -v ver="${version}" '
  $0 ~ "^## \\[" ver "\\]" { found = 1; next }
  found && /^## \[/        { exit }
  found && /^\[[^]]+\]: /   { next }   # link-reference footer at end of file
  found                    { print }
' "${changelog}")"

# Trim leading/trailing blank lines.
notes="$(printf '%s\n' "${notes}" | sed -e '/./,$!d' | sed -e ':a' -e '/^\n*$/{$d;N;ba' -e '}')"

if [[ -z "${notes//[[:space:]]/}" ]]; then
  echo "release-notes.sh: no CHANGELOG.md section found for [${version}]" >&2
  exit 1
fi
printf '%s\n' "${notes}"
