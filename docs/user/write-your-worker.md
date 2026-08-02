# Org SDK 开发参考

这份参考说明 Worker 业务代码中常用的 Org SDK API。创建、构建和发布项目的完整步骤见[创建你的第一个 Worker](create-your-worker.md)。

## 代码结构

```text
types.go            输入、输出和稳定名称
activities.go       业务工作和外部 I/O
definition.go       Definition、Activity 与 Workflow
*_test.go           Activity 和 Workflow 测试
cmd/worker/main.go  hosted Worker 入口
```

Workflow 负责顺序、并行、等待和分支。Activity 负责 HTTP、数据库、文件等外部 I/O。Workflow 会被 replay，因此业务 I/O 应放在 Activity 中。

## Activity

使用 `NewActivity` 绑定稳定名称、ActivityPolicy 和 handler：

```go
process := orgsdk.NewActivity("process", policy,
	func(_ orgsdk.ActivityContext, input Input) (Result, error) {
		return Process(input)
	})
```

ActivityPolicy 描述副作用、超时和重试：

```go
policy := orgsdk.ActivityPolicy{
	SideEffect: orgsdk.SideEffectNone,
	Retry: orgsdk.RetryPolicy{
		InitialInterval:     100 * time.Millisecond,
		BackoffCoefficient:  2,
		MaximumInterval:     2 * time.Second,
		MaximumAttempts:     3,
		StartToCloseTimeout: 30 * time.Second,
	},
}
```

写入外部系统时使用 `SideEffectWrite`，并配置稳定的业务幂等键或 reconciliation policy。

## Definition

Definition 声明 Console 可以展示的节点及运行边界：

```go
definition := orgsdk.NewDefinition[Input, Result]("main", []orgsdk.NodeTemplate{
	orgsdk.ActivityNode(process, "Process", orgsdk.CardinalitySingleton),
	{ID: "completed", Label: "Completed", Type: orgsdk.NodeTypeSemantic},
}, orgsdk.RuntimeBounds{
	MaxInstancesPerFanOut: 1,
	MaxRuntimeNodes:       2,
	MaxProjectionBytes:    64 << 10,
})
```

`NodeTemplate` ID、Activity name 和 occurrence key 必须稳定。Org SDK 使用 Go input/output 类型及 `json` tag 生成 schema。

## Workflow

`NewWorkflowDefinition` 创建 Workflow。顺序执行使用 `ExecuteActivity`，完成业务节点使用 `CompleteSemantic`：

```go
workflow, err := orgsdk.NewWorkflowDefinition(
	WorkflowName,
	version,
	definition,
	func(ctx *orgsdk.WorkflowContext, input Input) (Result, error) {
		processNode, result, err := orgsdk.ExecuteActivity(
			ctx, process, "singleton", orgsdk.NodeRef{}, nil, input, "",
		)
		if err != nil {
			return Result{}, err
		}
		_, err = orgsdk.CompleteSemantic(
			ctx, "completed", "singleton", processNode, []orgsdk.NodeRef{processNode},
		)
		return result, err
	},
)
```

## 并行、等待和分支

| 场景 | API | 示例 |
|---|---|---|
| 并行 Activity | 多次 `StartActivity`，然后调用各自的 `ActivityFuture.Get` | [Parallel Confirmation](../../samples/parallel-confirmation/README.md) |
| 等待确认 | `AwaitConfirmation` | [Parallel Confirmation](../../samples/parallel-confirmation/README.md) |
| 等待结构化 action | `WaitForAction` | 本节下方示例 |
| 跳过未选择的节点 | `SkipNode` | [Dynamic Decision](../../samples/dynamic-decision/README.md) |
| 返回可展示的业务错误 | `NewUserError` | [Dynamic Decision](../../samples/dynamic-decision/README.md) |

这些节点都需要预先出现在 Definition 中。并行分支在调用 `Get` 前应全部启动；动态分支应把未选择的候选节点标记为 `skipped`。

等待带输入的 action 时，使用输入类型调用 `WaitForAction`：

```go
approval, action, err := orgsdk.WaitForAction[ApprovalInput](
	ctx, "approval-gate", "singleton", dependencies, 24*time.Hour,
)
```

返回的 `approval` 是 action payload，`action` 包含已接受 action 的运行信息。

## 测试

`NewTestEnvironment` 可以直接注册并执行 Worker，不需要启动本地 org：

```go
hostedWorker, err := NewWorker("v1")
if err != nil {
	t.Fatal(err)
}

env := orgsdk.NewTestEnvironment()
if err := env.Register(hostedWorker.Registrations()...); err != nil {
	t.Fatal(err)
}
env.ExecuteWorkflow(WorkflowName, Input{Value: "first run"})
if err := env.WorkflowError(); err != nil {
	t.Fatal(err)
}

var result Result
if err := env.Result(&result); err != nil {
	t.Fatal(err)
}
projection, err := env.Projection()
```

需要测试人工 action 时，使用 `SignalAction`；需要在 Workflow 执行期间安排回调时，使用 `After`。

## hosted Worker 入口

平台启动 Worker 时，入口加载 hosted 配置并把 registrations 交给 `RunHostedWorker`：

```go
cfg, err := orgsdk.LoadHostedWorkerConfig(os.Getenv, os.ReadFile)
if err != nil {
	return err
}

hostedWorker, err := NewWorker(cfg.Worker.BuildID)
if err != nil {
	return err
}

return orgsdk.RunHostedWorker(ctx, cfg, hostedWorker.Registrations()...)
```

> 产品术语遵循 [org glossary](architecture/glossary.md)：用户隔离边界统一称 Tenant。
