# Kubernetes runtime API client

> Terminology: this specification follows the canonical [org glossary](../../user/architecture/glossary.md). Product workloads continue to share one platform Kubernetes Namespace; a product Tenant is not a Kubernetes Namespace.

## 状态

**Approved — implementation authorized by the user on 2026-08-02.**

本规格把 org 长期运行进程中的 Kubernetes 访问从外部 `kubectl` 进程迁移为进程内 Kubernetes API client。面向开发者和运维人员的脚本仍可使用 `kubectl`。

## 决策摘要

org 采用以下边界：

1. `cmd/`、`internal/` 等长期运行的 Go 程序不得通过 `os/exec`、shell 或 `kubectl` 访问 Kubernetes。
2. Go 程序必须通过 Kubernetes API client 完成资源读写、TokenReview 和 readiness 观测。
3. `scripts/`、Makefile 和 E2E 环境准备/清理代码可以使用 `kubectl`，因为它们是面向开发者的操作入口，而不是 control-plane runtime dependency。
4. 单元测试必须 fake API boundary 或 fake typed client behavior，不再断言拼接出的 `kubectl` 参数和 stderr 文本。
5. 迁移后，运行 `org-console` 不要求主机安装 `kubectl`；本地开发脚本和 E2E target 仍可要求它。

```text
org-console
  -> Kubernetes REST config
  -> typed Kubernetes API client
  -> API server

developer / E2E script
  -> kubectl (allowed)
  -> API server
```

## 背景与问题

当前 `internal/platform/kube` 通过 `ExecRunner` 启动 `kubectl`，覆盖：

- Namespace get/create；
- ServiceAccount、Secret、Deployment、NetworkPolicy apply；
- Deployment rollout status；
- TokenReview；
- Pod list 与 ReplicaSet get。

这使 control plane 的行为依赖本机 binary、PATH、kubectl 版本、stderr 文案和 CLI 参数语义。错误只能通过字符串匹配分类，例如 Namespace NotFound/AlreadyExists；测试也主要验证命令字符串，而不是 Kubernetes resource contract。

`kubectl` 最终同样调用 Kubernetes API，但它是面向人的 CLI adapter，不应成为长期运行程序的 RPC transport。

## 目标

1. 消除 `internal/platform/kube` 对 `kubectl` 的运行时依赖。
2. 以 typed Kubernetes objects 表达 org 管理的资源，不再依赖 YAML 字符串拼接和 `strings.Replace` 修改 manifest。
3. 保留现有 kubeconfig、context、platform Kubernetes Namespace 和安全配置语义。
4. 使用结构化 Kubernetes API errors，不解析 CLI stderr。
5. 保持 apply 幂等、并发 Namespace 创建安全、Deployment readiness 与 bootstrap identity verification 的现有安全边界。
6. 保持单元测试快速、确定且不依赖真实 cluster；真实 kind E2E 继续验证 API adapter。

## 非目标

- 移除 `scripts/`、Makefile 或 E2E 操作代码中的所有 `kubectl`；
- 引入 Operator、CRD、controller-runtime 或持续 reconcile controller；
- 改变 Tenant、Worker、WorkerVersion、Task Queue 或 workload naming；
- 改变 Worker ServiceAccount RBAC、projected token audience 或 `automountServiceAccountToken: false`；
- 支持多 Kubernetes cluster；
- 在本里程碑引入 in-cluster config、exec credential plugin 或 production deployment packaging；
- 改变 Temporal deployment/promotion 语义。

## API client 构造

### REST config

程序入口根据现有配置构造 Kubernetes REST config：

- `ORG_KUBECONFIG` 非空时使用该文件；否则使用标准 kubeconfig loading rules；
- `ORG_KUBE_CONTEXT` 非空时覆盖 kubeconfig current context；
- API client 初始化失败时，Console 启动必须失败并返回不含 credential 的明确错误；
- 不读取或记录 bearer token、client certificate bytes 等认证材料；
- 本地默认 `kind-org` context 保持不变。

`cmd/org-console` 只负责 composition：构造 REST config、typed clients 和 kube adapter。业务 service 不得感知 kubeconfig、HTTP transport 或 Kubernetes SDK 类型。

### 注入边界

`internal/platform/kube` 必须支持注入最小 API interfaces。生产构造使用真实 client；单元测试使用 fake/stub。不得为方便测试保留通用 shell `Runner`。

推荐按职责分成两个内部组件：

- `WorkloadClient`：Namespace、ServiceAccount、Secret、Deployment、NetworkPolicy、readiness；
- `BootstrapEvidenceResolver`：TokenReview、Pod、ReplicaSet 读取。

两者可以共享底层 typed client，但不得共享可变业务状态。

## 资源模型与 apply

### Typed resources

原 `RenderManifest` 和 `RenderBootstrapManifest` 的资源内容应改由 typed object builders 产生：

- `core/v1.Namespace`；
- `core/v1.ServiceAccount`；
- `core/v1.Secret`；
- `apps/v1.Deployment`；
- `networking/v1.NetworkPolicy`。

builders 保持纯函数：输入为 canonical `domain.WorkerVersion`、kube config 和可选 bootstrap material，输出 typed resources 或 validation error。Secret 使用 bytes/data 字段，不生成或记录包含 credential 的 YAML。

所有现有约束必须保留，包括：

- canonical resource name 与 server-owned labels；
- immutable digest image；
- requests/limits；
- non-root、read-only root filesystem、drop all capabilities、RuntimeDefault seccomp；
- dedicated ServiceAccount；
- `automountServiceAccountToken: false`；
- bootstrap Secret file、Pod-bound projected token、Downward API Pod UID；
-可选 NetworkPolicy。

### Create 与 Server-Side Apply

Namespace 之外的 org-owned namespaced resources使用 typed Create 与 Server-Side Apply，field manager固定为：

```text
org-control-plane
```

规则：

1. 只允许 apply canonical name 且带 `app.kubernetes.io/managed-by=org` 的资源。
2. 资源不存在时使用带相同 field manager 的 typed Create，避免 Get→Apply 竞态接管同名第三方资源。
3. 并发 Create 返回 AlreadyExists 时必须重新 Get 并验证 ownership；若缺少 org ownership label，必须失败，不得接管。
4. 已存在且由 org 管理的资源使用 Server-Side Apply。首次从旧 `kubectl apply` 迁移时，可以 force ownership org 明确定义的字段；不得 force 未由 typed builder 声明的第三方字段。
5. API conflict、Forbidden、Invalid、TooManyRequests 和 transport failure 必须保留可判定的 error chain；不得压平为 stderr string。
6. 写入顺序保持：Namespace → ServiceAccount/Secret → Deployment → optional NetworkPolicy。
7. bootstrap 未启用时不得创建 bootstrap Secret；从 bootstrap deployment 切换的清理策略不在本里程碑扩张，保持现有 release lifecycle 行为。

### Namespace

Namespace 使用 `Get` 后按需 `Create`：

- 只有结构化 `IsNotFound` 才触发 Create；
- 并发 Create 返回 `AlreadyExists` 时重新 Get；
- 已存在 Namespace 缺少 org ownership label时保持现有行为：可以作为预先配置的共享 platform Kubernetes Namespace 使用，不主动接管或修改；
- org 创建的 Namespace必须带 managed-by label。

## Deployment readiness

`WaitReady` 通过 API 读取/观察目标 Deployment，不模拟 `kubectl rollout status` 文本输出。

成功至少满足：

- live Deployment UID 非空；
- `status.observedGeneration >= metadata.generation`；
- desired replicas 已 updated；
- desired replicas 已 available；
- unavailable replicas 为 0。

以下情况立即返回结构化失败：

- Deployment 不存在；
- `Progressing=False` 且 reason 为 `ProgressDeadlineExceeded`；
- ReplicaFailure condition 为 True；
- live Deployment UID 在一次等待过程中发生替换。

其余未就绪状态通过 bounded watch/poll 等待，受调用方 context 和 `ReadinessTimeout` 约束。API watch 中断、resource version 过期或暂时 transport failure时允许在 deadline 内重新 List/Get；context cancellation 和 timeout 必须可由 `errors.Is` 判定。

## Bootstrap workload evidence

现有验证顺序与 fail-closed 语义保持：

1. 调用 `authentication/v1 TokenReview.Create`，audience固定为 `org-worker-bootstrap`；
2. 校验 authenticated、audience、ServiceAccount username 和唯一 Pod UID claim；
3. 在固定 platform Kubernetes Namespace 中按 org label列出候选 Pod，匹配请求 Pod UID；
4. 校验 Pod ServiceAccount、server-owned labels 和 bootstrap generation；
5. 读取 live ReplicaSet，校验 Pod owner UID；
6. 校验 ReplicaSet 的 canonical Deployment controller owner；
7. 校验声明的 immutable image 与 runtime imageID。

TokenReview、Pod 和 ReplicaSet 都必须使用 typed API。不得通过 raw REST path 或 `kubectl create --raw` 访问。

### kind image linkage exception

当前 kind/containerd 本地导入可能让 runtime imageID 与 Pod spec 中的 digest reference 不完全相同，现有实现会在 Go 进程中执行 `docker exec ... crictl inspecti` 建立 exact import linkage。这不是 Kubernetes API 访问，但仍是程序对 CLI 的运行时依赖。

用户于 2026-08-02 确认把它拆成独立事项。本次只迁移 Kubernetes 操作：

- 本次 Kubernetes API 迁移不得把该逻辑混入新的 Kubernetes client；
- 保留为独立、仅 kind development context启用的 `RuntimeImageLinkVerifier`，使 Kubernetes 访问先完成 API 化；
- 后续应通过 container runtime/Docker API 或调整 E2E image distribution消除该 CLI dependency；
- 非 kind context仍要求 runtime imageID 与声明 image exact match，不调用本地 verifier。

消除该 CLI dependency 需要另写规格，不作为本规格实现或验收的阻塞条件。

## 错误与日志

- 对外业务错误继续由 service 层映射；kube adapter 返回 wrapped typed errors。
- 日志可以包含 operation、GroupVersionResource、Namespace、resource name、API status reason 和 request ID。
- 日志不得包含 bootstrap Secret data、ServiceAccount token、kubeconfig内容或认证 header。
- Forbidden 与 Unauthorized不得伪装成 NotFound；apply conflict不得自动无限重试。
- retry 必须 bounded，并服从 context deadline。

## 测试要求

### TDD 顺序

批准本规格后严格按以下 red → green 顺序推进：

1. client config 与 composition tests；
2. typed resource builder tests；
3. Namespace Get/Create race与 ownership tests；
4. Server-Side Apply action、field manager、ownership conflict tests；
5. readiness success/failure/cancel/re-watch tests；
6. TokenReview、Pod、ReplicaSet identity verification tests；
7. command-dependency guard test；
8. real kind E2E regression。

### 必须覆盖

- Kubernetes API adapter files不再 import `os/exec`；kind-only `kind_image_verifier.go` 是已批准拆分的独立例外；
- production adapter不包含字符串 `kubectl`；
- 没有安装 `kubectl` 时，使用可访问 kubeconfig的 `org-console` API client仍可初始化并工作；
- typed resources与当前 manifest的安全字段等价；
- NotFound、AlreadyExists、Forbidden、Conflict 和 timeout均按结构化类型处理；
-重复 apply 幂等；
- bootstrap credential不出现在 error、log fixture 或 object debug dump；
-现有 bootstrap identity adversarial cases全部保留；
-根模块 `go test -race ./...`、`go vet ./...` 和三个真实 kind + Temporal E2E通过。

E2E harness自身可以继续调用 `kubectl` 创建fixture、注入故障、观察结果和清理资源；验证对象必须是由真实 API adapter部署的 workload。

## 文档和兼容性更新

实现完成后同步更新：

- `001-worker-hosting-mvp.md` 中“real kubectl adapter”为“real Kubernetes API adapter”；
- `implementation-status.md` 中 configurable `kubectl` context描述；
-开发文档区分运行 binary dependency与 E2E/script prerequisites；
-配置文档继续保留 `ORG_KUBE_CONTEXT`、`ORG_KUBECONFIG` 与 `ORG_KUBE_NAMESPACE`。

本次变更不修改 user-facing HTTP API、持久化 schema 或 Worker SDK contract。

## 验收标准

1. `org-console` 的 Kubernetes runtime path不启动 `kubectl`。
2. Namespace、apply、readiness、TokenReview、Pod 与 ReplicaSet 操作全部通过 Kubernetes API client。
3. workload安全字段、bootstrap identity验证和 promotion gate无回退。
4. scripts和 E2E仍可使用 `kubectl`，但 production packages不依赖它。
5.结构化 API error与 context cancellation在单元测试中可判定。
6.全部 race、vet、文档和真实 E2E验收通过。
