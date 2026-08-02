# Sample repository independence amendment

> Terminology: this specification follows the canonical [org glossary](../../user/architecture/glossary.md). Product identity is Tenant → Worker → WorkerVersion → Workflow Run. Infrastructure continues to use one shared platform Temporal Namespace and one shared platform Kubernetes Namespace; Samples do not configure either product identity or platform routing.

## 状态

**Approved — implementation authorized by the user on 2026-08-01.**

本规格是 [`007-hello-org-sdk-sample.md`](007-hello-org-sdk-sample.md)、[`008-parallel-confirmation-org-sdk-sample.md`](008-parallel-confirmation-org-sdk-sample.md) 与 [`009-dynamic-decision-org-sdk-sample.md`](009-dynamic-decision-org-sdk-sample.md) 的共同 independence amendment。它不改变三个 Sample 的 Workflow、Activity、dynamic projection 或 bootstrap contract，只改变用户获取、测试、构建和交付 Sample 的仓库边界。

不得借本规格实现尚未批准的 [`010-workflow-execution-risk-defense.md`](010-workflow-execution-risk-defense.md)。

## 产品目标

`samples/hello`、`samples/parallel-confirmation` 与 `samples/dynamic-decision` 中的每一个目录都必须模拟用户自持的独立 Worker Git repository。用户的正常路径从 Sample 自己的目录开始：

```text
cd samples/<sample>
  -> test / vet
  -> build OCI image
  -> push registry, or load local kind
  -> receive immutable image@sha256:... reference
  -> publish WorkerVersion through org Console/API
  -> start Workflow and observe semantic projection/action/result
```

Sample 目录以外的 org 文件不得成为这条路径的运行前提。mono-repo 根级 target 可以保留为维护者批量验收入口，但只能委托给 Sample 自己的 target，不得复制或拥有 Sample build logic。

## 每个 Sample 的最小仓库契约

每个 Sample 必须完整拥有：

```text
README.md
Makefile
go.mod
go.sum
Dockerfile
definition.go / activities.go / types.go
cmd/worker/main.go
scripts/build-image.sh
scripts/push-image.sh
scripts/kind-load.sh
tests...
```

约束：

- 所有用户命令可在 Sample 根目录执行；脚本不得向上解析 org repository root。
- Docker build context 必须是 `.` / Sample root；Dockerfile 不得 `COPY` 根 module、`sdk/` 或其他 Sample。
- Sample source 只能 import versioned public Org SDK package，不得 import `internal/`、root test harness 或 root generator。
- `go.mod` 必须使用 Go module 可解析的明确 SDK version；禁止 `replace github.com/wu8685/org => ../..` 或其他 parent-directory replace。
- 当前尚无稳定 SemVer SDK tag时，允许 pin 到已发布 commit 的标准 Go pseudo-version。后续 SDK tag发布后按 SDK compatibility policy升级，不使用浮动 branch。
- Sample repository不保存generated contract JSON；contract测试直接从typed Definition验证内存结果。
- README 的相对链接必须在独立复制后仍有效；需要引用平台文档时使用稳定公开 URL，不能依赖 `../../docs`。
- Sample 不使用 symlink 引用目录外文件。

Per [`014-sample-slimming.md`](014-sample-slimming.md)，完整WorkerVersion publish request由org主项目的`docs/user/api`承接，不保存在用户Worker repository。Sample README只链接公开文档并说明本Sample的Worker、Workflow、input与建议resources。

## Sample Makefile contract

三个 Makefile 使用一致的用户 target：

| Target | 行为 |
|---|---|
| `make test` | 在当前 module运行`go test ./...` |
| `make vet` | 在当前 module运行`go vet ./...` |
| `make verify` | 执行无外部状态写入的test/vet/independence checks |
| `make image VERSION=... COMMIT=...` | 以Sample自身为context构建本地tag |
| `make push VERSION=... COMMIT=... IMAGE_REPOSITORY=...` | 构建并push，由registry结果输出`IMAGE_DIGEST=<repo>@sha256:...` |
| `make kind-load VERSION=... COMMIT=... [KIND_CLUSTER=org]` | 构建、加载kind并输出精确immutable digest |

`VERSION`、`COMMIT` 与 `IMAGE_REPOSITORY` 是用户/CI build provenance，不是 Worker runtime routing。`push` 不保存 registry credential；它只使用用户已配置的 Docker credential helper/session。输出不得打印 credential。

根 Makefile 可以保留兼容 target（例如`sample-test`、`parallel-sample-kind-load`），但实现必须形如 `$(MAKE) -C samples/<sample> <target> ...`。根 target不得直接调用 Sample script、设置Docker build context或计算digest。

## 配置与信任边界

用户在 Sample repository 中填写或控制：

- typed Definition、Activity business code与business input/output；
- image repository、release version与source commit；
- WorkerVersion description、version config、resources与Secret references；
- CI registry login和push过程；Secret value不得提交到Sample。

候选 Pod 部署时由 org 注入，下列值不是用户README要求手工填写的本地配置：

```text
ORG_BOOTSTRAP_ENDPOINT
ORG_BOOTSTRAP_TOKEN_FILE
ORG_BOOTSTRAP_WORKLOAD_TOKEN_FILE
ORG_BOOTSTRAP_POD_UID
ORG_BOOTSTRAP_EXPIRES_AT
TEMPORAL_ADDRESS
TEMPORAL_NAMESPACE
TEMPORAL_TASK_QUEUE
TEMPORAL_WORKER_DEPLOYMENT
TEMPORAL_WORKER_BUILD_ID
```

Org SDK hosted startup必须保持：load injected configuration → in-memory contract/digest → idempotent bootstrap registration → accepted → Temporal polling。Sample不得提供`.env`让用户伪造这些值，不得把bootstrap credential复制进image，也不得提供production standalone绕过路径。

## Publish 与 Run 用户体验

image命令成功后必须打印可直接复制到Console/API的不可变digest。README随后按具体Sample说明：

1. 在Console创建逻辑Worker；
2. 发布WorkerVersion，只提交version、description、digest、runtime/version config和provenance；
3. 等待SDK registration、poller与probe完成；
4. 触发该Sample固定Workflow；
5. Hello观察顺序DAG与结果；parallel-confirmation经Gateway action确认后观察fork/join；dynamic-decision分别观察selected与`skipped`分支。

README不要求上传manifest，不提供direct Temporal Signal/Task Queue操作，也不要求终端用户持有Temporal、Kubernetes或bootstrap credential。MVP中Console/API认证仍由org平台提供；Sample脚本不得保存或猜测control-plane session。

## 面向用户的项目文档

本里程碑同时补齐可从repository root导航的用户文档。对外文档先说明用户能完成什么，不讨论框架选型、替代方案比较或内部实施历史；这些材料继续留在`docs/development/specs/`。

必须提供：

- root `README.md`：org解决的问题、Tenant → Worker → Version → Workflow → Run对象关系、用户自有image + immutable digest + Org SDK自动注册体验、Console/API用途、明确局限与安全边界、最短本地路径及前置条件；
- `docs/user/getting-started.md`：启动local Temporal、kind与Console，进入一个Sample目录完成test/kind-load，复制digest发布WorkerVersion，再触发并观察Run；不要求用户理解manifest、Task Queue或Temporal Event History；
- `docs/user/architecture/overview.md`：可链接地解释Org SDK、control plane、Worker runtime、Temporal与Kubernetes各自职责；动态DAG和人工action只从validated semantic projection呈现，不从Temporal history猜测；
- `samples/README.md`：Hello → parallel-confirmation → dynamic-decision learning path，每个Sample写明学习目标、目录内命令与预期可观察结果；
- 每个Sample README：从其自身repository root出发，不引用org root target或`../../docs`，完整覆盖test、image、push/kind-load、digest publish与Workflow观察路径。

根README和Getting Started必须链接上述架构概览与三个Sample README。所有链接在mono-repo内有效；单独复制Sample后，其README仍自包含，外部平台文档引用使用公开repository URL。

一致性检查必须拒绝：把Tenant写成底层基础设施对象、public `scope`、mutable tag作为publish输入、manifest upload/file作为运行前提、要求用户填写bootstrap/Temporal routing配置，以及从root目录运行Sample命令的主路径。

## 可复制/拆仓验收

对每个Sample执行以下独立性验收：

1. 只复制该Sample目录到一个空临时目录，不复制org root、其他Samples或root `go.work`。
2. `GOWORK=off go mod download`可从versioned module graph解析依赖。
3. `GOWORK=off make test`、`make vet`与`make verify`成功。
4. source/go.mod/scripts/Dockerfile/README不包含parent replace、`repo_root`、`COPY sdk`、`COPY samples/`、root Makefile target或`../../docs`依赖。
5. `docker build`只接收复制后的目录作为context并生成Worker binary image。
6. `make kind-load`在`kind-org`输出digest reference；该digest可被org pending WorkerVersion部署并完成bootstrap registration。
7. 检查repository不含generator、generated contract或control-plane publish body。
8. `git init && git add .`可形成自包含repository，不出现broken symlink或缺失tracked input。

## TDD 与真实验收顺序

1. 先加root-owned independence contract test，令当前缺失Sample Makefile、parent `replace`、root Docker context与`repo_root`依赖产生失败。
2. 先加每个Sample的Makefile/script contract test，再创建Makefile和local-context script。
3. 移除parent replace，pin可解析SDK version；以`GOWORK=off`在复制目录运行unit/vet。
4. 先加fake Docker CLI test，覆盖image/push输出、digest格式、参数校验与不泄露credential，再实现push path。
5. 改Dockerfile为self-contained context，分别真实build三个image。
6. 根Makefile test先证明聚合target只委托，再最小改为`make -C`。
7. 扩展org真实E2E harness：每个image必须通过对应Sample目录的`make kind-load`构建；随后由root-owned control-plane acceptance test使用返回digest完成kind+Temporal publish/bootstrap/run验收。
8. E2E完成后检查Namespace、Pod、Secret、临时复制目录与本地image清理。
9. 先扩展docs test，证明root README、Getting Started、architecture overview、learning path、链接与术语/contract一致性缺失时失败；再写用户文档并验证其中命令。
10. 运行root与三个独立module的unit、race、vet、docs tests；审查diff后才形成独立里程碑commit/push。

真实E2E仍由org control plane repository拥有，因为它验证publish、Tenant隔离、bootstrap、Temporal与Kubernetes adapters；Sample只作为外部用户repository fixture。这个归属不允许E2E绕过Sample自己的Makefile，也不允许Sample反向import org `internal/`测试代码。

## 验收结果

2026-08-01实施结果：

- 三个Sample均拥有自己的Makefile、versioned `go.mod`、local-context Dockerfile、build/push/kind scripts与value-first README；
- 将任一Sample单独复制到空目录后，`GOWORK=off make test`与module resolution通过，不读取org parent；
- fake Docker contract tests证明image/push target只使用Sample root context，并从registry结果输出immutable digest；
- 三个Sample分别完成真实Docker build；
- org E2E harness直接从各Sample目录执行`make kind-load`，Hello、parallel-confirmation与dynamic-decision均通过真实`kind-org` + Temporal publish/bootstrap/Run验收；
- root sample targets只调用`$(MAKE) -C samples/<sample>`，不再拥有build/digest逻辑；
- root README、Getting Started、architecture overview、learning path与三个独立README已加入术语、链接和publish-contract tests；
- generated contract与publish request均不保存在Sample repository；contract测试直接验证typed Definition，publish示例集中在org `docs/user/api`。
