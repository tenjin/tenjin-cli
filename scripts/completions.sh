#!/bin/sh
# Writes the shell completion scripts that release archives and the Homebrew
# cask ship. GoReleaser runs it before building.
set -eu
rm -rf completions
mkdir -p completions
for shell in bash zsh fish powershell; do
	go run ./cmd/tenjin completion "$shell" > "completions/tenjin.$shell"
done
