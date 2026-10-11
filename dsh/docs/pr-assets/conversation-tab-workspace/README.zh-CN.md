# 会话 Agent 未加载时的会话标签页记忆系统

[English](./README.md) | [验证数据](./verification.json)

在**记忆系统入口 → 会话标签页**模式下，项目档案可能报 `memory Source is unavailable in the requested management scope: source:include:mnemon-source-documents`，同时状态页显示“等待工作区”。原因是 Host 只能通过会话已加载的 Agent 找到它所属的工作区。DSH 会在恢复会话的 Agent 之前就显示这个会话；有些会话能显示却根本无法恢复，例如其 Agent 预设已不在当前 profile 中。在 Agent 加载之前，记忆系统拿不到工作区。

基线：`main` `06182f4d`，即已发布的 dsh-mnemon 0.5.21。修复：`e0f43ab186a84b586e28ba1644d75daa963daee5`。验证于 2026-10-02（北京时间）：
- macOS 15.6 arm64 与 Node 24.19.0；
- 无头 Chrome 154，1280×800，zh-CN，浅色；
- 仓库的一次性 WebUI 测试环境及其本机模型桩，分别运行在 DSH 0.2.0-rc.2 与 0.1.7-rc.2 上。

未调用真实模型，记忆均为合成数据。

## 复现

测试环境先用测试预设创建两个会话。在一次性 profile 中给这个预设改名并重启 DSH 后，会话仍会列出、内容可读，但 DSH 无法恢复它们的 Agent。打开其中一个会话的记忆系统标签，再看状态页和项目档案，两个宿主上都出现了反馈中的状态：

| 修复前：状态页 | 修复前：项目档案 |
|---|---|
| ![状态页：项目档案等待工作区](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/conversation-tab-workspace/before-status.jpg) | ![项目档案：memory Source is unavailable in the requested management scope](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/conversation-tab-workspace/before-documents.jpg) |

接着**新建档案**报 `current DSH agent is not live; reopen or resume the conversation and try again`。在侧边栏中的记忆系统里，项目档案能通过所选工作区加载，但新建档案同样失败。

## 修复

- **从 DSH 注册表取工作区。** 无论会话是否已加载，DSH 的工作区注册表都列有它（`Workspace.sessionIds`，DSH 0.1.7-rc.2 与 0.2 中相同）。请求中的会话 Agent 未加载时，Host 从注册表取该会话的工作区；请求与会话保持对齐，就像 Agent 已加载一样。
- **写入不再依赖会话 Agent。** 此前只要页面与会话对齐，新建档案和存入记忆就会交给会话的 Agent。现在只有该 Agent 已加载时才这样做；否则直接写入会话所属工作区的 Source，与没有会话的页面一致。
- **需要 Agent 时改用任务 Agent。** 项目档案已满时，由会话所属工作区中的任务 Agent 先归档最久未用的档案再写入，与会话的 Agent 做法相同。新建记忆空间时也由任务 Agent 选择 Provider。在记忆空间中询问 Agent、整理名称与描述，此前就是这样工作的。
- **工作区存储使用正确的根目录。** 使用工作区或集中存储时，这种标签页此前在 DSH 启动时所在的目录中读写运行时记忆和记忆空间，现在改用会话所属的工作区。按旧方式保存的条目仍留在那个目录中。

| 修复后：状态页 | 修复后：在这里新建的档案 |
|---|---|
| ![状态页：项目档案已计数](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/conversation-tab-workspace/after-status.jpg) | ![项目档案已加载，并出现新建的档案](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/conversation-tab-workspace/after-documents.jpg) |

两个宿主上，同一个会话：
- 状态页显示项目档案数量；
- 项目档案能加载并新建档案；
- 能添加运行时记忆（[运行时记忆](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/conversation-tab-workspace/after-runtime.jpg)）；
- 能打开记忆空间。

侧边栏中的记忆系统同样能新建档案。所有运行都没有控制台错误。

## 切换测试

每个宿主上都用修复后的构建运行了一组脚本化场景（不改名预设），记录可见错误与状态页的项目档案卡片：

1. 已加载会话中依次打开记忆系统的每个标签；
2. 切换到另一个会话后立即打开项目档案；
3. DSH 重启后，切换到停留在记忆系统标签的会话；
4. 在两个会话之间、跨标签快速切换六次；
5. 在刚切换到的会话中新建档案；
6. 记忆系统打开时，从会话标签页切到侧边栏再切回，并走遍每个标签；
7. 侧边栏模式下重启后切换会话；
8. 新会话在发送第一条消息前后；
9. 项目档案打开时重启 DSH。

九项在 DSH 0.2.0-rc.2 与 0.1.7-rc.2 上全部通过，没有可见错误或控制台错误。未修复时，场景 1–3 与 9 在 DSH 0.2.0-rc.2 上同样通过，因为测试环境的 DSH 会在约一秒内恢复所选会话。上面的复现覆盖了这些切换到达不了的状态。

## 自动检查

- 新增测试在全局、工作区与按工作区三种存储方式下，按注册表为 Agent 未加载的会话路由：
  - 状态与项目档案的管理读取成功；
  - 手动写入档案成功；
  - 不在任何工作区中的会话仍然没有工作区。
- 另一项测试确认会话 Agent 未加载时，存入记忆与新建档案直接写入 Source。
- 其他测试覆盖任务 Agent：项目档案容量已满时交给任务 Agent，其他失败和没有会话的页面仍返回 Source 的结果；Provider 选择交给任务 Agent。看板会把这种会话按注册表工作区对齐。
- `pnpm run verify` 通过：文档（2,981 个本地链接）、类型检查、确定性构建，全部 17 个插件的构建、类型检查与测试，根测试（113 个文件，1,566 通过，6 跳过），Headless 激活，包内容（解压后 1,500,772 字节，在 1,501,000 预算内），公开入口、publint 与 attw。

## 限制

- 复现通过给测试预设改名来得到无法恢复的状态。其他途径，例如 DSH 尚未恢复的会话，走的是同一条 Host 路径，由单元测试覆盖。
- 不在任何工作区中的会话仍然没有工作区。已归档的会话在注册表中保留原位置（DSH 0.1.7-rc.2 与 0.2.0-rc.2），因此同样路由到其工作区。
- 在会话自己的 Agent 中运行的工作，例如它的后台审查，仍要等 DSH 恢复 Agent。
- 任务 Agent 路径由单元测试覆盖；上述实测没有把项目档案写到容量上限。
