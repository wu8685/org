#!/bin/sh
set -eu

RUN_ID=${RUN_ID:-${1:-}}
case "$RUN_ID" in
	??????????) ;;
	*)
		echo "RUN_ID must be the exact 10-character ID printed by make e2e-local" >&2
		exit 2
		;;
esac
case "$RUN_ID" in
	*[!0-9a-f]*)
		echo "RUN_ID must contain only lowercase hexadecimal characters" >&2
		exit 2
		;;
esac

namespace="org-e2e-$RUN_ID"
kubectl --context kind-org delete namespace "$namespace" --ignore-not-found=true --wait=true --timeout=90s

node=$(kind get nodes --name org | head -n 1)
for suffix in a b; do
	commit=$(printf '%12s' '' | tr ' ' "$suffix")
	tag="org.local/hello-worker:e2e-$RUN_ID-$suffix-$commit"
	if docker exec "$node" crictl inspecti "$tag" >/dev/null 2>&1; then
		docker exec "$node" crictl rmi "$tag" >/dev/null
	fi
	if docker image inspect "$tag" >/dev/null 2>&1; then
		docker image rm "$tag" >/dev/null
	fi
done

echo "cleaned E2E resources for RUN_ID=$RUN_ID"
