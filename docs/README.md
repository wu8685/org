# org 文档

这里按“你现在想完成什么”组织文档。第一次使用时只走第一条路径；`docs/specs/` 是实现和评审依据，不是入门教程。

## 第一次使用

目标：在本地发布 Hello Worker，并看到第一个 Run 的动态 DAG。

1. [核心概念](concepts.md)：用一个业务场景理解 Tenant、Worker、Version、Workflow 和 Run。
2. [本地快速上手](getting-started.md)：启动依赖、构建 image、发布 Version、触发 Run。
3. [Hello Sample](../samples/hello/README.md)：回到代码，查看最小 Definition 和 Activities。

完成这三步后，再决定是否继续学习人工确认、动态分支或平台内部架构。

## 开发 Worker

| 你要做什么 | 从哪里开始 |
|---|---|
| 理解 Worker 代码由哪些部分组成 | [核心概念](concepts.md) |
| 查看最小顺序 Workflow | [Hello Sample](../samples/hello/README.md) |
| 加入人工确认和并行分支 | [Parallel Confirmation Sample](../samples/parallel-confirmation/README.md) |
| 根据运行结果选择分支 | [Dynamic Decision Sample](../samples/dynamic-decision/README.md) |
| 发布自己的 image | [发布 WorkerVersion](api/publish-worker-version.md) |
| 比较三个 Sample 的学习重点 | [Sample 学习路径](../samples/README.md) |

## 理解系统

| 问题 | 文档 |
|---|---|
| org、Worker、Temporal 和 Kubernetes 各负责什么 | [架构概览](architecture/overview.md) |
| 产品术语为什么必须区分 Tenant 和底层资源名称 | [术语表](architecture/glossary.md) |
| 当前已经实现到哪里 | [实现状态](implementation-status.md) |

## 维护 org

以下内容面向修改 control plane、Console、Org SDK 或 E2E 的维护者：

- [本地开发与 E2E](development.md)
- [API 发布请求](api/publish-worker-version.md)
- [设计与实现规格](specs/)

阅读 specs 时先检查文档顶部的状态。Draft 只表示设计输入；Approved 才能作为已授权行为的依据。较早的规格可能被后续 amendment 修订，不能脱离引用关系单独解释。

## 推荐阅读顺序

```text
新手
  README
    → 核心概念
    → 本地快速上手
    → Hello Sample

Worker 开发者
  Sample 学习路径
    → 发布 WorkerVersion
    → 架构概览

org 维护者
  本地开发与 E2E
    → 实现状态
    → specs/
```
