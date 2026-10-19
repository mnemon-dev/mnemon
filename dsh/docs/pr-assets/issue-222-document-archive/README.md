# Documents archive verification — issue #222

[Evidence index](../README.md) · [Issue #222](https://github.com/omdsh-dev/dsh-mnemon/issues/222)

## English

Verified on 2026-09-10 in an independent worktree based on `main` at `1790251919ec731f9dd49b43da78f78b37890a00`. The full WebUI journey (screenshots 01–12) tested `179589d152b3220ce32f2401de9f757ae421fe06`. Final automated checks and the additional WebUI archive smoke (screenshot 13) tested `90eb1d21eaf3d251937dafe6232a72c2509cc60c`, which also rechecks the live global read-only policy before planning and indexing.

Environment: macOS 15.6 arm64, Node 25.1.0, pnpm 11.19.0, published DSH 0.1.2-rc.1 Web profile, local dsh-mnemon 0.5.6 build, official npm Mnemon CLI 0.2.8, Chromium 153.0.8010.12 / Playwright 1.63.0, 1440×1050 viewport. Every profile, workspace, memory store and model endpoint was disposable. No live model API or personal memory was used.

### Reproduction and result

The legacy build was exercised through the real WebUI, DSH archive task and Native Provider. The scripted worker made an empty recall, actually wrote a cold index, then incorrectly referenced receipt 1 as its destination. The Host returned the exact reported error, `migration lineage destination does not match its recall receipt`. The document stayed active while one new index remained searchable. This reproduces the reported failure mode; the reporter's private model trace was not available to establish its exact sequence.

The fixed worker has only its terminal result tool. It proposes a summary and destination; the Host validates that plan before any index write, appends the exact cold path and hash, obtains the durable receipt, builds lineage, then commits the document archive. Invalid plans cannot write indexes. Exact existing references can be reused. On a later document commit failure, compensation targets only the newly created index; it preserves existing or ambiguous entries and reports an unsuccessful cleanup with the destination/id.

### Automated checks

| Command / boundary | Result |
|---|---|
| Targeted regressions against the legacy Host | 7 expected failures, 7 passes in the selected subset |
| Live read-only policy regressions before the final guard | 2 expected failures; both pass with the guard |
| `pnpm exec vitest run tests/subagent.spec.ts` | 85 passed, including 25 added cases |
| `pnpm verify` | 1,199 passed executions: 876 Root + 323 plugins; 7 opt-in skips; types, deterministic builds, real Headless activation and package validation passed |
| `pnpm verify:plugins` | 16 independent plugin repositories, 17 packed artifacts and external SDK/Client/real DSH composition passed |
| `MNEMON_NATIVE_TEST_CLI=... pnpm --filter dsh-mnemon-source-memory-spaces exec vitest run tests/native-integration.spec.ts` | 1 passed; real isolated Native write, recall and forget |
| `pnpm release:intent`, `pnpm verify:docs`, `git diff --check` | Passed |

The regression suite covers live global read-only changes before and during planning, malformed/failed plans, invalid or changed destinations, immutable View namespace grants, LRU capacity archival, revision conflicts before and after indexing, caller cancellation, deduplication readback, existing-index reuse, wrong-space/near-match recall, asynchronous or ambiguous receipts, failed cleanup, and preserving an index after the document has already committed. A composed Documents + Holographic test performs real persistence and verifies that compensation removes the new index while retaining the original document and an earlier index. The seven ordinary-suite skips are optional live-model/Native/Windows cases; the portable Native integration was run separately above.

### WebUI evidence

| Case | Observed result / screenshot |
|---|---|
| Legacy failure | [Exact error, active original](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-222-document-archive/01-before-archive-error.png); [one orphan index](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-222-document-archive/02-before-orphan-index.png) |
| Successful archive | [Archived original and receipt](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-222-document-archive/03-after-archived-original.png); [exact cold index](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-222-document-archive/04-after-cold-index.png) |
| Discoverability | [Real keyword recall returns the index](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-222-document-archive/05-after-keyword-recall.png) |
| Revision-2 invalid proposal | [Rejected, document remains active](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-222-document-archive/06-after-rejected-plan.png); [index count remains one](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-222-document-archive/07-after-rejected-no-index.png) |
| Corrected retry | [Rename and retry succeeds; two archives](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-222-document-archive/08-after-retry-success.png); exactly two indexes after the retry |
| No eligible destination | [Inactive space rejected before model work or indexing](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-222-document-archive/09-after-inactive-rejected.png) |
| Restart | [Two archived originals and one active rejected document survive](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-222-document-archive/10-after-restart.png); inactive space state also persists |
| Final build after reactivation | [Previously rejected document archives at revision 2](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-222-document-archive/13-after-final-build-archive.png); exactly three indexes and three archived originals in that profile |
| Actual named tools | [Three conversation turns: create, update, archive](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-222-document-archive/11-after-tool-roundtrip.png); [revision-2 content archived at revision 3](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-222-document-archive/12-after-tool-archived-original.png) |

Dialog cancellation preserved the active document. All four final document bodies were read from disk and matched their stored SHA-256. After the final smoke all four were archived, across two disposable profiles. The named-tool case asserted the actual returned lineage's source revision/digest and Provider destination. See [minimal structured evidence](./verification.json) for exact synthetic ids, hashes and selected model-fixture events. All thirteen screenshots were visually reviewed.

### Repeat locally

```sh
pnpm install --frozen-lockfile
pnpm verify
pnpm verify:plugins
MNEMON_CLI_PATH=/absolute/path/to/mnemon pnpm e2e:serve --document-archive
```

Choose the printed disposable workspace and the Mnemon E2E preset. Create and activate a Native Memory Space, then create/archive a document in Memory System. A title containing `REJECT` deliberately proposes an invalid destination; check the active original and index count, rename it, and retry. Deactivate the destination to test preflight refusal. Send `archive-tool-222 prepare`, `archive-tool-222 update`, and `archive-tool-222` in three separate conversation turns for the named-tool path. The fixture scripts only model decisions; all tools and storage operations remain real. Stop with Ctrl-C to remove the temporary data.

### Limits and compatibility

Windows and the reporter's original desktop installation were not available. This does not measure live LLM summary quality or certify every remote Provider. Archive now requires an existing active destination with exact writes and safe forget; asynchronous extraction and non-reversible destinations are rejected before writing. There is no storage-format migration. Native compensation uses its normal soft-delete semantics, so tombstones/audit history can remain. Provider timeouts, process crashes, unidentifiable receipts or failed cleanup cannot provide a distributed transaction guarantee; the implementation fails closed and avoids deleting pre-existing or uncertain data. Older orphan indexes are not bulk-deleted on upgrade.

## 中文

2026-09-10，从 `main` 的 `1790251` 创建独立 worktree，完整 WebUI 流程（截图 01–12）实测提交为 `179589d`；最终自动校验和追加 WebUI 归档复验（截图 13）实测 `90eb1d2`，后者增加规划前及索引写入前的实时全局只读检查。环境为 macOS 15.6 arm64、Node 25.1.0、pnpm 11.19.0、正式 DSH 0.1.2-rc.1 Web profile、本地 dsh-mnemon 0.5.6 构建、官方 npm Mnemon CLI 0.2.8，以及 Chromium 153 / Playwright 1.63。所有工作区、记忆和模型端点均为临时夹具，没有调用真实模型服务或读取私人记忆。

旧构建通过真实 WebUI、归档任务和 Native Provider 复现了报告中的完整现象：脚本子代理先空召回、再实际写入冷索引，随后错误引用第 1 份回执；Host 抛出同样的 lineage 匹配错误，档案仍为 active，空间却多出一条索引。用户原始模型轨迹不可得，因此这证明的是同类失败路径，而不是声称还原了用户模型的具体思考过程。

修复后，子代理只拟定摘要和目标空间，Host 在写入前校验方案，补齐精确路径与哈希，根据自己的持久回执生成 lineage，再移动原文。无效方案没有索引副作用；旧的精确索引可复用；文档提交失败时仅补偿本次新建的索引，不删除已有或结果不确定的数据。

`pnpm verify` 共通过 1,199 次测试执行（Root 876、插件 323），另有 7 项 opt-in 跳过；类型、确定性构建、真实 Headless 激活及包校验全部通过。独立验证通过 16 个插件仓库、17 个制品和外部组合；真实 Native 写入、召回、删除冒烟另行通过。子代理套件共 85 项，新增 25 项；追加的两项实时只读回归在补充检查前均失败、修复后均通过。套件覆盖实时只读变化、无效方案、修订冲突、取消、重复索引、清理失败、已提交状态保护、受限 View 与 LRU 归档；真实 Documents + Holographic 组合验证了补偿清理和原文保留。

上表保留了 13 张实测截图：旧错误与孤儿索引、正常归档和召回、revision 2 的拒绝路径与索引数量不变、修正后重试、停用目标后的提前拒绝、重启持久化，以及三个真实用户回合中的 `mnemon_document_manage` 新建、更新和归档。最终构建重启后重新启用目标空间，原先被拒绝的文档成功归档，该 Profile 恰好有三个归档原文和三条索引。两个临时 Profile 的四个文档最终均已归档，磁盘正文 SHA-256 与记录的内容哈希均一致。真实工具回执包含指向 revision 2 的 source lineage，归档后文档 revision 为 3。精简事件、id 和哈希见 [verification.json](./verification.json)。

复跑命令和测试指令见上方；`REJECT` 标题用于拒绝路径，`archive-tool-222 prepare`、`archive-tool-222 update`、`archive-tool-222` 分别触发三个真实工具回合。夹具只控制模型决策，不模拟工具或存储。停止服务会清理临时数据。

未实测 Windows 原环境、真实 LLM 摘要质量或所有远程 Provider。归档要求已有、已启用且支持精确写入和安全删除的目标空间；异步或不可补偿目标在写入前拒绝。持久化格式未变，升级不会批量删除旧孤儿索引。Native 清理沿用软删除语义，可能保留墓碑和审计历史；超时、崩溃、无法识别的回执及清理失败不具备分布式事务保证，需根据返回的目标信息核对状态。
