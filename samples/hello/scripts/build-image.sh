#!/bin/sh
set -eu

VERSION=${1:?usage: build-image.sh VERSION COMMIT [--kind-load]}
COMMIT=${2:?usage: build-image.sh VERSION COMMIT [--kind-load]}
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
repo_root=$(CDPATH= cd -- "$sample_dir/../.." && pwd)
image_repository=${SAMPLE_IMAGE_REPOSITORY:-org.local/hello-worker}
image_tag="$image_repository:$VERSION-$COMMIT"

docker info >/dev/null
docker build --file "$sample_dir/Dockerfile" --build-arg "VERSION=$VERSION" --build-arg "COMMIT=$COMMIT" --tag "$image_tag" "$repo_root"

if [ "$mode" = "--kind-load" ]; then
	sh "$sample_dir/scripts/kind-load.sh" "$image_tag" "${KIND_CLUSTER:-org}"
elif [ -n "$mode" ]; then
	echo "unknown option: $mode" >&2
	exit 2
else
	echo "IMAGE_TAG=$image_tag"
fi
