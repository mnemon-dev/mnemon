# DeepSeek V4 Flash Runtime 容量压力测试

[English](README.md) · [汇总指标](results.json) · [修复前最小样例](before-fixes.json) · [完整证据 ZIP](https://github.com/user-attachments/files/32006240/dsh-mnemon-pr212-evidence-20260909-2994e3d.zip) · [归档 SHA-256 与内容清单](../runtime-flash-evidence-20260909.json)

**通过：**2026-09-09，三个场景完成 240 次写入、38 次自动归档；带引号输入诊断另完成 96 次写入、17 次归档。已提交原文完整保留，默认 10,240 字节上限未突破，维护任务无残留。所有请求及实际响应均为 `deepseek-v4-flash`，关闭思考。

工作负载参考 [issue #203](https://github.com/omdsh-dev/dsh-mnemon/issues/203)：后端、前端、Android、运维四条工作流共享项目 Runtime，使用合成事实与临时存储。环境为 macOS arm64、Node v25.1.0、DSH 0.1.2-rc.1、Native CLI 0.2.7。记录基线是 `583842c2847bfb1f0bf4423cc41c261fd4b941d9` 加最终提交于 `fce3873e9669c9a9eefb902b76ca8d0ddbf68700` 的修复；实测源文件哈希保留在汇总指标中。

| 场景 | 写入 / 归档 | 热记忆峰值字节 | 最终热 / 冷行数 | 耗时 |
| --- | ---: | ---: | ---: | ---: |
| 四个主会话、单空间 | 96 / 16 | 10,235 | 11 / 94 | 67.5 秒 |
| 四个子代理、四空间 | 96 / 16 | 10,229 | 13 / 95 | 244.0 秒 |
| 无打开的用户会话、Web RPC | 48 / 6 | 10,237 | 12 / 45 | 76.5 秒 |
| 带引号输入诊断、单空间 | 96 / 17 | 9,646 | 11 / 94 | 65.5 秒 |

热记忆与冷记忆可以重叠，不能相加计算唯一事实数。两个并发场景均达到四路模型请求及写入调度重叠。子代理设置 `maxDepth: 1`；子代理场景的 80 个、无会话场景的 30 个维护任务均已释放。无会话场景在写入前释放全部父会话句柄。

主测试使用 275 次 Flash 调用、441,228 输入 Token、77,231 输出 Token。工具写入 p95：单空间 834 毫秒、四空间子代理 14,157 毫秒；未单独测量 RPC 延迟。耗时包含排队与模型路由。

## 发现与限制

- **已修复精确归档：**Native 常规语义去重对三个相似原文仅导入一个。子代理工作负载在 14 次写入后因 Host 拒绝不精确回执而阻塞。Provider 现复用快照中的完全相同内容，其余原文通过 `--no-diff` 导入并校验精确回执；普通 `remember` 语义不变。
- **已修复 UTF-8 损坏：**逐块解码在 84 次写入、13 次归档后损坏一个跨块中文逗号。各自所属的进程模块现独立维护 stdout/stderr 流解码。诊断重跑完整保留 96 条实际提交内容，模型添加的外围引号单独计量。
- **路由仍有偏差：**子代理场景 85/95 条冷记忆主题匹配，十条进入其他已授权空间，保留三个重复冷副本；无会话场景为 45/45。授权与原文保留通过，语义分类是独立质量指标。
- 本轮显式写入记忆以制造容量压力，关闭空闲召回与整理。自然对话质量见独立的[自动记忆验收](../runtime-memory-quality-flash-20260909/README.zh-CN.md)。未测试 Windows、独立 DSH 进程、长时间运行或其他真实 Provider。

## 复现与核对

通过环境提供 `DEEPSEEK_API_KEY` 与已验证的 `MNEMON_NATIVE_TEST_CLI`，运行保留的[显式启用测试](../../../tests/runtime-capacity-flash-stress.spec.ts)：

```sh
MNEMON_RUN_FLASH_STRESS=1 MNEMON_FLASH_STRESS_ROUNDS=8 MNEMON_FLASH_STRESS_REPORT=/tmp/mnemon-flash-stress.json pnpm exec vitest run tests/runtime-capacity-flash-stress.spec.ts
MNEMON_RUN_FLASH_STRESS=1 MNEMON_FLASH_STRESS_ROUNDS=8 MNEMON_FLASH_STRESS_JSON_PROMPT=1 MNEMON_FLASH_STRESS_REPORT=/tmp/mnemon-flash-json.json pnpm exec vitest run tests/runtime-capacity-flash-stress.spec.ts -t 'four concurrent root'
```

测试在发出请求前拒绝非 Flash 模型，核对响应模型，每场景最多 240 次调用，并清理会话与存储。常规 CI 跳过三个真实测试。配置八轮时，无会话场景执行四个波次。

ZIP 原样保留 `2994e3de8cc27a53cd3b54d9c5dc7d6628d1646c` 的完整报告、逐轮结果和测试脚本快照。请按链接清单校验压缩包 SHA-256；包内 `MANIFEST.json` 列出各文件哈希。本次仅调整证据存放形式，没有重跑模型或修改指标。

修复后的完整验证通过 834 项根测试，独立制品门禁通过 16 个插件仓库、17 个制品。合并 main 后完整验证通过 836 项根测试（跳过五项真实 API 测试），以及类型、文档、确定性构建、Headless、包检查；各次 revision 见[验证记录](../runtime-memory-quality-flash-20260909/validation.json)。
