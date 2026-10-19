# 策略名称

[English](./README.md)

实测实现：`852373e4`，截图时叠加了其上的数据目录改动，名称保持不变。macOS 15.6、Node 25.1.0、已发布的 DSH 0.1.7-rc.2、headless Chrome 1280 × 860。每次运行都从 Starter 默认配置与其测试模型的隔离 WebUI fixture 开始，未使用个人记忆或凭据。

## 主策略选择器

两个主策略的名称直接说明它们做什么：分层策略让运行时记忆常驻，档案与记忆空间按需读取；通用策略提供全部 Source，由模型决定如何使用。

| 浅色 | 深色 |
|---|---|
| ![主策略菜单](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/strategy-names-20260927/names-menu.png) | ![主策略菜单（深色）](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/strategy-names-20260927/dark-names-menu.png) |

## DSH 组件列表

DSH 按包的显示元数据为每一行命名，列表与选择器使用同样的名称。

![组件列表](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/strategy-names-20260927/names-rows.png)

## 验证

- `tests/plugin-metadata.spec.ts` 校验各包的 DSH 元数据与其声明一致；组合、增强与组件页测试使用新名称。
- 完整的 `pnpm run verify`、`pnpm run verify:plugins`、`pnpm run verify:docs` 与 `pnpm run release:intent` 均通过。
- WebUI：脚本化走查截取 2 个浅色与 1 个深色状态，控制台无错误。

## 限制

类型 id（`default-three-tier`、`general`）、包名与配置键保持不变，已保存的选择与数据不受影响。已发布的版本说明保留发布时的名称。
