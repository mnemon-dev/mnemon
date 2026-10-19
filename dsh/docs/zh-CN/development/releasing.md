# 独立包发布流程

**简体中文** | [English](../../en/development/releasing.md) | [开发与验证](./README.md)

通过发布 PR 准备版本，不从功能分支直接发布。README 等进入 npm 制品的文档也需要发布意图；仅站点文档或历史证据的改动不要求包升级。

```text
改动与 changeset → 审阅 PR → 版本 PR → main revision
→ 验证并打包变化的包 → npm-release Environment 批准
→ 按依赖层发布变化的包 → 安装验证完整固定组合
→ 发布 Starter → Registry 升级 smoke → GitHub Release
```

官方包采用独立版本。`dsh-mnemon` Starter 是经过测试的物料清单：它精确固定每个内置插件，而插件 peer dependency 描述当前 minor 线内的兼容范围。只有自身制品或发布元数据变化的插件才升级；只要固定的组合发生变化，Starter 就升级。仅作为反向依赖测试目标的包不会因此重新发布。

凡 PR 会改变发布制品，都使用 `pnpm changeset` 添加发布意图；`pnpm release:status` 可预览版本变化，已有的 source CI job 会把 PR 与其精确 base revision 比较，并拒绝任何已变化却未出现在新增 changeset 中的发布包。新增包的 PR 同样要为它添加发布意图；该包随下一个发布 PR 首次发布，版本由这份意图决定。专用发布 PR 运行 `pnpm release:version`，一次性应用意图、同步 Starter 与外部 fixture 的精确依赖、更新 Provider 中生成的版本声明并刷新 lockfile。workspace peer 与开发关系使用 minor 线兼容范围，因此未变化包的 manifest 也保持不变。若变化的插件开始使用同一版本新增的 Starter SDK 导出，发布 PR 还要把该插件的 `dsh-mnemon` peer 下限提高到新的 Starter 版本。`pnpm release:check` 是只读检查，验证完整的混合版本组合、内部兼容范围、Starter 精确版本、仓库元数据和每个包的 npm 通道。

手动触发的 npm workflow 接收完整 commit SHA，且该 SHA 必须已经等于 `main`。它从 Git 历史确定上一个发布 revision，只选择版本发生增长的包。在取得 npm 凭证前，workflow 会运行完整 workspace 与独立插件验证，只打包选中的插件和 Starter，并记录完整组合、两个 revision、字节数与 SHA-512 integrity。受保护的 `npm-release` Environment 是 Registry 写入门禁。批准后，变化的插件在依赖安全的层内并发发布；一层在 Registry 可读后才进入依赖它的下一层。随后通过冻结的本地 Starter 安装完整混合版本组合，最后发布 Starter、验证干净 Registry 安装并运行真实升级，之后才创建 GitHub Release。

中断的流程可以恢复：Registry 中已有的选中版本只有在 integrity 与冻结 tarball 完全一致时才能复用；同版本内容不同会立即终止。正式包使用 `latest`，预发布显式使用 `alpha`、`beta` 或 `rc` 通道。未变化的包必须已存在于 Registry，并通过完整 Starter 安装接受验证，但不会再次打包或发布。

npm 发布不是事务。失败时保留冻结制品并重跑同一 revision；完整性检查会安全跳过匹配版本并继续补齐缺失版本。不能覆盖或撤销不可变版本。只有新 Starter 及其精确组合中的所有版本都可安装，才算发布完成。开发验证不会创建 Git tag、GitHub Release 或 npm 发布。
