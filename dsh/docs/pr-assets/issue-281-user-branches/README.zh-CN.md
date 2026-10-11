# Runtime USER 空分支 — Issue 281

[English](./README.md)

基线为 `6dc4e4201585223f7f1da371256da11deee34d6c`（v0.5.14）。环境为 macOS arm64、Node 24.20.0、pnpm 10.13.1、官方 Mnemon CLI 0.2.9，以及正式发布的 DSH 0.1.7-rc.2。两套 WebUI 均安装完整的十七个打包制品，没有 workspace 链接；三个可选 Strategy 增强同时开启。前后只有 Root 制品发生变化。

## 复现与修复

在真实 WebUI 中，先发送 `USER281_PREPARE`，通过 `mnemon_runtime_memory` 省略 `branches` 新增合成 USER 条目；再发送 `USER281_REPLACE`，携带 `branches: []` 替换同一条目。基线返回 `Error: branches applies to target=memory only`，原条目保持不变。修复版提交替换后仍恰好只有一条 USER 记录，没有重复。

具名工具只对 `target=user` 的字面空数组做省略处理。USER 的非空和畸形分支值仍被拒绝；Runtime Source、通用 View action 和 RPC 保持严格契约；MEMORY 替换仍区分省略范围与显式空范围。不需要迁移存储或发布 Source 包。

实际出站 schema 仅要求 `action` 和 `target`，正式 rc.2 的序列化也保留原始参数契约。证据证明“调用者传入 `[]` 时失败”，并未证明 DSH 强制要求该字段。

| 修复前 | 修复后 |
| --- | --- |
| ![基线 USER 替换错误](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-281-user-branches/before-user-replace.png) | ![修复后 USER 替换回执](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-281-user-branches/after-user-replace.png) |

## 验证

- 新回归文件在基线为 **4 项失败、9 项通过**，修复后 **13 项全部通过**。覆盖父/子 Agent、默认组合、三个增强加重命名 Source entry、USER 增改删及 MEMORY 不变、畸形参数、MEMORY 分支保留/更换/清空，以及 Source/通用 action 的严格边界。
- Runtime、RPC 和 Strategy 定向检查：**45 项通过**。
- `pnpm verify`：Root **1,373 项通过 / 7 项跳过**，插件 **402 项通过 / 2 项跳过**；类型、确定性构建、真实 Headless 激活与重启、设置持久化、出口和包检查均通过。Root 解包体积 **1,374,961 字节**，低于现有 1,376,000 字节上限。
- `MNEMON_PLUGIN_VERIFY_CONCURRENCY=1 pnpm verify:plugins --skip-build`：**16 个独立插件仓库、17 个制品全部通过**，包括外部 SDK/Client 消费者及三个可选 Strategy 同时启用。
- 通过 `MNEMON_NATIVE_TEST_CLI` 实际运行 Native Source/View 写入、召回与软删除，以及同一 View 新建两个 Native 目标并归档的用例。
- WebUI：USER 空数组增改删成功，非空分支拒绝，MEMORY 分支设置/清空成功。手动编辑 USER、创建档案及按正文搜索成功。界面创建并激活 Native 空间后，由已校验 CLI 写入合成记录，再由 WebUI 关键词检索读回完整原文。
- 设置保存出现成功回执。Host 冷启动后 Runtime 和 Documents 文件 SHA-256 完全一致，Native CLI 仍能召回同一个 ID 和原文。

![CLI 写入后由 WebUI 读回](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-281-user-branches/native-cli-webui-recall.png)

[verification.json](./verification.json) 保留最小实际调用、回执和内容哈希。[基线制品](./before-artifacts.json)与[修复制品](./after-artifacts.json)记录十七个 tarball 的哈希及合成输入。原始 Host 日志、本地认证 URL、npm 安装和完整会话日志保留在仓库之外。

## 重跑 WebUI

将 [rc2-profile.json](./rc2-profile.json) 复制为新临时目录中的 `package.json`，使用 Node 24 执行 `npm install --ignore-scripts`。先构建 Root，再构建独立插件；通过 `scripts/release.mjs` 的 `readReleasePackages`、`createReleasePlan`、`packRelease` 将十七个制品打包到空目录。本地验收包不发布到 registry。

```sh
MNEMON_CLI_PATH=/absolute/verified/mnemon node scripts/fixtures/serve-runtime-user-branches.mjs \
  --profile /absolute/rc2-profile --artifacts /absolute/packed-artifacts \
  --state /absolute/empty-disposable-state
```

打开 `private.json` 中的私有 URL，选择夹具的 `workspace` 目录和官方标准预设，每轮分别发送：`USER281_PREPARE`、`USER281_REPLACE`、`USER281_ADD`、`USER281_REMOVE`、`USER281_REJECT`、`MEMORY281_SCOPE`、`MEMORY281_CLEAR`。回环模型固定决策并汇总实际工具回执，不预设存储成功；正式适配器、工具执行、持久化、传输和浏览器都是真实实现。`SIGUSR2` 保留数据并重启 Host，重新连接前读取新 URL；可选 `--port` 在重启时保持指定端口（默认 `0` 自动选择空闲端口）；`SIGTERM` 停止夹具及 Host。

## 边界与剩余分类

本次没有在 Windows 执行，也不评价真实模型质量；无需外部 API 凭据。默认完整套件没有启用可选的 Flash、Teams、OpenViking 服务及独立 lifecycle 测试，保留其明确跳过记录。本次使用正式 DSH 与安装制品，没有使用可选的 Source-link WebUI 夹具。

冷启动后的新 WebUI 连接受到浏览器环境阻止（`ERR_BLOCKED_BY_CLIENT`），换回原端口复验仍相同。官方认证 URL 经 HTTP 仍正确返回认证重定向，并可凭其 cookie 取得页面；认证机制没有改变。旧页面已断线，不计为冷启动读回证据。冷启动结论仅限文件哈希、已保存配置和 CLI 读回；上述截图和交互检查均来自重启前正常连接的 Host。

仍开放的 #251 没有新增评论或失败原始会话；当前 main 中已有修复 #253 未变。六次新的只读预览审计保留原文件字节，并与既有迁移证据一致。在取得脱敏的完整失败事件链、确切源码构建 revision 及当前修复/迁移错误前，不据此新建重复 PR 或关闭 Issue。既有 UI/迁移证据仅复核，没有当作本轮新执行。
