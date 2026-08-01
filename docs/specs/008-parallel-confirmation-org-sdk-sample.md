# Parallel Confirmation Org SDK Sample

> Terminology: this specification follows the canonical [org glossary](../architecture/glossary.md). Product isolation is a Tenant; infrastructure uses the shared platform Temporal Namespace and platform Kubernetes Namespace.

## 状态与实施门槛

**Approved — implementation authorized only after Hello migration passes on the verified Org SDK.**

### Approved amendment: bootstrap consumer

**Approved for implementation.** Per [`012-worker-bootstrap-registration.md`](012-worker-bootstrap-registration.md), parallel-confirmation与Hello使用同一Org SDK hosted startup：从typed Definition构造canonical contract/digest，使用pending WorkerVersion绑定credential注册，accepted后才启动Temporal Worker polling。Sample不管理manifest artifact，也不自行传Tenant/Worker/version。

Per [`014-sample-slimming.md`](014-sample-slimming.md)，Sample repository不保留generator或checked-in generated contract；测试直接从typed Definition验证contract。真实E2E除既有idle action/restart/fork/join外，还须证明bootstrap exact retry不重复注册、registration后poller缺失不会Ready，并在poller + pinned verification成功后promotion。

当前只授权规格与 README 设计。不得在 SDK 验证及 Hello 迁移完成前创建 Sample 实现。

## 目标与叙事

使用中性的“确认后准备”叙事：

```text
approval-gate
  -> BuildPlan Activity
  -> recorded Plan result
  -> runtime fork: prepare-summary || verify-readiness
  -> join
  -> finalize
```

`AwaitConfirmation` 是 Workflow 内的 idle node，不是阻塞 Activity。`BuildPlan` 的 recorded result 提供两个带稳定 key 的 branch item；Workflow 基于该 result 创建两个并行 runtime nodes。

## 稳定 contract

| Field | Value |
|---|---|
| directory | `samples/parallel-confirmation` |
| Worker name | `parallel-confirmation-worker` |
| Workflow | `ParallelConfirmationWorkflow` |
| action | `confirm` |
| permission | `run:action:confirm` |
| Activities | `BuildPlan`, `ExecuteBranch`, `Finalize` |
| branch keys | `summary`, `readiness` |

所有 Activity 都无外部副作用。Signal name、projection query 与 operation envelope 由 SDK 保留协议生成，Sample 不声明 raw name。

## Runtime projection

- 启动：`approval-gate = waiting-for-user`，只允许 `confirm`。
- confirmation accepted：gate completed，`build-plan = running`。
- Plan result processed：创建 `branch/summary` 与 `branch/readiness`，二者同时 running，dependency 指向 `build-plan`。
- 单分支完成：一个 completed、一个 running，join pending。
- 双分支完成：join/finalize 运行并完成。
- timeout/cancel/stale/duplicate action 按 006 state machine 投影。

## TDD 验收

1. 未确认时没有 Activity 被 schedule。
2. `WaitForAction` 等待不占 Activity worker，restart/replay 后可继续。
3. Gateway-authorized action 才能发送；SDK 对 operation ID 二次去重。
4. BuildPlan result 驱动两个 stable-key branch。
5. 在等待任一 Future 前两个 branch 都已 schedule。
6. projection 同时展示两个 running nodes并保留 dependency。
7. join 仅在两个实际 branch completed 后执行。
8. Sample 无 raw Temporal import、手写 projection/Signal/metadata。
9. startup构造的canonical contract与Definition、action schema、runtime bounds一致；测试直接验证内存结果。
10. 真实 E2E 覆盖 waiting → action → parallel → join → completed 及 Worker restart。

## README 设计

README 依次说明：业务图、`AwaitConfirmation` 为 idle node、BuildPlan result 驱动 runtime fork、核心 Definition、测试命令、image/kind、org 受控 action 演示、projection 截图/JSON 示例、安全边界。

禁止提供 direct Temporal Signal 命令。UI 未实现前，以 org application service/测试客户端发起 action，不伪装成浏览器点击。
