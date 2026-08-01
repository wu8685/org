#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)

for required in docker kind kubectl make nc; do
	command -v "$required" >/dev/null || {
		echo "missing E2E prerequisite: $required" >&2
		exit 1
	}
done

docker info >/dev/null
kind get clusters | grep -Fqx org || {
	echo "missing kind cluster: org" >&2
	exit 1
}
kubectl config get-contexts -o name | grep -Fqx kind-org || {
	echo "missing Kubernetes context: kind-org" >&2
	exit 1
}
kubectl --context kind-org wait --for=condition=Ready node --all --timeout=30s >/dev/null
nc -z 127.0.0.1 7233 || {
	echo "Temporal is not reachable at 127.0.0.1:7233" >&2
	exit 1
}

test -f "$repo_root/samples/hello/Dockerfile" || {
	echo "missing samples/hello/Dockerfile" >&2
	exit 1
}
test -f "$repo_root/samples/parallel-confirmation/Dockerfile" || {
	echo "missing samples/parallel-confirmation/Dockerfile" >&2
	exit 1
}
test -f "$repo_root/samples/dynamic-decision/Dockerfile" || {
	echo "missing samples/dynamic-decision/Dockerfile" >&2
	exit 1
}

echo "E2E prerequisites ready: Temporal 127.0.0.1:7233, Kubernetes kind-org, Sample Dockerfiles present"
