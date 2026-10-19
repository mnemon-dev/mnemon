# Architecture cleanup verification / 架构清理验收

Base: `7bcadbcf368798bfee1f2c2c45aa9638c55dd09d` (v0.5.2). Production/test revision: `1c4e0c4`; subsequent evidence-only commits do not change the implementation.

## Scope / 范围

Host execution resources now have one owner; Strategy configuration rules live in the SDK; Memory Spaces owns its configuration normalization. The internal workbench transport loses unused migration paths, and each default Source owns its page copy and styles. Public RPCs, storage formats, configuration keys, optional page-kit class names, and all 920 bilingual keys remain available. No new Core service, plugin category, UI framework, or automatic write authority was added.

Host 执行资源统一所有权；Strategy 配置规则归 SDK；Memory Spaces 配置归 Source。删除未使用的工作台传输路径，默认 Source 自己持有页面文案与样式。公开 RPC、存储格式、配置键、可选页面工具类名及全部 920 个双语词条保留；没有增加 Core 服务、插件分类、UI 框架或自动写入权限。

The resource-move commit `4e99900` preserved 1,310 CSS rules/conditions and 294 public class mappings against the base. A separate compact-header fix (`3ff8116`) prevents the connection label from shrinking into the title and uses the accessible back icon at narrow widths. The original fingerprint remains recorded alongside this explicit correction; it is not presented as an unchanged visual baseline.

资源迁移提交 `4e99900` 保持原 1,310 条样式规则/条件及 294 个公开类名映射。后续单独的窄屏页头修正 `3ff8116` 防止连接状态侵入标题，并在窄屏使用保留可访问名称的返回图标。原始指纹与这项明确修正分别记录，不将修正后的界面宣称为完全未变。

## Automated verification / 自动化验证

Environment: macOS, Node 24.19.0, pnpm 10.13.1, published DSH 0.1.2-rc.1; Native integration uses installed Mnemon CLI 0.2.7 with temporary storage.

```sh
MNEMON_NATIVE_TEST_CLI=/opt/homebrew/bin/mnemon pnpm verify
pnpm verify:plugins
pnpm release:check
pnpm release:intent
git diff --check origin/main
```

- `verify`: 121 test files / 1,050 tests passed across root and packages; one Windows-only file/test skipped on macOS. Deterministic builds, local composition performance fences, real Headless activation/restart, public entries, package contents, publint and type checks passed.
- `verify:plugins`: 16 copied standalone repositories and 17 packed artifacts passed installation, type checking, tests and builds; public SDK/Client consumption, three optional Strategies together, and real DSH v0.4.7-to-current Starter upgrade passed. The Client consumer also tests Source-owned resources against a deliberately stale shared page kit.
- Release metadata and changeset coverage passed. No version bump, publication, or merge is part of this PR.

- 根与各包共 121 个测试文件、1,050 项测试通过；macOS 跳过 1 个 Windows 专用文件/测试。确定性构建、本地组合性能围栏、真实 Headless 启动/重启、公开入口、包内容、包 lint 与类型检查通过。
- 16 个仓库外独立插件和 17 个打包产物的安装、类型检查、测试与构建通过；公共 SDK/Client 消费、三个辅助策略共存及真实 DSH v0.4.7 升级通过。消费测试还模拟旧版共享页面工具，验证 Source 自有文案与样式可独立加载。
- 发布元数据和 changeset 覆盖通过。本 PR 不升级版本、不发布、不合并。

The first artifact run hit a registry TLS reset. A subsequent run exposed missing tarball integrity in the test registry, allowing pnpm to reuse old bytes with the same unreleased version. The registry now serves the actual `npm pack` SHA-512 integrity; the full artifact run was repeated successfully, without replacing package dependencies or skipping upgrade checks. Published DSH primitives also emit a missing-source-map warning during Vitest; this is not counted as a test failure.

首次产物验证遇到 registry TLS 重置；后续发现测试 registry 未提供 tarball 完整性摘要，使 pnpm 复用了同版本旧包。现已提供真实 `npm pack` SHA-512 摘要并重跑完整验证，未替换依赖或跳过升级检查。Vitest 中已发布 DSH primitives 的缺失 source map 警告保留，不记为测试失败。

## Browser checks / 浏览器检查

Run `pnpm e2e:serve` after building. This uses a disposable real DSH WebUI/Profile with a loopback-only model stub; all notes and Documents below are synthetic. No personal DSH profile, private memory, or cloud Provider account was used. Two Native facts were seeded via the CLI into the same explicit temporary Store, then read through the UI; this is not claimed as LLM-authored capture.

构建后运行 `pnpm e2e:serve`。环境为隔离的真实 DSH WebUI/Profile，模型仅使用本地桩；所有记忆与档案均为合成材料。未使用个人 DSH 配置、私有记忆或云端 Provider 账户。两条 Native 事实通过 CLI 写入同一显式临时 Store，再由 UI 读取，不将其称为 LLM 主动记录。

| Check / 检查 | Observed result / 实际结果 |
| --- | --- |
| Sidebar launch, return, one chat turn / 入口、返回、会话回合 | Connected; local stub reply completed / 已连接，本地桩回合完成 |
| Runtime add/edit/clear branch / 新增、编辑、清空分支 | Count and content updated; scope badge removed / 条目数与内容更新，分支标签移除 |
| Documents create/edit/search / 档案新增、编辑、检索 | Markdown table rendered; revision 1 → 2; query found the document / 表格渲染，修订递增，检索命中 |
| Native space create/activate / 原生记忆空间创建、激活 | Created through the 390px dialog; 1/1 active / 窄屏创建成功，1/1 激活 |
| Native browse/keyword recall/related/forget / 浏览、召回、关联、忘记 | 2 results and a related item; confirmed soft-delete reduced recall to 1 / 召回 2 条并展示关联，确认软删除后剩 1 条 |
| Three enhancements / 三项增强 | All three switches persisted on together / 三个开关同时开启并保存 |
| Layer disable/re-enable / 记忆层关闭与恢复 | Documents alone became Off; Host stayed Connected; revision-2 data returned after enabling / 仅档案关闭，Host 保持连接，重启该层后修订 2 数据恢复 |
| Locale/theme/layout / 国际化、主题、布局 | Chinese dark and English light; Sidebar and Builtin; 390px dialog and page / 中英双语、深浅主题、两种入口、390px 弹窗和页面 |

The automation's empty-string `fill` initially left a branch input unchanged. Reading the actual input exposed the issue; keyboard select-all/backspace and Save correctly cleared the scope. It was not a Source persistence failure.

自动化的空字符串 `fill` 曾未清空分支输入。核对真实输入值后，使用键盘全选删除并保存，范围正确清除；这不是 Source 持久化故障。

### Remaining integration observation / 保留的集成观察

In the DSH 0.1.2-rc.1 Builtin host at 1280×720, a DSH-owned `[data-width-handle="left"]` overlay occupied x=376–416, including the Runtime tab's center. Center clicks hit that drag handle; clicking the uncovered part of the tab worked. The Sidebar checks do not have this interception. No DSH code or global host-handle CSS was modified, and no pre-cleanup browser run was used to assign regression causality. This remains a separate Builtin integration issue, not an all-clear UI claim.

在 1280×720 的 DSH 0.1.2-rc.1 Builtin 宿主中，DSH 自身的左宽度拖柄覆盖 x=376–416，包含 Runtime 页签中心。中心点击被拖柄拦截，未遮挡区域可正常切换；Sidebar 无此拦截。未修改 DSH 代码或全局拖柄样式，也未用清理前浏览器基线判断回归归因。这是单列保留的 Builtin 集成问题，不宣称界面所有交互均无问题。

Windows execution, real cloud Provider services, mobile Safari, LLM summarization quality, and production model latency/cost were not tested here. Model-stub completion and local performance fences do not demonstrate benchmark accuracy or efficiency gains.

本次未测试 Windows 运行、真实云端 Provider、移动 Safari、LLM 整理质量或生产模型延迟/费用。本地模型桩回合及性能围栏不代表 benchmark 准确率或效率收益。

## Screenshots / 截图

These are unedited browser captures of this branch using only the synthetic fixture. / 以下均为本分支合成环境的原始浏览器截图。

- [Runtime edit / 运行时编辑](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/architecture-cleanup/runtime-edited-desktop.png)
- [Document revision / 档案修订](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/architecture-cleanup/documents-revision-desktop.png)
- [Native recall / 原生召回](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/architecture-cleanup/native-recall-desktop.png)
- [Enhancements together / 增强共存](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/architecture-cleanup/enhancements-enabled-desktop.png)
- [Mobile creation / 窄屏创建](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/architecture-cleanup/space-create-mobile.png), [reachable footer actions / 可达的底部按钮](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/architecture-cleanup/space-create-mobile-actions.png)
- [English compact page / 英文窄屏页](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/architecture-cleanup/documents-mobile-en.png)
- [Layer disabled / 关闭单层](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/architecture-cleanup/documents-disabled-en.png)
- [Builtin English light / 内置入口英文浅色](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/architecture-cleanup/builtin-runtime-en-light.png)
