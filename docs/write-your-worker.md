# 编写你的第一个 Org SDK Worker

本页面向想用 code 创建自己的 Workflow 的开发者。你只需要维护业务代码、测试和 image；org 负责把已发布的 Worker 部署起来、验证它暴露的能力，并在 Console 中展示每次 Run。

> 产品术语遵循 [org glossary](architecture/glossary.md)：用户隔离边界统一称 Tenant。

## 先建立正确的边界

一个 Worker repository 通常只有四类内容：

```text
definition.go       用 Org SDK 声明 Workflow、Activity 和节点
activities.go       放业务工作和外部 I/O
*_test.go           验证业务行为、重试与分支
cmd/worker/main.go  从平台注入的配置启动 hosted Worker
```

你负责：Workflow 的业务顺序、Activity 的业务实现、输入/输出类型、测试和 image。

org 负责：Worker 启动时的 contract registration、Version promotion、Tenant authorization、人工 action 的 Gateway、Run 状态和 dynamic DAG 展示。

不要在业务代码中手写 projection、Temporal Signal、platform credential 或 routing。也不要把 manifest 文件、bootstrap credential 或 Temporal endpoint 写入 repository；Org SDK 与平台会在启动阶段处理这些内容。

## 最小结构：两个顺序步骤

先复制 [Hello Sample](../samples/hello/README.md)，它是最小可运行的起点。核心是三件事：声明 Activity、把它们放进 Definition、在 Workflow 中执行它们。

```go
prepare := orgsdk.NewActivity("prepare", readPolicy, Prepare)
compose := orgsdk.NewActivity("compose", readPolicy, Compose)

definition := orgsdk.NewDefinition[Input, Output]("greeting", []orgsdk.NodeTemplate{
	orgsdk.ActivityNode(prepare, "Prepare greeting", orgsdk.CardinalitySingleton),
	orgsdk.ActivityNode(compose, "Compose greeting", orgsdk.CardinalitySingleton),
	{ID: "completed", Label: "Completed", Type: orgsdk.NodeTypeSemantic},
}, orgsdk.RuntimeBounds{
	MaxInstancesPerFanOut: 1,
	MaxRuntimeNodes:       3,
	MaxProjectionBytes:    64 << 10,
})

workflow, err := orgsdk.NewWorkflowDefinition("GreetingWorkflow", version, definition,
	func(ctx *orgsdk.WorkflowContext, input Input) (Output, error) {
		prepareNode, prepared, err := orgsdk.ExecuteActivity(ctx, prepare, "singleton", orgsdk.NodeRef{}, nil, input, "")
		if err != nil { return Output{}, err }
		composeNode, result, err := orgsdk.ExecuteActivity(ctx, compose, "singleton", prepareNode, []orgsdk.NodeRef{prepareNode}, prepared, "")
		if err != nil { return Output{}, err }
		_, err = orgsdk.CompleteSemantic(ctx, "completed", "singleton", composeNode, []orgsdk.NodeRef{composeNode})
		return result, err
	})
```

`NodeTemplate` 是 Console 可以展示的稳定业务节点；`ExecuteActivity` 会在运行时创建节点、执行 Activity 并更新 projection。名称和 occurrence key 必须稳定，不能依赖随机值或当前时间。

## Workflow 和 Activity 各该做什么

Workflow 决定流程：顺序、并行、等待、分支与补偿选择。它必须可 replay，因此不能在里面直接请求 HTTP、读数据库、读文件、取当前随机数或调用不可重放的 SDK。

Activity 执行工作：调用外部服务、读写数据库、发送通知或访问其他 I/O。`ActivityPolicy` 声明重试、超时和副作用类型。写入型 Activity 必须传递稳定业务键，并选择幂等性或 reconciliation 策略；重试不等于外部效果 exactly once。

```go
writePolicy := orgsdk.ActivityPolicy{
	SideEffect: orgsdk.SideEffectWrite,
	Idempotency: &orgsdk.IdempotencyPolicy{
		BusinessKeyRequired: true,
		PropagationField:    "idempotency_key",
	},
	Retry: orgsdk.RetryPolicy{/* timeout 与 retry policy */},
}

_, _, err := orgsdk.ExecuteActivity(ctx, sendInvoice, "invoice-42", parent, deps, input, "invoice-42")
```

## 从顺序执行扩展出去

| 你要表达的事情 | 使用方式 | 可参考的 Sample |
|---|---|---|
| 并行分支后汇合 | 多次 `StartActivity`，再逐个 `future.Get()`，最后 `CompleteSemantic` | [Parallel Confirmation](../samples/parallel-confirmation/README.md) |
| 等待用户确认 | `AwaitConfirmation`；它是可恢复的 Workflow 等待，不是阻塞 Activity | [Parallel Confirmation](../samples/parallel-confirmation/README.md) |
| 基于外部结果选择路径 | 先用 Activity 取得并记录结果，再在 Workflow 中依据该结果分支；未选节点显式标为 `skipped` | [Dynamic Decision](../samples/dynamic-decision/README.md) |

人工 action 由用户在 Console 中提交给 Gateway。Gateway 会校验 Tenant、权限、input schema、projection revision 和幂等键，然后受控地送达 Worker；终端用户不直连 Temporal，也不直接发送 Signal。

## 启动、测试与发布

你的 Worker 入口只需要加载平台注入的 hosted 配置并注册 Definition 返回的 registrations：

```go
cfg, err := orgsdk.LoadHostedWorkerConfig(os.Getenv, os.ReadFile)
if err != nil { return err }

worker, err := NewWorker(version)
if err != nil { return err }

return orgsdk.RunHostedWorker(ctx, cfg, worker.Registrations()...)
```

然后按正常的软件交付方式进行：

1. 为顺序、错误、重试、并行或分支写测试；先从 [Hello Sample](../samples/hello/README.md) 的测试结构开始。
2. 在自己的 repository 执行 `make verify`。
3. 用 CI 或 Sample 的 Makefile 构建并 push image；记录 registry 返回的 `repository@sha256:...` digest。
4. 在 org Console 创建 Worker，发布一个新 Version，并粘贴该 immutable digest。
5. 等待 Version Ready / Current，再在 Workflows 中用 JSON/YAML payload 启动 Run。

org 不会从 Git repository 构建 image，也不要求你上传 manifest。SDK 会在候选 Worker 启动后从 typed Definition 在内存中生成并注册 contract；Console 只读展示验证后的结果。

## 下一步

依次运行三个独立 Sample：

1. [Hello](../samples/hello/README.md)：最小顺序 Activity。
2. [Parallel Confirmation](../samples/parallel-confirmation/README.md)：人工确认、恢复、并行和 join。
3. [Dynamic Decision](../samples/dynamic-decision/README.md)：由已记录 Activity result 决定后续路径。

需要完整本地发布路径时，回到[本地快速上手](getting-started.md)。需要 API 集成时，阅读[发布 WorkerVersion](api/publish-worker-version.md)和[启动 Workflow Run](api/start-workflow-run.md)。
