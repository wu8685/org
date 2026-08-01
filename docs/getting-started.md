# 本地快速上手

这是一条只包含必要步骤的 golden path。你会使用仓库自带的 Hello Sample，不需要先编写 Worker，也不需要准备 contract 文件。

## 完成后你会得到

- 一个名为 `kind-org` 的本地 Kubernetes cluster。
- 一个本地 Temporal development server。
- 一个只绑定 loopback 的 org Console。
- 一个从 Hello Sample 构建的 immutable image digest。
- 一个 Ready / Current 的 `hello-worker` Version。
- 一个完成的 `HelloWorkflow` Run，以及三节点 dynamic DAG。

如果还不理解 Worker、Version 和 Run，先读 [核心概念](concepts.md)。

## 前置条件

- Go 1.26
- Docker
- `kubectl`
- `kind`
- Temporal CLI
- `make`

确认 Docker 正在运行，并确保端口 `7233`、`8080`、`8090` 没有被其他程序占用。

在仓库根目录检查命令是否齐全：

```sh
make check-tools
```

整个过程需要三个终端：

| 终端 | 保持运行的进程 |
|---|---|
| A | Temporal development server |
| B | org Console |
| C | 执行检查、构建和操作命令 |

## 第 1 步：创建本地 cluster

在终端 C 的仓库根目录运行：

```sh
make kind-up
kubectl --context kind-org get nodes
```

### 检查点

输出中应出现一个状态为 `Ready` 的 node，当前 context 名为 `kind-org`。

如果这里失败，先修复 Docker、`kind` 或 `kubectl`，不要继续后面的步骤。

## 第 2 步：启动 Temporal

在终端 A 的仓库根目录运行：

```sh
make temporal-dev
```

该命令会保持运行：

- Temporal service：`127.0.0.1:7233`
- Temporal Web：`http://127.0.0.1:8080`

### 检查点

终端 A 应保持运行，并显示 Temporal development server 已启动。不要关闭这个进程。

Temporal Web 是高级诊断入口；完成新手流程不需要操作它。

## 第 3 步：启动 org Console

在终端 B 的仓库根目录运行：

```sh
ORG_REGISTRY_ALLOWLIST=org.local,ghcr.io make console-dev
```

打开 [http://127.0.0.1:8090](http://127.0.0.1:8090)。本地 Console 只绑定 loopback，并使用服务端配置的开发 Tenant 和 principal。

### 检查点

页面侧边栏应显示总览、Workers、Workflows 和 Runs。此时列表为空是正常的。

## 第 4 步：构建 Hello Worker

在终端 C 进入 Hello Sample：

```sh
cd samples/hello
make test
make vet
make kind-load \
  VERSION=2026.08.1 \
  COMMIT=$(git rev-parse --short=12 HEAD)
```

`make kind-load` 会从当前 Sample 目录构建 image、加载到 `kind-org`，并打印：

```text
IMAGE_TAG=org.local/hello-worker:2026.08.1-<commit>
IMAGE_DIGEST=org.local/hello-worker@sha256:<digest>
```

### 检查点

复制完整的 `IMAGE_DIGEST`。后续发布只使用 digest，不使用 tag，也不要把 tag 拼成 `tag@digest`。

## 第 5 步：发布 WorkerVersion

回到 Console：

1. 打开 Workers，创建 Worker `hello-worker`。
2. 在该 Worker 下新建 Version。
3. version 填写 `2026.08.1`。
4. description 填写例如 `First local Hello release`。
5. image 粘贴上一步的 `IMAGE_DIGEST`。
6. runtime 填写 `100m` CPU 和 `128Mi` memory。
7. source 填写当前 repository、branch、commit 和 CI reference；本地体验可以填写清晰的开发标识。
8. 提交发布。

候选 Pod 启动后，Org SDK 会从 typed Definition 在内存中生成 contract 并自动注册。Console 只读展示 contract，不要求上传 manifest。

### 检查点

等待以下阶段全部通过：

```text
candidate deployment
  → SDK automatic registration
  → Worker polling
  → pinned contract probe
  → Ready / Current
```

如果需要使用 HTTP API，字段和 CSRF/Idempotency 规则见 [发布 WorkerVersion](api/publish-worker-version.md)。

## 第 6 步：触发第一个 Run

1. 打开 Workflows。
2. 选择 `HelloWorkflow`。
3. 输入：

```json
{"name":"Codex"}
```

4. 触发 Workflow，并打开新建的 Run。

### 检查点

Run detail 应显示：

```text
prepare-greeting → compose-greeting → completed
```

三个节点最终都应为 `completed`，结果中包含 `Hello, Codex!` 和实际使用的 Worker Version。

到这里，第一次完整体验已经完成。

## 第 7 步：清理

普通结束只需停止终端 A 和 B 中的进程。Worker、Version 和 Run 会保存在本仓库的 local demo 数据中，方便下次继续。

如果需要重新从空 demo 开始，见下一节。如果连本地 cluster 也不再需要，可以运行：

```sh
make kind-down
```

该命令只删除 `kind-org`，不会删除无关 Kubernetes cluster。

## 重置本地 demo

遇到失败发布或想重新演示 golden path 时，可以使用仓库提供的受限 reset。先停止终端 A 的 Temporal 和终端 B 的 Console；脚本检测到端口 `7233` 或 `8090` 仍被监听时会拒绝执行，不会直接终止进程。

先查看精确计划：

```sh
make demo-reset-dry-run
```

确认后执行：

```sh
RESET_DEMO=1 make demo-reset
```

该命令只处理本仓库 `.org/state.json`、`.org/temporal.db` 及其精确 SQLite sidecar，以及固定 `kind-org` / `org-workers` 中带 org 标记的 demo resource。原文件会移动到输出所示的 `.org/reset-backups/` 私有备份；kind cluster、Docker/kind image、E2E resource 和其他 platform Kubernetes Namespace 均保留。脚本不调用 Temporal deletion API，也不接触远程 Temporal resource。

完成后重新执行第 2、3 步的启动命令。Console 应只显示自动初始化的 local Tenant，Workers 和 Runs 为空，`org-workers` 保持 `Active`。

如需恢复 control-plane 或 Temporal 数据，先再次停止两个进程，再把目标备份目录中的文件移回 `.org/`。Kubernetes workload 不在备份中；重新发布 WorkerVersion 即可重建。

## 成功之后学什么

- [Hello Sample](../samples/hello/README.md)：回看刚才运行的 Definition 和 Activities。
- [Parallel Confirmation](../samples/parallel-confirmation/README.md)：学习人工确认、等待恢复和并行分支。
- [Dynamic Decision](../samples/dynamic-decision/README.md)：学习根据 Activity result 选择 runtime 分支。
- [架构概览](architecture/overview.md)：理解 org、Worker、Temporal 和 Kubernetes 的职责边界。

## 可选：push 到自己的 registry

完成 golden path 后，如果要使用自己的 registry，在 Sample 目录运行：

```sh
make push \
  IMAGE_REPOSITORY=registry.example.com/team/hello-worker \
  VERSION=2026.08.1 \
  COMMIT=$(git rev-parse --short=12 HEAD)
```

脚本使用你已经配置的 Docker registry session，输出 registry 返回的 immutable digest，但不保存 registry credential。

## 遇到问题

| 现象 | 首先检查 |
|---|---|
| `kind-org` node 不是 Ready | Docker 是否运行；`kind` 与 `kubectl` 是否可用 |
| Worker Pod 连接 Temporal 失败 | host Temporal 是否仍在 `127.0.0.1:7233` 运行；Docker Desktop 是否允许 kind container 访问 host |
| image 被拒绝 | 是否使用完整的 `registry/repository@sha256:<64 lowercase hex>`；是否误用了 tag 或 `tag@digest` |
| Version 一直等待 registration | 查看候选 Pod log；不要打印 bootstrap token 或 workload token |
| Run 没有 DAG | 读取 org semantic projection；不要从 Temporal Event History 自行推断业务节点 |

> 产品术语遵循 [org glossary](architecture/glossary.md)：用户隔离边界统一称 Tenant。
