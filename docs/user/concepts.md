# 核心概念

本页只回答新手最先遇到的三个问题：org 管什么、五个对象是什么、一次发布和一次运行分别发生什么。

> 产品术语遵循 [org glossary](architecture/glossary.md)：用户隔离边界统一称 Tenant。

## 从一个 Worker 开始

Worker 是你交给 org 运行的业务程序。它通常包含：

- Definition：声明有哪些 Workflow、Activity、节点和人工 action。
- Workflow：决定步骤顺序、并行关系、等待和分支。
- Activity：执行可能访问外部系统的工作。
- Org SDK：把业务执行投影成 org 可以展示和验证的 contract 与 dynamic DAG。

例如 Hello Worker 接收一个名字，依次准备和拼接问候语：

```text
prepare-greeting → compose-greeting → completed
```

## 五个对象

### Tenant：谁的资源

Tenant 是产品中的用户、数据和授权边界。认证主体决定当前 Tenant，请求不能自行切换到另一个 Tenant。

本地开发环境默认使用一个本地 Tenant。这里先把它理解成“当前团队空间”即可。

### Worker：什么能力

Worker 是稳定的逻辑运行单元，例如 `hello-worker`。一个 Worker 可以包含多个 Workflow，也可以连续发布多个 Version。

Worker 不是某次部署产生的 Pod，也不是某个 Run。

### Version：运行哪一版

Version 是 Worker 的一次发布。OCI image digest、runtime 配置、Workflow contract 和路由身份一旦创建就不可更改；version-level description 是唯一允许后续更新的面向人的说明。可信审计 metadata 由平台记录，不是用户可编辑的发布字段。

一个 Worker 可以同时保留：

- Current Version：未指定版本时默认使用。
- 历史 Version：长运行 Workflow 或显式历史运行仍可使用。

### Workflow：可以做什么

Workflow 是可触发的业务流程，例如 `HelloWorkflow`。它定义执行顺序、并行、等待、重试和动态分支，但不能直接访问外部服务。

外部 I/O 必须放在 Activity 中。

### Run：这一次发生了什么

Run 是 Workflow 的一次独立执行。即使输入相同，两次触发也会产生两个 Run。

Run 保存选定 Version、输入、状态和 semantic projection。Console 根据 projection 展示 dynamic DAG，而不是从 Temporal Event History 猜测业务节点。

## 一次发布

发布的输入不是源码，而是已经构建完成的 immutable image digest：

```text
用户代码与 CI
  → 构建并 push image
  → 得到 image@sha256:...
  → 在 org 创建 Version
  → org 部署候选 Worker
  → Org SDK 自动注册 contract
  → poller 与 probe 验证通过
  → Version Ready / Current
```

如果 image 或 contract 有问题，应修复后发布新 Version，不覆盖旧 Version。

## 一次运行

```text
用户选择 Workflow 和输入
  → org 创建 Run
  → Worker 执行 Workflow 与 Activities
  → Org SDK 持续更新 semantic projection
  → Console 展示节点状态和允许的 action
  → Run completed / failed / cancelled
```

人工确认不会让浏览器直接发送 Temporal Signal。Console 将 action 提交给 Gateway，由 Gateway 校验 Tenant、permission、schema、revision 和 `Idempotency-Key`。

## 用户负责，org 负责

| 用户负责 | org 负责 |
|---|---|
| Worker 业务代码和测试 | Version 与 Run 的产品模型 |
| CI、image build 和 push | 候选 Worker 部署与 promotion |
| Workflow 与 Activity 的业务语义 | contract registration 与 probe |
| 外部写操作的幂等或补偿策略 | Tenant authorization 与 Gateway |
| Secret reference 和 runtime 需求 | dynamic DAG 展示与 Audit |

## 常见误解

- “org 会从 Git repository 构建 Worker”：不会。org 从 immutable image digest 开始接手。
- “Version 就是 Kubernetes Deployment”：不是。Version 是产品发布对象，Deployment 是内部实现资源。
- “Workflow 的 DAG 发布时必须完全固定”：不是。运行时可以根据已记录结果产生分支或 fan-out。
- “Run 状态来自 Temporal UI”：不是。普通用户读取 org semantic projection；Temporal Web 只用于高级诊断。
- “Temporal retry 保证外部写 exactly once”：不保证。write Activity 仍需要 idempotency 或 reconciliation/compensation。

## 下一步

现在进入 [本地快速上手](getting-started.md)，用 Hello Sample 跑通一次完整发布和运行。
