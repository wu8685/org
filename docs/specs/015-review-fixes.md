# Evidence-based review fixes amendment

> Terminology follows the canonical [org glossary](../architecture/glossary.md). Product identity remains Tenant → Worker → WorkerVersion → Workflow Run. Infrastructure continues to use one shared platform Temporal Namespace and one shared platform Kubernetes Namespace.

## 状态与边界

**Approved — the user authorized evidence-based review repairs on 2026-08-01.**

本规格是001、003、005、006、011与012的repair amendment，只修复已批准行为的实现缺口。每项在改production code前必须先有可重复失败的test；review文字本身不算证据。不得借本规格实现尚未批准的[`010-workflow-execution-risk-defense.md`](010-workflow-execution-risk-defense.md)策略。

G及相关docs已作为独立stage由commit `04f0123`完成：durable publish idempotency、CSRF文档、required Sample input与无Git metadata命令。其余finding按下列依赖分阶段交付，每个stage全量验证后独立commit/push。

## 已核验缺口

| ID | 当前证据 | 必须守住的不变量 |
|---|---|---|
| A | TokenReview只验证audience与ServiceAccount username；未比较token Pod UID claims。Pod lookup未验证Tenant/version labels、rollout generation或owner Deployment | credential只能由binding对应的确切candidate Pod generation注册；同ServiceAccount旧Pod不得复用 |
| B | promotion在持有control-plane mutex前调用Temporal SetCurrent；default Start记录本地Current但发送unversioned start | Run记录的selectedVersion必须等于Temporal实际选择的WorkerVersion |
| C | accepted registration依次保存WorkerVersion、credential receipt、Audit；后两步失败会留下不可exact-retry的部分状态 | contract、receipt与accepted Audit一次durable commit或全部不变 |
| D | registration handler用untracked goroutine promotion并丢弃error；startup无pending promotion恢复 | accepted release的promotion是durable resumable job；restart不能永久pending或泄露lease |
| E | probe使用固定Workflow ID + reject duplicate；并发/retry promotion可把AlreadyStarted当release失败 | 每个promotion attempt只能有一个有效probe outcome；exact retry/concurrency/restart不误伤合法release |
| F | Start先调用Temporal，再保存Invocation | 外部Workflow存在前必须有durable start reservation；crash/failure后可发现并reconcile |
| H | 每个WaitForAction并发消费同一Signal channel；不属于自身node的envelope被`continue`丢弃 | action envelope最多被路由一次，且永不因另一个waiting node先收到而丢失 |
| I | Start/Signal/Query只检查声明名称；未按contract schema验证input | invalid input在quota reservation或executor调用前拒绝并审计 |
| J | run lease主要在Run detail读取terminal状态时释放；promotion/failure release依赖best-effort defer | terminal/failure transition与lease release必须durable、可reconcile且无需用户读取详情 |
| K | FileStore先mutate live map再persist；persist失败后process内state已变化 | 任一fsync/rename/persist failure后in-memory和on-disk state都保持旧快照 |
| L | bootstrap只有accepted registration Audit；issuance/revocation/rejection/poller/probe/promotion transition缺记录 | 所有安全相关transition写Tenant-scoped Audit，不含token/secret/完整contract |
| M | parallel required input已修；parallel/dynamic README仍缺建议resources | 每个Sample给出可运行的最小resources，同时声明production按实际负载调整 |

## Repair stage 1：transactional persistence（K → C）

先建立FileStore copy-on-write mutation primitive：在lock内clone完整`fileState`，只修改candidate snapshot，先将candidate安全写盘并rename，成功后才替换live state。所有FileStore mutation（Tenant、Worker、Version、Invocation、Audit、quota/action/publish/bootstrap records）必须经过该primitive，禁止先改live map再persist。

测试通过injectable persistence seam稳定模拟write/fsync/rename failure；不得用依赖操作系统权限偶然行为的测试。每类代表性mutation至少证明：返回error、reader仍看到旧值、重新打开文件仍是旧值；quota acquire/release/reconcile必须覆盖。

随后增加Store级atomic bootstrap acceptance：一个调用同时CAS pending WorkerVersion identity、写accepted contract、credential receipt/retry window与accepted Audit。MemoryStore在一个lock内完成；FileStore在一个copy-on-write commit内完成。任何fault使三者都不变化；exact retry读取同一receipt，different contract conflict。

## Repair stage 2：candidate workload identity（A）

bootstrap projected ServiceAccount token的TokenReview必须返回并匹配`authentication.kubernetes.io/pod-uid` claim；缺失、多个冲突值或与header/live Pod UID不一致全部拒绝。不得仅相信Worker提交的Pod UID header。

每次candidate rollout生成不可预测、server-owned generation ID，写入bootstrap credential binding及Pod template label。Resolver必须验证：

- exact Pod UID、ServiceAccount与token claim；
- `org.wu8685.dev/tenant-hash`、worker、version-hash、generation labels与binding逐项相同；
- Pod owner ReplicaSet存在，ReplicaSet owner Deployment等于binding的canonical Deployment；
- declared image与runtime image linkage仍满足012。

stale same-ServiceAccount Pod、旧generation Pod、伪造label、错误owner、缺claim均先以resolver/verifier red tests证明当前会通过，再实现拒绝。真实kind E2E读取真实TokenReview extras与owner chain。

## Repair stage 3：durable promotion controller（D、E、L的一部分）

registration accepted只enqueue/标记durable promotion state并立即返回，Worker随后开始polling。control plane启动时扫描accepted/pending releases恢复promotion；同一WorkerVersion进程内singleflight，跨restart依靠durableattempt state与CAS。

probe Workflow ID必须包含server-owned promotion attempt ID，或安全attach到同attempt的existing execution；不得把AlreadyStarted直接等价为invalid release。poller/probe/SetCurrent失败写durable phase/failure与Audit，controller按有界retry/reconcile规则推进。HTTP goroutine不再是唯一owner，error不得丢弃。

fault-injection覆盖accepted后crash、poller前restart、probe response丢失、同receipt并发promotion、SetCurrent成功后local persist失败。真实Temporal E2E至少证明exact retry与restart最终得到唯一Ready/Current结果。

## Repair stage 4：Current/start consistency与quota（B、F、J）

promotion与Start必须通过同一Worker级serialized transition协调。default start解析Current后，直到Temporal接受start前该Current不可被promotion切换；显式历史版本继续使用Pinned override。若Temporal API无法证明unversioned start实际选择结果，则control plane必须使用与resolved Current等价的server-selected override，并在文档中说明这是race-free Current snapshot，不改变用户“未指定即Current”语义。

MVP采用后一方案：default start仍由服务端解析调用时的Current，但发送Temporal请求时显式使用该resolved WorkerVersion的Pinned override。promotion的SetCurrent与default start从解析Current到Temporal接受start共用同一serialized section；因此Run记录、实际执行版本与用户看到的“调用时Current”一致。该内部override不向用户暴露Temporal概念，也不改变显式历史版本选择语义。

Start先durable保存`starting` reservation（deterministic Workflow ID、selected version、input digest、actor、quota lease），再调用Temporal，成功后保存Run ID/`running`；失败或crash由reconcilerdescribe/start conflict恢复，不创建第二个Workflow。Invocation persistence failure不得产生不可见execution。

`starting` Invocation与run quota lease必须在同一次store commit中创建。reconciler只在与foreground Start相同的serialized section内重新读取后恢复仍为`starting`的记录；Temporal AlreadyStarted只attach到相同deterministic Workflow ID返回的既有Run。FileStore使用仅供durable snapshot的routing sidecar保存Task Queue、Worker Deployment与Temporal Workflow/Run identity；domain的public JSON继续隐藏这些字段。

release/run terminal transition与quota lease release组成可重试durable state；后台reconciler处理process crash与persist failure，不依赖GetInvocation。fault tests覆盖SetCurrent/Start/SaveInvocation/ReleaseLease各crash window及并发default start/promotion。

Run terminal state与run lease删除同一次commit；Cancel同样提交`canceled`与lease删除。WorkerVersion保存durable deployment-active标记，后台reconciler从非failed releases、active deployments及starting/running Runs重建Tenant quota active set，清理failed release或已结束operation遗留的lease。Current切换将Worker.currentVersion、候选/旧版本Current标记和可选promotion Audit一次提交。

## Repair stage 5：SDK action routing与input schemas（H、I）

WorkflowContext维护deterministic action dispatcher/inbox。Reserved Signal只被一个dispatcher消费，再按stable runtime node ID路由；node尚未开始等待时可有界暂存。duplicate operation仍由operation ID去重。并发两个WaitForAction的Temporal test必须反向发送两个node action并证明无丢失、无串扰、replay稳定。

Start使用Workflow input schema，Signal/Query使用各自Operation input schema，复用受限JSON Schema validator；验证发生在quota/executor之前。schema本身在contract admission已验证。red tests记录fake executor/quota零调用。

## Repair stage 6：Audit与Sample completion（L、M）

补齐bootstrap credential issuance、expiry/revocation、identity/contract rejection、registration accepted、poller/probe/promotion success/failure/retry Audit。Audit只含Tenant、Worker/version、operation/attempt/receipt ID、digest、Pod UID hash、phase/outcome/error class；不含token、workload token、Secret、完整manifest/input。

parallel-confirmation与dynamic-decision README补建议CPU/memory，保留目录内独立路径并说明production需按Activity资源画像调整。docs tests钉住required input与resource values。

## 每阶段共同验收

1. 先执行最小test并保存预期red证据，再写production code。
2. root与三个Sample分别`go test -race ./...`、`go vet ./...`；docs/link/terminology与复制目录测试通过。
3. 涉及Temporal/Kubernetes的stage运行真实`kind-org` + `127.0.0.1:7233` E2E，并清理Namespace/image。
4. `git diff --check`、010 no-diff、fault-injection无sleep/flaky依赖、工作树scope审查通过。
5. 一个stage完整通过后有意图commit并push `origin/main`；不得把下一stage的半成品混入。
