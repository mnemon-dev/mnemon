# 皮肤开发与 Mnemon 适配

**简体中文** | [English](../../en/development/skin-integration.md) | [开发与验证](./README.md)

本指南面向希望让 Mnemon 工作台与宿主皮肤保持一致的作者。Mnemon 提供稳定的页面选择器和背景变量，皮肤负责选择颜色、透明度与可读性。安装一套现有皮肤不会自动启用 Mnemon 专用的玻璃效果；需要由皮肤显式提供覆盖规则。

## 公开约定与适用范围

Sidebar 与会话标签页共用以下根选择器：

```css
[data-dsh-plugin="dsh-mnemon"][data-dsh-part="mnemon-view"]
```

受支持的背景变量为 `--mn-bg`、`--mn-backdrop` 和 `--mn-surface`。值类型、默认合成、层叠优先级与继承范围以[界面指南的皮肤约定](../guides/ui-guide.md#theme-skin-overrides)为准。未覆盖时保留默认背景，官方主题保持不透明。

该约定随 [PR #284](https://github.com/omdsh-dev/dsh-mnemon/pull/284) 引入；测试时应使用包含该变更的构建。较早版本可能没有此根标记，此时规则不会匹配。不要把 CSS-module 哈希类名、未列出的内部变量或 DOM 层级当作兼容接口。

背景适配属于皮肤的 Client 样式，不需要新增 Mnemon 设置，也不需要修改 Host、Source、Strategy、Provider 或存储格式。根规则不覆盖其他插件，也不覆盖挂载到 `body` 的 portal 弹窗；弹窗继续使用自己的表面样式。

原生 Sidebar 入口与“插件”一样，由 DSH 通过 `sidebar.panellist` 渲染。因此，皮肤的原生面板行规则统一提供颜色、字体、间距、选中状态和折叠表现。Mnemon 只提供标签及跟随请求尺寸的图标；上面的工作台背景契约作用于打开后的页面。

替换布局若没有原生面板席位，Mnemon 会在“新建会话”下方插入自己的入口：`[data-dsh-plugin="dsh-mnemon"][data-dsh-part="sidebar-entry"]`。它沿用 DSH 面板行的数值：继承的 14px 字号、主要文字色、36px 行高配 `7px 8px` 内边距与 `0 2px` 外边距、`--dsw-radius-md` 圆角、16px 图标（折叠侧栏为 18px），悬停与打开状态使用同一悬停背景。打开时还会设置 `data-active` 与 `aria-current="page"`。重绘原生面板行的皮肤可以用该选择器保持一致。

原生主面板仅在最外层预留一次 DSH 的 `--dsh-frame-top-clearance`，保证桌面窗口控件可用。页面层位于 body 装饰层之上、DSH 窗口控件之下；皮肤不应再给内部工作台叠加窗口标题栏偏移。

## 在皮肤中添加覆盖

1. 将规则放入皮肤实际加载的 CSS 文件，随皮肤一起安装、切换和卸载。不要修改 Mnemon 的源码或生成的 `lib/` 文件。
2. 用皮肤自己的激活标记限定范围，再连接完整的 Mnemon 根选择器。这样切换皮肤后，规则就不再命中。
3. 在 Mnemon 根元素上赋值。仅在 `html` 或其他祖先元素上设置 `--mn-*`，会被根元素自身的默认值覆盖。

以 [dsh-web 皮肤中心](https://github.com/zhu1090093659/dsh-skins)的 v2 皮肤格式为例，皮肤激活标记是 `html[data-dsh-skin="<id>"]`。可将适配规则放入 `skin.json` 中 `contributes.patches` 指定的 `patches.css`。若已有该文件，追加规则并保留原有内容；新增时只补充相应字段，不替换完整清单。

下面的 `my-glass-skin` 是示例 ID，需要替换为你自己皮肤的 `skin.json` 中的 `id`。示例假定皮肤已经按深色和浅色模式，将 `--dsw-alias-bg-base` 设置为合适的半透明颜色：

```css
html[data-dsh-skin="my-glass-skin"]
[data-dsh-plugin="dsh-mnemon"][data-dsh-part="mnemon-view"] {
  --mn-bg: var(--dsw-alias-bg-base);
  --mn-backdrop: var(--mn-bg);
  --mn-surface: var(--mn-bg);
}
```

此规则让使用 `--mn-surface` 的工作台区域采用皮肤基础色，替换默认的背衬合成。如果基础色完全透明，结果也会完全透明；需要皮肤作者选择合适的透明度。壁纸与模糊效果仍由皮肤负责，这三个变量不会自动创建它们。这里的 dsh-web 文件格式与激活标记属于皮肤中心；其他皮肤系统应采用自己的加载方式和激活范围，保留同一个 Mnemon 根选择器。

规则应放在未分层的样式表中，保留两个 Mnemon 属性。仅这两个属性的优先级就高于默认声明，无需依赖样式表加载顺序或添加 `!important`。不要把普通覆盖放进 `@layer`，它会低于 Mnemon 未分层的默认声明。

## 从旧类名写法迁移

对于已有的 `[class*="_shell"]` 规则，保留皮肤自己的背景计算，把目标替换为带皮肤激活范围的公开根选择器。删除旧的宽泛匹配，避免误改其他插件中同名的内部布局。两个入口使用同一条规则，无需为 Sidebar 和会话标签页各维护一份皮肤代码。

例如 Issue #273 使用的 `--pg-panel-rgb`、`--pg-bg-glass` 和 `--pg-glass-floor` 是该皮肤自己的变量；Mnemon 不提供这些变量。迁移时保留皮肤对它们的定义，不要将它们加入 Mnemon 配置。

## 真实 WebUI 验证

使用[开发与验证指南](./README.md)中的隔离环境，安装准备发布的皮肤及 Mnemon 制品，使用合成记忆完成下列检查：

| 检查 | 预期结果 |
|---|---|
| 默认主题与停用皮肤 | 保留默认表面，皮肤覆盖不残留 |
| Sidebar 与会话标签页 | 同一个根选择器命中当前工作台，两个入口均可导航 |
| 深色、浅色与窄窗口 | 文本、按钮、输入框清晰；背景和响应式行为符合皮肤设计 |
| Runtime 新增、编辑、刷新 | 操作成功，刷新后记录保留 |
| 弹窗打开与取消 | 弹窗保持可读，关闭后可继续操作工作台 |
| 多插件组合与返回会话 | 其他插件页面及会话输入不被 Mnemon 规则误改 |
| 皮肤切换和重新加载 | 激活范围随皮肤变化，效果与持久化选择一致 |

在相同页面、数据、明暗模式和窗口尺寸下保留默认与适配后的截图，并记录 Mnemon、DSH、皮肤中心及皮肤的版本。原版皮肤展示与加入适配 CSS 后的展示应明确区分。

若覆盖没有生效，依次检查皮肤是否真正激活、根标记是否存在、样式表是否加载，以及开发者工具中的 `--mn-surface` 计算值。规则被划掉时检查选择器和 `@layer`；计算值已改变但看不见壁纸时，检查皮肤自己的背景、遮罩和响应式规则。皮肤安装或设置保存失败，应单独记录，不能据此判断 Mnemon 钩子失效。

维护 Mnemon 本身时，还应运行现有的定向回归与文档检查：

```sh
pnpm exec vitest run tests/client.spec.tsx -t 'accepts scoped skin variables'
pnpm verify:docs
```

定向回归覆盖两个入口和其他插件的样式隔离；实际绘制、样式加载顺序和可读性仍需真实浏览器验证。已有复现步骤及前后对照见 [Issue #273 验收记录](../../pr-assets/issue-273-skin-hooks/README.zh-CN.md)。
