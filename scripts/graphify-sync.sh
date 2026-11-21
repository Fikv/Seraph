#!/usr/bin/env bash
set -euo pipefail

ROOT="$(git -C "$(dirname "$0")" rev-parse --show-toplevel)"
VAULT="${SERAPH_VAULT:-/mnt/c/Users/jakub/Dropbox/DropsyncFiles/A_OFFICAL/Seraph}"
MAP="$VAULT/Mapa-Kodu"

cd "$ROOT"
graphify update . --force
graphify tree --root .
mkdir -p "$MAP"
graphify export obsidian --dir "$MAP/graphify"
cp graphify-out/graph.html "$MAP/graph.html"
cp graphify-out/GRAPH_TREE.html "$MAP/GRAPH_TREE.html"
{
  printf -- '---\ntags: [seraph, graphify, generated]\n---\n\n'
  printf '> [!info] Generowane przez `scripts/graphify-sync.sh` (%s). Nie edytuj ręcznie.\n\n' "$(date '+%Y-%m-%d %H:%M')"
  cat graphify-out/GRAPH_REPORT.md
} > "$MAP/Raport-Grafu.md"
echo "graphify -> $MAP"
