#!/bin/sh
set -eu

VERSION=${1:?usage: build-image.sh VERSION COMMIT [--kind-load|--push]}
COMMIT=${2:?usage: build-image.sh VERSION COMMIT [--kind-load|--push]}
mode=${3:-}

case "$VERSION" in
	[A-Za-z0-9]*) ;;
	*) echo "version must start with an alphanumeric character" >&2; exit 2 ;;
esac
case "$VERSION:$COMMIT" in
	*[!A-Za-z0-9_.:-]*) echo "version or commit contains unsafe characters" >&2; exit 2 ;;
esac
case "$COMMIT" in
	*[!A-Fa-f0-9]*) echo "commit must be hexadecimal" >&2; exit 2 ;;
esac
if [ "${#COMMIT}" -lt 7 ] || [ "${#COMMIT}" -gt 64 ]; then
	echo "commit must contain 7 to 64 hexadecimal characters" >&2
	exit 2
fi

sample_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
image_repository=${IMAGE_REPOSITORY:-org.local/dynamic-decision-worker}
image_tag="$image_repository:$VERSION-$COMMIT"
source_repository=${SOURCE_REPOSITORY:-https://github.com/wu8685/org-sample-dynamic-decision}

docker info >/dev/null
docker build --file "$sample_dir/Dockerfile" --build-arg "VERSION=$VERSION" --build-arg "COMMIT=$COMMIT" --build-arg "SOURCE_REPOSITORY=$source_repository" --tag "$image_tag" "$sample_dir"

if [ "$mode" = "--kind-load" ]; then
	sh "$sample_dir/scripts/kind-load.sh" "$image_tag" "${KIND_CLUSTER:-org}"
elif [ "$mode" = "--push" ]; then
	sh "$sample_dir/scripts/push-image.sh" "$image_tag"
elif [ -n "$mode" ]; then
	echo "unknown option: $mode" >&2
	exit 2
else
	echo "IMAGE_TAG=$image_tag"
fi
