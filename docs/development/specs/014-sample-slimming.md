# Sample slimming amendment

> Terminology follows the canonical [org glossary](../../user/architecture/glossary.md). Product identity remains Tenant → Worker → WorkerVersion → Workflow Run; infrastructure uses one shared platform Temporal Namespace and one shared platform Kubernetes Namespace.

## 状态

**Approved — implementation authorized by the user on 2026-08-01.** 010风险防御不在本规格范围。

本次只实施第一步：删除Sample冗余artifact/config plumbing、集中publish API文档并完成全量验收。SDK薄入口、Build ID传播与`RunHostedWorkerFromEnvironment`留给独立小slice/spec，不得混入本里程碑。

本规格是006–009、012、013的Sample cleanup amendment，不改变三个Workflow的教学行为。

## 目标

用户阅读一个Sample时，主要只看到：

```text
业务 Definition
业务 Activities / types
最薄的 main + Org SDK hosted入口
Dockerfile
Makefile / scripts
README
tests
```

控制面publish payload、provenance字段、contract artifact和Temporal routing plumbing都不属于用户业务代码，应从Sample移除或由Org SDK/主项目文档承接。

## 当前审计

三个Sample均有以下冗余：

```text
cmd/generate-manifest/
generated/org-worker-manifest.json
config/release.example.json
config.go / config_test.go
```

- bootstrap auto-registration已从typed Definition在process内生成contract/digest；startup、Docker build、publish和E2E均不读取generated JSON。
- `config/release.example.json`是org publish API body，不是Worker runtime config。
- `cmd/worker/main.go`已经调用`orgsdk.LoadHostedWorkerConfig(os.Getenv, os.ReadFile)`，没有调用Sample的`LoadConfig`；因此三个`config.go`只是重复/未使用的Temporal environment plumbing。

## 拟删除与外移

每个Sample删除：

```text
cmd/generate-manifest/
generated/
config/release.example.json
config.go
config_test.go
```

完整publish JSON、runtime resources、source provenance与digest-only规则移到org主项目：

```text
docs/user/api/publish-worker-version.md
docs/user/api/examples/publish-worker-version.json
```

Sample README只写本Sample的Worker name、Workflow name、input、建议resources、`IMAGE_DIGEST`复制位置和可观察结果，并链接主项目API文档。Worker repo不保存control-plane request body。

## 最小目录结构

以Hello为例：

```text
README.md
Makefile
go.mod / go.sum
Dockerfile
definition.go
activities.go
types.go
cmd/worker/main.go
scripts/build-image.sh
scripts/push-image.sh
scripts/kind-load.sh
*_test.go
```

Parallel Confirmation与Dynamic Decision保持相同骨架，只增加各自业务Definition/Activity/tests。

## Org SDK host config最小方案

### 当前可行基础

平台继续向Pod注入bootstrap credential、Pod identity、Temporal address、platform Temporal Namespace、Task Queue、Worker Deployment和Build ID。终端用户不填写这些值。

现有SDK已经拥有`LoadHostedWorkerConfig`与`RunHostedWorker`。删除Sample `config.go`不会改变runtime，因为当前main没有调用它。

### 推荐新增的薄入口

在Org SDK增加：

```go
func RunHostedWorkerFromEnvironment(
    ctx context.Context,
    registrations ...Registration,
) error
```

该函数内部固定执行：

```text
read/validate platform-injected config
→ construct canonical contract
→ bootstrap register and await accepted
→ start Temporal polling
```

Sample main只保留业务registration与process lifecycle：

```go
worker, err := sample.NewWorker(buildIDProvidedBySDKContext)
// register Definition/Activities
err = orgsdk.RunHostedWorkerFromEnvironment(ctx, worker.Registrations()...)
```

实现时需解决`NewWorker`当前需要Build ID的问题。推荐让SDK runtime context提供version/build identity，而不是让Sample读取`TEMPORAL_WORKER_BUILD_ID`；Definition本身不得依赖环境变量。若这一改动会扩大SDK compatibility surface，则分两步：

1. 本cleanup先删除未使用的Sample `config.go`，main继续调用现有`LoadHostedWorkerConfig`；
2. 单独SDK ergonomics slice引入薄入口，再把main缩到单调用。

**推荐两步实施**：先取得确定的Sample瘦身，不为隐藏一个已有SDK调用而仓促改变Build ID传播语义。

## 外部接口与兼容性

保持不变：

- `make test|verify|image|push|kind-load`；
- immutable `IMAGE_DIGEST`输出；
- Worker/Workflow names、inputs/results与dynamic projection；
- bootstrap registration、poller/probe promotion、Current/历史Pinned语义；
- Sample可复制为独立repository。

可能受影响的只有直接运行旧开发路径的CI：

```text
go run ./cmd/generate-manifest
diff generated/org-worker-manifest.json
read config/release.example.json
```

推荐一次性删除，不保留stub：contract测试直接调用typed Definition/`orgsdk.GenerateManifest`；运行时contract从control plane只读接口查看；publish示例改用org API docs。已发布image不含对这些host-side文件的依赖。

## README、Makefile与E2E调整

- Makefile用户target不变；没有manifest target需要迁移。
- README删除generator/generated/release-example路径，只保留业务学习与目录内命令。
- root README/Getting Started链接集中publish文档。
- independence test要求上述旧路径和Sample `config.go`不存在。
- copied-repository test直接`GOWORK=off make test`，不再先删generated目录。
- Docker context检查确保不携带generated contract或publish JSON。
- root-owned E2E仍从每个Sample目录执行`make kind-load`，再用digest完成publish/bootstrap/Run。

## TDD与真实验收（批准后）

1. 先写失败的absence/stale-reference tests，覆盖五类待删路径。
2. 先把publish example contract test迁到`docs/user/api`，再外移JSON。
3. 保留既有SDK host-config/accepted-before-polling tests；SDK薄入口不属于本阶段，后续须另写spec并先red再实现。
4. 删除Sample generator、generated、release example及重复`config.go`；更新README/specs。
5. 三个复制目录分别执行`GOWORK=off make test/verify`。
6. 三个Sample分别真实`make image`与`make kind-load`，检查digest。
7. 运行Hello、Parallel Confirmation、Dynamic Decision真实kind+Temporal E2E，验证串行、人工确认+并行、runtime分支+`skipped`。
8. 运行root与三个module的unit/race/vet/docs/link tests、`git diff --check`和010 no-diff审计；清理Namespace/images。
9. 形成独立cleanup milestone commit/push。

## 第一阶段实施结果

2026-08-01完成本规格第一阶段：

- 三个Sample删除generator、checked-in generated contract、publish request副本及未使用的Sample config plumbing；未保留兼容stub；
- publish request示例集中到`docs/user/api`，由domain validator测试digest-only与server-owned field边界；
- 三个README只保留业务学习、目录内test/build/push/kind-load、digest和Run观察路径，并链接主项目发布文档；
- absence、stale path、公开文档链接/术语与复制目录测试已加入root test suite；
- root与三个独立module的race/vet通过，三个Sample从各自目录构建并通过真实`kind-org` + Temporal E2E；
- 当前main继续使用既有`LoadHostedWorkerConfig`。SDK薄入口和Build ID隐藏仍是下一阶段，不在本commit中。

## 已确认决定

1. 同时删除generator、generated JSON、release examples和三个未使用的Sample `config.go/config_test.go`。
2. 完整publish示例集中到`docs/user/api`，Sample README只保留Sample-specific发布路径，不复制JSON body。
3. SDK薄入口按两步实施：本cleanup继续使用现有`LoadHostedWorkerConfig`；Build ID传播另开小slice/spec。
4. 旧generator/golden path一次性删除，不保留兼容stub；runtime与已发布image不受影响。
