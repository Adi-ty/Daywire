#!/usr/bin/env bash
# Only dated public edition JSON goes on the content branch. Never stage the
# working tree wholesale: local docs and reference files must stay untracked.
set -euo pipefail

action="${1:?use restore, restore-public or save}"
if [[ "$action" == restore-public ]]; then
  # Vercel's source export may have no .git directory or origin remote.
  # Fetch the public archive independently of the application checkout.
  repository="${DAYWIRE_CONTENT_REPOSITORY:-${VERCEL_GIT_REPO_OWNER:-}/${VERCEL_GIT_REPO_SLUG:-}}"
  if [[ ! "$repository" =~ ^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9_][A-Za-z0-9_.-]*$ ]]; then
    echo 'Set DAYWIRE_CONTENT_REPOSITORY to the public GitHub owner/repo (for example Adi-ty/Daywire).' >&2
    exit 1
  fi
  archive_dir="$(mktemp -d "${TMPDIR:-/tmp}/daywire-content.XXXXXX")"
  trap 'rm -rf "$archive_dir"' EXIT
  if ! GIT_TERMINAL_PROMPT=0 git -c credential.helper= clone --quiet --bare --depth=1 \
      --single-branch --branch content "https://github.com/$repository.git" "$archive_dir/repo.git"; then
    echo 'Could not fetch the public content branch; check repository visibility and publish the archive before deploying.' >&2
    exit 1
  fi
  mkdir -p content
  git -C "$archive_dir/repo.git" archive HEAD editions | tar -x -C content
  echo "Restored public editions from $repository (content branch)"
elif [[ "$action" == restore ]]; then
  mkdir -p content
  if git ls-remote --exit-code --heads origin content > /dev/null; then
    git fetch --depth=1 origin content
    git archive FETCH_HEAD editions | tar -x -C content
  else
    result=$?
    if [[ "$result" != 2 ]]; then exit "$result"; fi
    mkdir -p content/editions
  fi
elif [[ "$action" == save ]]; then
  go run ./cmd/daywire validate --out content/editions
  publication_dir="$(mktemp -d "${RUNNER_TEMP:-/tmp}/daywire-publication.XXXXXX")"
  # Remove the empty directory so worktree can create it.
  rmdir "$publication_dir"
  if git ls-remote --exit-code --heads origin content > /dev/null; then
    git fetch --depth=1 origin content
    git worktree add --detach "$publication_dir" FETCH_HEAD
  else
    result=$?
    if [[ "$result" != 2 ]]; then exit "$result"; fi
    git worktree add --detach "$publication_dir" HEAD
    git -C "$publication_dir" switch --orphan daywire-content-initial
  fi
  trap 'git worktree remove --force "$publication_dir"' EXIT
  mkdir -p "$publication_dir/editions"
  cp content/editions/*.json "$publication_dir/editions/"
  git -C "$publication_dir" config user.name 'daywire[bot]'
  git -C "$publication_dir" config user.email 'daywire@users.noreply.github.com'
  # Explicit pathspec: no docs, reference HTML, secrets, or run state.
  git -C "$publication_dir" add -- editions/*.json
  if ! git -C "$publication_dir" diff --cached --quiet; then
    git -C "$publication_dir" commit -m 'Publish validated Daywire editions'
    git -C "$publication_dir" push origin HEAD:content
  fi
else
  printf 'Unknown content action: %s\n' "$action" >&2
  exit 1
fi
