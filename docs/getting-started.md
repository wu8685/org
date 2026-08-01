# Getting Started

这条路径从本地依赖启动，到独立Sample构建、WorkerVersion发布和Run观察。你不需要准备contract文件，也不需要理解Temporal内部对象。

> 产品术语遵循 [org glossary](architecture/glossary.md)：用户隔离边界统一称 Tenant。

## 1. 前置条件

- Go 1.26
- Docker
- `kubectl`
- `kind`
- Temporal CLI
- `make`

确认Docker运行正常，并确保端口`7233`、`8080`、`8090`可用。

## 2. 启动 kind 与 Temporal

在仓库根目录创建本地`kind-org`：

```sh
make kind-up
kubectl --context kind-org get nodes
```

在单独终端启动本地Temporal：

```sh
make temporal-dev
```

控制面从host连接`127.0.0.1:7233`；候选Worker Pod使用平台注入的可达地址。用户不需要填写连接地址或Task Queue。

## 3. 启动 Console

在另一个终端运行：

```sh
ORG_REGISTRY_ALLOWLIST=org.local,ghcr.io make console-dev
```

打开 [http://127.0.0.1:8090](http://127.0.0.1:8090)。这个入口只绑定loopback，用于本地体验。

## 4. 在 Sample 自己的目录工作

Hello是最短路径：

```sh
cd samples/hello
make test
make vet
make kind-load \
  VERSION=2026.08.1 \
  COMMIT=$(git rev-parse --short=12 HEAD)
```

`kind-load`从当前目录构建image、加载到`kind-org`，最后打印：

```text
IMAGE_TAG=org.local/hello-worker:2026.08.1-<commit>
IMAGE_DIGEST=org.local/hello-worker@sha256:<digest>
```

发布只使用`IMAGE_DIGEST`。tag便于本地识别，但不是WorkerVersion identity。

若使用自己的registry：

```sh
make push \
  IMAGE_REPOSITORY=registry.example.com/team/hello-worker \
  VERSION=2026.08.1 \
  COMMIT=$(git rev-parse --short=12 HEAD)
```

脚本使用你已配置的Docker registry session，push后按registry结果输出不可变digest；它不保存registry credential。

## 5. 发布 WorkerVersion

字段与HTTP请求示例见 [Publish a WorkerVersion](api/publish-worker-version.md)。Console与API使用同一份digest-only契约。

在Console中：

1. 创建Worker `hello-worker`；
2. 新建Version，填写`2026.08.1`与版本description；
3. image粘贴上一步的`IMAGE_DIGEST`；
4. runtime填写`100m` CPU、`128Mi` memory；
5. source填写你的repository、branch、commit与CI reference；
6. 提交并等待deployment、SDK registration、poller与probe完成。

候选Pod启动时，Org SDK会从typed Definition在内存中构造contract并自动注册。Console只读展示结果；用户无需上传、编辑或把contract放进image。

## 6. 触发并观察 Run

在Workflow目录选择`HelloWorkflow`，输入：

```json
{"name":"Codex"}
```

触发后会创建一个独立Run。Run detail应显示：

```text
prepare-greeting → compose-greeting → completed
```

三个节点依次进入`completed`，最终结果包含`Hello, Codex!`与实际Worker version。

继续学习：

- [`parallel-confirmation`](../samples/parallel-confirmation/README.md)：Run先显示`waiting-for-user`，确认后展开并行分支与join；
- [`dynamic-decision`](../samples/dynamic-decision/README.md)：Activity result选择一个runtime分支，未选分支显示`skipped`。

## 7. 清理

停止Console和Temporal进程。如不再需要本地cluster：

```sh
make kind-down
```

真实验收测试会使用独立的临时Kubernetes资源并负责清理。若测试被外部中断，按终端打印的`RUN_ID`运行：

```sh
make e2e-clean RUN_ID=<printed-id>
```

## 遇到问题

- Pod连接失败：确认host Temporal仍在`127.0.0.1:7233`运行，且Docker Desktop允许kind容器访问host。
- image被拒绝：确认使用完整`registry/repository@sha256:<64 lowercase hex>`，不要使用tag或`tag@digest`。
- Version停在等待注册：检查候选Pod日志，但不要打印bootstrap token或workload token。
- Run没有DAG：读取org semantic projection；不要从Temporal Event History自行推断业务节点。
