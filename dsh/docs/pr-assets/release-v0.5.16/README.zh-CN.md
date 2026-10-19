# v0.5.16 发布验收

[English](./README.md)

本记录覆盖版本化 Starter `0.5.16`、Memory Spaces Source `0.5.10` 和十五个版本不变的配套插件。验收使用隔离存储、正式发布的 DSH `0.1.7-rc.2`、官方 Mnemon CLI `0.2.9` 和合成数据。

## 已完成的功能验收

通过官方 DSH CLI 安装全部十七个版本化 tarball，已安装的 57 个运行时 JavaScript/CSS 文件均与制品字节一致。scoped、light-context 和 auto-capture 三个可选 Strategy 同时启用。Host 与官方 DSH 包均未修改。

本地审计代理向 DeepSeek Messages API 转发了十三次真实请求，全部返回 HTTP 200，响应模型均为 `deepseek-flash`；工具选择和回答均由真实模型生成。计数不含启动时的两次协议冒烟请求。本记录不包含凭据、带认证参数的 URL 或完整模型载荷。

| 链路 | 实际结果 |
|---|---|
| Flash 对话 → Runtime | 状态健康；USER 写入返回已提交回执；重复写入未产生重复条目；WebUI 与磁盘内容一致。 |
| Runtime 编辑器 | 创建并编辑 MEMORY 条目，用键盘清空分支限制；持久化条目不再含有 `branches` 字段。 |
| Runtime 容量 → Native | 连续添加 5,750 与 5,825 字节合成条目跨过 10 KB 上限；旧内容先写入 Native 索引，再提交待写条目；页面显示归档完成，活跃使用量为 5.8 KB。USER 保持本地。 |
| Documents → Native | 创建、检索并阅读档案；真实 Flash 拟定归档计划，Native 建立冷索引；归档原文仍可阅读，内容哈希不变。 |
| 沉淀 → 检索 → 关联 | 真实 Flash 检查激活空间与已有证据，随后提交指定约定；页面关键词检索与官方 CLI 均能读到；关联面板标题和关闭按钮可见。 |
| Sidebar 与页面框架 | 切换插件面板再返回记忆系统后，检索条件保留；两行原生 Sidebar 均为 252 × 36 px、x=14；1280 × 720 视口中，四个页面标题起点均为 x=296、y=101.148 px。 |
| 冷重启 → 新会话 | 替换 Host 进程并刷新浏览器；Runtime/Documents 的五个文件 SHA-256 均不变；Native 保留四条记忆与八条关系；新 Flash 会话可召回 Native 测试记忆，并逐字引用两条注入的热记忆，未重新写入。 |

请求审计、制品标识、重启哈希、布局测量与截图哈希见 [verification.json](./verification.json)。

## 自动化验证

- 版本更新前的完整组合 `pnpm verify` 通过，验证树与三个 PR 合入 `main` 后完全一致。
- 更新版本后，文档、类型、确定性构建、workspace 构建与插件测试通过。完整 Root 测试以两个 worker 运行：**103 个文件、1,421 项通过**，按配置跳过三个文件、八项测试。两项性能断言单独运行也通过，阈值未修改。
- 版本化 Root 的 Headless 激活、公共入口、包内容和严格 package lint 通过。
- 官方 CLI 集成通过：**43 项 Runtime 容量测试**与**一项 Native Source 组合集成测试**，包括真实 Native 写入、召回、忘记和多命名空间归档。
- `MNEMON_PLUGIN_VERIFY_CONCURRENCY=1 pnpm verify:plugins --skip-build` 通过：**16 个独立插件仓库、17 个制品**，以及外部 SDK 消费者、真实 DSH 从 v0.4.7 升级和三个可选 Strategy 同时启用。

本机一次高并发验证超过两项 Root 性能测试的墙钟时限；同时进行的独立插件验证也有 Runtime UI 测试失败，其中包含超时。随后完整 Root 使用两个 worker 通过，独立插件验证串行通过；没有放宽断言、超时或性能阈值。正式 CI 在全新 runner 上执行原始完整命令。

## 截图

![真实 Flash 写入 Runtime](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.16/flash-runtime-write.jpg)
![Flash 与 Native 完成档案归档](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.16/documents-archived.jpg)
![Runtime 容量归档完成](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.16/runtime-capacity-archive.jpg)
![Native 检索与可见的关联操作](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.16/native-related.jpg)
![冷重启后的版本与存储状态](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.16/status-after-restart.jpg)
![新会话读取重启前的热记忆](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.16/cold-restart-flash-recall.jpg)

修复前后对比见[元数据](../pr-286-plugin-metadata/README.zh-CN.md)、[Sidebar](../sidebar-native-20260926/README.zh-CN.md)与[页面框架](../source-page-frames-20260926/README.md)记录。

## 验证范围

没有配置第三方 Provider 凭据；独立包与契约验证不代表对应远程服务已完成真实调用验收。官方 DSH `0.1.7-rc.2` 的 `cordis:group` 容器展示与开关问题仍存在，本版不包含 DSH 补丁或替代插件管理适配器。
