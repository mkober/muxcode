#!/usr/bin/env bash
# Creates the labels .github/release.yml buckets release notes by, on the
# repo gh resolves from the current checkout (or GH_REPO). Idempotent:
# --force updates an existing label's colour and description in place, so a
# re-run after a rename converges rather than failing on "already exists".
set -euo pipefail

labels=(
  "breaking|d73a4a|Breaking change — MINOR bump while 0.x, MAJOR after 1.0"
  "type:feature|0e8a16|New capability — a MUX spec, CLI verb, flag or env var"
  "type:defect|e4e669|Defect fix from the ranked-defect table"
  "docs|0075ca|Documentation only"
)

for entry in "${labels[@]}"; do
  IFS='|' read -r name color description <<<"$entry"
  gh label create "$name" --color "$color" --description "$description" --force
  echo "label ok: $name"
done
