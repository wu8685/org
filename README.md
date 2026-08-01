# org

org 让团队把自己编写的 Worker image 交给一个统一控制面运行，并以业务可理解的 Workflow、动态 DAG、人工操作和 Run 状态观察执行过程。用户保有代码、CI 与镜像；org 负责版本发布、运行编排、Tenant 范围授权和本地 Kubernetes 部署。

> 产品术语遵循 [org glossary](docs/architecture/glossary.md)：用户隔离边界统一称 Tenant。

## 你能得到什么

- 用 `Tenant → Worker → Version → Workflow → Run` 组织业务执行。
- 每个发布版本使用不可变 OCI `image@sha256:...`，Current 与显式历史版本可以并存。
- Worker 启动后由 Org SDK 自动注册只读 Workflow contract；Console 不要求上传或编辑 contract 文件。
- 每次触发创建独立 Run；页面展示动态 DAG、逐节点状态、等待原因与允许的人工操作。
- 人工确认经过 org Gateway 的授权、schema 校验和幂等处理，不让终端用户直连执行引擎。

## 用户路径

```text
编写 Org SDK Definition 与 Activities
  → 在自己的 Worker repository 测试
  → 由自己的 CI 构建并推送 image
  → 得到 immutable image digest
  → 在 org Console/API 发布 WorkerVersion
  → SDK 自动注册 contract
  → 触发 Workflow 并观察 Run 的动态 DAG
```

org 不替用户构建或发布镜像。三个 [`samples/`](samples/README.md) 都按独立 Worker repository 组织，可以从各自目录完成测试、构建、push 或本地 kind load。

## 五个核心对象

| 对象 | 用户视角 |
|---|---|
| Tenant | 认证主体所属的产品隔离边界；请求不能自行指定另一个 Tenant |
| Worker | 稳定的逻辑执行边界，例如 `hello-worker` |
| Version | 一次不可变 image/runtime发布，带独立description与source provenance |
| Workflow | Worker通过Org SDK声明的可触发业务流程 |
| Run | 一次独立执行，包含选定版本、状态、semantic projection与允许操作 |

## 最短本地上手

前置条件：Go 1.26、Docker、`kubectl`、`kind`、Temporal CLI 与 `make`。

在三个终端分别启动本地依赖：

```sh
make kind-up
make temporal-dev
ORG_REGISTRY_ALLOWLIST=org.local,ghcr.io make console-dev
```

然后只进入一个 Sample 目录：

```sh
cd samples/hello
make test
make kind-load VERSION=2026.08.1 COMMIT=$(git rev-parse --short=12 HEAD)
```

命令会输出 `IMAGE_DIGEST=org.local/hello-worker@sha256:...`。打开 [http://127.0.0.1:8090](http://127.0.0.1:8090)，创建 `hello-worker`，用该digest发布版本，再触发 `HelloWorkflow`。

完整步骤见 [Getting Started](docs/getting-started.md)；职责边界见 [Architecture Overview](docs/architecture/overview.md)。

## Console 与 API

Console 提供 Worker/Version发布、Workflow目录、Run列表、动态DAG和人工action。HTTP/JSON API承载同一产品契约，Tenant来自认证主体，release contract由候选Worker自动注册并只读展示。

Temporal Web只用于高级诊断。普通用户不需要知道Task Queue、Signal名称或底层routing，也不持有Temporal或Kubernetes凭证。

## 边界与安全提示

- MVP使用共享的底层运行基础设施；Tenant隔离由org控制面执行，不宣称是基础设施级硬隔离。
- OCI digest将发布绑定到具体image，但不证明image本身可信；供应链签名/attestation不在当前MVP内。
- Workflow代码不得直接执行外部I/O。外部调用只能在Activity中；write Activity必须使用稳定idempotency key，或声明reconciliation/compensation policy。
- Temporal重试不等于外部副作用exactly once。外部系统成功而完成确认丢失时，Activity必须安全重试或reconcile。
- 不要把Secret、敏感input或credential写进image、Workflow history、projection、log或Audit。
- 本地Console是loopback开发入口，不是production authentication/deployment方案。

## 文档导航

- [Getting Started](docs/getting-started.md)
- [Sample learning path](samples/README.md)
- [Architecture Overview](docs/architecture/overview.md)
- [Development and local E2E](docs/development.md)
- [Approved specifications](docs/specs/)
