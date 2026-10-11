# Issue #203 自动记忆验收

[English](README.md) · [汇总指标](results.json) · [最小问题样例](examples.json) · [完整证据 ZIP](https://github.com/user-attachments/files/32006240/dsh-mnemon-pr212-evidence-20260909-2994e3d.zip) · [归档 SHA-256 与内容清单](../runtime-flash-evidence-20260909.json)

**验收通过，保留非阻塞改进项。**用户复核后接受本轮有界模拟结果：新会话答对 144/152 项（94.7%），八项为 unknown，16 项正式纠正全部回答正确。未出现旧值、跨模块或无依据回答；192 个开发回合的最终配置检查全部通过。

实际测试的生产 revision：`fce3873e9669c9a9eefb902b76ca8d0ddbf68700`，日期 2026-09-09。所有请求及实际响应均为 `deepseek-v4-flash`，关闭思考。原始严格自动评分仍为 `fail`，后续用户复核通过的决定记录在 `acceptanceDecision` 中。以下发现尚未修复。

| 指标 | 单空间、12 波次 | 四空间、12 波次 | 四空间、24 波次 |
| --- | ---: | ---: | ---: |
| 开发回合 / 新会话 | 48 / 12 | 48 / 12 | 96 / 28 |
| 当前事实覆盖 | 35/36 | 36/36 | 80/80 |
| 新会话答对 | 34/36 | 36/36 | 74/80 |
| 正式纠正答对 | 4/4 | 4/4 | 8/8 |
| 残留临时标记 | 0 | 1 | 3 |
| 陈旧基础文档 | 4 | 4 | 4 |
| 最终热记忆 / 文档条数 | 22 / 6 | 29 / 5 | 13 / 23 |
| 热记忆峰值 / 最终字节 | 7,095 / 7,095 | 6,684 / 6,684 | 10,079 / 5,615 |
| 空闲审阅 / 模型调用 | 40 / 321 | 41 / 299 | 85 / 654 |
| 被拒绝的记忆调用 | 1 | 3 | 4 |
| 耗时 | 4 分 34 秒 | 4 分 9 秒 | 12 分 5 秒 |
| 复核验收 | 通过 | 通过 | 通过 |

## 对照用户吐槽的发现

- **部分复现“乱七八糟”：**长程第 21、23 波次将局部文本交给整条替换。一条前端记忆丢失模块名称和七个字段，另一条丢失 `timeout_ms=1800`。[examples.json](examples.json) 保留原样的前后文本和成功调用。[Runtime Source](../../../plugins/dsh-mnemon-source-runtime/src/source.ts) 按收到的内容替换整条记忆，未涉及容量归档。
- **保存不等于召回：**短程一项事实未被保存，另一项已存在但回答 unknown。长程六项 unknown 都是仍在 Documents 中的前端配置；部分读者只搜索空的冷记忆，没有继续搜索文档。
- **旧值和无须保存的材料残留：**Runtime 已纠正，但共 12 份基础文档仍有旧值；维护权限只能创建文档，补充文档持续累积。第 13 波次文档保存了第 10 波次明确要求不落盘的标记原文。禁止写入回合本身没有记忆修改，后续历史审阅重新引入了样例。
- **操作能够恢复：**八次调用被拒绝，重复热记忆和缺少归属继续保留为改进项。所有开发回合最终检查通过；short-four 的 `development-workload-incomplete` 原始评分误计了两次已恢复的中间失败，原始标记与人工裁定同时保留。
- **未发生人工清理或容量阻塞：**模型自然整理让热记忆保持在 10,240 字节以下。三组归档次数均为零、冷存储为空，本轮没有验收归档后质量；重复归档另见[容量压力测试](../runtime-capacity-flash-20260909/README.zh-CN.md)。

## 方法与限制

后端、前端、Android、运维四个会话在一个真实 DSH 进程内并发运行。开发工具实际读取、更新、校验临时 JSON 配置，模型自行选择记忆操作，默认生命周期执行空闲审阅。新读者不能访问开发对话或文件。本轮是有界配置任务，没有构建完整应用。

环境为 macOS arm64、Node v25.1.0、DSH 0.1.2-rc.1、Native CLI 0.2.7。guided 召回/写回和默认容量保持不变，只将空闲防抖由 30 秒缩短到 5 秒。[protocol.json](protocol.json) 保留预登记参数、哈希及长程运行前确定的扩展理由。每种条件只有一次正式运行，两波次校准不计分。未覆盖 Windows、独立进程或真人半天工作流。

冻结评分要求覆盖率和准确率至少 90%、纠正全部正确，且噪声、旧值/跨模块/无依据回答、禁写回合修改、冷副本重复、超容和执行错误均为零。原始严格评分失败与后续复核通过分别记录。共审阅全部 98 条最终记忆、152 项回答；使用 1,274 次 Flash 调用、4,557,451 输入 Token、292,774 输出 Token。

## 复现与核对

通过环境提供 `DEEPSEEK_API_KEY` 与已验证的 `MNEMON_NATIVE_TEST_CLI`。[测试入口](../../../tests/runtime-memory-flash-quality.spec.ts)、[真实运行夹具](../../../tests/fixtures/flash-quality/live.ts)及[工作负载生成器](../../../tests/fixtures/flash-quality/workload.ts) 继续保留在 Git 中：

```sh
MNEMON_RUN_FLASH_QUALITY=1 MNEMON_FLASH_QUALITY_REPORT=/tmp/mnemon-flash-quality.json pnpm exec vitest run tests/runtime-memory-flash-quality.spec.ts
MNEMON_RUN_FLASH_QUALITY=1 MNEMON_FLASH_QUALITY_WAVES=24 MNEMON_FLASH_QUALITY_REPORT=/tmp/mnemon-flash-quality-long.json pnpm exec vitest run tests/runtime-memory-flash-quality.spec.ts -t 'four preconfigured'
```

ZIP 原样保留 `2994e3de8cc27a53cd3b54d9c5dc7d6628d1646c` 的完整报告、展开输入、逐轮指标、152 项回答、全部 98 条最终记忆及审阅标记和测试脚本快照。包内 `MANIFEST.json` 列出逐文件哈希，链接清单记录 ZIP 的 SHA-256。本次调整存放形式，没有重跑或重新评分；执行后的报告器改动见 protocol。

独立的[验证记录](validation.json) 对应合并 main 后的代码：完整验证通过 836 项根测试（跳过五项真实测试），独立验证通过 16 个插件仓库、17 个制品。历史模型结果仍只对应注明的生产 revision。
