#!/bin/sh

set -eu

files='docs/development/specs/001-worker-hosting-mvp.md
docs/development/specs/002-hello-worker-sample.md
docs/development/specs/003-multi-tenant-shared-infrastructure.md
docs/development/specs/004-worker-identity-and-description.md
docs/development/specs/005-interactive-parallel-dag-contract.md
docs/development/specs/006-org-sdk.md
docs/development/specs/007-hello-org-sdk-sample.md
docs/development/specs/008-parallel-confirmation-org-sdk-sample.md
docs/development/specs/009-dynamic-decision-org-sdk-sample.md
docs/development/specs/010-workflow-execution-risk-defense.md
docs/development/specs/011-console-ui-http-api.md
docs/development/specs/012-worker-bootstrap-registration.md
docs/development/specs/013-sample-repository-independence.md
docs/development/specs/014-sample-slimming.md
docs/development/specs/015-review-fixes.md
docs/development/specs/016-local-demo-reset.md
docs/development/specs/022-worker-starter-and-development-guide.md
docs/development/ui-sdd-input.md
docs/development/README.md
docs/development/implementation-status.md
README.md
docs/README.md
docs/user/README.md
docs/user/concepts.md
docs/user/getting-started.md
docs/user/create-your-worker.md
docs/user/write-your-worker.md
docs/user/architecture/overview.md
docs/user/api/publish-worker-version.md'

failed=0
glossary='docs/user/architecture/glossary.md'

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
