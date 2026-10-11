# 记忆子代理与要求用户查询的聊天模板：issue #327

[English](./README.md) | [验证数据](./verification.json)

Issue [#327](https://github.com/omdsh-dev/dsh-mnemon/issues/327) 反馈：通过 Ollama 0.33 运行本地模型、且聊天模板要求用户查询（例如 Qwen3.x）时，后台审查在第 4 次请求报 `500 no user query found in messages`，而此前它已经创建了项目档案。

基线：`main` `06182f4d`，即已发布的 dsh-mnemon 0.5.21。修复：`771d43e2bdc3ffcc06d2fa057ae0e9d913ce884f`。验证于 2026-10-03（北京时间）：
- macOS 15.6 arm64 与 Node 24.19.0；
- 无头 Chrome 154，1280×800，zh-CN，浅色；
- DSH 0.1.7-rc.2（反馈中的版本）与 0.2.0-rc.2。

本机没有 Ollama 和 Qwen 模型，由本机回环的模型桩扮演服务端。DSH、Mnemon 和浏览器均为真实运行，未调用真实模型，记忆均为合成数据。

## 原因

- Mnemon 的记忆子代理（后台审查、记住等）只有一条用户消息，即委派给它的任务提示。之后每一步都会追加一次助手的工具调用及其结果。
- Ollama 0.33 渲染时保留系统提示，以及其余消息中能放进 `num_ctx` 的最长后缀，并且总会保留最后一条消息。工具结果填满窗口后，这段后缀就从任务提示之后开始。
- 要求用户查询的模板随即失败，Ollama 返回 500 `no user query found in messages`（[ollama/ollama#18303](https://github.com/ollama/ollama/issues/18303)）。截断时保留用户消息的修复 [ollama/ollama#18697](https://github.com/ollama/ollama/pull/18697) 尚未发布。
- 与 DSH 自己的上下文压缩无关：它会把压缩的区段替换为一条用户角色的摘要。

## 模拟方式

模型桩的行为与 Ollama 0.33 一致：
- 按上述规则截断，token 数按 JSON 长度除以 4 估算；
- 截断后没有用户文本消息时返回 500。

审查发出第 3 次请求时，窗口定为恰好容纳这次请求从任务提示开始的部分；再多一轮工具调用，第 4 次请求就会溢出，与反馈一致。只有模型的选择是脚本化的：检索项目档案、新建档案、再次检索、完成。

## 修复前（main）

![状态页：后台审查失败，memory subagent stopped with error: SERVER: no user query found in messages，并列出已提交的档案回执](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-327-subagent-user-turn/before-status.jpg)

两个宿主上：
- 第 1 至 3 次请求都保留了任务提示；
- 第 4 次请求只剩工具消息，服务端返回 500。DSH 重试了 5 次，结果相同；
- 状态页显示“后台审查失败”与 `memory subagent stopped with error: SERVER: no user query found in messages`，并列出已提交的 `mnemon_document_create · created` 回执，即反馈中“先写后死”的情形。

## 修复

每个委派子代理在 DSH 发布时都会挂上两个 DSH 钩子。在服务端以 `no user query found in messages` 拒绝它的某次请求之前，什么都不改变：
- **被拒的这一步。** `agent/request-error` 钩子在子代理的会话中追加一条简短的 Mnemon 用户消息 `Continue from the tool results above.`，并立即重试这一步，与 DSH 自己的上下文溢出恢复做法相同。被拒的请求没有进行推理，这个钩子也排在 DSH 的退避重试之前。
- **该子代理之后的步骤。** 上下文仍是满的，`agent/pre-step` 钩子让之后每次工具续轮都以同样的消息结尾。服务端总会保留最后一条消息，因此每次请求都带有用户查询。

从不拒绝的路由，发出的请求与 main 完全相同。这一点不只关乎 Ollama：包括 Qwen3 在内的一些聊天模板只显示最后一条用户查询之后的推理内容，如果每轮工具调用后都追加用户消息，在 vLLM 这类从不截断的服务端上，前面各步的推理就会被丢掉。主会话不变。

| 修复后：状态页 | 修复后：审查创建的档案 |
|---|---|
| ![状态页：系统正常，没有审查失败，1 份活跃档案](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-327-subagent-user-turn/after-status.jpg) | ![项目档案：Review checkpoint storage](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-327-subagent-user-turn/after-documents.jpg) |

两个宿主上，第 4 次请求丢掉了任务提示，被拒一次，随即带着续行消息重试并成功。状态页显示“系统正常”，没有审查失败，项目档案中出现了这份档案。所有运行都没有控制台错误。

## 逐次请求

进程内组合的结果，fork 与 spawn 两种审查相同：

| 请求 | main：最后一条消息 | main：用户查询 | 修复：最后一条消息 | 修复：用户查询 | 保留任务提示 |
|---|---|---|---|---|---|
| 1 | DSH 运行时上下文（user） | 有 | DSH 运行时上下文（user） | 有 | 是 |
| 2 | 工具结果 | 有 | 工具结果 | 有 | 是 |
| 3 | 工具结果 | 有 | 工具结果 | 有 | 是 |
| 4 | 工具结果 | **无：500** | 工具结果 | **无：500** | 否 |
| 4（重试） | — | — | 续行消息（user） | 有 | 否 |

窗口从不溢出时，修复版本发出的 4 次请求与 main 相同。两个宿主上的 WebUI 运行与上表一致。

## 设计复核

本修复的第一版 `b0dc3c7e` 在所有路由上，让每个委派子代理的每次工具续轮都以这条用户消息结尾。复核发现了代价：
- 在 DeepSeek 路由上没有收益，每步多几个 token，子代理记录里每步多一行“上下文注入”。
- 在 OpenAI 兼容路由上的推理模型，即使没有发生截断，模板也会丢掉前面各步的推理。

现在只在被拒之后才生效。复核还建议 pre-step 钩子保留传入 decision 的其他字段，并在其他处理器清空某一步时不再改动，与 DSH 自己的钩子一致。两处都已照做。

## 自动检查

- `tests/review-user-turn-host.spec.ts` 在进程内运行真实的 DSH 0.1.7-rc.2 组合并接入上述模拟：agent 循环、fork 与 spawn provider，以及 Mnemon 的生命周期、协调器与工具。
  - main 上两种审查都以反馈中的错误失败，并留下部分写入回执。
  - 修复后，会截断的窗口只被拒一次、重试一次，两种审查都创建了档案。
  - 从不溢出的窗口，请求与 main 完全相同。父会话没有任何续行消息。
- `tests/continuation-turn.spec.ts` 覆盖两个钩子：
  - 被拒之前什么都不改变；
  - 其他失败、第 1 步和已中止的步骤交给下一个处理器；
  - 被拒的步骤只重试一次；
  - 之后的续轮保留传入的 decision，已带用户文本和被清空的步骤保持不变；
  - 钩子只挂到自己这次启动所创建的子代理上，启动相互重叠时也能区分；启动失败或运行结束时释放。
- `tests/subagent.spec.ts`：委派的写入子代理同样能恢复。
- `pnpm run verify` 通过：文档（2,979 个本地链接）、类型检查与确定性构建，全部 17 个插件的构建、类型检查与测试，根测试（115 个文件，1,571 通过，6 跳过），Headless 激活，包内容（解压后 1,502,943 字节，预算调整为 1,504,000），公开入口、publint 与 attw。

## 限制

- 服务端是依据 Ollama 0.33 已公开行为（ollama/ollama#18303 与 #18697）构建的模拟，并非真实的 Ollama 加 Qwen 模型。token 估算较粗，窗口在运行中确定，使溢出与反馈一样落在第 4 次请求。
- 恢复依据错误文本 `no user query found in messages` 触发，Ollama 返回的、以及 Qwen 自己的聊天模板抛出的都是这段文本。措辞不同的同类拒绝不会被恢复。
- 截断仍会丢掉较早的上下文，包括任务提示。调大 Ollama 的上下文长度（`OLLAMA_CONTEXT_LENGTH` 或模型的 `num_ctx`）能让整个审查都留在窗口内。
- 主会话由 DSH 负责，本次不变；反馈中主会话没有失败。
