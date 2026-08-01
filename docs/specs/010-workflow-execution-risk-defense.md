# Workflow 执行过程风险防御

> Terminology: this specification follows the canonical [org glossary](../architecture/glossary.md). Product isolation is a Tenant; infrastructure uses one shared platform Temporal Namespace and one shared platform Kubernetes Namespace. Tenant 不映射为任何底层资源边界。

## 状态

**Draft — awaiting explicit user approval. No implementation authorization.**

本文件只定义当前架构下的威胁模型、信任边界、风险接受标准、分层防御责任与验收。它不授权修改 Org SDK、control plane、Temporal/Kubernetes adapter、Samples、E2E、UI 或 HTTP handler，也不授权 commit/push。

本文扩展已批准的 `003-multi-tenant-shared-infrastructure.md`、`004-worker-identity-and-description.md` 与 `006-org-sdk.md`。现有 SDK 与三个 Samples 的实施顺序不因本 Draft 改变。本文获批后，新增防御仍须按独立纵向切片执行 SDD/TDD；不得把 Draft 条目当作已经存在的安全能力。

## 安全目标与明确非目标

安全目标：

1. Tenant、Worker、WorkerVersion、Workflow Run 与 action 的所有访问都由认证主体派生 Tenant，并保持不可碰撞的服务端 identity。
2. 用户提交的 OCI image、manifest、projection 与 action payload 均按不可信输入处理。
3. Workflow replay、Activity retry、Worker restart 与网络不确定性不能被错误表述为外部效果 exactly once。
4. 动态 DAG 的节点数、fan-out、等待时间、projection 大小与 workload 资源消耗有显式上限。
5. 平台能阻断已知不安全发布、记录关键安全事件、隔离或停止受影响 workload，并保留 reconciliation 所需证据。
6. Secret、业务 input/output、Temporal History、日志、telemetry 与 audit 之间有明确的数据最小化边界。

非目标：

- 不把共享 platform Temporal Namespace 或共享 platform Kubernetes Namespace 误称为硬多租户隔离。
- 不保证恶意代码在同一 Kubernetes security boundary 内绝无逃逸；底层 runtime/kernel/containerd 风险仍存在。
- 不从 Temporal Event History 推断业务 DAG 或证明 Worker 返回的业务事实真实。
- 不由 org 自动理解任意外部系统的幂等、补偿或 reconciliation 语义。
- 不承诺 Signal transport success 等于 action 已被 Workflow 接受。
- 不承诺 SDK hook 能约束已能任意执行进程代码的恶意 Worker；hook 是受控 authoring/runtime contract，不是进程内强安全边界。

## 威胁主体与资产

### 威胁主体

- 因错误或依赖漏洞而行为异常的正常 Worker；
- 有意绕过 Org SDK、伪造 manifest/projection、探测其他 Task Queue 或消耗共享资源的恶意 Worker image；
- 伪造 Tenant、Run、runtime node、action 或 operation ID 的终端用户；
- 被盗的用户 session、CI credential、OCI registry credential、Temporal credential 或 Kubernetes credential；
- compromise 的第三方依赖、base image、registry、CI 或 cluster node；
- 平台 operator 的误操作，以及因日志、audit、History 或 telemetry 默认采集造成的非恶意数据泄露。

### 受保护资产

- Tenant-scoped Worker/WorkerVersion/Run/action/audit 数据；
- OCI image digest、source provenance、SDK manifest digest 与 contract probe 结果；
- Workflow input/output、Activity input/result、Signal payload 与 Temporal Event History；
- platform Temporal credential、Task Queue、Worker Deployment、Workflow ID 与 platform Kubernetes credential；
- Secret、外部系统 credential、idempotency key 与 reconciliation evidence；
- 平台容量、Tenant quota、公平性、可用性和事故审计证据。

## 信任边界

```text
browser / API client (untrusted input)
        |
        v
org authentication + Gateway + control plane
        |                 |
        |                 +--> org store / audit / quota ledger
        |
        +--> platform Kubernetes control plane
        |       |
        |       v
        |   user Worker Pod (untrusted workload)
        |       |
        |       +--> external systems (outside org transaction boundary)
        |       |
        |       +--> shared platform Temporal Namespace
        |
        +--> shared platform Temporal Namespace
```

边界规则：

- org Gateway/control plane 是 Tenant authorization、identity derivation、发布 admission、action forwarding 与 audit 的安全边界。
- 用户 Worker Pod 与其 projection 都是不可信来源。control plane 只能验证其协议、identity、schema、状态转换约束与 bounds，不能据此证明业务结果诚实。
- Org SDK 是 production authoring contract 和降低误用的机制；对恶意 image，不能仅靠同一进程内 SDK 阻止 raw Temporal client、网络调用或 telemetry bypass。
- Temporal 持久化 durable execution state，但不替 org 执行 Tenant authorization；Task Queue 名称不是 authorization mechanism。
- Kubernetes Pod security、ServiceAccount、NetworkPolicy、resource controls 是纵深防护；共享 platform Kubernetes Namespace 本身不是 Tenant security boundary。
- 外部系统成功与 Temporal Activity completion 之间不存在共同 transaction。ambiguous outcome 必须由幂等或 reconciliation/compensation 处理。

## 风险分级与处置词汇

| 处置 | 含义 |
|---|---|
| **必须阻断** | admission、authorization 或 runtime guard 必须拒绝；不能只记录 warning 后继续 |
| **必须告警** | 允许当前操作继续或进入安全失败态，但必须产生 tenant-qualified audit/metric/alert，供 operator 处理 |
| **暂缓支持** | 当前架构无法给出可接受保证；产品必须明确不提供，不能以隐藏开关或文档免责声明假装支持 |
| **可接受残余风险** | 已有明确边界、最小权限、监控与恢复方案，剩余概率/影响在目标环境内可接受 |

### 不可接受风险

- 从请求 body/query 的 `tenantId` 选择 Tenant，或允许跨 Tenant IDOR；
- 将一份可访问共享 platform Temporal Namespace 全部 Task Queue/Workflow 的通用 credential 交给任意不可信多租户 Worker，并宣称 Tenant 隔离成立；
- 运行 privileged、hostNetwork、hostPID、hostPath、可写 root filesystem 或带 Kubernetes API 权限的默认 Worker Pod；
- 接受 mutable image tag、manifest digest 与 image/SDK runtime 无绑定，或 contract probe mismatch 后仍设为 Current；
- write Activity 无稳定 idempotency key 且无 reconciliation/compensation policy，却允许自动 retry；
- 无上限 fan-out、runtime node、projection size、Run concurrency 或 idle wait；
- 把 `delivered` / `delivery-unknown` 显示成 action 已接受；
- 把 Secret 明文放进 audit、普通日志或 UI diagnostic response；
- 仅依赖 Worker 自报 projection/hook telemetry 执行安全 authorization 或费用结算。

### 可接受残余风险

- 在本地 development、单一受信 operator、无真实 Secret/生产数据的 kind 环境中，共享且弱认证的 Temporal 可用于验收；该配置不得被描述为 production multi-tenant security。
- 审核过的 Worker 仍可能因 bug 产生业务错误；平台验证 contract 与执行边界，不验证领域结果正确性。
- Kubernetes container isolation 不能消除 kernel/runtime zero-day；通过 hardened Pod、node patching、runtime monitoring 与 kill switch 降低风险。
- 采用幂等或 reconciliation 后，外部写仍可能暂时处于 ambiguous state；允许以明确的 `reconciling` / `manual-review` 状态暴露，不允许虚报成功。

## 分层防御矩阵

下表的 MVP 结论以 production-capable control plane 为目标；local kind 验收可使用显式标记的 development exception。

| 风险 | Org SDK 责任 | org control plane 责任 | Kubernetes / Temporal 责任 | 用户 Worker 责任 | MVP 处置 |
|---|---|---|---|---|---|
| image 供应链与 contract bypass | 生成 canonical manifest、协议/SDK version、stable digest；production API 不暴露 raw runtime escape hatch | 只收 immutable digest；校验 registry/source provenance、manifest digest、probe identity/build ID；保存 admission evidence | admission 按 digest 拉取；可接入 signature/SBOM/provenance verifier | 固定依赖、构建 attestations、修复漏洞，不手改 generated manifest | mutable tag、digest/probe mismatch **阻断**；缺 signature/SBOM 的一般 production image 在策略启用前 **暂缓或限 trusted allowlist** |
| 不可信 Worker 使用共享 Temporal credential/Task Queue 越权 | 不暴露 raw client；canonical routing 由注入 config 使用 | 生成 tenant-qualified routing；不得下发终端用户 credential；建立 workload identity/connection broker 方案 | credential 必须 workload-scoped，或由授权 proxy 限定 Worker Deployment/Task Queue/Workflow；Task Queue name 本身不算 ACL | 不尝试访问其他 routing identity | 任意不可信 image + 覆盖整个 shared platform Temporal Namespace 的 credential **暂缓支持**；MVP production 只允许 trusted/admin-reviewed image，违规探测 **告警并 kill** |
| Tenant/Run/action IDOR | action envelope 固定 operation/node/action identity并在 Workflow 内去重 | Tenant 只从认证主体派生；所有 lookup/store/audit 加 Tenant key；检查 Run 属主、manifest action、permission、schema、projection revision | Temporal/Kubernetes resource name由服务端 canonical derivation；用户不能提交底层 name | 不从业务 input 覆盖内部 identity | 任一 mismatch、forged `tenantId`、跨 Tenant lookup **阻断并审计** |
| forged projection/action | projection 由 deterministic runtime state生成；stable node ID、合法 transition、action outcome | projection 按 stored manifest、Run/WorkerVersion identity、bounds、node template、schema、dependency/cycle规则验证；action 同时验证 auth 与当前 allowed action | Temporal Query/Signal 只经平台 client；限制直连 credential | 不手写/伪造 projection，不把 action transport 当业务授权 | invalid projection/action **阻断**；连续 mismatch **告警、隔离 WorkerVersion**；业务真实性仍属用户责任 |
| Activity retry 与外部成功后 completion 丢失 | stable Activity ID/idempotency key、attempt context、retry hook；write policy必须声明幂等或 reconciliation/compensation | admission 检查 policy 完整性；投影 ambiguous/reconciling 状态；保存 operation evidence，不声称 exactly once | Temporal 提供 durable retry/timeout，但不提供外部 transaction | downstream 必须实际使用 idempotency key；实现 reconcile/compensate 并安全处理重复请求 | 无安全 policy 的 retryable write **阻断**；ambiguous outcome **告警并进入 reconciliation/manual review** |
| 动态 DAG 无限 fan-out/循环/资源耗尽 | append-only stable node identity；max fan-out/runtime nodes/projection bytes；dependency cycle/duplicate拒绝 | manifest admission 限制 bounds；Tenant run/deploy/quota ledger；projection 超限拒绝并隔离 | Pod requests/limits、cluster capacity、Temporal visibility/History limits与 rate limit | fan-out item stable key、有业务上限，避免无界 payload/history | 无 bounds 或超限 **阻断当前 Run/发布**；反复超限 **告警/暂停 WorkerVersion** |
| Workflow/Activity timeout、取消与僵尸等待 | Activity 显式 timeout/retry；WaitForAction 使用 durable Timer/Signal；取消传播；terminal projection | 启动策略限制最大 Run/idle deadline；暴露 cancel/terminate 差异；reconciler 扫描 stale wait/action | Temporal durable Timer/cancellation；Kubernetes liveness/readiness不等于业务完成 | Activity 响应 cancellation；外部写取消后 reconcile；等待声明 expiry行为 | 无 timeout 的 Activity/人工等待 **阻断**；stale Run **告警**；强制 terminate 仅 operator kill switch 且记录风险 |
| action delivery-unknown、重复或过期 | operation ID 去重；accepted/rejected/duplicate/expired outcome持久化到 projection | 先 reserve audit record，再 Signal；transport error 标记 `delivery-unknown`；同 operation安全重投/查询 outcome；权限与 schema始终重验 | Temporal durable Signal；网络错误仍可能发生在服务端已接收后 | action handler按 operation ID幂等；过期/stale action不得改变业务状态 | 不确定结果不得显示 accepted；长期 unknown **告警并 reconcile**；不同 payload复用 operation ID **阻断** |
| Secret/input/history/log/audit 泄露 | 默认 telemetry 不记录 payload；schema 可标 sensitive 字段；hook 只传 identity/digest | input/output大小限制、字段 redaction、audit仅存 digest/引用；diagnostic权限；不向 UI/终端返回底层 credential | Temporal payload encryption/codecs 与 retention；Kubernetes Secret最小挂载；日志访问控制 | Secret 使用 reference/短期 credential，在 Activity 内获取；禁止写日志或返回 result | 明文 platform credential/Secret 进入 audit/log **阻断或立即 redaction+告警**；生产 Secret value直接进入 History 在 Secret broker/codec前 **暂缓支持** |
| Kubernetes privilege、egress、quota、Pod escape | 无权改变 Pod security spec | 只生成安全模板；校验 resources、image digest、tenant label、ServiceAccount；Tenant quota lease；kill/scale-to-zero | restricted Pod Security：non-root、drop capabilities、read-only rootfs、seccomp、无 privileged/hostNetwork/hostPID/hostIPC/hostPath；default-deny NetworkPolicy按需开 egress；node/runtime patch | 声明最小资源与目标 egress，不依赖 Kubernetes API | privileged/无 limits/默认 K8s API token **阻断**；NetworkPolicy 在 shared platform Kubernetes Namespace 仅是纵深防护；高风险外部 egress **暂缓/allowlist** |
| SDK hooks/telemetry 不可用或被旁路 | hooks must-fail-safe：projection state不能依赖异步 exporter成功；协议带 SDK/runtime version | probe验证保留 query、manifest digest、build ID；缺 telemetry不自动当业务失败；检测长时间无心跳/contract drift | Temporal execution与Kubernetes health提供独立观测面 | exporter失败不得改变 deterministic Workflow结果；不得绕过 wrapper | contract/projection hook缺失 **阻断发布/查询**；optional telemetry缺失 **告警**；不能把 hook 当恶意进程隔离边界 |
| 审计、告警、kill switch、事故恢复 | 投影保留最近状态事件/action outcome，不存安全审计真相 | append-only tenant audit；异常规则；暂停 Tenant/WorkerVersion、撤 Current、拒新 Run/action、scale zero、credential revoke、cancel/terminate/reconcile runbook | Temporal visibility/backup/retention；Kubernetes events/runtime alerts；credential rotation | 提供 reconcile/compensation runbook与业务 owner | audit写入失败时高风险 mutation **fail closed**；kill switch与恢复演练 **MVP 必须验收** |

## 关键防御设计

### 1. OCI image、manifest 与 SDK runtime 绑定

发布对象必须形成不可变证据链：

```text
OCI image digest
  + source repository / branch / commit / CI reference
  + generated manifest bytes and digest
  + Org SDK module/runtime protocol version
  + Temporal Build ID / WorkerVersion
  + contract-probe observed identity
  -> WorkerVersion admission record
```

- mutable tag 只能作为构建日志信息，不能成为部署输入。
- manifest 由同一 typed Definition 生成并随 image 发布；UI 只读展示。用户提交的 runtime config 不得覆盖 node/action/projection contract。
- probe 必须显式选择待发布 WorkerVersion，并校验 workflow name、build ID、manifest digest、SDK/runtime protocol。probe timeout、mismatch 或 reserved query缺失均不得设为 Current。
- 单独 probe 无法证明 image 没有恶意路径。production 需要 registry allowlist + digest、CI provenance，以及可配置的 signature/SBOM/vulnerability policy。
- 在 supply-chain verifier 落地前，production MVP 只能接收 platform-admin 允许的 trusted image；“任意用户 image 安全运行”属于暂缓能力。

### 2. shared platform Temporal Namespace 的硬限制

tenant-qualified Task Queue、Worker Deployment 与 Workflow ID 解决命名碰撞，不解决 credential authorization。若 Worker Pod 能用同一 Temporal credential任意调用 shared platform Temporal Namespace，它可能：

- poll 其他 Tenant 的 Task Queue；
- Signal、Query、Cancel 或枚举其他 Workflow；
- 启动资源消耗型 Workflow；
- 读取可见性数据或间接获得 payload。

因此 production 接受任意不可信 Worker 的前置条件至少满足一个：

1. Temporal 提供并经实测验证的 workload-scoped authorization，使 credential 只能访问 tenant+worker 派生资源；或
2. Worker 只连接 org-managed Temporal proxy/broker，proxy 从 workload identity派生允许的 Task Queue、Worker Deployment、Workflow operation，并拒绝任意 API；或
3. 改用更强底层隔离架构，需另写 SDD。

在此前，local kind 与 trusted-image MVP 可以继续开发，但文档/UI 不得宣传为 adversarial multi-tenant isolation。Terminal users 永远不获得 Temporal credential。

### 3. Projection 可信度与 authorization

projection 是产品运行状态的唯一 DAG 展示来源，但不是独立 authorization authority：

- control plane 先用认证主体、stored Run ownership、stored WorkerVersion manifest 与 permission授权；
- 再验证 projection 的 contract version、workflow/version identity、revision、node template、stable runtime ID、dependency、状态、action schema与 bounds；
- 只有 stored manifest声明且当前 projection允许的 action可以转发；
- action outcome 由 SDK projection确认，transport response只表示 delivery state；
- invalid projection不得降级到从 Event History猜 DAG。产品返回 contract error，记录审计，并按阈值隔离 WorkerVersion。

恶意 Worker仍可在允许的 schema内伪造业务状态。org 只证明“该 image按已验证协议声称此状态”，不证明外部世界确实如此。需要外部真实性时，由 reconciliation Activity或独立系统证据验证。

### 4. 外部副作用与 ambiguous outcome

write Activity 的状态机必须容纳：

```text
not-started -> executing -> external-success? -> Temporal-completed
                                  |
                                  +-> crash/timeout/connection-loss
                                       -> ambiguous -> reconcile
                                       -> confirmed-success | retry-safe | compensate | manual-review
```

- stable idempotency key必须由 Run identity + stable Activity identity或稳定业务 key确定性派生，并实际传播给 downstream。
- 仅声明 key但 downstream不支持去重，不构成防御。
- reconciliation应先查外部事实，再决定“已成功”“可重试”“需补偿”或“人工处置”。
- cancellation不能撤销已经发生的外部写；cancel/terminate后的 Run detail仍须暴露待 reconciliation effect。
- crash-after-external-success-before-completion测试必须证明无重复外部效果；测试结论只能是依赖 downstream idempotency/reconciliation，不是 exactly once。

### 5. 动态 DAG 与资源上限

每个 manifest必须声明 platform policy允许范围内的：

- `maxInstancesPerFanOut`；
- `maxRuntimeNodes`；
- `maxProjectionBytes`；
- Activity timeout/retry attempts；
- Workflow/idle wait最大期限；
- 单 Run input/output/action payload上限。

SDK runtime创建节点前执行本地 guard；control plane对 manifest和每次 projection再次验证；Tenant quota ledger限制并发 Run/Deployment/Pod资源。动态 dependency必须引用已存在稳定节点，拒绝 self-edge、cycle、重复 stable key和已终态节点的非法回退。

超限是确定性业务失败或平台保护失败，不能静默截断 projection后继续执行。若 projection已经超过 transport上限，control plane返回明确 contract violation并触发暂停/隔离阈值。

### 6. timeout、cancel、idle action 与 zombie Run

- Activity 必须显式声明 schedule/start-to-close timeout与有限 retry policy。
- `WaitForAction` / `AwaitConfirmation` 必须在 Workflow内使用 durable Signal + Timer；普通 Activity不得作为 Signal receiver阻塞。
- 每个 wait node声明 deadline/expiry outcome：timeout failure、default branch、cancel 或 escalation。无限人工等待在 MVP 阻断。
- Gateway记录 `reserved -> delivered | delivery-unknown -> accepted | rejected/expired`；相同 operation ID + 相同 payload可安全重试，不同 payload冲突。
- `cancel` 是 cooperative；`terminate` 是 operator事故手段，可能留下外部效果。两者必须在 audit和 Run state中区分。
- reconciler扫描超过 SLA 的 running/waiting/action-unknown/compensating Run，并产生 tenant-qualified alert。

### 7. 敏感数据边界

数据分类至少区分：public contract、tenant business data、sensitive business data、Secret/credential。默认规则：

- manifest、description、node label和error message不得包含 Secret；
- audit记录 identity、permission、operation ID、schema outcome、payload digest与状态，不记录 action/input原文；
- SDK hook/metric只带 Tenant/WorkerVersion/Run/node/attempt等受控标签，不复制 input/result；高基数字段受限；
- Worker普通日志禁止打印 Workflow/Activity/Signal完整 payload；错误需可 redaction；
- UI/API diagnostic默认不返回 Temporal History、Kubernetes Secret、Pod env或platform credential；
- 生产 Secret优先以 reference传入，由 Activity在运行时通过最小权限 broker获取；Secret value不应进入 Workflow input/result/Signal，从而避免持久化进 History。

如确需持久化 sensitive business payload，必须另行定义 payload encryption/key rotation/retention/export/redaction SDD。未完成前，不支持把 platform credential或高敏 Secret作为业务 input。

### 8. Kubernetes runtime hardening

Worker Pod模板的不可覆盖安全默认：

- image by digest；`runAsNonRoot`、固定非零 UID、`allowPrivilegeEscalation: false`；
- drop all Linux capabilities、`seccompProfile: RuntimeDefault`、read-only root filesystem；
- 禁止 privileged、hostNetwork、hostPID、hostIPC、hostPath与额外 device；
- 每 Pod CPU/memory requests/limits；Tenant quota ledger/lease限制资源与并发；
- dedicated Worker ServiceAccount，默认不自动挂载 token、无 Kubernetes API RBAC；
- NetworkPolicy作为可选纵深防护，默认拒绝非必要 ingress；egress按 Temporal endpoint、DNS和声明外部目标逐步收紧；
- tenant label、worker/version label与owner reference完整，但 label不是安全 quota boundary；Kubernetes ResourceQuota不能原生按 tenant label提供配额。

共享 platform Kubernetes Namespace仍不是硬隔离。对不可信互联网代码、强合规数据或高影响外部 credential，应暂缓到 dedicated sandbox/node/runtime class或更强隔离规格。

### 9. Audit、告警、kill switch 与恢复

必须审计：publish/probe/current变更、description revision、Run start/cancel/terminate、action reserve/delivery/outcome、projection violation、quota拒绝、credential rotation、kill switch与恢复。audit至少含 Tenant ID、principal、request/operation ID、resource identity、outcome、reason、timestamp和不可逆 payload digest。

最低告警：

- probe/manifest反复 mismatch；
- Worker尝试非派生 Task Queue/Workflow操作；
- projection identity/transition/bounds违规；
- action delivery-unknown超过 SLA；
- Run/idle/reconciliation超时；
- quota持续拒绝或资源异常；
- Pod security/runtime异常、restart storm、unexpected egress；
- audit sink、telemetry或reconciler不可用。

kill switch层级：

1. Tenant suspended：拒绝新发布、Run和action；
2. WorkerVersion quarantined / Current撤回：拒绝新 Run，保留历史证据；
3. Worker deployment scale-to-zero与credential revoke；
4. cooperative cancel；
5. operator terminate，仅在风险大于未完成外部效果时使用。

恢复前必须重新验证 image/manifest/probe、rotate credential、确认 quota/egress、reconcile ambiguous external effects，并记录 operator批准。备份/恢复不能只恢复 org store；还要校验 Temporal execution state、WorkerVersion可用 image与外部系统事实。

## MVP 策略清单

### 必须阻断

- Tenant/Run/action IDOR、forged tenant identity、用户提交底层 routing name；
- mutable image部署、unsupported contract/SDK major、manifest/probe/build ID mismatch；
- privileged Pod、Kubernetes API token/RBAC、缺 resources、platform credential泄露；
- write Activity retry policy不满足 idempotency或reconciliation/compensation；
- 无 bounds 的动态 DAG、无 timeout 的 Activity/idle wait、cycle/duplicate node identity；
- action permission/schema/stale projection失败、operation ID payload冲突；
- invalid projection identity、undeclared template/action、超限 projection；
- 高风险 mutation无法写 audit；
- 将 delivery-unknown/transport success误报为 Workflow accepted。

### 必须告警

- contract/projection重复违规、probe failure storm、Worker restart storm；
- action delivery-unknown、Run/idle/reconciliation超过 SLA；
- Tenant quota持续耗尽、异常 fan-out或projection增长；
- SDK optional telemetry缺失、audit/reconciler/alert pipeline健康异常；
- unexpected egress、credential使用异常、Pod runtime security event；
- cancellation/termination后仍有 ambiguous external effect。

### 暂缓支持

- 在缺少 workload-scoped Temporal authorization/proxy时运行任意不可信多租户 image；
- 将共享 platform Kubernetes Namespace描述为强安全/合规隔离；
- 将 platform credential或高敏 Secret value直接作为 Workflow/Signal payload；
- 无 signature/provenance policy却允许任意公网 image进入production；
- 无边界 fan-out、无限人工等待、用户自定义raw Signal/projection或raw Temporal escape hatch；
- 自动保证任意外部系统的 exactly-once、补偿或事务一致性。

## 验收计划

本文获批并分阶段实现后，至少需要以下测试；每项仍遵循先 red 后 green：

### Admission 与供应链

1. tag、digest格式、registry/source policy、manifest digest、SDK/runtime version、probe build ID任一 mismatch均拒绝设为 Current。
2. probe必须命中显式历史 WorkerVersion；其他版本伪造响应不能通过。
3. 生成 manifest与image attestation不一致时拒绝，并写 tenant audit。

### Tenant、routing 与 action

4. 两 Tenant使用同 Worker name/version/run/action ID无 store、Task Queue、Worker Deployment、Workflow ID、Kubernetes name串扰。
5. forged `tenantId`、cross-Tenant Run/node/action ID、未授权 permission和stale projection全部在发送 Signal前拒绝。
6. 相同 operation ID/相同 payload重试只形成一个逻辑 action；不同 payload冲突；transport丢失展示 `delivery-unknown`，reconcile后才变 accepted/rejected。
7. malicious projection的错误 identity、undeclared node/action、cycle、非法状态回退、超 bounds均拒绝并触发隔离阈值。

### Activity 外部效果

8. Worker在外部成功后、Temporal completion前 crash；retry使用相同stable idempotency key，外部系统只产生一个效果。
9. downstream不支持幂等时先 reconciliation再决定retry；无法确认时进入manual-review，不虚报completed。
10. cancel/terminate后保留pending reconciliation evidence和审计。

### 动态 DAG 与可用性

11. if/else skipped、bounded fan-out/join正常；超fan-out、节点数、projection bytes、payload size确定性失败。
12. Worker在waiting-for-user期间重启，idle node不占Activity worker，恢复后action继续。
13. Activity timeout/retry exhausted、wait expiry、Workflow cancel与operator terminate分别产生可区分projection/audit。
14. restart storm、zombie wait与action-unknown超过SLA触发告警。

### 数据与runtime安全

15. audit/log/telemetry fixture包含Secret时验证redaction，普通Run detail不能读取History、Pod env或credential。
16. Worker Pod admission拒绝privileged/host access/root/writeable rootfs/token mount/缺limits；默认ServiceAccount不能调用Kubernetes API。
17. NetworkPolicy允许声明的Temporal/DNS/fixture endpoint并阻断测试中的未声明egress；明确此测试不证明hard Tenant isolation。
18. Tenant quota lease/reconciler在并发、crash与cleanup后不超配且可回收。

### 事故演练

19. quarantine WorkerVersion、撤Current、拒新Run、scale-to-zero、credential rotate、恢复probe全程有audit。
20. org store恢复后校验Temporal execution、pinned旧WorkerVersion image与ambiguous effect；缺任一项不得宣告恢复完成。

真实本地 E2E继续使用 Temporal `127.0.0.1:7233` 与 Kubernetes context `kind-org`，只验证development路径。涉及adversarial credential isolation、Pod escape、production NetworkPolicy和Secret broker的测试必须在独立安全环境运行，不能用kind通过就宣称production安全。

## 可观测性与产品表达

用户可见 Run state至少区分：running、waiting-for-user、reconciling、manual-review、completed、failed、timed-out、canceled，以及action的delivered、delivery-unknown、accepted、rejected/expired。block reason来自SDK projection或control-plane protection，不从Event History猜测。

Temporal Web与Kubernetes diagnostics只对受限operator提供deep link。终端用户不获得platform credential、raw Signal、Task Queue或底层resource操作入口。所有安全状态必须说明证据来源：SDK projection、Temporal execution、Kubernetes health、org audit或external reconciliation，不能合并成一个未经标注的“healthy”。

## 待用户确认的关键取舍

1. **不可信 image门槛（推荐）**：在workload-scoped Temporal authorization或org-managed proxy完成前，production只允许platform-admin审核/allowlist的trusted image；任意用户image仅限隔离development环境。
2. **供应链基线（推荐）**：MVP强制digest + source provenance + contract probe；production policy再强制signature和SBOM。若用户希望首版production就接收第三方image，应把signature/SBOM verifier提升为MVP阻断项。
3. **Secret策略（推荐）**：MVP禁止platform credential与高敏Secret value进入Workflow input/Signal/History，只允许Secret reference；Secret broker与payload encryption另立规格。
4. **egress策略（推荐）**：先默认拒绝非必要ingress和Kubernetes API；对egress采用声明式allowlist逐步收紧。若默认deny-all egress，需同时设计外部Activity endpoint声明/DNS/代理契约。
5. **kill switch（推荐）**：默认先quarantine/scale-to-zero/revoke credential，再按业务owner runbook决定cancel或terminate；平台不自动terminate含write Activity的Run。
6. **无限等待（推荐）**：MVP禁止无deadline人工action；允许最长等待由platform policy设上限，并要求明确expiry outcome。
