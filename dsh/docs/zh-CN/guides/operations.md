# 运维、安全与故障排查

**简体中文** | [English](../../en/guides/operations.md) | [文档中心](../README.md)

## 健康检查

打开记忆系统的“状态”页。使用 Mnemon Native 时再检查它的二进制，其他 Provider 不需要：

```sh
command -v mnemon
mnemon --version
```

Windows PowerShell：

```powershell
Get-Command mnemon -ErrorAction SilentlyContinue
Test-Path "$env:LOCALAPPDATA\Programs\mnemon\mnemon.exe"
```

```text
/mnemon status
```

![状态页：每个记忆组件、Provider 与存储区域](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/assets/webui-v0.5.19/zh-CN/memory-status.jpg)

状态页显示 dsh-mnemon 与 Mnemon 的版本、每个记忆组件一张卡片、各个 Provider，以及当前实际目录。`mnemon status` 会打开有效 Store，上游 CLI 可能初始化数据或执行迁移，因此不是完全无副作用的只读探测。

如果 OpenViking 提示 `/api/v1/admin/*` access restrictions，在**插件 → 可组合记忆**的记忆空间页面填写 **User Key 所属用户（跳过 Admin）**（`discoveryUser`）、账号标识、服务地址与 user API key。这会用数据 API 验证所选记忆根；根目录被拒绝或不存在时保留原配置。此校验不证明写入权限，也不改变 key 绑定的身份。留空仍使用 admin 发现。参见 [Provider 边界与降级步骤](./memory-providers.md#运维边界)。

## 版本检查与更新

状态页的“检查版本”打开“检查与更新版本”面板：

- **dsh-mnemon**：当前版本是所属 Profile 记录的版本（状态页显示正在运行的版本），更新查询 npm `latest`；已安装的 beta/alpha/rc 同时查询自身通道，也可升级到更高的正式版。稳定版用户不会自动进入预发布通道。
- **Mnemon CLI**：本地版本来自 `mnemon --version`，最新版本来自官方 `@mnemon-dev/mnemon` npm 包。只有 Mnemon Native 需要它，未安装时显示为可选并给出安装命令。

检查只读，不会自动安装。只有发现更高版本并安全识别安装来源时才显示“更新”：Mnemon 支持官方 npm 启动器、Homebrew Cask / Formula 与 `go install`；dsh-mnemon 支持当前 DSH Profile 中的 npm 安装。`link:` / `file:` 开发版本与无法识别的手工安装只显示说明，避免覆盖源码。

dsh-mnemon 通过 DSH 自己的插件安装器更新，效果与 `dsh plugin add dsh-mnemon@<版本>` 相同；Profile 单独安装的可选 Strategy 也是如此。安装器使用 Profile 自己的包管理器，桌面版中即应用自带的 pnpm，因此 PATH 上不需要 pnpm；同时沿用 Profile 的安装源设置与备用源，并等待 Profile 的锁。保留新版本之前，DSH 会检查它声明支持的 DSH 版本。安装失败时，DSH 恢复 Profile 的 `package.json` 与锁文件，面板显示 pnpm 的错误行和 DSH 的日志路径，已启用的插件保持不变；安装已下载的文件可能留下，“检查版本”仍显示 Profile 记录的版本。通过 DSH 的安装器，Host 只更新自己所运行的 Profile。DSH 未提供插件安装器时，Host 在所属 Profile 中运行 PATH 上的 pnpm。既没有应用自带的 pnpm、PATH 上也没有 pnpm 时，面板会提示安装 pnpm 并重启 DSH。dsh-mnemon 0.5.21 及更早的版本只有 pnpm 这一种方式：在桌面版中从这些版本更新时，需要在插件页移除 dsh-mnemon 后重新添加一次，记忆数据会保留。

npm 更新要求当前启动器属于现有 npm 所报告的全局安装目录；不同 Node/npm 环境或启动器故障会显示修复指引。首次安装或迁移使用 `npm install --global @mnemon-dev/mnemon@latest`，后续使用 `mnemon update`。命令在 DSH 宿主运行，需要 Node.js 22+。修改 PATH 或 CLI 配置后，重新检查并核对面板中的可执行文件路径。

在 Windows 桌面宿主中，记忆操作和版本检查会直接调用已识别的官方 Mnemon npm 安装所锁定的原生程序，避免 npm 启动器额外弹出控制台窗口，并保留隐藏子进程、超时、取消和已保存的嵌入设置。界面显示的安装路径和 npm 更新归属仍指向原启动器。对于未知包布局或缺失的原生依赖，继续使用启动器回退路径及其修复诊断。

展开 dsh-mnemon 子包列表，可按 Source、Strategy、Provider 查看版本。主包固定的依赖随主包更新，仅当前所属 Profile 中独立安装的包支持单独更新；源码链接保持原维护方式。包更新串行执行，重新检查或重开面板仍保留待重启提示。只读连接可检查版本与复制命令；页面更新需要管理权限且开启 `writeEnabled`。

Go 更新还要求当前执行文件确实位于本机 Go 的安装输出位置（`GOBIN`，或 `GOPATH` 第一项的 `bin` 目录），且未配置交叉编译目标。不能仅因下载的二进制包含 Go 构建信息就认定它由 Go 管理。CLI 更新后还会核验当前执行文件已达到所检查的版本，才报告成功。

更新命令由 Host 固定选择：浏览器不能传入命令或参数，执行禁用 shell，并限制时间与输出。插件更新在所属 profile 中安装已检查的精确版本，确认实际安装版本后才报告成功，避免固定 beta 版本未变却提示已更新。更新完成后界面自动重新检查两个组件并刷新状态。Mnemon CLI 从下一次调用起生效；dsh-mnemon 在 DSH 重启后才加载新插件代码：停止并重新运行 `dsh web`，或完全退出桌面版（macOS 上按 `Cmd+Q`）后重新打开。在此之前 Host 仍运行之前的版本，而 DSH 会在文件变化后立即载入新的记忆系统页面；该页面会重新打开**检查版本**，显示这次更新以及需要的重启。DSH 重启之前，记忆系统每个页面的顶部都会提示已安装的版本和仍在运行的版本，无论更新来自检查版本还是 `dsh plugin`；单独更新的子包也会列出。状态页显示的是正在运行的版本，**检查版本**显示已安装的版本，不会再次提供这次更新。

<a id="dsh-015-兼容与旧会话恢复"></a>

## 旧会话恢复

支持的宿主为 DSH `0.1.7-rc.2` 与 `0.2.0-rc.2`，参见[兼容性矩阵](../reference/compatibility.md)。升级 Mnemon 后重启 Web Profile：Starter 补丁为拥有路由的 `connection` Entry 同时声明 `webRuntime` 和 `webServer`，恢复此前在“记忆系统”或其配置页返回 HTTP 405 的全部七个 RPC 通道。绕过 Starter 独立安装 Host 的自定义 Profile，也应在自己的 connection Entry 声明这两个依赖，并保留自定义组合原有的其他依赖。不修改 DSH 包源码；浏览器认证和 Mnemon grant 仍然生效。

另一项 `source summary requires notice form; source v0 artifact remains unchanged` 错误来自旧版 Mnemon 写入的 DSH 会话消息。新消息已移除 recall/instructions 中不合法的 summary；更新插件不会改写现有会话。修复单个受影响日志时：

1. 停止所属 DSH 进程，单独备份完整会话存储根及全部 generation；Mnemon Pack 不包含这些会话。以 DSH 错误中的准确 `raw log` 路径为准，不扫描或改写其他会话。
2. 安装修复后的 Mnemon，对**备份副本**执行下面的预览命令。`.jsonl.zstd` 使用 Node `22.19+` 或 `24+`；普通 `.jsonl` 也支持 Node 20。
3. 将结果写入会话目录外的新路径，核对 `repairedMessages`、`repairedDescriptors`、`expandedToolChunkRows`、`expandedToolChunks`、SHA-256 报告和 `blockers`。预览计数表示候选修改；发现已知不支持的形状时退出码为 1，副本请求返回 `mode: "refused"` 且不生成输出。已有输出文件会被拒绝。
4. 先在一次性的 Profile 副本中，用修复副本替换对应 `session.jsonl` 或 `session.jsonl.zstd`，保留原始备份。让 DSH 加载、续写，重启后再验证会话。副本验证通过后，才在已停止的原 Profile 中执行同样的明确替换。命令本身绝不替换输入或正在使用的会话。

```sh
dsh-mnemon-repair-session --input /backup/session.jsonl.zstd
dsh-mnemon-repair-session --input /backup/session.jsonl.zstd --output /backup/repaired-session.jsonl.zstd
```

可执行命令随 `dsh-mnemon` 安装；Profile 内安装可在该目录使用 `pnpm exec dsh-mnemon-repair-session`。源码 checkout 使用 `node bin/repair-legacy-session.mjs`。下表两种 stream 规范化本身不改 ID；同一日志也可叠加经过证明的身份恢复。副本修复仅覆盖经过契约审计的历史形状：

| 历史形状 | 副本行为 |
|---|---|
| `dsh-mnemon` instructions summary 为 `Optional memory recall and remember reminder`；recall summary 为 `Memory View snapshot` 或 `Runtime memory snapshot` | 仅移除该 source 的 `summary` 成员，保留其他插件消息和陌生 summary。 |
| 严格符合旧版封闭字段集合且组合参数满足冻结 v3 契约的 subagent descriptor v2 | 仅将版本值改为 3，保留 provider、成对模型字段、persona、label 和 toolFilter；不添加默认值或 reasoning effort。多余字段、不成对模型和其他不支持的版本会被拒绝。 |
| 严格符合旧版结构、字符串 `id: ""` 或 `name: ""` 的 `tool-call-chunks` | 展开为原来的 raw delta 事件，保留 ID、name 是否存在、参数字符串、序号和时间戳。不丢弃 chunk，provenance 引用无需重编号。已经是 raw 的空字符串 delta 保持字节不变。 |
| 严格符合结构、自有成员为 `name: null` 的 raw 或 packed 工具 delta | 保留成员，将值改为 `""`；packed 行按原逻辑序号与时间展开。ID、参数和持久消息不变。两种值均不改变组装状态，保留成员可维持 token 计时。`normalizedToolChunkNames` 统计受影响的逻辑 delta 数。 |
| 完成的 `deepseek-official` stream 已记录唯一非空 provider ID，后续空 ID 覆盖了持久工具链身份 | 核对完整有序 chunk provenance、按 index 组装的 block、相同 name/参数和唯一 call/result 关联后，仅恢复该已记录 ID。保留首个候选之前的空占位值、消息 UUID、正文及其他插件内容；支持逐条证明的多工具，以及 text/reasoning block。不生成 ID。 |
| 没有已记录 ID、身份冲突、中断、待修 owner 对象结构未知，或 approval/dispatch/plugin/消息副本引用未闭合 | 报告 blocker 并拒绝输出。任意空 ID 历史及 owner replay state 仍需原写入方恢复。 |

ID 检查使用原 stream 完整的 `sourceEventSeqs`，包括 packed 逻辑成员。result provenance 必须精确指向对应 call；字段缺省时，仅允许没有其他 attempt 的唯一单调用 step。显式错误引用不能使用此回退。通过序号或消息 ID 关联的额外引用会跨 step 检查；普通 compaction 序号引用与无关插件仍可原样保留。`recoveredToolIdentityChains` 统计恢复的链，`recoveredToolIdentityFields` 统计逻辑身份字段，`toolIdentityRecoveryRefusals` 用有数量上限的位置样例解释无法证明的形状。

工具只接受 v0、合法 UTF-8 JSON 记录和完整的普通 Zstandard 帧；原始输入、解压输入和展开输出均限制为 128 MiB。格式损坏、不完整帧、修改或身份判断路径中的歧义重复键、无法安全表示的 delta 序号或时间会在发布输出前被拒绝。仅改写匹配的 summary/version/name/identity 成员和需要展开的 packed 行，其他解压字节均保留。诊断统计每类已知 blocker 的全部次数，并最多列出十个行号/事件/字段路径样例，不输出消息正文。`migrationValidated: false` 明确表示扫描器不会修复其他损坏，也不保证任意会话都能迁移；最终仍以安装的官方 DSH 加载器和 stream 回放为准。参见 [issue #251 契约审计与验证](../../pr-assets/issue-251-legacy-repair/README.zh-CN.md)。

DSH 以写权限打开旧会话时，会迁移为不可变的 v3 generation。Mnemon Runtime、档案与记忆空间维持原有格式。回滚 DSH 时，在独立旧版本 Profile 中恢复升级前会话备份；不要让旧 DSH 打开 v3 generation。参见[验证与截图](../../pr-assets/issue-223-dsh-015/README.zh-CN.md)。

## 备份与恢复

<a id="推荐设置页-zip"></a>

### 推荐：插件页 ZIP

**插件 → 可组合记忆**页面“存储”组中的“备份与迁移”针对**当前有效根**工作：

- **导出 ZIP**：包含 Runtime、Documents 和全部 Mnemon Native Memory Spaces；三方连接、本地外部 Store 与远程数据不进入包；
- **导入 ZIP**：先预检，再合并到当前有效根；
- 包内包含 `manifest.json`、SHA-256 清单和三类数据摘要；
- 导出与导入持有组件锁，Memory Space 仍有未 checkpoint 的 WAL 时会拒绝；
- 导入检查路径、数量、压缩 / 展开大小、JSON schema、Document 哈希、registry 与 SQLite 头；
- 合并先写 staging，再替换目标组件；提交失败会恢复导入前目录。

当前 UI 只提供安全合并，不做“覆盖一切”：

- Runtime 按目标与内容去重；
- 相同 Document ID + 相同内容跳过，ID 冲突且内容不同则生成新 ID；
- 相同 Memory Space ID + 相同数据库跳过，内容不同则生成新 ID。

导入受 `writeEnabled` 控制，只读部署会拒绝。ZIP 包含私有记忆，应加密、限制访问并验证恢复。Provider 凭据保存在 `state/memory-providers.json`（`0600`），不会进入 ZIP。已保存的凭据值也不会经管理通道返回；若要备份连接，需要按下述离线快照保护整个 `state/`。

![安全导入前经过校验的备份预览](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/assets/webui-v0.5.19/zh-CN/plugin-backup-preview.jpg)

### 恢复演练

1. 在“存储”中将“数据目录”设为“自定义”，填写一个隔离目录并应用。
2. 确认状态页显示的当前实际读写根正是该目录。
3. 在“备份与迁移”中选择备份，阅读预检摘要后执行导入。
4. 在状态页检查 Runtime、Documents、Memory Spaces 与目录。
5. 用一个聚焦查询验证直接检索，再阅读一份档案。
6. 验证无误后再决定是否切换正式范围。

不要在没有备份时把恢复直接指向唯一生产根。

### 文件系统级快照

需要保留预留 `state` 或做离线完整快照时，可以停止所有使用该根的 DSH / Mnemon 进程后复制：

```text
<storageRoot>/runtime
<storageRoot>/documents
<storageRoot>/data
<storageRoot>/state    # 若存在；不在内置 ZIP 的三类数据组件中
```

复制完成后生成文件清单或校验和，并在隔离路径演练恢复。不要在多个进程仍写入时把普通目录复制当作一致快照。

## 切换存储范围

保存 `global` / `workspace` / `custom` / `workspaces` 后，Host 先初始化新运行图，再原子切换；页面自动重新读取，但**不会迁移数据**：

```text
旧范围 -- 保存设置 --> 新的空目录或既有目录

不自动复制
不自动合并
不自动删除
```

推荐迁移流程：在旧范围导出 ZIP → 切换到新范围并确认显示目录 → 导入 ZIP → 验证。工作区模式下先确认查看工作区与会话执行工作区是否是预期目标。

使用 `workspaces` 时，备份整个集中目录可覆盖所有工作区，导出 Pack 则只覆盖所选工作区。移动或重命名工作区会产生新路径哈希；恢复旧数据需要操作者显式执行。


现有回合和已委托的子 Agent activation 可能仍使用旧运行图。迁移或停用其数据前，应等待它们结束或取消这些任务。父回合结束本身不会释放异步子任务的委托；新创建或冷恢复的 activation 会捕获自己获准使用的 generation。

<a id="cloud-hosted-webui"></a>

## 云端 WebUI

DSH 0.1.7-rc.2 与 0.2.0-rc.2 是支持的 registry 安装目标。页面、每个 RPC 与每条 stream 都通过 Host 输出的启动 token URL 建立同一份、与 authority 绑定的浏览器会话。`--trusted-host` 仍只是 Host/Origin 防线，不能替代 HTTPS 或部署层访问控制。

1. 在反向代理或访问网关终止 HTTPS，并只向预期用户开放公网入口。把同源的 `/` 与 `/api` 流量（包括 stream）代理到 `http://127.0.0.1:3080`，同时保留外部 `Host` authority。
2. 使用外部 authority 启动回环服务。参数应为裸 `host[:port]`，不是 URL：

   ```sh
   dsh web --trusted-host memory.example.com --no-open
   ```

   公网入口使用非默认端口时，应传入准确 authority，例如 `memory.example.com:8443`。DSH 会刻意拒绝 `--host 0.0.0.0`；请让服务保持在回环地址，只由代理或 SSH tunnel 访问。
3. 如果浏览器还没有该公网 authority 的有效 Cookie，请使用终端里以 `dsh web: ...` 输出的启动 token URL。经过反向代理时，只把其中的回环 origin 替换成公网 HTTPS origin，保留 `/` 路径与 `?token=...` query。例如把 `http://127.0.0.1:3080/?token=...` 转为 `https://memory.example.com/?token=...`。该 URL 等同凭据，不要放入日志、Issue 或聊天。DSH 会把它交换为 HttpOnly、SameSite Cookie，再重定向到干净的 `/`；尚未过期且 authority 相同的 Cookie 可以跨 Host 重启继续使用。
4. 打开干净的公网 URL，确认“状态”和**插件 → 可组合记忆**页面都能加载，并且整页刷新后仍保持认证。远程设置默认只读；需要管理时，先应用[显式远程管理授权](#remote-management)、重启 DSH，再验证一次有意的小范围保存。

HTTP 403 可能来自 Host/Origin 不匹配，或旧远程 Client 仍调用独立通道。请检查 `--trusted-host`、公网 authority 与代理路由，再升级 dsh-mnemon、重启 DSH 并刷新浏览器；远程 Mnemon 调用使用已认证 API Gateway。HTTP 401 需要恢复 Host 浏览器认证或配对。若返回远程管理需要 `remoteAccess: trusted-host`，则是另一个 Mnemon 授权检查；浏览器认证成功本身不授予管理权限。

### 停用完整 Starter

既有的 `mnemon` Entry 继续作为整个 Starter 的生命周期总开关。需要停用 Mnemon 时，在 profile patch 中加入以下配置并重启 DSH；不需要逐项停用 Source 或 Strategy：

```yaml
- id: mnemon
  disabled: true
```

该开关会同时停用 Core/Host、三个随附 Source、两个主策略和三个可选 Strategy 增强，不会卸载包或删除记忆数据。删除该覆盖项，或把它改为 `false`，再重启 DSH，即可重新启用完整 Starter。

<a id="remote-management"></a>

### 远程管理

对于已认证的远程客户端，`remoteAccess: trusted-host` 授予管理操作；远程读取与小范围激活不需要该授权。没有该授权时，远程页面的记忆系统与插件设置会写明只读的原因。本机浏览器与 DSH 桌面版窗口不是远程客户端，不需要该授权。仅为预期的已认证用户配置远程管理权限。需要运行更早的 DSH 时，请配套使用针对它验证过的 Mnemon 版本及其升级前的会话备份，参见[兼容性矩阵](../reference/compatibility.md)。

1. 打开 `~/.dsh/profiles/web/cordis.patch.yml`；如果设置了 `DSH_HOME`，则路径为 `$DSH_HOME/profiles/web/cordis.patch.yml`。如果已经有顶层 `- id: mnemon`，请直接修改该项，不要添加重复项。如果初始化文件仍以 `[]` 结尾，请用下面的完整配置行替换它；否则把该行追加到现有顶层 YAML 列表：

   ```yaml
   - id: mnemon
     config:
       routingGuidance: true
       lifecycleEnabled: true
       recallMode: guided
       writebackMode: guided
       idleReviewMs: 30000
       tabEnabled: true
       writeEnabled: true
       remoteAccess: trusted-host
       timeoutMs: 10000
       defaultRecallLimit: 10
       embedding:
         enabled: false
         endpoint: http://localhost:11434
         model: nomic-embed-text
       recallQuality:
         policy: strict-v1
         lowScoreThreshold: 0.25
         highScoreThreshold: 0.6
         candidateMultiplier: 3
         maxMediumResults: 4
         maxUnknownResults: 2
   ```

   Profile patch 会替换目标行的完整 `config`，不会只深度合并一个字段。请保留已有自定义项；插件升级后用 `dsh web --dump-default-config` 对照它，避免遮蔽新增的包内默认值。
2. 运行 `dsh web --dump-config` 检查最终配置树。确认最后的 `mnemon` 行包含 `remoteAccess: trusted-host`，并且 stderr 没有报告无法匹配 `mnemon` 目标。
3. 使用同一条 `--trusted-host` 命令启动 DSH。每次修改 `remoteAccess` 后都要重启，因为 Mnemon 只在启动时捕获该策略。最后通过已认证远程连接验证“状态”、设置加载和一次有意的小范围保存。

## 安全边界

### 进程

- CLI 使用 `spawn(command, args, { shell: false })`，不拼接 shell。
- 每次调用的 stdout + stderr 合计限制 2 MiB。Mnemon Native 在内容列表、图谱，以及运行时记忆归档前的逐字比对中会读取整个记忆空间，这些读取随 Store 增长，上限为 128 MiB。
- 调用失败时会写明原因：只有启动失败才提示安装 Mnemon 或设置 `mnemon.cliPath`；超时、取消和输出超出上限会各自说明。
- 每次调用受 `timeoutMs` 与 AbortSignal 控制；取消先 `SIGTERM`，1.5 秒后 `SIGKILL`。
- 单个 Runner 内调用串行；跨 DSH 进程仍依赖 Mnemon / SQLite 并发语义。

### 文件

- Runtime、Documents 和 Pack 操作使用进程内队列或组件锁。
- lock 默认等待 5 秒，超过 30 秒才视为 stale。
- 写入使用临时文件、staging 与 rename。
- Runtime revision 阻止过期压缩覆盖；Document revision 阻止移动已更新原文。
- `sourcePaths` 不能逃出发起会话工作区，也不能指向受管 Documents 目录。

### Web 与模型

- DSH 负责远程 RPC 与 stream 的认证或配对；Mnemon 的 API Gateway 映射对管理操作另行要求 `remoteAccess: trusted-host`，本地回环客户端与 DSH 桌面版窗口（`dsh-app://app/`）使用各自的通道。
- Provider 目录和管理响应始终脱敏；界面只显示已配置字段名，不返回已保存凭据值。
- WebUI 依据 Host 返回的可写 settings snapshot 判断产品能力，不再根据传输位置猜测权限；设置通道不可用时会显示明确诊断，而不是空白页。
- WebUI 不直接读取 SQLite、启动进程、调用远程 Provider 或指定任意更新命令；Provider 网络访问只发生在 Host。
- worker 使用 persona、工具白名单与 `maxDepth: 1`。固定结果工具仅接受当前子任务的可撤销请求 ID，并按每次操作的 schema 校验结果。
- 蒸馏和 supervised writeback worker 不能调用 `mnemon_forget`；后台审查的档案写入只开放仅创建工具，不能覆盖用户原文，也不能通过归档腾出容量。这些限制默认启用，无需增强插件。
- 空闲审查每轮只写一层：建了项目档案后，修改工作记忆会被拒绝；修改工作记忆后，新建项目档案会被拒绝。`idleReview.runtimeMemory: false` 会完全收回它的运行时记忆工具。
- 查询、候选、档案正文与历史记忆全部按不可信数据处理。

这些边界不是秘密扫描器。当前没有确定性的凭据检测；不要提交密钥、token、私钥和原始敏感日志。

### 安全问题报告

按 [SECURITY.md](../../../SECURITY.md) 私下报告漏洞，不要创建公开 issue。数据丢失、路径穿越、锁 / revision 绕过、子 Agent 隔离破坏和 WebUI 记忆内容注入都在范围内。

## 故障排查

`mnemon.cliPath` 接受显式路径，也接受按 Host 的 PATH 查找的命令名。DSH 运行时，若二进制安装或恢复到既有搜索目录，点击记忆系统顶栏的**刷新**即可更新可用状态，无需重启；Host 进程环境变量的变化仍需重启。状态与版本检查解析同一个配置命令。

| 现象 | 检查与处理 |
|---|---|
| 插件详情中 `mnemon-bundle` 容器行（0.5.20 起名为 `dsh-mnemon/bundle`，此前为 `cordis:group`）显示“已关闭”，点击提示“找不到该插件” | DSH `0.1.7-rc.2` 的已知容器展示问题（`0.2.0-rc.1` 同样显示为已关闭）。先检查“记忆系统 → 状态”和实际组件；正常时可继续使用。整套组合用顶层 bundle 或 `mnemon` 核心组件开关，不要重置数据或删除分组。参见[原因、处理步骤与修复边界](../reference/compatibility.md#dsh-017-bundle-组件列表)和[上游 #649](https://github.com/dsh-external/issues/issues/649)。 |
| Windows 切换会话时终端窗口闪现 | 更新 Starter，或独立安装的 Runtime Source，然后重启 DSH Host。Runtime 的 Git 分支检测会隐藏控制台窗口；Git 失败、超时或 HEAD 分离时仍回退到不按分支筛选的 Runtime 视图。 |
| Mnemon 不可用 | macOS/Linux 运行 `command -v mnemon`、`mnemon --version`；Windows PowerShell 运行 `Get-Command mnemon`、`Test-Path "$env:LOCALAPPDATA\Programs\mnemon\mnemon.exe"`。设置 `MNEMON_CLI_PATH` 或 `mnemon.cliPath` 后重启 |
| Electron 桌面 Host 无法运行 npm CLI 脚本 | 经过验证的 npm 启动器仅在子进程中设置 `ELECTRON_RUN_AS_NODE=1`。如果桌面壳关闭了 [Electron `runAsNode` fuse](https://www.electronjs.org/docs/latest/tutorial/fuses#runasnode)，该变量会被忽略；请将 `mnemon.cliPath` 指向官方原生二进制（Windows 为 `mnemon.exe`）。npm 自动更新仍需要 Host 能够运行 JavaScript 启动器 |
| Headless Agent 没有 Mnemon 工具 | 插件按 profile 独立安装；运行 `dsh plugin --profile headless add dsh-mnemon`，Web profile 的安装不会自动带入 |
| 找不到“记忆系统”入口 | 检查 `tabEnabled=true`；`displayMode=sidebar` 使用侧边栏，`displayMode=builtin` 使用已打开会话的标签页。本地 link 先 `pnpm run build` 再重启 profile |
| 旧的 `buildin` 偏好打开了会话标签页 | 它会保存为 `builtin`；如需独立入口，请在**界面**中选择**侧边栏**。记忆范围与已存数据不变 |
| 状态正常但召回为空 | 检查 active 记忆空间、存储范围、查看目录、会话实际目录和查询是否足够聚焦 |
| 顶部提示目录未对齐 | 工作台正在查看另一个工作区；确认是否为预期范围。工作台任务在查看工作区执行，对话工具仍使用所属会话范围 |
| 设置看起来没有变化 | 开关与选择器立即生效，需要输入的值要点击**应用**；写入失败时控件旁会显示原因并恢复为已保存的值 |
| 自定义目录被拒绝 | 使用绝对路径、`~` 或 `~/...` |
| `memoryBodyId is required...` | active 数量不是恰好 1；显式选择目标 |
| `memory space is not active for reading` | 在概览激活目标；写入 inactive 可以，读取不行 |
| Provider 错误 | 审查需要受 guard 保护的本地子 Agent；默认有界 `spawn`，可选择 `fork`。DSH/Teams 0.1.7-rc.2 可选择 `idleReview.agentTeams: scoped`，默认仍为 `pause`；重试前核对部分写入回执 |
| 本地模型（例如 Ollama 上的 Qwen3.x）下后台审查报 `no user query found in messages` | 工具结果填满模型的上下文窗口后，Ollama 截断时丢掉了子代理的任务提示。0.5.22 起，Mnemon 会给被拒的子代理步骤追加一条用户消息并重试（[#327](https://github.com/omdsh-dev/dsh-mnemon/issues/327)），请更新 dsh-mnemon。调大 Ollama 的上下文长度（`OLLAMA_CONTEXT_LENGTH` 或模型的 `num_ctx`）也能避免截断。审查可能在失败前已创建项目档案，请核对部分写入回执 |
| Runtime replace 超容量 | 缩短 replacement 或先显式整理；自动维护只处理 add 溢出 |
| Document source path 被拒绝 | 路径必须在会话工作区内，且不能引用受管 Documents 目录 |
| CLI timeout | 增大 `timeoutMs`；大 Store 的状态与图谱可能超过 10 秒 |
| lock timeout | 检查其他写进程，不要删除仍属于活跃进程的 lock |
| ZIP 导出提示 WAL busy | 等待 Memory Space 写入完成并重试；不要绕过未 checkpoint WAL 检查 |
| ZIP 导入 checksum / schema 失败 | 备份损坏或格式不兼容；保留当前根，不要手工解压覆盖 |
| 更新按钮不出现 | 当前已是最新、远程检查失败，或安装来源是 link / 手工模式；按面板提示沿原方式更新 |
| 已认证远程页面能读取或激活记忆空间，但不能保存设置或执行其他写入 | 默认管理限制；确需远程管理时，保留当前配置、在本地设置 `remoteAccess: trusted-host` 并重启 DSH |
| DSH 桌面版中运行时记忆、记忆空间与插件设置均为只读，而 Agent 工具仍能写入 | dsh-mnemon 0.5.18 及更早版本把桌面窗口当成远程页面（[#310](https://github.com/omdsh-dev/dsh-mnemon/issues/310)）；在桌面版插件页更新到 0.5.19 或更新版本，无需修改 `remoteAccess` |
| DSH 0.2 安装或启动时提示 dsh-mnemon 不兼容 | 安装到的是 0.5.19 之前的版本；更新 dsh-mnemon，新版本发布 24 小时内按[安装与启动](./installation.md#常见问题)安装带版本号的包 |
| DSH 运行期间更新 dsh-mnemon 后，启用组件提示 `ERR_PACKAGE_PATH_NOT_EXPORTED`（`Package subpath './starter'` 或 `'./bundle'`） | 正在运行的进程仍按旧版本的包信息加载；完全退出并重新启动 DSH（桌面版按 `Cmd+Q`）。使用 0.5.18 或 0.5.19 时不要关闭 `dsh-mnemon/starter`，见[安装与启动](./installation.md#更新后启用时提示-err_package_path_not_exported) |
| `mnemon-bundle (cordis:group): pending (waiting for service: mnemonStarterReady)`，或桌面版启动失败并显示插件恢复页 | 0.5.18 或 0.5.19 的 `dsh-mnemon/starter` 被关闭；在**插件 → 可组合记忆**中打开它，或更新到 0.5.20（没有单独的就绪开关）。桌面版在恢复页卸载后重新添加 dsh-mnemon，见[安装与启动](./installation.md#dsh-提示waiting-for-service-mnemonstarterready) |
| 更新后提示 `mnemon-bundle (dsh-mnemon/bundle): pending (waiting for service: mnemonStarterReady)` | 更新到 0.5.20 后没有重启，正在运行的 DSH 仍保留旧的组件组；完全退出并重新启动 DSH，见[安装与启动](./installation.md#更新后启用时提示-err_package_path_not_exported) |
| DSH 重启或 authority 改变后 Mnemon RPC 返回 401 | 打开 `dsh web` 输出的启动 URL，让一次性 token 建立新的、与 authority 绑定的浏览器 Cookie |

## 已知限制

### 功能只读不等于磁盘只读

`writeEnabled=false` 禁用语义 mutation 与 Pack 导入，但启动可能初始化 / 修复 Runtime 投影，Document 搜索更新 `lastAccessedAt`，Mnemon 读命令也可能迁移数据库。

### Documents 的共享范围

`global` 与 `custom` 可能让多个工作区共享同一 Document index；记录没有独立 workspace ownership 字段。`sourcePaths` 只在写入时相对发起会话 cwd 校验。

### 跨系统事务

“先冷索引、后移动”保护 active 原文，但不是跨 Mnemon SQLite 与文件系统的可回滚分布式事务。索引后发生 revision 冲突时可能保留重复引用；系统选择保留数据。

### 后台水位

活动评分、最近 checkpoint 与重试状态未持久化；Host 重启会清空未处理活动。失败退避、熔断和人工重试入口尚未实现。

### 版本与国际化

支持 DSH 0.1.7-rc.2 与 0.2.0-rc.2，参见[兼容性矩阵](../reference/compatibility.md)。Web 界面为中英文双语，但命令、工具卡、兼容元数据和部分错误仍未完全国际化。

## 文档归档恢复

文档归档不再要求子代理为 remember/recall 回执编号。旧版本失败后若留下冷索引而文档仍为 active，重试可复用精确路径与当前内容哈希一致的索引；文档更新后需要匹配新修订的索引。异步提取或不支持安全删除的 Provider 会在写入前被拒绝。清理失败会报告目标空间和本次新建的 id；确认结果前应保留已有数据。

## 运行时归档恢复

容量归档要求已激活的记忆空间，其 Provider 必须支持精确写入和安全删除。Hindsight 等异步提取目标会在任何归档写入前被排除。如果没有合适的目标，请激活支持这些能力的记忆空间，或增大 `runtimeMemory.memoryLimitBytes`；被拒绝的 mutation 不会改变现有热记忆。直接向 Provider 写入仍保持原有的异步行为。

对话归档使用所选 Source 的写入范围：回合中激活的已知空间，以及当前 View 创建的空间，都可作为归档目标。在 View 固定之后由其他操作创建的空间，需要新回合才能使用。空范围始终不授权任何空间。目标错误会区分目录为空、View 范围为空或排除了当前空间、Provider 能力不支持，并报告目录、授权和可写数量。失败时待新增内容尚未保存；修正所报告的原因后，请在新回合重试同一份工具输入。已有热记忆会保留。

归档回执或本地提交失败时，Host 仅尝试删除回执能证明由本次操作新建的条目，保留跳过或复用的既有条目。本地提交报错后，如果 Runtime 修订已变更或无法读取，则提交结果不确定：保留归档条目，避免丢失已提交的记忆。清理失败会标出剩余目标空间和条目 id。Provider 请求失败且没有返回回执时，远端结果可能不确定，因此这不是分布式事务；重试前应检查对应 Provider。
