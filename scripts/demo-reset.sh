#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
project_root=$(CDPATH= cd -- "$script_dir/.." && pwd)
state_file="$project_root/.org/state.json"
temporal_db="$project_root/.org/temporal.db"
kube_context=kind-org
kube_namespace=org-workers
managed_selector='app.kubernetes.io/managed-by=org'
unmanaged_selector='app.kubernetes.io/managed-by!=org'
resource_set='pods,deployments.apps,replicasets.apps,statefulsets.apps,daemonsets.apps,jobs.batch,cronjobs.batch,services,configmaps,secrets,serviceaccounts,networkpolicies.networking.k8s.io,roles.rbac.authorization.k8s.io,rolebindings.rbac.authorization.k8s.io,persistentvolumeclaims'
dry_run=0
confirmed=0
timestamp=$(date -u +%Y%m%dT%H%M%SZ)
backup_dir="$project_root/.org/reset-backups/$timestamp-$$"

usage() {
	echo "usage: scripts/demo-reset.sh [--dry-run|--yes]" >&2
}

fail() {
	echo "demo-reset refused: $*" >&2
	exit 1
}

for argument in "$@"; do
	case "$argument" in
		--dry-run) dry_run=1 ;;
		--yes) confirmed=1 ;;
		*) usage; fail "unknown argument: $argument" ;;
	esac
done
if [ "$#" -gt 1 ]; then
	usage
	fail "only one explicit mode is accepted"
fi
if [ "${RESET_DEMO:-}" = 1 ]; then
	confirmed=1
elif [ -n "${RESET_DEMO:-}" ]; then
	fail "RESET_DEMO must be exactly 1"
fi
if [ "$dry_run" -eq 1 ] && [ "$confirmed" -eq 1 ]; then
	fail "--dry-run and confirmation cannot be combined"
fi

cat <<EOF
RESET PLAN
  repository: $project_root
  backup: $backup_dir
  move to the backup: .org/state.json
  move to the same backup: .org/temporal.db and exact SQLite sidecars
  context: $kube_context
  platform Kubernetes Namespace: $kube_namespace
  delete only namespaced resources matching: $managed_selector
  retain Kubernetes-created serviceaccount/default and configmap/kube-root-ca.crt
  retain all Docker and kind images
  retain kind cluster, every other platform Kubernetes Namespace, all remote Temporal resources, and all E2E resources
  do not call Temporal deletion APIs
EOF

if [ "$dry_run" -ne 1 ] && [ "$confirmed" -ne 1 ]; then
	fail "explicit confirmation required; use --yes or RESET_DEMO=1"
fi

[ "$project_root" != / ] || fail "unsafe repository root"
[ -e "$project_root/.git" ] || fail "repository root does not contain .git"
case "${ORG_STATE_FILE:-$state_file}" in
	"$state_file"|.org/state.json) ;;
	*) fail "ORG_STATE_FILE must identify this repository's .org/state.json" ;;
esac
case "${ORG_TEMPORAL_DB_FILE:-$temporal_db}" in
	"$temporal_db"|.org/temporal.db) ;;
	*) fail "ORG_TEMPORAL_DB_FILE must identify this repository's .org/temporal.db" ;;
esac

for path in "$project_root/.org" "$project_root/.org/reset-backups" "$state_file" "$temporal_db" "$temporal_db-wal" "$temporal_db-shm" "$temporal_db-journal"; do
	[ ! -L "$path" ] || fail "symbolic links are not accepted: $path"
done

command -v kubectl >/dev/null 2>&1 || fail "kubectl is required"
command -v lsof >/dev/null 2>&1 || fail "lsof is required for process safety checks"
current_context=$(kubectl config current-context 2>/dev/null) || fail "cannot read the current Kubernetes context"
[ "$current_context" = "$kube_context" ] || fail "current Kubernetes context must be exactly $kube_context (found $current_context)"

for port in 8090 7233; do
	if lsof -nP -tiTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
		case "$port" in
			8090) fail "Console is listening on port 8090; stop it before reset" ;;
			7233) fail "local Temporal is listening on port 7233; stop it before reset" ;;
		esac
	fi
done

namespace_exists=0
namespace_error=$(mktemp "${TMPDIR:-/tmp}/org-demo-reset-namespace.XXXXXX")
trap 'rm -f "$namespace_error"' EXIT HUP INT TERM
if kubectl --context "$kube_context" get namespace "$kube_namespace" >/dev/null 2>"$namespace_error"; then
	namespace_exists=1
elif grep -Eiq "namespaces?[[:space:]]+\"$kube_namespace\"[[:space:]]+not found" "$namespace_error"; then
	namespace_exists=0
else
	error_text=$(tr '\n' ' ' <"$namespace_error")
	fail "cannot verify fixed platform Kubernetes Namespace: $error_text"
fi

check_unmanaged_resources() {
	objects=$(kubectl --context "$kube_context" -n "$kube_namespace" get "$resource_set" -l "$unmanaged_selector" -o name) || fail "cannot inspect non-org objects in $kube_namespace"
	unsafe=
	for object in $objects; do
		case "$object" in
			serviceaccount/default|serviceaccount.serviceaccounts/default|configmap/kube-root-ca.crt|configmap.configmaps/kube-root-ca.crt) ;;
			*) unsafe="$unsafe $object" ;;
		esac
	done
	[ -z "$unsafe" ] || fail "non-org objects exist in $kube_namespace:$unsafe"
}

if [ "$namespace_exists" -eq 1 ]; then
	check_unmanaged_resources
fi

if [ "$dry_run" -eq 1 ]; then
	echo "DRY RUN complete: all preflights passed; no files or Kubernetes resources were changed."
	exit 0
fi

mkdir -p "$backup_dir"
chmod 0700 "$project_root/.org/reset-backups" "$backup_dir"
for path in "$state_file" "$temporal_db" "$temporal_db-wal" "$temporal_db-shm" "$temporal_db-journal"; do
	if [ -e "$path" ]; then
		mv "$path" "$backup_dir/$(basename "$path")"
	fi
done

if [ "$namespace_exists" -eq 1 ]; then
	kubectl --context "$kube_context" -n "$kube_namespace" delete "$resource_set" -l "$managed_selector" --ignore-not-found=true --wait=true --timeout=90s
else
	kubectl --context "$kube_context" create namespace "$kube_namespace"
	kubectl --context "$kube_context" label namespace "$kube_namespace" "$managed_selector"
fi

phase=$(kubectl --context "$kube_context" get namespace "$kube_namespace" -o jsonpath='{.status.phase}') || fail "cannot verify reset platform Kubernetes Namespace"
[ "$phase" = Active ] || fail "reset platform Kubernetes Namespace is not Active (phase=$phase)"
remaining=$(kubectl --context "$kube_context" -n "$kube_namespace" get "$resource_set" -l "$managed_selector" -o name) || fail "cannot verify org resource cleanup"
[ -z "$remaining" ] || fail "org resources remain after reset: $remaining"
check_unmanaged_resources

echo "demo reset complete"
echo "backup: $backup_dir"
echo "restart Temporal: make temporal-dev"
echo "restart Console: ORG_REGISTRY_ALLOWLIST=org.local,ghcr.io make console-dev"
