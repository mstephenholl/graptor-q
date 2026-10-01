#!/usr/bin/env bash
# Creates the labels the workflows read, and creates or updates the
# repository rulesets from the JSON files next to this script, matching them
# by name, so it is safe to run again. GitHub does not read these files: they
# are applied with this script by the repository owner (gh authenticated as
# the owner), and are the record of what is set.
#
#   .github/rulesets/apply.sh               # everything as committed
#   .github/rulesets/apply.sh --without-perf # main without the perf check,
#                                            # until it is calibrated
#
# Rulesets need a public repository on GitHub Free (HTTP 403 while private).
set -euo pipefail

repo=${REPO:-mstephenholl/graptor-q}
dir=$(dirname "$0")
without_perf=false
[ "${1:-}" = --without-perf ] && without_perf=true

gh label create release:major --repo "$repo" --force --color B60205 \
  --description "Release a new major version; in v0, v1.0.0"
gh label create release:minor --repo "$repo" --force --color D93F0B \
  --description "Release a new minor version: new API, or a breaking change in v0"
gh label create release:patch --repo "$repo" --force --color 0E8A16 \
  --description "Release a patch, the default without a release label"
gh label create perf:accept --repo "$repo" --force --color FBCA04 \
  --description "Accept the slowdown the perf check found; counts when the owner adds it"

existing=$(gh api "repos/$repo/rulesets" --jq '.[] | "\(.name)\t\(.id)"')

for f in "$dir"/*.json; do
  body=$(jq . "$f")
  if $without_perf; then
    body=$(jq '(.rules[] | select(.type == "required_status_checks") | .parameters.required_status_checks)
      |= map(select(.context != "perf"))' <<<"$body")
  fi
  name=$(jq -r .name <<<"$body")
  id=$(awk -F'\t' -v n="$name" '$1 == n { print $2 }' <<<"$existing")
  if [ -n "$id" ]; then
    gh api -X PUT "repos/$repo/rulesets/$id" --input - <<<"$body" >/dev/null
    echo "updated ruleset $name ($id)"
  else
    gh api -X POST "repos/$repo/rulesets" --input - <<<"$body" >/dev/null
    echo "created ruleset $name"
  fi
done

# What GitHub stored, to check by eye: the bypass list, and
# current_user_can_bypass "always" for the owner.
for id in $(gh api "repos/$repo/rulesets" --jq '.[].id'); do
  gh api "repos/$repo/rulesets/$id" --jq '{name, enforcement, current_user_can_bypass, bypass_actors,
    checks: [.rules[] | select(.type == "required_status_checks") | .parameters.required_status_checks[].context]}'
done
