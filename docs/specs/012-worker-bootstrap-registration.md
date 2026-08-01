# Worker bootstrap registration protocol

> Terminology: this specification follows the canonical [org glossary](../architecture/glossary.md). Product identity is Tenant → Worker → WorkerVersion. Infrastructure continues to use one shared platform Temporal Namespace and one shared platform Kubernetes Namespace; a product Tenant is never mapped to either underlying platform resource boundary.

## 状态

**Approved — implementation authorized by the user on 2026-08-01.**

本规格取代 `006-org-sdk.md` 与 `011-console-ui-http-api.md` 中由发布请求或UI提供manifest artifact的旧设计。实现必须严格遵循SDD → TDD，不得把`010-workflow-execution-risk-defense.md`中尚未批准的扩展防御混入本里程碑。

## 决策摘要

用户发布 WorkerVersion 时只提交 release 与部署输入：

```text
authenticated Tenant + route workerName
version + description
immutable OCI image digest
versionConfig + runtime resources/Secret references
```

2026-08-02批准的公开输入修订：publish API/UI不再接受`source`、repository、branch、commit或CI reference。可信审计主体、request ID、镜像观测身份等metadata由服务端产生；既有WorkerVersion已保存的source字段只为读取兼容保留，不得由新publish覆盖。

用户不提交、上传或编辑 SDK manifest。org 创建 pending release，部署候选 Pod，并注入一个短生命周期、单用途、服务端绑定到确切 Tenant + Worker + WorkerVersion + expected image digest 的 bootstrap credential。Org SDK startup 从用户已编译进 Worker 的 typed Definition 在内存中构造 canonical manifest/digest，向内部 registration endpoint 自注册。control plane 验证 credential、Pod/image identity、contract 与 SDK protocol 后保存只读 contract，再通过既有 Worker poller + pinned contract verification 做 promotion gate。

```text
Console publish
  -> org creates pending WorkerVersion and publish operation
  -> org deploys candidate Pod with bound bootstrap material
  -> Org SDK startup constructs canonical manifest from typed Definition
  -> internal bootstrap registration
  -> org verifies binding + Pod/image + contract/protocol/policy
  -> immutable contract is stored
  -> poller ready + post-poller pinned contract probe matches registration
  -> WorkerVersion Ready and optionally Current
```

org 仍不 build 或 push 用户 image。bootstrap registration 证明的是“被部署候选 runtime 使用该 credential 声明并加载了哪个 contract”，不能单独证明 image 不含恶意代码或 raw SDK bypass；`010-workflow-execution-risk-defense.md` 中更强的 supply-chain 与不可信 Worker 防御仍未获批准。

## 目标与非目标

### 目标

1. UI 与 user-facing publish API 不再接收 manifest bytes 或 manifest digest。
2. contract 来源与实际启动的候选 Worker runtime 绑定，而不是浏览器上传文件。
3. Worker 不能选择或覆盖 Tenant、Worker、WorkerVersion、image 或 routing identity。
4. registration 可安全处理 Worker restart、网络重试与重复请求，不允许不同 contract 覆盖。
5. registration verified、poller ready 与 contract probe verified 是三个独立 promotion 条件。
6. bootstrap credential 不能调用 Run/action、读取其他资源或注册其他 release。
7. 失败、timeout、rotation 与拒绝均产生 Tenant-scoped Audit，但不记录 credential。

### 非目标

- 让 Worker 创建 Worker/WorkerVersion；
- 让 Worker 提供 Tenant ID、Worker name、version 或 platform routing name；
- 用 bootstrap token 作为正常 control-plane session、Temporal credential 或 Kubernetes API credential；
- 允许 registration 更新已 Ready release 的 contract；
- 在 UI 中恢复 manifest file upload；
- 用 registration 代替 poller、contract probe、OCI provenance 或未来 signature/attestation；
- 承诺恶意 image 无法伪造其自身 contract。

## WorkerVersion 状态模型

建议把 publish operation 与 durable WorkerVersion verification state 分开。publish operation 可完成/失败；WorkerVersion 保存可恢复的细分状态：

```text
pending
  -> deploying
  -> awaiting-registration
  -> contract-registered
  -> poller-ready
  -> probe-verified
  -> ready/current

any pre-ready state
  -> failed-registration-timeout
  -> failed-image-mismatch
  -> failed-contract-validation
  -> failed-protocol-unsupported
  -> failed-probe-mismatch
  -> failed-dependency
```

实现不必把每个内部阶段都暴露为可写 enum，但 read model 必须分别表达：

- Kubernetes candidate health；
- registration status 与安全 failure category；
- Worker poller status；
- contract probe status；
- promotion/current status。

`ready` 必须满足：候选 workload Ready、registration verified、该 WorkerVersion poller 可见、pinned probe 返回相同 manifest digest / SDK protocol / Build ID。任一条件缺失时不得标为 Ready 或 Current。

## User-facing publish contract

### OCI image identity

发布输入必须是完整的不可变OCI digest reference：

```text
<allowlisted-registry>/<repository>@sha256:<64 lowercase hex>
```

- mutable tag（如`:latest`、`:2026.08.1`）一律拒绝；`tag@digest`也拒绝，避免同一输入同时携带可变与不可变identity；
- registry host按control-plane allowlist校验；org不把一个registry的digest重写或镜像到另一个registry；
- digest是发布、credential binding、Pod image observation、Audit与WorkerVersion immutability的唯一image identity；
- MVP要求**platform-specific image manifest digest**。OCI image index / manifest list digest拒绝发布，因为节点运行时通常暴露其platform child digest，无法与index做离线exact compare；
- multi-arch支持留待独立后续规格：届时必须从allowlisted registry解析index、验证observed child属于index，并定义registry auth/cache/TOCTOU；不得仅因字符串digest相同就宣称已验证；
- control plane在部署前可以检查registry media type；registration时仍必须将Pod observed `imageID`规范化后与expected platform digest exact compare；任一步不支持、无法解析或不一致都不能promotion。

#### kind/containerd 本地导入说明

`kind load docker-image` 会让 containerd CRI 创建 `docker.io/library/import-<date>@sha256:...` 导入 wrapper；Pod `imageID` 因而不一定逐字返回 Pod spec 中的 `org.local/...@sha256:...`。这不是生产 registry 验证的宽松例外。仅当 Kubernetes context 是显式配置的 kind development context 时，验证器必须同时满足：Pod spec `containers[worker].image` 与 expected reference 完全一致；Pod UID、ServiceAccount 与 TokenReview 绑定一致；在该 Pod 所在 kind node 上执行 `crictl inspecti <expected reference>`，其 `repoDigests` 同时包含 expected reference 与 Pod runtime `imageID`。三者组成可验证的 exact import linkage。非 kind context 仍要求 normalized runtime `imageID` 与 expected reference 完全一致；不得仅比较 tag、repository 或任意一个 SHA 字符串。

### 请求

```http
POST /api/v1/workers/{workerName}/versions
Idempotency-Key: publish-...
Content-Type: application/json

{
  "version": "2026.08.1",
  "description": "本版本做什么；创建时必填。",
  "image": "registry.example.com/worker@sha256:...",
  "versionConfig": {
    "region": "ap-southeast-1",
    "provider": {"secretRef": "provider-token"}
  },
  "runtime": {
    "cpu": "100m",
    "memory": "128Mi",
    "environment": []
  }
}
```

请求使用 authenticated principal 派生 Tenant；`workerName` 只来自 tenant-scoped route lookup。body 必须拒绝：

- `tenantId`、`tenantSlug`、`scope`、重复 `workerName`；
- `source`、repository、branch、commit、CI reference及其aliases；
- manifest、metadata、projection、contract、manifest digest；
- Task Queue、Worker Deployment、Workflow ID、Kubernetes workload name；
- bootstrap endpoint、credential、Pod UID、Build ID 或 SDK protocol override。

### 响应

```http
HTTP/1.1 202 Accepted
Location: /api/v1/operations/pub-...

{
  "operation": {
    "id": "pub-...",
    "state": "running",
    "phase": "awaiting-registration",
    "statusUrl": "/api/v1/operations/pub-...",
    "workerVersionUrl": "/api/v1/workers/example/versions/2026.08.1"
  }
}
```

相同 publish idempotency key + canonical payload 返回同一 pending release/operation；同 key 不同 payload 返回 conflict。相同 Tenant + Worker + version 已存在时，不得创建第二个 release 或替换其 contract。

### Approved clarification: publish idempotency ledger

本段澄清既有契约，不改变产品边界：

- `Idempotency-Key`是publish必填header，必须为1–200个visible ASCII字符（`!`–`~`，不含空格）；不得写入log或Audit原文，ledger只保存其hash。
- reservation作用域是authenticated `{tenantID, principalID, keyHash}`。同Tenant的另一principal或另一Tenant使用同一header值，不共享operation。
- canonical payload digest包含route中的`workerName`以及规范化后的version、description、immutable image、versionConfig与runtime；JSON object字段顺序和无意义空白不影响digest。CSRF token、request ID、server-derived metadata和credential不进入digest。
- reservation必须在启动异步publish前durable保存并原子判定：同scope + 同digest返回原`202`、`Location`和operation，不再次调用publish/deploy；同scope + 不同digest返回`409 conflict`。
- new、replay与payload conflict都写Tenant-scoped Audit，只记录operation ID、payload digest与idempotency key hash等非秘密引用。
- terminal reservation默认保留24小时并可配置；running reservation不因retention到期而回收。terminal记录过期后可lazy cleanup并允许key重新reservation，但相同Worker/version仍受immutable release conflict约束。
- process restart后从control-plane durable store恢复reservation与operation read model；浏览器或Console内存不是idempotency事实来源。

## Pending release 与 server-derived binding

org 接收 publish 后，在任何 Pod 启动前冻结：

```text
tenantID / tenantSlug
workerName
WorkerVersion record ID + public version
expected immutable OCI digest
server-derived canonical routing names
versionConfig/runtime digests
server-derived audit and image-observation references
publish operation ID
registration deadline
```

只有该 pending release 可以获得 bootstrap credential。Worker registration body 不含这些 target identity；endpoint 从 credential reservation 与 authenticated workload evidence中恢复它们。

pending release 在 registration 前不可启动用户 Run、不可成为 Current。配额 lease 从 pending deployment 开始占用；失败或明确撤销时按现有 quota reconciliation 释放。

## Bootstrap credential

### 属性

credential 必须：

- 由密码学安全随机源生成，opaque，服务端只保存不可逆 hash；
- 短生命周期；推荐初始默认 15 分钟且可配置；
- audience 固定为 `org-worker-bootstrap`；
- capability 只有 `register-contract`；
- 精确绑定 pending release ID、Tenant、Worker、version、expected image digest 与 deployment generation；
- 不能兑换 user session、Temporal credential或其他 API capability；
- registration verified、release failed/deleted、token rotated 或过期时立即吊销；
- 不进入 log、event、error、telemetry、Audit、Pod label/annotation或 user-facing API。

“单用途/单次”采用**一次逻辑写入 + exact retry receipt**语义：第一个成功的 canonical contract 以 compare-and-swap 固化；同 credential、同 digest 的重复请求返回同一 registration receipt，不再次写 contract；不同 digest/identity 永远 conflict 并触发安全 Audit。这样既保持 single-use，又允许 HTTP response 丢失与 Worker restart 后重试。

registration identity/idempotency material由服务端按以下绑定计算，不接受Worker提供target locator：

```text
registrationKey = H(
  pendingWorkerVersionRecordID,
  expectedImageDigest,
  manifestDigest,
  sdkModuleVersion,
  runtimeProtocolVersion,
  contractVersion,
  observedBuildID
)
```

同一binding与canonical body返回同一结果；manifest/protocol/Build ID变化会得到不同material并被既有reservation拒绝，而不是创建第二次注册。

### Pod 注入

推荐注入方式：

- bootstrap endpoint 作为非敏感配置；
- opaque credential 通过只读 projected Secret file 注入，不放普通 env；
- Pod UID 通过 Downward API 提供，但它只是 locator，不是独立可信证据；
- 显式 projected ServiceAccount token 使用专用 audience、短 expiration 与 Pod binding，提交给 org 做 TokenReview；
- `automountServiceAccountToken: false` 保持不变；专用 projected token不授予 Worker Kubernetes API RBAC。

建议内部变量/文件 contract：

```text
ORG_BOOTSTRAP_ENDPOINT
ORG_BOOTSTRAP_TOKEN_FILE
ORG_BOOTSTRAP_WORKLOAD_TOKEN_FILE
ORG_BOOTSTRAP_POD_UID
```

control plane 自己持有完成 TokenReview 与 Pod read 所需的最小 Kubernetes 权限；Worker ServiceAccount 仍无 Kubernetes API 权限。

### rotation 与 restart

- registration 前同一 Pod process restart：只要 credential 未过期，可 exact retry。
- Pod replacement 或 deployment generation 变化：org 只为同一 pending release签发新 credential，绑定新 Pod/generation，并立即吊销旧 credential。
- registration 成功但 Worker 未收到 response：同 token + 同 digest 可读取同一 receipt，直到短 retry grace 结束；不能提交新 contract。
- registration成功后、开始Temporal Worker polling前process crash：release停留在`contract-registered`而非Ready；restart使用同一binding读取receipt，随后开始polling。若始终没有poller，则按poller deadline失败。
- credential 过期而 release 仍在 registration deadline 内：推荐由 controller 在观察到合法 replacement Pod 后自动轮换一次；反复轮换达到上限后失败，避免无限等待。
- Ready/failed release restart：不重新打开 registration。runtime 通过 pinned contract probe证明已加载保存的 contract；若确需 contract 变化，必须发布新 WorkerVersion。

## Internal registration endpoint

建议 endpoint：

```http
PUT /internal/v1/worker-bootstrap/registration
Authorization: Bootstrap <opaque-token>
X-Org-Workload-Token: <pod-bound-token>
Content-Type: application/json

{
  "canonicalManifest": {},
  "manifestDigest": "sha256:...",
  "sdkModuleVersion": "...",
  "runtimeProtocolVersion": "...",
  "contractVersion": "...",
  "projectionEventVersion": "...",
  "dynamicNodeIdVersion": "...",
  "capabilities": [],
  "observedBuildId": "..."
}
```

该 endpoint 是 Worker data-plane bootstrap endpoint，不是 browser/user API：

- 不接受 cookie user session、Tenant header、route worker/version 或 raw platform name；
- strict JSON，拒绝任何 target identity、runtime routing或credential字段；
- request/body、canonical manifest、schema depth/count/bytes均有固定上限；
- rate limit key 使用 credential hash + source workload identity，不回显 secret；
- response只返回 opaque registration receipt、状态与安全错误码。

`observedBuildId` 是 runtime evidence，不是 Worker 选择版本的 locator。control plane从 pending binding计算 expected Build ID，并要求相等；不相等即拒绝。Worker 不能借它注册到另一 version。

## Org SDK startup 行为

Org SDK 保持 code-first。用户只声明typed Definition并调用SDK提供的hosted Worker启动入口；不加载、定位或管理manifest artifact。hosted startup adapter顺序固定：

1. 加载并验证平台注入的非业务配置；
2. 从当前process中的typed Definition构造contract model；
3. 按SDK版本锁定的canonicalization生成manifest bytes与digest；
4. 核对SDK module、runtime protocol、contract/projection/ID versions与runtime Build ID；
5. 读取injected bootstrap credential与workload evidence；
6. 以bounded timeout调用registration endpoint；
7. 对network timeout/5xx使用bounded exponential retry；对4xx identity/contract错误停止重试；
8. await明确的accepted或rejected结果；
9. **只有accepted/idempotent accepted后**才创建并启动Temporal Worker polling；
10. credential、workload token与完整sensitive response不得进入日志或telemetry。

SDK构造出的manifest是typed Definition的deterministic serialization，不是用户文件输入。Definition、SDK version、canonicalization与Build ID相同必须产生相同digest；startup发现同process内重复/冲突Definition、unsupported schema或non-canonical value时必须fail-closed，不能先启动poller再补报contract。

SDK 不把 Tenant、Worker 或 public version写入 manifest，也不让用户 Workflow读取 bootstrap credential。startup adapter是 SDK/runtime integration，不是用户可调用 Workflow primitive。

本地 SDK unit/testkit 可显式使用 `bootstrap disabled` test profile；平台托管 Pod 检测到 injected bootstrap endpoint 后不得绕过 registration。是否允许 production standalone mode 是待确认策略，不能靠缺少 env 静默降级。

## Control-plane verification

registration 必须在同一原子/可恢复 transaction 中完成以下检查：

1. credential 存在、未过期/吊销、capability/audience正确；
2. bound pending release仍存在且未注册其他 contract；
3. workload token经 Kubernetes TokenReview 验证指定 audience、ServiceAccount与Pod binding；
4. Pod UID、owner Deployment generation、tenant/worker/version labels与 pending record匹配；
5. workload spec image等于 expected immutable digest；
6. observed `containerStatuses.imageID` 与 expected digest满足已批准的 image identity policy；
7. manifest digest等于 server canonicalization结果；
8. contract/schema/template/action/Activity policy/bounds满足006规则；
9. SDK/runtime/contract/projection/node-ID versions 与 capability set受支持；
10. observed Build ID等于 server expected Build ID；
11. compare-and-swap保存 immutable contract、digest、runtime identity、Pod evidence摘要与registration receipt；
12. credential进入consumed状态并触发后续 poller/probe gate。

Worker self-report不能覆盖 server-derived identity。Pod label、Downward API值与body都不是单独信任根；验证结论来自 credential binding、TokenReview与control-plane直接读取的 Pod/deployment状态组合。

### OCI image observation

control plane必须区分publish-time registry media-type validation与runtime Pod observation。前者确认输入不是mutable tag或multi-arch index；后者确认实际候选container运行的platform digest就是pending release的expected digest。只完成其中一个不能promotion。

## Contract immutability 与 probe gate

成功 registration 后：

- canonical manifest bytes、digest、SDK/runtime identity只读保存；
- same receipt exact retry不改变revision；
- 不允许 UI、user API、Worker restart、new Pod 或 description PATCH修改 contract；
- description仍可按 WorkerVersion revision/If-Match独立更新；
- image/runtime/versionConfig/Build ID变化必须创建新 WorkerVersion；旧记录的source兼容字段不提供更新入口；
- probe返回的 digest、protocol、capabilities、Build ID必须与registration record逐项相等；
- mismatch使release失败/隔离，不允许“warning但Ready”；
- failed release不可被重新注册为另一个 contract；用户修复后发布新version。

## Failure、timeout 与 recovery

| 场景 | 行为 | user-facing state |
|---|---|---|
| Worker未注册 | 在deadline内等待/有限轮换 | `awaiting-registration` |
| registration deadline到期 | revoke token、停止promotion、保留安全摘要 | `failed: registration-timeout` |
| token过期/未知/已吊销 | 401/403；不泄露binding是否存在 | pending继续或最终timeout |
| wrong Pod/generation/ServiceAccount | 拒绝、revoke或隔离、security Audit | `failed: workload-identity-mismatch` |
| expected/observed image不匹配 | 不保存contract、不probe、不Ready | `failed: image-mismatch` |
| canonical digest不匹配 | 422；不保存部分contract | `failed: contract-digest-mismatch` |
| policy/bounds/schema不合法 | 422并返回安全category | `failed: contract-invalid` |
| SDK/protocol unsupported | 422 | `failed: protocol-unsupported` |
| exact duplicate | 返回原receipt | 状态不倒退 |
| duplicate不同manifest | 409 + security Audit | 原contract保持不变 |
| registration成功、poller缺失 | contract可读但不Ready | `contract-registered` / poller timeout |
| probe mismatch | revoke promotion、保留evidence | `failed: probe-mismatch` |

controller restart后从 durable pending record、credential reservation hash、Pod observation与registration receipt恢复，不依赖HTTP handler goroutine。registration acceptance同时创建server-owned promotion attempt ID；该attempt在poller、pinned probe、SetCurrent与最终本地状态提交之间保持稳定。probe Workflow ID包含attempt ID；若start response丢失后Temporal返回AlreadyStarted，controller只attach到该相同attempt的既有Run，不创建另一个probe，也不把合法release误判为失败。

promotion阶段与对应Tenant-scoped Audit在同一次store commit中持久化；写失败时二者都不前进。controller对仍为pending的瞬态持久化失败继续调度同一receipt/attempt，process restart也可再次恢复；poller、probe、identity或SetCurrent的确定性失败进入terminal failed阶段。任何retry不得创建第二个WorkerVersion、重置已消费credential或生成新的promotion attempt。

## Credential泄露边界

泄露 token 的攻击者即使成功调用 endpoint，也只能尝试给一个确切 pending release注册一次 contract；仍必须通过 workload attestation、image与Build ID检查。防护层：

- short TTL、single capability、exact binding、hash-at-rest与立即revoke；
- projected file最小权限、read-only root filesystem、禁止log/env dump；
- Pod-bound audience token + TokenReview；
- internal endpoint network exposure最小化、rate limit与可选NetworkPolicy；
- registration first-writer CAS与different-digest alert；
- no user/control-plane credential in response。

共享 platform Kubernetes Namespace中的NetworkPolicy只是额外防护，不是硬隔离。若恶意代码已运行在合法候选 image中，它可能读取自己的bootstrap material并注册与自身一致的恶意 contract；本协议不解决该supply-chain信任问题，也不得宣称解决。

## Audit 与 observability

至少记录以下 Tenant-scoped Audit events：

```text
worker.version.publish.requested
worker.bootstrap.credential.issued
worker.bootstrap.credential.rotated
worker.bootstrap.registration.received
worker.bootstrap.registration.verified
worker.bootstrap.registration.rejected
worker.bootstrap.credential.revoked
worker.version.poller.ready | timeout
worker.version.probe.verified | mismatch
worker.version.promoted | failed
```

Audit包含 Tenant ID/slug、principal（发布动作）或workload principal摘要（registration）、Worker/version、release/operation/receipt ID、image/manifest digest、Pod UID hash、request ID、outcome/error class与时间。不得包含token、workload token、Secret value、完整manifest、versionConfig/input或credential-bearing headers。

实现采用durable promotion phase action承载poller/probe/promotion事件：`waiting-for-poller + ready`、`probing-contract + verified`、`setting-current`、`succeeded`；retry使用当前phase加`retrying`，failure额外记录`failedPhase`。credential issuance、已绑定credential的registration/revoke/reject与相应状态必须和Audit原子提交。unknown token没有可信Tenant归属，只计入去标识化platform metric，禁止信任请求提供的Tenant来伪造Tenant-scoped Audit。Pod UID只能保存SHA-256摘要。

metrics按phase/failure category聚合；高频invalid token、workload mismatch、different-digest duplicate、跨release尝试触发告警。告警不得把秘密写入label。

## Console UX amendment

Publish form移除 manifest file picker、digest input和editable/read-only pre-submit contract preview。用户只填写release与部署输入。提交后：

1. UI收到`202 Accepted`并进入WorkerVersion pending detail；
2. verification timeline分别显示部署、等待Worker注册、contract validation、poller、probe与promotion；
3. registration前，contract区域显示“等待候选 Worker 自注册”，不能上传补救；
4. registration verified后，只读展示server-stored contract与runtime identity；
5. timeout/mismatch显示安全failure category、request ID与建议动作，不暴露token、Pod credential或底层routing；
6. browser refresh从durable operation/release read model恢复状态；不依赖内存operation；
7. UI不允许针对failed/ready release重新上传或覆盖contract；修复动作是发布新WorkerVersion。

## Optional generated JSON artifact

Per [`014-sample-slimming.md`](014-sample-slimming.md)，org维护的三个用户Sample不提供generator，也不check in该artifact。以下能力只描述可选SDK/CI tooling，不构成Sample目录或用户发布路径。

SDK tooling可以选择输出deterministic JSON artifact，但hosted startup、publish API与promotion都不依赖该文件。它只服务于：

- CI golden diff、schema/policy lint与code review；
- CI中复算typed Definition contract/digest；
- SBOM/signature/provenance或OCI artifact/referrer的未来输入；
- 离线合规审计、故障诊断与Sample contract tests；
- SDK backward compatibility与old-history replay fixtures。

没有generated JSON文件不影响正常发布；用户只调用SDK启动入口。MVP不提供user-facing“离线manifest导入”。若未来需要air-gapped/operator import，必须另写admin-only SDD，定义signature、image binding、权限与它是否仍需runtime registration/probe；不能把Console file upload悄悄恢复为安全边界。

## TDD 实施顺序

1. 修改006/011的正式contract与publish request tests，证明user API拒绝manifest/identity字段；
2. pending release/operation的durable state与publish idempotency；
3. bootstrap credential reservation、hash/TTL/binding/revoke/rotation tests；
4. Kubernetes projected material、Pod/generation/image observation与TokenReview adapter tests；
5. strict internal registration endpoint auth/body/size/rate tests；
6. manifest canonicalization、policy/protocol/Build ID admission；
7. first-writer CAS、exact retry、different-payload conflict与controller restart recovery；
8. Org SDK typed Definition startup construction、registration retry/redaction与accepted-before-polling tests；
9. poller + pinned probe + promotion state machine；
10. Console pending/read-only contract UX，移除file picker的contract tests；
11. Hello → parallel-confirmation → dynamic-decision Sample迁移；
12. 真实kind E2E：restart/retry、image mismatch、timeout、双Tenant无串扰与成功promotion。

每个行为必须先出现失败测试，再做最小实现。

## 已批准的安全与运行默认

1. bootstrap opaque token之外，强制使用audience=`org-worker-bootstrap`的Pod-bound projected ServiceAccount token + TokenReview；TokenReview中的`authentication.kubernetes.io/pod-uid`必须唯一且与请求header及live Pod UID相同。Worker ServiceAccount仍无Kubernetes API RBAC。
2. MVP只接受platform-specific immutable image manifest digest；mutable tag、`tag@digest`与multi-arch index digest拒绝。
3. credential TTL默认15分钟；Pod scheduled后的registration deadline默认10分钟；合法replacement Pod最多自动rotation一次。
4. testkit/local sample允许显式disable bootstrap；platform hosted mode检测到bootstrap配置后fail-closed，不允许缺少字段时静默开始polling。
5. contract/image/protocol/workload identity mismatch使WorkerVersion terminal failed；修复必须发布新version。dependency timeout只允许controller在deadline内有限重试。
6. production internal endpoint强制TLS和内部service identity；kind使用开发CA，不允许明文传输bearer credential。
7. successful registration保留token hash对应的exact-retry receipt 5分钟，随后删除credential reservation；durable registration record继续存在但不能用作一般credential。

每次candidate rollout使用密码学随机、服务端生成的deployment generation，并同时写入credential binding与Pod template label。注册时control plane读取live Pod及其owner chain，要求Tenant hash、Worker、version hash、generation标签完全匹配binding，Pod controller owner为live ReplicaSet，且该ReplicaSet的controller owner是binding中的canonical Deployment；相同ServiceAccount的旧Pod或旧generation不能使用泄露的bootstrap token完成注册。
