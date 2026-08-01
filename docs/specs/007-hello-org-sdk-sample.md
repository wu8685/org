# Hello Sample 迁移至 Org SDK

> Terminology: this specification follows the canonical [org glossary](../architecture/glossary.md). Product isolation is a Tenant; infrastructure uses the shared platform Temporal Namespace and platform Kubernetes Namespace.

## 状态与实施门槛

**Approved — implementation authorized only after 006 Org SDK is implemented and verified.**

### Approved amendment: bootstrap consumer

**Approved for implementation.** Per [`012-worker-bootstrap-registration.md`](012-worker-bootstrap-registration.md), Hello Worker startup must call the Org SDK hosted entrypoint: typed Definition → in-memory canonical contract/digest → idempotent bootstrap registration → await accepted → start Temporal Worker polling. Sample用户不读取或提交manifest file。

`generated/org-worker-manifest.json`降为可选golden/CI/debug artifact；它可以继续用于contract diff和测试，但删除该文件不得破坏正常hosted startup。真实E2E需验证registration后、polling前restart可exact retry，最终registration/poller/probe三项一致后Ready；现有Current与历史Pinned验收继续保留。

在 Org SDK 完成 unit、race、vet 与 Temporal runtime verification 之前，本规格只授权文档设计，不授权修改 `samples/hello`。

## 目标

保留 `samples/hello` 的极简用户体验，同时移除用户代码中的 raw Temporal SDK、手写 projection 与手写 metadata：

```text
prepare-greeting -> compose-greeting -> completed
```

用户只提供 typed input/output、两个无外部副作用的 Activity 和 Org SDK Workflow Definition。SDK 负责 stable node/Activity ID、retry options、projection、manifest 与 Worker registration。

## 稳定 contract

| Field | Value |
|---|---|
| directory | `samples/hello` |
| Worker name | `hello-worker` |
| Workflow | `HelloWorkflow` |
| Activity templates | `prepare-greeting`, `compose-greeting` |
| terminal semantic node | `completed` |
| image repository | `org.local/hello-worker` |

Input/output 与现有 Sample 保持兼容。两个 Activity 均为 `sideEffect: none`，retry/timeout 通过 SDK Definition 声明。Worker version 由 SDK runtime context 注入 Activity，不由 replay-time Workflow 读取环境。

## 目标目录

```text
samples/hello/
  README.md
  go.mod
  go.sum
  definition.go
  activities.go
  definition_test.go
  activities_test.go
  contract_test.go
  Dockerfile
  cmd/worker/main.go
  scripts/build-image.sh
  scripts/kind-load.sh
  generated/org-worker-manifest.json
```

删除 raw `workflow.go`、手写 projection structs 与手写 `worker-metadata.json`。generated manifest 必须由 Definition 产生，测试禁止直接编辑。

## TDD 验收

1. 先写失败测试，证明 Definition 只声明两个顺序 Activity 与 terminal node。
2. SDK testkit 执行后返回原有 greeting/result。
3. projection 逐节点为 completed，dependency 正确，ID replay 稳定。
4. Sample source 不 import `go.temporal.io/sdk/*`，不注册 raw query/Signal。
5. startup从Definition构造的canonical contract/digest稳定；可选generated JSON（若生成）与其一致。
6. Docker/build/kind contract 与现有 immutable digest 行为不退化。
7. 真实 E2E 继续覆盖 Current 与显式历史 Pinned version。

## README 设计

README 固定按以下顺序：

1. “这是最小 Org SDK Worker”与三节点图；
2. 用户只需阅读的 `definition.go` / `activities.go`；
3. `make sample-test`；
4. image build/kind-load；
5. org 注册、启动、读取 projection；
6. 明确 SDK 内部使用 Temporal，但普通用户不手写或直连 Temporal；
7. 写 Activity 的 idempotency/reconciliation 提醒。

README 不展示 raw Task Queue、Signal 或 projection JSON 的构造代码；开发配置表可保留由平台注入的技术环境变量。
