#!/usr/bin/env bash
# Build every cmd/** main package into bin/**, preserving the cmd/ layout:
# cmd/sf → bin/sf, cmd/common/<tool> → bin/common/<tool>,
# cmd/projects/<name>/<tool> → bin/projects/<name>/<tool>.
set -euo pipefail
cd "$(dirname "$0")/.."

go list -f '{{if eq .Name "main"}}{{.ImportPath}}{{end}}' ./cmd/... | while read -r pkg; do
	[ -n "$pkg" ] || continue
	out="bin/${pkg#*/cmd/}"
	mkdir -p "$(dirname "$out")"
	go build -o "$out" "$pkg"
	echo "→ $out"
done
