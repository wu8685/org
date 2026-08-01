# org

org 是一个面向团队内部 Workflow 的运行控制面：你提交自己构建的 Worker image，org 负责发布版本、运行 Workflow、展示动态 DAG，并把人工操作收进可授权、可审计的入口。

用户保有业务代码、CI 和 image；org 不接管源码仓库，也不替用户构建 image。

## 先理解一个场景

假设你有一个“生成发布说明”的流程：

1. 读取本次变更。
2. 等待负责人确认。
3. 并行生成摘要和风险检查。
4. 汇总结果。

普通任务系统只能告诉你“任务正在运行”。org 希望让你直接看到业务步骤、当前等待谁、哪些分支正在并行，以及此刻允许执行什么操作：

```text
等待确认 → 生成计划 ─┬→ 生成摘要 ─┐
                    └→ 风险检查 ─┴→ 汇总完成
```

业务执行仍由 Worker 中的 Workflow 完成。org 提供版本发布、运行入口、状态投影、权限和操作边界。

## 五个对象就够了

```text
Tenant
  └── Worker
        └── Version
              └── Workflow
                    └── Run
```

| 对象 | 新手可以先这样理解 | 示例 |
|---|---|---|
| Tenant | 一个团队或隔离边界 | `local` |
| Worker | 一组相关业务流程的运行单元 | `hello-worker` |
| Version | Worker 的一次 immutable 发布 | `2026.08.1` + OCI digest |
| Workflow | 可以被触发的业务流程定义 | `HelloWorkflow` |
| Run | Workflow 的一次独立执行 | 一次问候语生成 |

更完整的解释见 [核心概念](docs/concepts.md)。第一次使用时，不需要先理解 Temporal、Task Queue、Signal 或 Kubernetes resource naming。

## org 帮你做什么

- 使用不可变 OCI `image@sha256:...` 发布 Worker Version。
- 由 Org SDK 从 Worker 代码生成并自动注册只读 contract。
- 创建 Run，并在 Console 中展示 dynamic DAG、节点状态和等待原因。
- 让人工 action 经过 Gateway 的 Tenant authorization、schema validation 和 idempotency handling。
- 保留 Current Version，同时允许显式运行历史 Version。

org 不构建或 push Worker image。你的 CI 负责得到 immutable image digest，org 从这个 digest 开始接手。

## 第一次使用

推荐按这个顺序，不要从内部 specs 开始读：

1. 打开 [文档首页](docs/README.md)，确认适合自己的阅读路径。
2. 阅读 [核心概念](docs/concepts.md)，建立五个对象的心智模型。
3. 跟随 [本地快速上手](docs/getting-started.md)，跑通 Hello Sample。
4. 再从 [Sample 学习路径](samples/README.md) 进入人工确认和动态分支。

如果环境已经准备好，可以直接进入快速上手。完整路径会带你启动本地依赖、构建 Hello image、发布 Version，并看到第一个 Run 完成。

## 三条使用边界

- Workflow 代码不能执行外部 I/O；外部调用放在 Activity 中。
- write Activity 必须使用稳定的 idempotency key，或声明 reconciliation/compensation policy。Temporal retry 不等于外部副作用 exactly once。
- 不要把 Secret、敏感 input 或 credential 写进 image、Workflow history、projection、log 或 Audit。

共享基础设施下的 Tenant 隔离、安全声明和组件职责见 [架构概览](docs/architecture/overview.md)。

## 文档入口

- [文档首页：按目标选择阅读路径](docs/README.md)
- [核心概念：先看懂对象和生命周期](docs/concepts.md)
- [本地快速上手：完成第一个 Run](docs/getting-started.md)
- [Sample 学习路径：从顺序执行到动态分支](samples/README.md)
- [发布 WorkerVersion：Console/API 字段与约束](docs/api/publish-worker-version.md)
- [架构概览：系统边界与可信来源](docs/architecture/overview.md)
- [本地开发与 E2E：维护 org 本身](docs/development.md)

> 产品术语遵循 [org glossary](docs/architecture/glossary.md)：用户隔离边界统一称 Tenant。
