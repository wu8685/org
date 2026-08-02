# org

org 是一个用 Go code 定义、并由平台托管运行长期 Workflow 的项目。你使用 Org SDK 编写步骤、并行关系、运行时分支和人工确认；org 负责部署 Worker、管理版本、可靠运行、权限校验和状态展示。

传统流程编排产品通常要求用户学习画布、节点配置或专用 DSL。org 选择 code 作为流程定义：代码仍在你自己的 repository 里，可以 review、测试、提交和持续演进。你可以自己写，也可以让 coding Agent 以 Sample 为起点协助编写；Agent 不是运行时依赖。

## 从一句需求到一个 Workflow

你可以这样描述一个流程：

```text
写一个发布审批流程：
1. 收集本次变更和风险项；
2. 等待负责人确认；
3. 确认后并行生成发布说明和回滚检查；
4. 两个分支完成后汇总结果；
5. 外部写操作必须幂等。
```

Agent 根据需求生成 Worker code：

```text
definition.go   Workflow、Activity、节点依赖和人工 action
activities.go   业务工作和输入校验
*_test.go       顺序、并行、等待、分支和错误场景
Dockerfile      可发布的 Worker image
```

然后沿着一条普通的软件交付路径进入 org：

```text
业务意图
  → 开发者写 code（可使用 coding Agent）
  → 本地测试与 code review
  → CI 构建 immutable Worker image
  → 在 org 发布 Worker Version
  → org 部署 Worker，并验证 SDK 自动注册的 Workflow contract
  → 用户在 Console 中触发、观察和操作 Run
```

流程不是藏在聊天记录里。最终产物是 code、tests 和 immutable image，可以复现，也可以审计。

## 为什么用 code 定义流程

静态 DAG 很适合固定步骤，但真实流程经常包含运行时分支、fan-out、人工等待和补偿。code 能直接表达这些行为，也能使用正常的软件工程工具验证它们。

coding Agent 可以让写 code 的门槛降下来：你描述业务约束和验收结果，它帮助从 Sample 起步；生成结果仍要经过 compiler、tests 和 review，而不是把自然语言直接交给生产环境执行。

这也意味着：

- 流程定义跟随 repository 版本化，不锁在某个可视化画布里。
- Agent 可以继续修改已有 Worker，而不是每次重新生成一份孤立配置。
- 动态分支由 Workflow code 决定，org 不需要猜业务逻辑。
- 用户保有源码、CI 和 image；org 从 immutable image digest 开始接手。

## org 接管什么

Worker 发布后，Org SDK 会从运行中的 typed Definition 构造 contract，并在启动时自动注册。org 在此基础上提供：

- Worker Version 发布、历史版本和 Current Version 管理。
- Workflow contract 注册、poller 检查和 pinned probe。
- 独立 Run，以及 dynamic DAG、节点状态和等待原因。
- 经过 Gateway 的人工 action、authorization、schema validation 和 idempotency handling。
- Tenant 范围的数据、quota 和 Audit。

Agent 是 authoring 方式，不是 org runtime 的隐藏依赖。Run 执行的是经过测试和发布的 Worker code；org 不会在每次运行时临时询问 LLM 下一步该做什么。

## 五个对象

```text
Tenant
  └── Worker
        └── Version
              └── Workflow
                    └── Run
```

| 对象 | 含义 | 示例 |
|---|---|---|
| Tenant | 团队、数据和授权边界 | `local` |
| Worker | 一组相关流程的运行单元 | `release-worker` |
| Version | Worker 的一次 immutable 发布 | `2026.08.1` + OCI digest |
| Workflow | Agent 编写并由 Worker 暴露的流程 | `ReleaseApprovalWorkflow` |
| Run | Workflow 的一次独立执行 | 某次发布审批 |

更完整的生命周期说明见 [核心概念](docs/user/concepts.md)。

## 先跑通，再让 Agent 改

仓库提供三个逐步增加复杂度的 Sample：

1. [Hello](samples/hello/README.md)：两个顺序 Activity。
2. [Parallel confirmation](samples/parallel-confirmation/README.md)：人工确认、恢复和并行分支。
3. [Dynamic decision](samples/dynamic-decision/README.md)：根据 Activity result 选择 runtime path。

第一次使用建议先跟随 [本地快速上手](docs/user/getting-started.md) 跑通 Hello。确认发布、注册和 Run 路径都正常后，再让 Agent 以 Sample 为参照改成自己的流程。

完整阅读路径见 [文档首页](docs/README.md)，三个 Sample 的差异见 [Sample 学习路径](samples/README.md)。

## 重置本地 demo

如果本地试验留下了失败 Version 或候选 Pod，先停止 Console 和本地 Temporal，再在仓库根目录检查并执行受限 reset：

```sh
make demo-reset-dry-run
RESET_DEMO=1 make demo-reset
```

reset 会把本仓库的 control-plane state 与 Temporal development database 移入 `.org/reset-backups/` 备份，并清理固定 `kind-org` / `org-workers` 中带 org 标记的 demo workload。它保留 kind cluster、镜像、E2E 资源及其他 platform Kubernetes Namespace；恢复方式和完整检查点见 [本地快速上手](docs/user/getting-started.md#重置本地-demo)。

## 使用边界

- Workflow code 不能执行外部 I/O；外部调用放在 Activity 中。
- write Activity 必须使用稳定的 idempotency key，或声明 reconciliation/compensation policy。Temporal retry 不等于外部副作用 exactly once。
- 不要把 Secret、敏感 input 或 credential 写进 image、Workflow history、projection、log 或 Audit。
- org 不替用户构建或 push image，也不把共享基础设施描述成硬多租户隔离。

## 文档入口

- [文档首页：按目标选择阅读路径](docs/README.md)
- [核心概念：对象、发布和运行生命周期](docs/user/concepts.md)
- [本地快速上手：完成第一个 Run](docs/user/getting-started.md)
- [Sample 学习路径：从顺序执行到动态分支](samples/README.md)
- [发布 WorkerVersion：Console/API 字段与约束](docs/user/api/publish-worker-version.md)
- [架构概览：系统边界与可信来源](docs/user/architecture/overview.md)
- [本地开发与 E2E：维护 org 本身](docs/development/README.md)

> 产品术语遵循 [org glossary](docs/user/architecture/glossary.md)：用户隔离边界统一称 Tenant。
