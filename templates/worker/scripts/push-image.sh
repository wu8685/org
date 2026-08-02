#!/bin/sh
set -eu

image_tag=${1:?image tag is required}
push_output=$(docker push "$image_tag" 2>&1)
printf '%s\n' "$push_output"
digest=$(printf '%s\n' "$push_output" | awk '$1 == "digest:" && $2 ~ /^sha256:[0-9a-f]{64}$/ { value=$2 } END { print value }')
case "$digest" in
	sha256:*) ;;
	*) echo "registry did not return an immutable image digest" >&2; exit 1 ;;
esac

echo "IMAGE_TAG=$image_tag"
echo "IMAGE_DIGEST=${image_tag%:*}@$digest"
