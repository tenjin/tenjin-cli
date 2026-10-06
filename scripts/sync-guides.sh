#!/bin/sh
# Copies the SDK integration guides that `tenjin sdk guide` embeds from a
# revision of github.com/tenjin/sdk-llm-guides, and records the revision.
#
#   scripts/sync-guides.sh <revision> [checkout]
#
# Use a revision on main whose guides pass that repository's checks against
# the SDK sources. With no checkout given, the repository is cloned.
set -eu
revision=${1:?usage: scripts/sync-guides.sh <revision> [checkout]}
checkout=${2:-}
if [ -z "$checkout" ]; then
	checkout=$(mktemp -d)
	trap 'rm -rf "$checkout"' EXIT
	git clone --quiet https://github.com/tenjin/sdk-llm-guides "$checkout"
fi
out=internal/guides/data
rm -f "$out"/*.md
for platform in android ios flutter react-native ionic unity; do
	git -C "$checkout" show "$revision:guides/$platform/llm-guide.md" > "$out/$platform.md"
done
git -C "$checkout" show "$revision:LICENSE.txt" > "$out/LICENSE.txt"
git -C "$checkout" rev-parse --short "$revision" > "$out/REVISION"
echo "Guides synced from sdk-llm-guides at $(cat "$out/REVISION")."
