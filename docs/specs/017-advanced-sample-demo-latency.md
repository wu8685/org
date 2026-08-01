# Advanced Sample demo latency

> Terminology: this specification follows the canonical [org glossary](../architecture/glossary.md). Product isolation is a Tenant; infrastructure uses the shared platform Temporal Namespace and platform Kubernetes Namespace.

## 状态

**Approved — implementation authorized by the user on 2026-08-02.**

本 amendment 扩展 [`008-parallel-confirmation-org-sdk-sample.md`](008-parallel-confirmation-org-sdk-sample.md) 与 [`009-dynamic-decision-org-sdk-sample.md`](009-dynamic-decision-org-sdk-sample.md)。它只改变两个高级 Sample 的教学演示时长，不改变 Org SDK、control plane、Temporal retry、DAG contract、Activity 结果或 production 默认行为。Hello 已有约 10 秒 `ComposeGreeting` 演示延迟，本规格不修改 Hello。

## 目标

Console 用户应有足够时间观察 Activity 的 `running` projection，而不是只看到瞬间完成后的最终 DAG。每个实际执行的高级 Sample Activity 在其 Activity handler 内等待一个独立随机时长：

- 下界 2 秒，上界 5 秒，包含边界；
- 每次 Activity execution 独立取值；parallel 的两个 `ExecuteBranch` 分别取值；
- delay 发生在 Activity 内，不得在 Workflow code 内 sleep、读取 wall clock 或生成随机数；
- context cancellation 必须立即中断等待并返回 context error；
- timeout/retry policy 必须容纳最大 delay，但不改变 side-effect 或 idempotency 声明；
- delay 仅为 Sample 教学行为，README 必须明确 production Worker 不应照搬人为 sleep。

随机源与 sleeper 必须可注入。默认 runtime 使用本地随机源；unit test 使用确定性 source 和不等待的 fake sleeper，不因演示时长变慢。

## Parallel Confirmation

实际执行的 `BuildPlan`、每一个 `ExecuteBranch` 与 `Finalize` 均先执行 demo delay，再运行原业务函数。`approval-gate` 是 Workflow 内 idle `WaitForAction` node，等待 Signal 时不得调用 delay、占用 Activity worker 或产生 Activity call。

投影验收顺序：

```text
approval-gate = waiting-for-user
  → build-plan = running
  → execute-branch/summary = running || execute-branch/readiness = running
  → join
  → finalize = running
  → completed
```

真实 E2E 必须在同一 projection 中观察到两个 branch 同时 `running`；只依赖 2 秒下界提供观察窗口，不断言精确 wall-clock 或两个随机值的先后顺序。

## Dynamic Decision

实际执行的 `DetermineRoute`、唯一 selected branch Activity 与 `Finalize` 均执行独立 demo delay。未选 candidate 仍由 deterministic Workflow 立即创建为 `skipped`，reason code 保持 `route-not-selected`：

- skipped handler 调用次数必须为 0；
- skipped node 不取随机 delay，不调用 sleeper，不制造 `running` 状态；
- selected branch `running` 时，E2E 应在同一 projection 看到 unselected branch 已为 `skipped`；
- concise 与 detailed 两条 route 都必须验证，不能用一个 route 代替另一个。

## TDD 与验收

1. red test 先证明两个 Sample 尚未调用注入的 delay runtime。
2. unit tests 断言每次实际 Activity 的 delay 均在 `[2s, 5s]`，parallel 两个 branch 各自调用一次且可获得不同值。
3. 已取消 context 的 5 秒等待须快速返回 `context.Canceled`，测试不得真实等待 5 秒。
4. parallel idle action wait 期间 delay call count 为 0；确认后恰好覆盖 BuildPlan、两个 branch 与 Finalize。
5. dynamic 每条 route 只覆盖 DetermineRoute、selected branch 与 Finalize；skipped branch delay/handler count 均为 0。
6. Sample tests、race 与 vet 通过；README 说明可观察阶段、2–5 秒随机性及 production 禁止照搬。
7. real `kind-org` + local Temporal E2E 观察上述 running/skipped 状态后完成，不以精确 wall-clock 作为断言。

