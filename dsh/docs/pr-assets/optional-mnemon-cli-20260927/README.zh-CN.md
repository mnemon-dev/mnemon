# Mnemon CLI 作为可选的 Provider 后端

[English](./README.md)

实测实现：`c45680308ad74be9963f9e20e41897da5af2f9a8`，叠加在主策略选择（`533b67fe78470fb08488a379346313f26e80ec21`）之上。环境为 macOS 15.6、Node 25.1.0、pnpm 11.19.0、正式 DSH 0.1.7-rc.2 与 Mnemon CLI 0.2.7。第三方 Provider 使用仓库中的 [Provider Lab](../../../provider-lab/README.md) 在 Docker 中运行，只绑定 `127.0.0.1`；Ollama 0.34.4 同样运行在容器中，用 CPU 提供 `qwen2.5:3b` 与 `nomic-embed-text`。未使用个人记忆或凭据。

## 没有 Mnemon CLI 时

`pnpm e2e:serve --without-mnemon-cli` 会隐藏已安装的 CLI。随后在“设置 → 记忆系统”中以自托管方式连接 Mem0。

| 页面 | 结果 | 截图 |
|---|---|---|
| 状态 | 不显示 Mnemon Native 卡片；Provider 列表显示 Mem0 就绪 | [状态](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/optional-mnemon-cli-20260927/nocli-status.png) |
| 版本 | 先列 dsh-mnemon；Mnemon CLI 显示“未安装（可选）”并给出安装命令 | [版本](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/optional-mnemon-cli-20260927/nocli-versions.png) |
| 设置 | Native 标记为“未安装 CLI（可选）”；依赖 CLI 的向量测试被停用 | [设置](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/optional-mnemon-cli-20260927/nocli-settings.png) |
| 创建记忆空间 | 默认选中 Mem0；Mnemon Native 被停用并提示“需要在本机安装 Mnemon CLI” | [创建](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/optional-mnemon-cli-20260927/nocli-create.png) |

从该对话框创建的空间落在 Mem0 上，存储正常。之后对同一个 Mem0 容器执行 Headless 流程，走默认沉淀策略：没有就绪的 Provider 时，沉淀失败并提示“No memory provider is ready: install the Mnemon CLI to use Mnemon Native, or connect another provider in Settings”；连接 Mem0 后，策略显示为 Mem0，沉淀出的空间创建在 Mem0 上，写入一条事实后，用自然语言问题召回到了它。见 [evidence.json](./evidence.json)。

## 安装 Mnemon CLI 并接入 Provider Lab 时

安装了 CLI 的 WebUI 夹具通过公开的插件组合（`scripts/seed-provider-lab.mjs`）写入测试数据：Mnemon Native、OpenViking、Honcho、Mem0、Hindsight、Holographic、RetainDB 与 Supermemory 各自创建了一个空间并写入五条事实。见[状态](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/optional-mnemon-cli-20260927/lab-status.png)截图。

- 状态页把 Mnemon Native 卡片显示为连接正常，含 Mnemon 0.2.7 与它的空间，并列显示已启用的第三方 Provider。
- 七个其他 Provider 同时就绪（其中四个按字母排在它前面）时，创建对话框仍默认选中 Mnemon Native。这一改动的第一版会选中按字母排序的第一个就绪 Provider；WebUI 检查发现了这个问题，现在只要 Native 就绪就排在首位。
- 检索按每个激活空间列出一个来源，并为每条结果标注 Provider。Ollama 空闲时，“记忆系统的三层结构是什么？”从 OpenViking、Mem0、Honcho 与 RetainDB 召回了对应事实，也召回了无 CLI 流程中创建的 Mem0 空间里的事实。
- 内容页列出每个可枚举 Provider 的事实。共显示 43 条：Mnemon Native、OpenViking、Honcho、Holographic、RetainDB 与 Supermemory 各 5 条，三个 Mem0 空间合计 11 条，Hindsight 在抽取仍在进行时显示了前 2 个单元（[内容](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/optional-mnemon-cli-20260927/lab-content.png)）。
- 实体页只把 Mnemon Native、Hindsight 与 Holographic 作为实体索引，其他 Provider 都标记为不支持。共 24 个活跃实体，来自 Mnemon Native（9）、Hindsight（15）与 Holographic（13）（[实体](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/optional-mnemon-cli-20260927/lab-entities.png)）。

## 验证

- `pnpm verify`：文档（2,122 个本地链接、81 个锚点）、确定性构建（42 个文件）、17 个 workspace 包的构建、类型检查与测试（记忆空间 174 通过且 2 个条件跳过、Runtime 61、项目档案 39、通用主策略 6）、根测试 1,417 通过且 6 个条件跳过，另有一个对计时敏感的 Session 修复测试在 Docker 抽取占满机器时超时，单独运行通过（132/132）；真实 Headless 激活（37 个工具）、旧设置导入与重启、根停用门、打包 52 个文件、publint 与 attw。
- `pnpm verify:plugins --skip-build`：17 个独立插件仓库与 18 个打包制品通过，包括独立运行的记忆空间测试与从正式 0.5.15 升级的真实 DSH 检查。
- `pnpm release:intent`：覆盖 dsh-mnemon、dsh-mnemon-provider-mnemon-native 与 dsh-mnemon-source-memory-spaces。
- 新增的回归测试在旧代码上失败：无 CLI 时沉淀、按字母排序的目录中 Native 仍居首位、版本探测失败后仍保留 Provider 统计、没有 Native 时的创建对话框。

## 限制

Ollama 在 Docker 中用 CPU 运行。抽取队列（Hindsight 事实抽取、Supermemory 摄取、OpenViking 语义写入）会占满它；在此期间，每次查询都需要向量化的 Provider 会超时并显示为不可用，队列清空后恢复正常。有一次 OpenViking 写入报告 10 秒超时，但事实实际已写入。ByteRover 未验证，因为本机没有安装它的 `brv` CLI。脚本化检查只说明各条路径可以端到端走通，不代表各 Provider 的抽取或排序质量。
