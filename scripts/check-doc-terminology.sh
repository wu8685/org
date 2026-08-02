#!/bin/sh

set -eu

files='docs/specs/001-worker-hosting-mvp.md
docs/specs/002-hello-worker-sample.md
docs/specs/003-multi-tenant-shared-infrastructure.md
docs/specs/004-worker-identity-and-description.md
docs/specs/005-interactive-parallel-dag-contract.md
docs/specs/006-org-sdk.md
docs/specs/007-hello-org-sdk-sample.md
docs/specs/008-parallel-confirmation-org-sdk-sample.md
docs/specs/009-dynamic-decision-org-sdk-sample.md
docs/specs/010-workflow-execution-risk-defense.md
docs/specs/011-console-ui-http-api.md
docs/specs/012-worker-bootstrap-registration.md
docs/specs/013-sample-repository-independence.md
docs/specs/014-sample-slimming.md
docs/specs/015-review-fixes.md
docs/specs/016-local-demo-reset.md
docs/ui-sdd-input.md
docs/development.md
docs/implementation-status.md
README.md
docs/README.md
docs/concepts.md
docs/getting-started.md
docs/write-your-worker.md
docs/architecture/overview.md
docs/api/publish-worker-version.md
samples/README.md
samples/hello/README.md
samples/parallel-confirmation/README.md
samples/dynamic-decision/README.md'

failed=0
glossary='docs/architecture/glossary.md'

if [ ! -f "$glossary" ]; then
	echo "$glossary: canonical glossary is missing" >&2
	exit 1
fi

for required in \
	'产品/API/UI 层统一使用 **Tenant**' \
	'one shared platform Temporal Namespace' \
	'one shared platform Kubernetes Namespace' \
	'Tenant 不映射为、也不拥有底层 platform Temporal Namespace 或 platform Kubernetes Namespace'
do
	if ! grep -Fq "$required" "$glossary"; then
		echo "$glossary: missing required terminology invariant: $required" >&2
		failed=1
	fi
done

for file in $files; do
	if ! grep -q 'glossary.md' "$file"; then
		echo "$file: missing canonical glossary reference" >&2
		failed=1
	fi

	remaining=$(
		perl -pe '
			s/platform Temporal Namespace//ig;
			s/platform Kubernetes Namespace//ig;
			s/Namespace \(Tenant\)//ig;
		' "$file" | grep -Ein '\bnamespace\b|命名空间' || true
	)
	if [ -n "$remaining" ]; then
		echo "$file: ambiguous Namespace terminology:" >&2
		echo "$remaining" >&2
		failed=1
	fi
done

if [ "$failed" -ne 0 ]; then
	exit 1
fi

echo "documentation terminology check passed"
