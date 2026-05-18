# Loop Module Standard

英文版本：[LOOP_MODULE_STANDARD.md](../../harness/LOOP_MODULE_STANDARD.md)

本文定义 Mnemon harness loop module 的标准结构。这个标准与宿主无关。
Claude Code、Codex、OpenClaw 或未来 runtime 都应该通过各自的 host projection
adapter 使用同一套 loop module。

## 核心模型

Mnemon 把 canonical harness state 和 host runtime projection 分开：

```text
.mnemon canonical state
    |
    | 由 host adapter 投影
    v
.claude / .codex / 其他宿主配置
    |
    v
host runtime
```

Loop module 拥有 policy、lifecycle reminders、protocol skills、maintenance
agents、environment contract 和 setup adapters。宿主 runtime 拥有 conversation
loop、prompt assembly、tool routing、native skill discovery、权限模型和 UI。

## 标准目录

每个可安装 loop module 应该遵循这个结构：

```text
harness/modules/<loop-name>/
├── README.md
├── module.json
├── env.sh
├── GUIDE.md
├── hooks/
│   ├── prime.md
│   ├── remind.md
│   ├── nudge.md
│   └── compact.md
├── skills/
│   └── <protocol-skill>.md
├── subagents/
│   └── <maintenance-agent>.md
```

Host-specific projection logic 位于 modules 之外：

```text
harness/hosts/<host>/
├── projector.sh
├── templates/
└── scripts/
```

Shared setup entrypoints 负责组合 modules 和 hosts：

```text
harness/setup/
├── install.sh
├── status.sh
└── uninstall.sh
```

如果某个 loop 的契约需要额外 runtime 文件，可以加入该目录，例如 Memory Loop
的 `MEMORY.md`。

## 概念

| 概念 | 是否必需 | 作用 |
| --- | --- | --- |
| `module.json` | 是 | 机器可读的 loop identity、资产声明、state 目录、lifecycle events 和已支持 host adapters。 |
| `GUIDE.md` | 是 | 定义 loop 何时应该行动、宿主 agent 应该如何判断，以及哪些内容不属于该 loop。 |
| `env.sh` | 是 | scripts、hooks、protocol skills 和 maintenance agents 使用的运行时路径契约。 |
| `hooks/*.md` | 是 | 与宿主无关的 lifecycle reminders。描述 agent 在生命周期边界应考虑什么。 |
| `skills/*.md` | 通常是 | 用于在线可复用操作的 protocol skills。它们定义流程，不定义宿主安装方式。 |
| `subagents/*.md` | 可选 | 用于较重 review、consolidation 或 proposal generation 的维护角色。没有 native subagent 的宿主可以降级为人工或定时 job。 |
| `harness/hosts/<host>/` | 整体至少一个 host | Host-specific projection adapter，把 modules 安装或移除到某个宿主 runtime。 |

## 生命周期事件

Mnemon 标准化 lifecycle 词汇，让不同宿主可以把自己的 native extension points
映射到同一套 loop semantics。

| 事件 | 含义 | 常见用途 |
| --- | --- | --- |
| `prime` | Session 或 runtime 启动。 | 让 loop policy、重要 state 和 active surfaces 可见。 |
| `remind` | 用户请求或任务边界。 | 判断 recall、observation 或其他 loop action 是否会改变当前任务。 |
| `nudge` | 回合结束或工作完成。 | 判断 durable writeback、evidence capture 或 report generation 是否有必要。 |
| `compact` | Context compaction 或 checkpoint 边界。 | 保存关键连续性，并在 state 过大或过旧时触发维护。 |
| `maintenance` | 离线或显式维护任务。 | 运行较重的 consolidation、curator review、evaluation、audit 或 proposal 工作。 |

Adapter 可以优雅降级。如果宿主没有完全对应的 hook，可以映射到最接近的
lifecycle boundary，或通过 app-server eval API 显式触发。

## Host Projection

Host projection adapter 把 canonical loop module 渲染到宿主原生 surface。投影不能制造第二份真实状态。

```text
canonical loop module
    |
    | install / project
    v
host-native files
```

典型职责：

- 解析 canonical `.mnemon` 和 project-local paths。
- 复制或引用 loop assets。
- 渲染宿主可读的 skills、hooks 和配置。
- 当宿主支持时注册 native lifecycle hooks。
- 在 `.mnemon/hosts/<host>/` 下写入 host manifest。
- 卸载时保留 canonical state，除非用户显式要求破坏性删除。

## Canonical State

Canonical state 属于 `.mnemon`，不属于某个宿主目录。`.claude` 或 `.codex`
这类宿主目录只保存 projections。

推荐布局：

```text
.mnemon/
├── data/
│   └── <store>/mnemon.db
├── harness/
│   ├── memory-loop/
│   └── skill-loop/
├── reports/
├── proposals/
├── audit/
├── hosts/
│   ├── claude-code/
│   │   └── manifest.json
│   └── codex/
│       └── manifest.json
└── manifest.json
```

当前 MVP setup scripts 仍可能把 runtime files 放在 host config 目录下。新的
adapters 应逐步转向 canonical `.mnemon` 布局，并把 host directories 只作为
projection surfaces。

## Manifest Schema

每个 loop module 应该包含一个 `module.json` 文件，使用这个稳定结构：

```json
{
  "schema_version": 1,
  "name": "memory-loop",
  "version": "0.1.0",
  "description": "Connects prompt-facing working memory with Mnemon long-term memory.",
  "lifecycle_events": ["prime", "remind", "nudge", "compact"],
  "assets": {
    "guide": "GUIDE.md",
    "env": "env.sh",
    "hooks": {
      "prime": "hooks/prime.md",
      "remind": "hooks/remind.md",
      "nudge": "hooks/nudge.md",
      "compact": "hooks/compact.md"
    },
    "skills": ["skills/memory_get.md", "skills/memory_set.md"],
    "subagents": ["subagents/dreaming.md"]
  },
  "state": {
    "canonical": [".mnemon/data", ".mnemon/reports", ".mnemon/proposals", ".mnemon/audit"],
    "loop_runtime": []
  },
  "host_adapters": {
    "claude-code": "../../hosts/claude-code"
  }
}
```

在 MVP 阶段，manifest 是描述性的。后续 setup tooling 可以基于这些 metadata
验证 loop modules、生成 projections，并驱动 app-server eval。

## Adapter Mapping

同一个标准概念在不同宿主中有不同投影方式：

| Loop Standard | Claude Code Projection | Codex Projection |
| --- | --- | --- |
| `GUIDE.md` | Claude Code 可见的 prompt guide 或 skill guidance。 | Codex 可见的 instruction 或 skill guidance。 |
| `hooks/prime.md` | Session-start hook。 | Session init hook 或 app-server lifecycle endpoint。 |
| `hooks/remind.md` | User-prompt hook。 | Request 或 message boundary hook。 |
| `hooks/nudge.md` | Stop 或 turn-end hook。 | Turn-end hook 或 app-server lifecycle endpoint。 |
| `hooks/compact.md` | Pre-compact hook。 | Compact、checkpoint 或显式 eval lifecycle endpoint。 |
| `skills/*.md` | `.claude/skills` projection。 | `.codex/skills` 或 Codex skill surface projection。 |
| `subagents/*.md` | 可用时投影为 native subagent。 | Codex subagent、task adapter 或 maintenance job。 |
| `env.sh` | 被 hook scripts source，并注入上下文。 | 被 Codex adapter 和 app-server eval runtime source。 |

## 质量规则

- Loop modules 默认保持 host-agnostic。
- Host-specific code 只放在 `setup/<host>/`。
- 不要把 canonical state 复制成宿主目录下的第二份真实状态。
- 把 host directories 视为可重新生成的 projection。
- setup、status 和 uninstall 行为必须明确、可审计。
- 卸载时保留用户状态，除非用户显式传入破坏性选项。
- 新增或修改公开 harness 概念时，同步维护英文和中文文档。
