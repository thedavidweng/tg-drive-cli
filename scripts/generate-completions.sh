#!/usr/bin/env bash
set -euo pipefail

# Generate shell completion scripts for the td CLI.
# Builds the binary into a temp dir, then writes completion files into completions/.

script_dir="$(cd "$(dirname "$0")" && pwd)"
repo_root="$(cd "$script_dir/.." && pwd)"
cd "$repo_root"

out_dir="$repo_root/completions"
mkdir -p "$out_dir"

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT

bin="$tmp_dir/td"
echo "==> Building td"
go build -o "$bin" ./cmd/td

echo "==> Generating completions in $out_dir"
"$bin" completion bash       > "$out_dir/td.bash"
"$bin" completion zsh        > "$out_dir/_td"
"$bin" completion fish       > "$out_dir/td.fish"
"$bin" completion powershell > "$out_dir/td.ps1"

echo "==> Wrote td.bash, _td, td.fish, td.ps1"
