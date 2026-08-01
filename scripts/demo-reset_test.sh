#!/bin/sh
set -eu

project_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
subject="$project_root/scripts/demo-reset.sh"
test_root=$(mktemp -d "${TMPDIR:-/tmp}/org-demo-reset-test.XXXXXX")
trap 'rm -rf "$test_root"' EXIT HUP INT TERM

fail() {
	echo "demo-reset test: $*" >&2
	exit 1
}

new_fixture() {
	name=$1
	unset RESET_TEST_ACTIVE_PORT RESET_TEST_CONTEXT RESET_TEST_NAMESPACE_EXISTS RESET_TEST_NAMESPACE_ERROR RESET_TEST_NONORG RESET_TEST_ORG_STATE_FILE RESET_TEST_ORG_TEMPORAL_DB_FILE
	fixture="$test_root/$name"
	mkdir -p "$fixture/scripts" "$fixture/.git" "$fixture/.org" "$fixture/fake-bin"
	cp "$subject" "$fixture/scripts/demo-reset.sh"
	chmod +x "$fixture/scripts/demo-reset.sh"
	printf '{"workers":{"demo":{}}}\n' >"$fixture/.org/state.json"
	printf 'sqlite-main\n' >"$fixture/.org/temporal.db"
	printf 'sqlite-wal\n' >"$fixture/.org/temporal.db-wal"
	: >"$fixture/kubectl.log"
	: >"$fixture/mutation.log"

	cat >"$fixture/fake-bin/lsof" <<'EOF'
#!/bin/sh
case "${RESET_TEST_ACTIVE_PORT:-}" in
	8090|7233)
		case "$*" in *TCP:"$RESET_TEST_ACTIVE_PORT"*) echo 4242; exit 0;; esac
		;;
esac
exit 1
EOF
	cat >"$fixture/fake-bin/kubectl" <<'EOF'
#!/bin/sh
set -eu
printf '%s\n' "$*" >>"$RESET_TEST_KUBECTL_LOG"
case "$*" in
	"config current-context") printf '%s\n' "${RESET_TEST_CONTEXT:-kind-org}" ;;
	"--context kind-org get namespace org-workers")
		[ -z "${RESET_TEST_NAMESPACE_ERROR:-}" ] || { echo "$RESET_TEST_NAMESPACE_ERROR" >&2; exit 1; }
		[ "${RESET_TEST_NAMESPACE_EXISTS:-1}" = 1 ] || { echo 'Error from server (NotFound): namespaces "org-workers" not found' >&2; exit 1; }
		;;
	"--context kind-org -n org-workers get "*"app.kubernetes.io/managed-by!=org"*)
		printf '%s\n' serviceaccount/default configmap/kube-root-ca.crt
		[ -z "${RESET_TEST_NONORG:-}" ] || printf '%s\n' "$RESET_TEST_NONORG"
		;;
	"--context kind-org -n org-workers delete "*)
		printf 'delete\n' >>"$RESET_TEST_MUTATION_LOG"
		;;
	"--context kind-org create namespace org-workers")
		printf 'create-namespace\n' >>"$RESET_TEST_MUTATION_LOG"
		;;
	"--context kind-org label namespace org-workers app.kubernetes.io/managed-by=org")
		printf 'label-namespace\n' >>"$RESET_TEST_MUTATION_LOG"
		;;
	"--context kind-org get namespace org-workers -o jsonpath="*) printf 'Active' ;;
	"--context kind-org -n org-workers get "*"app.kubernetes.io/managed-by=org"*) ;;
	*) echo "unexpected kubectl call: $*" >&2; exit 64 ;;
esac
EOF
	chmod +x "$fixture/fake-bin/lsof" "$fixture/fake-bin/kubectl"
}

run_reset() {
	fixture=$1
	shift
	env \
		PATH="$fixture/fake-bin:/usr/bin:/bin" \
		RESET_TEST_KUBECTL_LOG="$fixture/kubectl.log" \
		RESET_TEST_MUTATION_LOG="$fixture/mutation.log" \
		RESET_TEST_ACTIVE_PORT="${RESET_TEST_ACTIVE_PORT:-}" \
		RESET_TEST_CONTEXT="${RESET_TEST_CONTEXT:-kind-org}" \
		RESET_TEST_NAMESPACE_EXISTS="${RESET_TEST_NAMESPACE_EXISTS:-1}" \
		RESET_TEST_NAMESPACE_ERROR="${RESET_TEST_NAMESPACE_ERROR:-}" \
		RESET_TEST_NONORG="${RESET_TEST_NONORG:-}" \
		ORG_STATE_FILE="${RESET_TEST_ORG_STATE_FILE:-}" \
		ORG_TEMPORAL_DB_FILE="${RESET_TEST_ORG_TEMPORAL_DB_FILE:-}" \
		"$fixture/scripts/demo-reset.sh" "$@"
}

assert_untouched() {
	fixture=$1
	[ -f "$fixture/.org/state.json" ] || fail "$fixture state was changed"
	[ -f "$fixture/.org/temporal.db" ] || fail "$fixture Temporal DB was changed"
	[ -f "$fixture/.org/temporal.db-wal" ] || fail "$fixture Temporal sidecar was changed"
	[ ! -s "$fixture/mutation.log" ] || fail "$fixture Kubernetes was mutated: $(cat "$fixture/mutation.log")"
}

new_fixture missing-confirmation
if run_reset "$fixture" >"$fixture/output" 2>&1; then
	fail "reset without confirmation succeeded"
fi
grep -q 'RESET PLAN' "$fixture/output" || fail "missing confirmation did not print plan"
assert_untouched "$fixture"

new_fixture dry-run
run_reset "$fixture" --dry-run >"$fixture/output" 2>&1
grep -q 'retain all Docker and kind images' "$fixture/output" || fail "dry-run did not name retained images"
grep -q 'context: kind-org' "$fixture/output" || fail "dry-run did not print fixed context"
grep -q 'platform Kubernetes Namespace: org-workers' "$fixture/output" || fail "dry-run did not print fixed platform Kubernetes Namespace"
grep -Eq '\.org/reset-backups/[0-9]{8}T[0-9]{6}Z-[0-9]+' "$fixture/output" || fail "dry-run did not print exact backup directory"
assert_untouched "$fixture"

new_fixture wrong-context
if RESET_TEST_CONTEXT=production run_reset "$fixture" --yes >"$fixture/output" 2>&1; then
	fail "reset accepted non-kind context"
fi
grep -q 'kind-org' "$fixture/output" || fail "wrong-context refusal was not actionable"
assert_untouched "$fixture"

new_fixture wrong-state-path
if RESET_TEST_ORG_STATE_FILE=/tmp/not-org-state.json run_reset "$fixture" --yes >"$fixture/output" 2>&1; then
	fail "reset accepted arbitrary state path"
fi
assert_untouched "$fixture"

new_fixture wrong-temporal-path
if RESET_TEST_ORG_TEMPORAL_DB_FILE=/tmp/not-org-temporal.db run_reset "$fixture" --yes >"$fixture/output" 2>&1; then
	fail "reset accepted arbitrary Temporal DB path"
fi
assert_untouched "$fixture"

new_fixture unknown-argument
if run_reset "$fixture" --force >"$fixture/output" 2>&1; then
	fail "reset accepted unknown argument"
fi
assert_untouched "$fixture"

new_fixture symlink-state
mv "$fixture/.org/state.json" "$fixture/state-target.json"
ln -s "$fixture/state-target.json" "$fixture/.org/state.json"
if run_reset "$fixture" --yes >"$fixture/output" 2>&1; then
	fail "reset accepted symlink state path"
fi
[ -L "$fixture/.org/state.json" ] || fail "symlink guard mutated state"
[ ! -s "$fixture/mutation.log" ] || fail "symlink guard mutated Kubernetes"

new_fixture symlink-backup
mkdir "$fixture/backup-target"
ln -s "$fixture/backup-target" "$fixture/.org/reset-backups"
if run_reset "$fixture" --yes >"$fixture/output" 2>&1; then
	fail "reset accepted symlink backup directory"
fi
assert_untouched "$fixture"

new_fixture active-console
if RESET_TEST_ACTIVE_PORT=8090 run_reset "$fixture" --yes >"$fixture/output" 2>&1; then
	fail "reset accepted active Console"
fi
grep -q '8090' "$fixture/output" || fail "active Console refusal omitted port"
assert_untouched "$fixture"

new_fixture active-temporal
if RESET_TEST_ACTIVE_PORT=7233 run_reset "$fixture" --yes >"$fixture/output" 2>&1; then
	fail "reset accepted active Temporal"
fi
grep -q '7233' "$fixture/output" || fail "active Temporal refusal omitted port"
assert_untouched "$fixture"

new_fixture non-org-resource
if RESET_TEST_NONORG=deployment.apps/user-owned run_reset "$fixture" --yes >"$fixture/output" 2>&1; then
	fail "reset accepted non-org resource"
fi
grep -q 'deployment.apps/user-owned' "$fixture/output" || fail "non-org refusal omitted object"
assert_untouched "$fixture"

new_fixture namespace-read-error
if RESET_TEST_NAMESPACE_ERROR='kubeconfig not found' run_reset "$fixture" --yes >"$fixture/output" 2>&1; then
	fail "reset accepted ambiguous Namespace read failure"
fi
assert_untouched "$fixture"

new_fixture confirmed-existing
RESET_DEMO=1 run_reset "$fixture" >"$fixture/output" 2>&1
[ ! -e "$fixture/.org/state.json" ] || fail "confirmed reset retained live state"
[ ! -e "$fixture/.org/temporal.db" ] || fail "confirmed reset retained live Temporal DB"
[ ! -e "$fixture/.org/temporal.db-wal" ] || fail "confirmed reset retained live Temporal sidecar"
backup=$(find "$fixture/.org/reset-backups" -mindepth 1 -maxdepth 1 -type d | head -n 1)
[ -n "$backup" ] || fail "confirmed reset did not create backup"
for file in state.json temporal.db temporal.db-wal; do
	[ -f "$backup/$file" ] || fail "backup missing $file"
done
grep -q '^delete$' "$fixture/mutation.log" || fail "confirmed reset did not delete org resources"
if grep -Eq 'delete namespace|kind delete|workflow|docker|crictl|org-e2e' "$fixture/kubectl.log"; then
	fail "confirmed reset crossed its ownership boundary: $(cat "$fixture/kubectl.log")"
fi
if grep -q '^label-namespace$' "$fixture/mutation.log"; then
	fail "existing platform Kubernetes Namespace was adopted"
fi

new_fixture confirmed-missing-namespace
RESET_TEST_NAMESPACE_EXISTS=0 run_reset "$fixture" --yes >"$fixture/output" 2>&1
grep -q '^create-namespace$' "$fixture/mutation.log" || fail "missing platform Kubernetes Namespace was not created"
grep -q '^label-namespace$' "$fixture/mutation.log" || fail "new platform Kubernetes Namespace was not marked as org-created"

echo "demo-reset contract tests passed"
