# Host Projection

英文版本：[HOST_PROJECTION.md](../../harness/HOST_PROJECTION.md)

本文定义 Mnemon loop module 如何投影到具体宿主 runtime，例如 Claude Code、
Codex、OpenClaw，或未来的 app-server eval host。

Loop Module Standard 定义 canonical package shape。Host Projection 定义这套
package 如何在某个宿主 runtime 中变得可见、可执行。

## 原则

Mnemon 把 canonical harness state 保存在 `.mnemon`。宿主目录只保存可重新生成的 projections。

```text
.mnemon/
  canonical state, loop modules, reports, proposals, audit
      |
      | 由 setup/<host> 投影
      v
.claude/ or .codex/
  宿主可读的 skills、hooks、config，以及指回 .mnemon 的路径
      |
      v
host runtime
```

Projection adapter 不应该制造另一份真实状态。它只渲染足够的宿主原生文件，
让宿主能够发现和使用 loop，同时把持久状态保留在 `.mnemon` 下。

## 职责

Host projection adapter 负责：

| 职责 | 说明 |
| --- | --- |
| Path resolution | 解析 project root、host config directory、canonical `.mnemon`、active store 和 loop module path。 |
| Asset projection | 渲染或复制宿主可读的 GUIDE、hooks、protocol skills 和 subagents。 |
| Hook registration | 当宿主支持时，注册宿主生命周期 hooks。 |
| Environment injection | 让 `MNEMON_DATA_DIR`、`MNEMON_STORE`、`MNEMON_HARNESS_DIR` 和 loop-specific env 对 hooks 和 skills 可见。 |
| Manifest writing | 在 `.mnemon/hosts/<host>/manifest.json` 记录投影了什么、投影到哪里。 |
| Validation | 检测缺失资产、过期 projection、不兼容宿主能力和路径冲突。 |
| Uninstall | 删除宿主 projection 文件，默认保留 canonical `.mnemon` 状态。 |

## 非职责

Host projection adapter 不应该：

- 重新实现 Mnemon memory storage 或 retrieval。
- 把 canonical state 移动到 `.claude`、`.codex` 或其他宿主目录。
- 把宿主特定行为隐藏在 loop module 根目录文件里。
- 修改声明区域之外的用户宿主配置。
- 删除 memory、reports、proposals 或 audit records，除非用户显式要求破坏性清理。

## Canonical Layout

目标 canonical layout：

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

当前 MVP scripts 仍可能把 loop runtime files 放在 host config 目录下。新的
projection adapters 应逐步转向 canonical `.mnemon` 布局，并把 host directories
作为 generated views。

## Projection Layouts

### Claude Code

Claude Code projection 使用宿主原生的 skill、hook、subagent 和 settings surface。

```text
.claude/
├── skills/
│   └── <projected protocol skills>
├── hooks/
│   └── <projected hook entrypoints>
├── agents/
│   └── <projected subagents>
└── settings.json
```

Claude Code projection 应该：

- 在 `settings.json` 中注册 lifecycle hooks。
- 让生成的 hook entrypoints 保持很小。
- 尽可能从 canonical `.mnemon` 位置 source Mnemon env files。
- 把 policy 保留在 `GUIDE.md` 和 hook prompts 中，而不是 shell glue 中。

### Codex

Codex projection 应遵循同一个 canonical model，同时渲染到 Codex-native surfaces。

```text
.codex/
├── skills/
│   └── <projected protocol skills>
├── hooks/
│   └── <projected lifecycle adapters, when supported>
├── agents/
│   └── <projected maintenance agents, when supported>
└── config/
    └── <runtime or app-server config>
```

Codex projection 应该：

- 把 protocol skills 投影到 Codex skill surface。
- 当 Codex 支持对应 hook 时，把 lifecycle events 映射过去。
- 当 direct hooks 不可用时，使用 app-server lifecycle endpoints 作为降级路径。
- 通过 env 或 runtime config 把 canonical `.mnemon` paths 传给 app server 和 skills。
- 把 eval artifacts 写入 `.mnemon/reports`、`.mnemon/proposals` 和 `.mnemon/audit`。

Codex 的精确路径可能会随 Codex host capabilities 演化。Adapter 应该把实际选择的路径记录在 `.mnemon/hosts/codex/manifest.json`。

## Lifecycle Mapping

Host adapters 把 Mnemon lifecycle events 映射到宿主 native events：

| Mnemon Event | Claude Code Projection | Codex Projection | Fallback |
| --- | --- | --- | --- |
| `prime` | Session start hook。 | Session init hook 或 app-server session start。 | 显式 `/lifecycle/prime` eval call。 |
| `remind` | User prompt hook。 | Request 或 message boundary hook。 | 显式 `/lifecycle/remind` eval call。 |
| `nudge` | Stop 或 turn-end hook。 | Turn-end hook 或 response finalization。 | 显式 `/lifecycle/nudge` eval call。 |
| `compact` | Pre-compact hook。 | Compact、checkpoint 或 context-save event。 | 显式 `/lifecycle/compact` eval call。 |
| `maintenance` | Subagent 或 manual task。 | Subagent、background task 或 app-server job。 | 显式 maintenance command。 |

这个 mapping 是语义映射，不要求一对一。如果宿主不能提供完全对应的 lifecycle
event，adapter 应选择最接近且安全的边界，并在 host manifest 中记录。

## Host Manifest

每次 projection 都应该写入 host manifest：

```text
.mnemon/hosts/<host>/manifest.json
```

推荐结构：

```json
{
  "schema_version": 1,
  "host": "codex",
  "installed_at": "2026-05-14T00:00:00Z",
  "project_root": "/path/to/project",
  "mnemon_dir": "/path/to/project/.mnemon",
  "store": "default",
  "loops": {
    "memory-loop": {
      "module_path": ".mnemon/harness/memory-loop",
      "module_version": "0.1.0",
      "projection_path": ".codex",
      "projected_assets": {
        "skills": [".codex/skills/memory_get.md"],
        "hooks": [".codex/hooks/prime.sh"],
        "subagents": []
      },
      "lifecycle_mapping": {
        "prime": "session-init",
        "remind": "message-boundary",
        "nudge": "turn-end",
        "compact": "explicit-eval"
      }
    }
  }
}
```

Manifest 是 setup、status、uninstall 和 eval tooling 之间的桥。

## Setup Contract

所有 host adapters 应支持同一组高层操作：

```text
install
  validate loop module manifests
  resolve canonical .mnemon
  install canonical loop assets if needed
  render host projection
  register hooks/config
  write host manifest

status
  read host manifest
  validate projected files exist
  validate registered hooks/config
  report stale or missing projections

uninstall
  remove projected host files
  unregister hooks/config
  preserve canonical .mnemon state by default
  update or remove host manifest
```

`status` 对 app-server eval 很重要，因为 orchestrator 可以用它确认当前 run
正在测试预期的 projection。

## App-Server Eval Host

App-server eval host 是用于测试 loop 行为的一次性宿主 runtime。它应该使用与真实宿主相同的 projection contract：

```text
eval orchestrator
    |
    | create isolated workspace and .mnemon
    | run setup/<host>/install
    | start host app server
    v
host app server
    |
    | API-driven scenarios
    v
harness loop projection
    |
    v
Mnemon engine and canonical state
```

Eval 应测试 harness influence 下的 host behavior，而不是只测试 Mnemon CLI CRUD。
有价值的断言包括：

- App server 使用隔离的 `.mnemon`。
- 安装了预期版本的 loop modules。
- Lifecycle events 通过 manifest 声明的 mapping 被调用。
- Recall decisions 影响后续任务行为。
- Writeback decisions 只在合理时写入 durable memory。
- Reports、proposals 和 audit records 写入 canonical locations。

## 质量规则

- Projection files 应保持小而明确，并从 canonical assets 生成。
- Host-specific behavior 放在 `setup/<host>/` 或生成的 adapter files。
- Setup 应尽可能可重复、幂等。
- Uninstall 应保守，默认保留 canonical state。
- Manifest paths 尽可能使用相对路径；只有 runtime execution 需要时才使用绝对路径。
- 公开 projection 行为必须同时维护英文和中文文档。

