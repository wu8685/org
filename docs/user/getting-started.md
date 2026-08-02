# 本地快速上手

按照下面的步骤启动本地 org，并运行仓库自带的 Hello Sample。

## 完成后你会得到

- 一个名为 `kind-org` 的本地 Kubernetes cluster。
- 一个本地 Temporal development server。
- 一个本地 org Console。
- 一个已发布的 `hello-worker` Version。
- 一个完成的 `HelloWorkflow` Run。

如果还不理解 Worker、Version 和 Run，先读 [核心概念](concepts.md)。

## 前置条件

- 已验证的 golden path：macOS + Docker Desktop + 本机 kind。Linux 或其他 container runtime 可以用于开发，但尚未作为本教程的完整验证环境。
- Go 1.26（用于构建 Worker）
- Docker Desktop 正在运行
- `kubectl`
- `kind`
- Temporal CLI
- `make`

确认 Docker 正在运行，并确保端口 `7233`、`8080`、`8090` 没有被其他程序占用。

在仓库根目录检查命令是否齐全：

```sh
make check-tools
go version
make --version
docker info
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

输出中应出现一个状态为 `Ready` 的 node。

## 第 2 步：启动 Temporal

在终端 A 的仓库根目录运行：

```sh
make temporal-dev
```

### 检查点

终端 A 保持运行，并显示 Temporal development server 已在 `127.0.0.1:7233` 启动。

## 第 3 步：启动 org Console

在终端 B 的仓库根目录运行：

```sh
make console-dev
```

打开 [http://127.0.0.1:8090](http://127.0.0.1:8090)。

### 检查点

页面顶部应显示当前 Tenant `Local Development`，侧边栏显示总览、Workers、Workflows 和 Runs。

## 第 4 步：构建 Hello Worker

在终端 C 进入 Hello Sample：

```sh
cd samples/hello
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

> 在 Console 打开 Workers，创建 Worker `hello-worker`，点击“录入版本”。
>
> version 填写 `2026.08.1`，description 填写 `First local Hello release`，image 粘贴上一步的 `IMAGE_DIGEST`。
>
> 点击“开始发布”，等待发布成功。

如果需要使用 HTTP API，字段和 CSRF/Idempotency 规则见 [发布 WorkerVersion](api/publish-worker-version.md)。

## 第 6 步：触发第一个 Run

> 打开 Workflows，选择 `HelloWorkflow`。
>
> Trigger 使用默认的 YAML 输入：`name: Codex`。
>
> Run description 可选，可以留空。启动 Workflow 后，打开新建的 Run。

输入解析失败时，Trigger 会保留原内容并标出 JSON/YAML path。HTTP/JSON API 见 [启动 Workflow Run](api/start-workflow-run.md)。

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

该命令删除 `kind-org`。

## 重置本地 demo

遇到失败发布或想重新演示时，先停止终端 A 的 Temporal 和终端 B 的 Console。

先查看精确计划：

```sh
make demo-reset-dry-run
```

确认后执行：

```sh
RESET_DEMO=1 make demo-reset
```

原有 demo 数据会备份到 `.org/reset-backups/`。命令完成后，重新执行第 2、3 步。

Console 中的 Workers 和 Runs 应恢复为空。

## 成功之后学什么

- [创建你的第一个 Worker](create-your-worker.md)：从 Worker Starter 开发、构建并发布自己的 Workflow。
- [Hello Sample](../../samples/hello/README.md)：回看刚才运行的 Definition 和 Activities。
- [Parallel Confirmation](../../samples/parallel-confirmation/README.md)：学习人工确认、等待恢复和并行分支。
- [Dynamic Decision](../../samples/dynamic-decision/README.md)：学习根据 Activity result 选择 runtime 分支。
- [架构概览](architecture/overview.md)：理解 org、Worker、Temporal 和 Kubernetes 的职责边界。

## 可选：push 到自己的 registry

完成 golden path 后，如果要使用自己的 registry，在 Sample 目录运行：

```sh
make push \
  IMAGE_REPOSITORY=registry.example.com/team/hello-worker \
  VERSION=2026.08.1 \
  COMMIT=$(git rev-parse --short=12 HEAD)
```

命令完成后，复制 registry 返回的 `IMAGE_DIGEST`。

## 遇到问题

| 现象 | 首先检查 |
|---|---|
| `kind-org` node 不是 Ready | Docker 是否运行；`kind` 与 `kubectl` 是否可用 |
| Worker Pod 连接 Temporal 失败 | host Temporal 是否仍在 `127.0.0.1:7233` 运行；Docker Desktop 是否允许 kind container 访问 host |
| image 被拒绝 | 是否使用完整的 `registry/repository@sha256:<64 lowercase hex>`；是否误用了 tag 或 `tag@digest` |
| Version 一直发布中 | 查看 Worker Pod log |
| Run 没有 DAG | 确认 Version 已发布成功，并选择了正确的 Workflow |

> 产品术语遵循 [org glossary](architecture/glossary.md)：用户隔离边界统一称 Tenant。
