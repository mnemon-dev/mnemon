# Issue #202: newest creations first / 创建时间倒序

Captured on 2026-09-09 using implementation `690a46b17526d4ad38f58b23f2f8d3d6896834e1`, based on `main` revision `0d5f5fafe4e8d7ec5e299c637baa1a05d3020c84`. Screenshots 01–02 use the main build; screenshots 03–08 use the fixed build. DSH `0.1.2-rc.1`, Starter `0.5.5`, Runtime/Documents Sources `0.5.4`, Node `25.1.0`, pnpm `11.19.0`; the browser uses its default 1280 × 720 viewport and Chinese locale.

截图日期为 2026-09-09，实现和 main 基线版本如上。01–02 为 main 构建，03–08 为修复后构建。使用正式发布的 DSH 契约、本次本地 Source 构建、浏览器默认 1280 × 720 视口和中文界面。

## Setup and scope / 环境与范围

`pnpm e2e:serve` provides a real DSH WebUI with a disposable `DSH_HOME`, data root, workspace and loopback model endpoint. Seeded through real Source management: 12 Runtime entries (six USER, six MEMORY) and 12 Documents (ten active, two archived). Creation dates span August 1–12, with insertion order 03, 01, 02, 04…12; a separate seeding process controls fixture dates. Runtime 02 and Document 01 were edited on a later fixture date. Subsequent browser edits and creations use the real clock.

`pnpm e2e:serve` 启动真实 DSH WebUI，使用一次性的配置、数据根、工作区和本机模型端点。通过真实 Source management 写入 12 条运行时记忆（USER、MEMORY 各六条）和 12 份档案（十份活跃、两份归档）。创建日期为 8 月 1–12 日，写入顺序为 03、01、02、04…12；仅独立播种进程控制夹具日期。运行时 02 和档案 01 随后在更晚的夹具日期编辑，浏览器里的新增和编辑使用实际时间。

All records are synthetic. Model replies are scripted; Source persistence, page loading, filtering, search, edits, creation and reloads are real. Archive records were seeded locally, so this does not test model-driven cold-index creation. This change only sorts the displayed matches: search eligibility, result limits and engine relevance ranking are unchanged. The shared Source pages serve Sidebar and Builtin; this browser run covers Sidebar.

全部资料为合成测试数据。只有模型回复被脚本化，Source 持久化、页面加载、筛选、检索、编辑、新增和刷新均走真实链路。归档记录由本地播种，因此不验证模型驱动的冷索引创建。改动仅调整返回结果的页面顺序，检索范围、结果数量上限和引擎相关性排序保持不变。Sidebar 与 Builtin 共享 Source 页面，本次浏览器验收覆盖 Sidebar。

## Results / 结果

| Check / 检查 | Result / 结果 |
|---|---|
| Before/after regression / 修复前后回归 | Both new page tests fail against main and pass against the fixed pages. / 两项新页面用例在 main 页面实现上失败，在修复后通过。 |
| Runtime / 运行时 | First page 12…03; Show more continues with 02, 01. MEMORY and text filters retain descending creation order. / 首屏 12…03，“再显示”追加 02、01，工作记忆和文本筛选保持倒序。 |
| Documents / 档案 | Active list and search show 10…03, then 02, 01; archived list and archived search show 12, 11. / 活跃目录及检索先显示 10…03，再显示 02、01；归档目录和归档检索显示 12、11。 |
| Actual UI writes / 实际界面写入 | Editing Runtime 02 and Document 01 preserves their creation times and positions. Document 01 reaches revision 3. New Runtime 13 and Document 13 appear first and remain first after reload. / 编辑运行时 02、档案 01 保持创建时间与顺序，档案 01 成为第 3 版。新增运行时 13、档案 13 置顶，刷新后保持。 |
| Data / 数据 | Disk assertions confirm original storage order and creation timestamps remain unchanged; final totals are 13 Runtime, 11 active Documents and two archived Documents. / 磁盘断言确认原存储顺序和创建时间保留，最终为 13 条运行时、11 份活跃和两份归档。 |
| Browser console / 浏览器控制台 | Zero errors or warnings during the fixed-build run. Four connection-retry warnings came from intentionally stopping the baseline server. / 修复后构建验收期间无错误或警告；四条连接重试警告来自主动停止基线服务。 |
| Full verification / 完整验证 | PASS: 1116 tests, two opt-in Native/Windows skips; types, deterministic builds, Headless and package checks. / 通过：1116 项测试，跳过两项需额外启用的 Native/Windows 测试；类型、确定性构建、Headless 与包校验通过。 |
| Independent packages / 独立发布包 | PASS: 16 independent plugin repositories, 17 packed artifacts, external consumer and real DSH activation with four workers. / 通过：四路并行验证 16 个独立插件仓库、17 个发布制品、外部消费方和真实 DSH 激活。 |

[Browser and disk assertions / 浏览器及磁盘断言](./browser-assertions.json)

The published upstream UI package has an existing missing-source-map warning during Vitest. It does not prevent the checks from passing. / 已发布上游 UI 包在 Vitest 中存在缺少 source map 的现有警告，不影响检查通过。

The first independent-package attempt exposed timeouts in the new tests while seeding repeatedly through Source management under parallel load. The final regressions use frozen page snapshots without changing the global clock. The first remote artifact run also exposed repeated whole-list accessibility visibility scans under CI load; ordering assertions now read the list nodes directly and use scoped labels/text for interaction. Existing real Source mutation tests, timeouts and actual WebUI writes remain in place.

首次独立包检查中，新增测试在并行负载下通过 Source management 反复播种时超时。最终回归使用冻结的页面快照，不再修改全局时钟。首次远端制品检查还暴露了 CI 负载下反复扫描整份列表可访问性与可见性的开销；排序断言现直接读取列表节点，交互使用有范围的标签/文本定位。原有真实 Source 写入测试、超时限制和本次实际 WebUI 写入验收均保留。

## Reproduce / 复现

```sh
pnpm install --frozen-lockfile
pnpm run verify
pnpm run verify:plugins --skip-build
pnpm e2e:serve
```

Select the printed disposable workspace and the **Mnemon E2E** preset, send a test message, then open **Memory system**. Create multiple Runtime entries across both targets and multiple Documents. Verify newest-first order, edit an older record, filter/search, load more, and reload. The page regression fixtures contain the out-of-order dates and pagination cases. Stop the fixture with Ctrl-C when finished.

选择打印的一次性工作区和 **Mnemon E2E** 预设，发送测试消息后打开**记忆系统**。分别创建两种目标的多条运行时记忆和多份档案，检查倒序、编辑旧记录、筛选/检索、加载更多和刷新。页面回归夹具覆盖乱序日期与分页场景。结束后用 Ctrl-C 停止夹具。

## Screenshots / 截图

| Evidence / 证据 | Screenshot / 截图 |
|---|---|
| Runtime before / 修复前运行时 | [01](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-202-created-order/01-runtime-before.png) |
| Documents before / 修复前档案 | [02](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-202-created-order/02-documents-before.png) |
| Runtime newest first / 运行时倒序 | [03](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-202-created-order/03-runtime-newest-first.png) |
| Runtime UI creation first / 界面新增运行时置顶 | [04](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-202-created-order/04-runtime-ui-create.png) |
| Active Documents newest first / 活跃档案倒序 | [05](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-202-created-order/05-documents-newest-first.png) |
| Archived Documents newest first / 归档倒序 | [06](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-202-created-order/06-archived-newest-first.png) |
| Edited old document stays last / 编辑旧档案后仍在末尾 | [07](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-202-created-order/07-documents-edit-preserved.png) |
| New document remains first after reload / 新档案刷新后仍置顶 | [08](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-202-created-order/08-documents-ui-create.png) |
