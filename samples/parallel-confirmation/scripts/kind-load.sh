#!/bin/sh
set -eu

image_tag=${1:?image tag is required}
cluster_name=${2:-org}

if ! kind get clusters | grep -Fqx "$cluster_name"; then
	echo "kind cluster $cluster_name does not exist" >&2
	exit 1
fi

kind load docker-image --name "$cluster_name" "$image_tag"
node=$(kind get nodes --name "$cluster_name" | head -n 1)
digest=$(docker exec "$node" ctr --namespace k8s.io images list "name==$image_tag" | awk 'NR == 2 {print $3}')
case "$digest" in
	sha256:*) ;;
	*) echo "could not resolve image digest in $node" >&2; exit 1 ;;
esac

digest_ref="${image_tag%:*}@$digest"
docker exec "$node" ctr --namespace k8s.io images tag --force "$image_tag" "$digest_ref" >/dev/null
docker exec "$node" crictl inspecti "$digest_ref" >/dev/null
echo "IMAGE_TAG=$image_tag"
echo "IMAGE_DIGEST=$digest_ref"
