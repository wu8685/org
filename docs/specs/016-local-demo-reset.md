# Local demo reset

> Terminology follows the canonical [org glossary](../architecture/glossary.md). This command operates only on this repository's local demo and the fixed `kind-org` development target.

## Status and scope

**Approved — the user authorized implementation on 2026-08-02.**

The user-visible `make demo-reset` command restores the repository's local demo to a clean initialization state after failed or exploratory publishes. It is not a production cleanup tool, an E2E garbage collector, or a general Kubernetes/Temporal administration command.

The reset owns exactly these local targets:

- repository control-plane state at `.org/state.json`;
- repository Temporal development database `.org/temporal.db` and the exact SQLite sidecars `.org/temporal.db-wal`, `.org/temporal.db-shm`, and `.org/temporal.db-journal`;
- org-managed demo resources inside the fixed `org-workers` platform Kubernetes Namespace in the fixed `kind-org` context;
- creation, when absent, of the fixed empty `org-workers` platform Kubernetes Namespace.

It never accepts a user-selected path, context, platform Kubernetes Namespace, label selector, Workflow query, or image reference.

## Command contract

```sh
# inspect the exact plan; never mutates
make demo-reset-dry-run

# execute after stopping Console and local Temporal
RESET_DEMO=1 make demo-reset
```

`scripts/demo-reset.sh --yes` is an equivalent explicit confirmation for direct use. With neither `--yes` nor `RESET_DEMO=1`, the script prints the complete plan and exits without mutation. Unknown flags and positional arguments are rejected.

Before any mutation, the script prints the exact repository root, files, Kubernetes context, platform Kubernetes Namespace, label boundary, retained resources, and automatic backup destination policy. It then completes every preflight check. A refusal or failed preflight leaves files and Kubernetes resources unchanged.

## Safety preflight

The script must reject before mutation when any condition holds:

1. repository root cannot be derived from the script location, `.git` is absent, or root is unsafe;
2. `ORG_STATE_FILE`, when set, is not the canonical `.org/state.json` path; a Temporal DB override, when set, is not `.org/temporal.db`;
3. `.org`, `.org/reset-backups`, state, database, or any recognized sidecar is a symbolic link;
4. `kubectl` is unavailable, current context is not exactly `kind-org`, or `kind-org` cannot be read;
5. a listener exists on Console port `8090` or Temporal port `7233`; the command tells the user to stop the corresponding process and does not signal or kill it;
6. `org-workers` contains a namespaced object in the reset resource set that is neither marked `app.kubernetes.io/managed-by=org` nor one of the Kubernetes-created `serviceaccount/default` and `configmap/kube-root-ca.crt` objects.

The command never removes `kind-org`, any other platform Kubernetes Namespace, or an existing non-org object. It does not call a Temporal delete API; resetting Temporal consists only of moving the stopped, repository-local dev database files.

## Mutation and recovery

After all preflights and explicit confirmation:

1. create `.org/reset-backups/<UTC timestamp>-<process id>/` with mode `0700`;
2. move each existing owned state/database file into that backup, preserving its basename and permissions;
3. delete only the fixed Kubernetes resource kinds inside `org-workers` that carry `app.kubernetes.io/managed-by=org`, using foreground/waiting deletion;
4. if `org-workers` does not exist, create it and label only the newly created platform Kubernetes Namespace `app.kubernetes.io/managed-by=org`; if it already exists, do not mutate its metadata;
5. verify the platform Kubernetes Namespace is `Active`, no org-managed resource from the reset set remains, and only the allowed Kubernetes-created baseline objects remain.

Images are deliberately retained. The reset must not delete Docker or kind/containerd images, including Sample tags, immutable digests, user images, or E2E images. A later image-pruning feature requires a separate spec with an exact ownership ledger.

The successful result is recoverable: stop the restarted services, move the desired backup files back to `.org/`, then restart. Kubernetes resources are not backed up; they are reproducible from WorkerVersion publish and are intentionally absent after reset.

## Post-reset initialization

The normal commands remain the only startup path:

```sh
make temporal-dev
ORG_REGISTRY_ALLOWLIST=org.local,ghcr.io make console-dev
```

Fresh Temporal startup creates its local database and shared platform Temporal Namespace. Fresh Console startup creates `.org/state.json` and seeds only the configured local Tenant. The `org-workers` platform Kubernetes Namespace is already `Active` and contains no org Worker workload.

## TDD and acceptance

Shell contract tests use an isolated copied repository fixture and fake command path. They must prove, before implementation:

- missing confirmation, unknown arguments, wrong context, unexpected path overrides, symlinks, active ports, and non-org platform Kubernetes Namespace objects all refuse without file or Kubernetes mutation;
- dry-run prints all owned and explicitly retained targets without mutation;
- confirmed reset moves only exact state/database basenames, uses only `kind-org` and `org-workers`, preserves images and E2E resources, deletes only the server-owned label, and verifies `Active`;
- an absent platform Kubernetes Namespace is created; an existing platform Kubernetes Namespace is not relabeled or deleted;
- command/docs links and `make` targets remain executable.

Real local acceptance stops Console and Temporal cleanly, runs dry-run and confirmed reset, verifies the backup and empty platform Kubernetes Namespace, restarts both services with the normal commands, and confirms a newly initialized Tenant with no Workers or Runs. It must not publish or trigger a business Run.
