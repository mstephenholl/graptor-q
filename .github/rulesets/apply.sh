#!/usr/bin/env bash
# Creates the labels the workflows read and creates or updates each ruleset
# in this directory, matched by name. GitHub does not read these files; run
# this as the repository owner. Rulesets need a public repository on GitHub
# Free (HTTP 403 while private).
set -euo pipefail

repo=${REPO:-mstephenholl/graptor-q}
dir=$(dirname "$0")

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
  name=$(jq -r .name "$f")
  id=$(awk -F'\t' -v n="$name" '$1 == n { print $2 }' <<<"$existing")
  if [ -n "$id" ]; then
    gh api -X PUT "repos/$repo/rulesets/$id" --input "$f" >/dev/null
    echo "updated ruleset $name ($id)"
  else
    gh api -X POST "repos/$repo/rulesets" --input "$f" >/dev/null
    echo "created ruleset $name"
  fi
done

# A broken required check would lock main for anyone who cannot bypass it.
status=0
for id in $(gh api "repos/$repo/rulesets" --jq '.[].id'); do
  stored=$(gh api "repos/$repo/rulesets/$id")
  jq '{name, enforcement, current_user_can_bypass, bypass_actors,
    checks: [.rules[] | select(.type == "required_status_checks") | .parameters.required_status_checks[].context]}' <<<"$stored"
  if ! jq -e '.current_user_can_bypass == "always" and (.bypass_actors | length) > 0' <<<"$stored" >/dev/null; then
    echo "apply.sh: ruleset $(jq -r .name <<<"$stored") ($id) needs current_user_can_bypass \"always\" and a non-empty bypass_actors; run it as the repository owner" >&2
    status=1
  fi
done
exit "$status"
