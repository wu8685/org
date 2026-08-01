# 维护者本地开发与 E2E

> 本文遵循 [org glossary](architecture/glossary.md)。产品隔离边界称为 Tenant；配置的基础设施目标是共享 platform Temporal Namespace 和 platform Kubernetes Namespace。

本文面向修改 org control plane、Console、Org SDK adapter 或真实 E2E 的维护者。第一次使用 org 请走 [本地快速上手](getting-started.md)，不要从本页开始。

MVP 开发环境由本地 `kind` Kubernetes cluster 和 host 上的 Temporal development server 组成。control plane 和 Worker 使用不同的连接地址，因为 kind Pod 内的 `localhost` 指向 Pod 自身。

## 环境拓扑

```text
host
  ├── org Console / control plane :8090
  ├── Temporal service            :7233
  └── Temporal Web                :8080

kind-org
  └── platform Kubernetes Namespace
        └── candidate / Ready Worker Pods
```

control plane 通过 `127.0.0.1:7233` 访问 Temporal；kind Pod 在 Docker Desktop 上通过 `host.docker.internal:7233` 访问同一个服务。

## 前置条件

- Go 1.26+
- Docker
- `kubectl`
- `kind`
- Temporal CLI

本地验收环境应提供 `org` kind cluster（`kind-org` context），并在 `127.0.0.1:7233` 提供 Temporal。`make e2e-preflight` 只验证它们的当前状态，不创建或修改资源。

## 启动依赖

```sh
make check-tools
make kind-up
make temporal-dev
```

`make temporal-dev` 在 `127.0.0.1:7233` 启动服务，在 `http://127.0.0.1:8080` 启动高级诊断 UI，并将开发状态持久化到 `.org/temporal.db`。

在 Docker Desktop 上，kind Pod 通过 `host.docker.internal:7233` 访问 host 服务。在 Linux 上，将 `ORG_WORKER_TEMPORAL_ADDRESS` 设置为 kind node 可访问的地址。不要把它设置为 Pod-local `127.0.0.1`。

## Runtime 配置

| 变量 | 开发环境默认值 | 用途 |
|---|---|---|
| `ORG_TEMPORAL_ADDRESS` | `127.0.0.1:7233` | host-side control-plane 连接地址 |
| `ORG_WORKER_TEMPORAL_ADDRESS` | `host.docker.internal:7233` | 注入 Worker Pod 的连接地址 |
| `ORG_WORKER_BOOTSTRAP_ENDPOINT` | `http://host.docker.internal:8090/internal/v1/bootstrap/register` | kind Pod 可访问的内部 endpoint；production 使用 TLS |
| `ORG_TEMPORAL_NAMESPACE` | `default` | 内部使用的共享 platform Temporal Namespace |
| `ORG_TEMPORAL_WEB_URL` | `http://127.0.0.1:8080` | 高级诊断 deep link 的 base URL |
| `ORG_KUBE_CONTEXT` | `kind-org` | 可配置的 Kubernetes context |
| `ORG_KUBECONFIG` | 空 | 可选 kubeconfig 路径 |
| `ORG_KUBE_NAMESPACE` | `org-workers` | Worker workload 使用的共享 platform Kubernetes Namespace |
| `ORG_REGISTRY_ALLOWLIST` | `ghcr.io` | 逗号分隔的 image registry allowlist |
| `ORG_STATE_FILE` | `.org/state.json` | 本地 audit projection store |
| `ORG_CONSOLE_ADDRESS` | `127.0.0.1:8090` | 仅 loopback 可访问的 Console listen address |
| `ORG_CONSOLE_TENANT_ID` | `tenant-local` | 服务端配置的本地 Tenant ID |
| `ORG_CONSOLE_TENANT_SLUG` | `local` | 服务端配置的本地 Tenant slug |
| `ORG_CONSOLE_TENANT_NAME` | `Local Development` | Tenant display name |
| `ORG_CONSOLE_PRINCIPAL_ID` | `local-developer` | 本地认证 principal |

Production 环境提供自己的 Kubernetes context、kubeconfig、Temporal endpoint、credential environment 和 registry allowlist。

## 启动 Console

内置 executable 有意设计为仅 loopback 可访问的开发入口。Tenant identity 和 permission 来自服务端配置；request header 不能选择 Tenant。Production deployment 必须将 `console.Authenticator` 边界接入真实的 session 和 membership system。

```sh
ORG_REGISTRY_ALLOWLIST=org.local,ghcr.io make console-dev
```

打开 `http://127.0.0.1:8090`。侧边栏只包含总览、Workers、Workflows 和 Runs。候选 Worker 在启动时自动注册 Org SDK contract；Console 只展示 registration/probe 状态和生成的只读 contract。Runtime DAG 来自经过验证的 semantic projection；节点 action 通过 Gateway action API 提交。

## Image 边界

`org` 不构建或发布 Worker image（does not build user images）。发布请求接收已经发布、由 `sha256` digest 固定的 OCI reference，以及 version description 和 runtime reference。部署后的 Org SDK 自动注册生成的 contract；Console 不接收 manifest upload，也不接收 repository、branch、commit 或 CI reference。可信 audit metadata 由 control plane 记录。

## 日常验证

每个 Sample 目录是用户路径的权威入口：

```sh
cd samples/hello
make verify
make image VERSION=2026.08.1 COMMIT=$(git rev-parse --short=12 HEAD)
```

根级 Sample target 只为维护者聚合验收，并委托到对应 Sample Makefile：

```sh
make docs-test
make backend-test
make sample-test
make parallel-sample-test
make dynamic-sample-test
```

## 真实本地端到端验收

control-plane 验收测试需要显式启用，因为它会构建 image 并创建真实本地资源。测试将每个独立 Sample Worker repository 作为外部用户 fixture；测试代码位于 `org` 的 `test/e2e`。E2E harness 以 Sample 目录为工作目录调用 `make kind-load`，然后发布返回的 digest。

```sh
make e2e-preflight
make e2e-local
make parallel-e2e-local
make dynamic-e2e-local
```

`e2e-preflight` 是只读检查。只有 Docker、`kind-org`、Ready Kubernetes node、`127.0.0.1:7233` 上的 Temporal 和三个 Sample Dockerfile 全部可用时，检查才会通过。生成的 manifest 文件不是 runtime 前置条件。

`e2e-local` 使用 Hello，并依次执行以下步骤：

1. 创建一个由两个测试 Tenant 共享的唯一 platform Kubernetes Namespace，以及一个幂等 downstream fixture。
2. 构建 sample version A 和 B，并以不可变 digest alias 加载到 kind。
3. 通过 `ControlPlane` 提交真实的 digest-only deployment request。
4. 等待 Kubernetes ready 和 Temporal Worker polling。
5. 验证未指定版本的 invocation 使用 Current version B。
6. 验证 semantic DAG projection 和结果。
7. 验证显式历史版本 invocation 使用 pinned version A。
8. 删除唯一的 platform Kubernetes Namespace 和测试创建的 image tag/alias。

`parallel-e2e-local` 使用 `samples/parallel-confirmation`，验证 idle `waiting-for-user`、Worker restart、经过 Gateway 授权且幂等的 `confirm` action、两个 runtime branch、join/finalize、action outcome reconciliation 和 Tenant audit。

`dynamic-e2e-local` 使用 `samples/dynamic-decision`，同时运行 `concise` 和 `detailed` route。每个 Run 只执行选中的 Activity，同时将未选节点保留为 `skipped`，最后完成共同的 finalize node。

测试在修改资源前打印 `E2E RUN_ID=<id>`。成功或失败时都会自动清理。如果进程在 Go cleanup 执行前被中断，使用打印的准确标识进行恢复：

```sh
make e2e-clean RUN_ID=<id>
```

这些 target 不会删除 `kind-org` cluster、host Temporal process、无关的 platform Kubernetes Namespace resource 或无关 image。普通 `go test ./...` 会完成编译但跳过基础设施测试；只有显式 E2E target 会设置 `ORG_E2E=1`。

真实 Hello 验收还会遍历 Console HTTP read contract，覆盖 Worker/Workflow、Current 和显式历史 Run detail、dynamic semantic projection、Tenant isolation，以及 server-rendered Run shell。
