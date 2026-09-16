#!/usr/bin/env bash
# Publishes docs/ to the GitHub wiki checkout given as $1.
# docs/README.md becomes Home; every other page keeps its name in Title-case.
# Links between pages are rewritten from file names to wiki page names.
set -euo pipefail

src="$(cd "$(dirname "$0")/.." && pwd)/docs"
dst="${1:?usage: sync-wiki.sh <wiki checkout>}"

page() { # commands.md -> Commands, manual-mode.md -> Manual-mode
  local n="${1%.md}"
  printf '%s%s' "$(tr '[:lower:]' '[:upper:]' <<<"${n:0:1}")" "${n:1}"
}

# link rewrites: commands.md#x -> Commands#x, ../README.md -> the repo README
rewrite=""
for f in "$src"/*.md; do
  b="$(basename "$f")"
  [[ "$b" == README.md ]] && continue
  rewrite+="s{\\]\\(\\Q$b\\E(#[^)]*)?\\)}{]($(page "$b")\$1)}g;"
done
rewrite+='s{\]\(\.\./README\.md\)}{](https://github.com/richdapice/lgtm#readme)}g;'

rm -f "$dst"/*.md "$dst"/*.gif "$dst"/*.png "$dst"/*.svg
for f in "$src"/*.md; do
  b="$(basename "$f")"
  out="$dst/$(page "$b").md"
  [[ "$b" == README.md ]] && out="$dst/Home.md"
  {
    echo "<!-- Generated from docs/$b in the main repo. Edit it there. -->"
    echo
    perl -pe "$rewrite" "$f" | perl -0pe 's/\A# [^\n]*\n+//'
  } > "$out"
done
cp "$src"/*.gif "$src"/*.png "$src"/*.svg "$dst"/ 2>/dev/null || true

{
  echo "**[Home](Home)**"
  echo
  for f in "$src"/*.md; do
    b="$(basename "$f")"
    [[ "$b" == README.md ]] && continue
    title="$(sed -n '1s/^# //p' "$f")"
    echo "- [$title]($(page "$b"))"
  done
} > "$dst/_Sidebar.md"
