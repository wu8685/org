# Dynamic Decision Org SDK Sample

> Terminology: this specification follows the canonical [org glossary](../architecture/glossary.md). Product isolation is a Tenant; infrastructure uses the shared platform Temporal Namespace and platform Kubernetes Namespace.

## 状态与实施门槛

**Approved — implementation authorized only after parallel-confirmation passes on the verified Org SDK.**

### Pending Draft amendment: bootstrap consumer

**Awaiting explicit user approval; not implemented.** If [`012-worker-bootstrap-registration.md`](012-worker-bootstrap-registration.md) is approved, dynamic-decision使用Org SDK hosted startup从typed Definition在内存构造canonical contract/digest，经bound bootstrap credential幂等注册，accepted后才启动Temporal Worker polling。Sample与用户均不选择、上传或读取manifest file。

可选generated JSON保留为CI/debug/golden artifact而非发布输入。真实E2E继续验证两条runtime route与`skipped`projection，并新增registration identity/image/protocol binding、restart exact retry与poller/probe promotion gate。

当前只授权规格与 README 设计。它是三个 Sample 中最后实施的一项。

## 目标与叙事

用“选择简洁或详细处理方式”的中性叙事，最小展示 recorded Activity result 驱动 if/else：

```text
determine-route
  -> route == concise  -> concise-branch
  -> route == detailed -> detailed-branch
  -> 未选 candidate = skipped
  -> finalize
```

`DetermineRoute` 可以根据 input 计算 route，但决策值必须作为 Activity result 被 Temporal 记录；Workflow 只读取该 result，不直接访问外部服务。

## 稳定 contract

| Field | Value |
|---|---|
| directory | `samples/dynamic-decision` |
| Worker name | `dynamic-decision-worker` |
| Workflow | `DynamicDecisionWorkflow` |
| Activities | `DetermineRoute`, `RunConcise`, `RunDetailed`, `Finalize` |
| candidate keys | `concise`, `detailed` |

Input 包含一个简单 `mode`；合法值映射为 `concise` / `detailed`。非法 route 是稳定业务错误，不默认选择任一分支。

## Runtime projection

- `determine-route` 先运行并完成。
- SDK 的受控 `If/Switch` 同时声明两个 candidate templates。
- selected candidate 从 pending 进入 running/completed。
- unselected candidate 被创建并标为 `skipped`，带安全 reason code `route-not-selected`。
- `finalize` 依赖 selected completed 与 unselected skipped，随后完成。
- projection 对两种 input 都返回同一 candidate template catalog，但 runtime status/edge 反映实际路径。

## TDD 验收

1. concise input 只执行 `RunConcise`，detailed node 为 skipped。
2. detailed input 只执行 `RunDetailed`，concise node为 skipped。
3. 未选 Activity handler 从未被调用。
4. selected/skipped node ID 在 replay 中稳定。
5. invalid route 产生稳定 failure projection，不偷偷选择 fallback。
6. finalize 只在合法 selected branch terminal 后执行。
7. Sample 无 raw Temporal import、手写 projection/Signal/metadata。
8. startup构造的canonical contract声明两个candidate templates、route schema与bounds；可选generated JSON（若生成）只用于golden diff。
9. unit tests 不用 wall-clock 推断路径；真实 E2E 分别运行两个 route 并读取 projection。

## README 设计

README 依次说明：if/else 图、为什么 Workflow 只能依据 recorded Activity result 决策、Definition 与三个业务 Activity、两条运行结果、skipped projection、测试、image/kind、org E2E。

README 明确：UI 从 SDK projection 渲染 selected/skipped path，不从 Temporal Event History 推断；Sample 无外部系统，重点只是动态决策。
