# Skill Loop MVP 设计

相关可视化页面：[site.html](../../../site/skill-loop/site.html)

英文版本：[DESIGN.md](../../../harness/skill-loop/DESIGN.md)

可安装 MVP 资产：[harness/modules/skill-loop](../../../../harness/modules/skill-loop/README.md)

Skill loop 的目标是让宿主 Agent 拥有一套可自我演进的 skill library，同时不替换宿主原生的 skill runtime。Skill 仍然是宿主可发现、可调用的原生资产；Mnemon 负责保存 canonical lifecycle state，以及支撑演进判断的 evidence。

MVP 的边界是“可见性治理”和“生命周期治理”：哪些 skill 当前应该可被发现，哪些进入维护，哪些仅保留为历史。它不把所有 skill 注入 prompt，也不要求新建或 patch 后的 skill 在当前 session 立即 reload。

## 目标

- 让 HostAgent 继续拥有执行、原生 skill discovery、subagent 调用和 tool routing。
- 在 `.mnemon` 下保存 canonical skill state，并划分为 `active`、`stale`、`archived`。
- 沿用 self-evolution harness 的通用概念：GUIDE、setup、hook、protocol skill、subagent。
- 在线只记录轻量 evidence，后续通过 curator 审阅和 proposal 修改 skill。
- 新的 active skill 集合在下一次 Prime 边界生效，而不是强制当前 session reload。

## 三大核心主体

| 主体 | 运行时职责 | 边界 |
| --- | --- | --- |
| HostAgent | 执行任务，拥有 ReAct loop、hook bus、prompt assembly、tool routing，以及宿主原生 skill/subagent 调用。 | 不拥有 canonical skill state。它决定何时加载 protocol skill，但 `.mnemon` 才是 source of truth。 |
| Host Skill Surface | 宿主原生的 skill discovery 位置，例如 `.claude/skills`。Host runtime 按自己的机制读取这里。 | 由 Prime 从 `.mnemon/skills/active` 生成、同步或挂载。它是 view，不是 canonical store。 |
| `.mnemon` Skill Library | 保存 skill 和 usage state 的 canonical filesystem：`skills/active`、`skills/stale`、`skills/archived`，以及 usage sidecar 或 signal report。 | 所有 lifecycle mutation 都通过 `skill_manage` 发生在这里。宿主目录应被视为 generated output。 |

关键区分是：HostAgent 拥有行为执行，`.mnemon` 拥有持久 skill state。Harness 通过 Prime 把 active skills 投射到 host-facing surface。

## Harness 概念

| 概念 | Skill Loop 资产 | 职责 | 边界 |
| --- | --- | --- | --- |
| GUIDE | `GUIDE.md` | 定义什么算 skill evidence、reusable workflow signal、review trigger、protected/pinned skill，以及 proposal-first policy。 | 只定义 policy，不生成、不 patch、不移动、不 archive skill。 |
| setup | setup scripts 和 bindings | 安装 hooks、protocol skills、curator subagent，并配置 host-native skill surface binding。 | 只负责安装和挂载，不参与每次 runtime 判断。 |
| hook | `prime`、`remind`、`nudge`、`compact` | 提供时机：Prime 同步 active skills，Nudge 提醒模型观察 evidence，Compact 可作为低频 review 边界，Remind 通常 no-op。 | hook 应保持短小；规则在 GUIDE 中，动作在 protocol skill 中。 |
| protocol | `skill_observe.md`、`skill_curate.md`、`skill_manage.md` | 定义 HostAgent 可加载的跨宿主流程：observe、curate、manage。 | protocol skill 通过 harness 环境定位 `.mnemon`，例如 `MNEMON_HARNESS_DIR`。 |
| subagent | `curator` | 低频审阅 evidence 和 skill library，并提出 create、patch、consolidate、stale、archive、restore 方案。 | 默认 proposal-first。批准后的变更由 `skill_manage` 执行。 |

## 生命周期模型

| 状态 | 含义 | 宿主可见性 |
| --- | --- | --- |
| `active` | 当前应该被宿主发现和使用的 skill。 | Prime 只把这个状态同步或挂载到 Host Skill Surface。 |
| `stale` | 当前不应默认暴露，但仍可审阅、修复、恢复或合并的 skill。 | 默认不可见。curator review 和显式 restore workflow 可读取。 |
| `archived` | 为审计、恢复和设计记忆保留的历史 skill。 | 默认不可见。MVP 中优先 archive，而不是 delete。 |

Lifecycle movement 应保守执行：

- `active -> stale`：当 evidence 显示低使用、被替代、重复、适配差或容易误导。
- `stale -> active`：当 review 认为 skill 仍有价值、已修复，或应该恢复。
- `stale -> archived`：当 skill 已过时，不应再进入常规 restore 候选。
- `archived -> stale` 或 `archived -> active`：只通过显式 restore proposal。

Protected 或 pinned skill 不应被自动迁移，除非 proposal 明确说明例外并获得批准。

## 运行时流程

```text
Prime 暴露 active skills
  -> host 使用原生 skill discovery
  -> Nudge 询问本轮是否产生 evidence
  -> skill_observe 只记录 evidence
  -> curator 审阅 evidence 并生成 proposal
  -> skill_manage 执行已批准的 canonical change
  -> 下一次 Prime 暴露新的 active set
```

### 1. Prime

Prime 是 `.mnemon` 与 host-native skill surface 之间的同步边界。

输入：

- GUIDE policy。
- `.mnemon/skills/active`。
- setup 创建的宿主绑定。

动作：

- 从 `.mnemon/skills/active` 同步、挂载或生成 host-native skill files。
- 让 `stale` 和 `archived` 默认不进入 host discovery path。
- HostAgent 仍通过原生机制发现和调用 skill。

边界：

- Prime 不把每个 skill body 注入 prompt。
- Prime 不决定创建、patch 或 archive 哪个 skill。
- host-native skill 目录是 generated view；`.mnemon` 是 canonical state。

### 2. Remind

Remind 在 skill loop 中通常是 no-op，因为宿主 Agent 已有原生 skill discovery。Memory loop 中 Remind 可以询问是否需要 recall；但 skill loop 如果每轮重复提醒 discovery，通常只会增加噪声。

如果某个宿主缺少原生 skill discovery，或确实需要轻量提醒，Remind 可以作为 host-specific fast path。它不是 MVP 默认路径。

### 3. Nudge

Nudge 运行在 agent-loop stop boundary，是一句短提醒。

动作：

- 要求模型遵循 GUIDE。
- 询问本轮是否产生 skill usage evidence 或 reusable workflow signal。
- 如果有，HostAgent 应加载 `skill_observe.md`。

边界：

- Nudge 不写 `.usage.json`。
- Nudge 不生成或 patch skill。
- Nudge 不运行 curator review。
- Nudge 只触发“是否 observe”的判断。

这样可以保持在线路径轻量：没有值得记录的 evidence 时，正常任务流不会被打断。

### 4. `skill_observe`

`skill_observe.md` 是在线轻量 protocol skill。它记录 evidence，但不把 evidence 解释成 lifecycle 决策。

可能输入：

- 某个 skill 被查看、选择或使用。
- 某个 skill 帮助完成了任务。
- 某个 skill 缺失、误导、过时，或导致失败路径。
- 用户对 workflow 给出反馈。
- Agent 重复执行了一个可能值得沉淀为 skill 的流程。
- 人工 patch 了 skill，需要记录为 evidence。

动作：

- 写入 usage sidecar，例如 `.mnemon/skills/.usage.json`；或在实现选择 report 文件时写入 signal report。
- 保留 curator review 所需的最小上下文：skill id、event type、task context、outcome，以及可选 evidence note。

边界：

- `skill_observe` 只记录 evidence。
- 它不决定是否生成新 skill。
- 它不修改 `active`、`stale`、`archived`。
- 它应避免保存敏感任务数据，除非 GUIDE 允许且 evidence 确实需要。

### 5. Curator Review

Curator 是低频维护 subagent。它可以手动运行，也可以在 compact/dreaming-like 边界、HostAgent scheduler 或足够强的 signal 后运行。

输入：

- GUIDE review policy。
- `.mnemon/skills/active`、`.mnemon/skills/stale`、`.mnemon/skills/archived` 中的现有 skills。
- usage sidecar 和 signal reports。
- 可选的宿主约束，例如 skill 格式或命名规则。

动作：

- 审阅 evidence 是否支持 create、patch、consolidate、active -> stale、stale -> archived、restore 等操作。
- 在合适时起草 `SKILL.md` 内容或 patch proposal。
- 输出 proposal 或 review report。

边界：

- Curator 不是每个任务都会执行的在线步骤。
- Curator 默认 proposal-first。
- Curator 不应直接启用新的 active skill。
- Curator 应显式说明不确定性、缺失 evidence 和风险，而不是把它们隐藏在 patch 中。

### 6. `skill_manage`

`skill_manage.md` 把已批准的 lifecycle 和内容变更应用到 `.mnemon`。

MVP 允许的操作：

- 批准后在 `active` 中创建 proposed skill。
- patch 现有 skill。
- 合并重复 skill。
- 移动 `active -> stale`。
- 移动 `stale -> archived`。
- 恢复 `stale -> active`。
- 在明确批准时恢复 `archived -> stale` 或 `archived -> active`。
- 更新 lifecycle 所需的 metadata 和 usage bookkeeping。

边界：

- `skill_manage` 修改 canonical `.mnemon` state，不直接修改宿主 runtime。
- 非平凡变更不应绕过 proposal-first review。
- protected 或 pinned skill 应跳过，除非批准的 proposal 明确覆盖。
- MVP 中优先 archive over delete。
- 新 active set 只有在下一次 Prime sync 后才对宿主可见。

## 当前 Session 生效边界

MVP 不强制新建或 patch 后的 skill 在当前 session reload。这是明确设计边界。

原因：

- 不同宿主 runtime 的 skill discovery cache 行为不同。
- 强制 reload API 通常是 host-specific，会降低 harness 的可移植性。
- 当前 session 可能已经基于旧 skill set 形成了 prompt 和 tool state。
- 下一次 Prime 是清晰、确定的刷新边界。

如果某个宿主支持 cache invalidation 或 immediate reload，setup 后续可以把它作为可选 fast path。可移植 contract 仍然是：`skill_manage` 更新 `.mnemon`；下一次 Prime 把 active set 投射到 Host Skill Surface。

## MVP Scope

MVP 范围内：

- canonical `.mnemon/skills/{active,stale,archived}` 布局。
- Prime 从 `active` 同步到 Host Skill Surface。
- GUIDE 定义 evidence、review trigger、lifecycle state 和 proposal-first 规则。
- Nudge 提醒模型判断是否需要 observe。
- `skill_observe` 记录 evidence。
- Curator 生成 proposal。
- `skill_manage` 执行已批准的 lifecycle mutation。
- 保守的 restore 和 archive flow。

MVP 范围外：

- 替换宿主原生 skill runtime。
- 把所有 skill content 注入 prompt。
- 保证当前 session 立即 reload skill。
- 不经 proposal review 的全自动 skill creation。
- 把删除 archived skill 作为常规生命周期动作。
- 全局 marketplace 发布或跨用户 skill sharing。
- 超出宿主原生 discovery 的复杂 ranking、embedding search 或 adaptive skill selection。
- 把 skill loop 当作 memory storage。持久任务事实属于 memory loop，不属于 skill state。

## 风险边界

- **Prompt 或 discovery 噪声：** active skills 过多会降低宿主行为质量。Curator 应把低价值或重复 skill 移到 stale。
- **Evidence 污染：** `skill_observe` 应记录结构化、可审阅 signal，避免把每个任务细节都变成 skill evidence。
- **过早自动化：** 从单个弱 signal 直接创建或 patch skill，容易固化错误 workflow。Curator 应要求 evidence 并 proposal-first。
- **状态漂移：** host-native skill 目录必须被视为 generated view。人工修改应迁移回 `.mnemon`，否则可能被 Prime 覆盖。
- **Protected skills：** pinned、built-in 或 safety-critical skill 需要显式处理，不应被静默迁移。
- **敏感数据：** skill 应描述可复用 procedure，而不是私有任务内容。Evidence sidecar 只保留 review 必需的最小上下文。
- **宿主可移植性：** sync/mount、短 hook 和 protocol skill 之外的能力应作为 host-specific extension，而不是基础 contract。

## 职责矩阵

| 概念 | 资产 | 运行时职责 | 边界 |
| --- | --- | --- | --- |
| Host runtime | HostAgent | 运行 ReAct loop，接收 hooks，决定是否加载 protocol skill 或 curator subagent。 | 不拥有 canonical skill state。 |
| Host-facing surface | Host Skill Surface | 宿主原生 skill discovery 读取的位置。 | 由 Prime 从 `.mnemon/skills/active` 生成或挂载。 |
| Canonical store | `.mnemon` Skill Library | 保存 active、stale、archived skills 和 usage evidence。 | Source of truth；host-native 目录只是 view。 |
| GUIDE | `GUIDE.md` | 定义 evidence、review trigger、protected/pinned 规则和 proposal-first policy。 | 只定义 policy，不做迁移。 |
| setup | setup + bindings | 安装 hooks、protocol skills、curator subagent 和 host-native skill surface binding。 | 只负责安装和挂载。 |
| hook | `prime/remind/nudge/compact` | 提供同步、observe 提醒和低频 review 边界。 | 只提供时机；规则在 GUIDE。 |
| protocol | `skill_observe` / `skill_curate` / `skill_manage` | 定义 observe、curate、manage 的执行流程。 | 通过 harness environment 定位 `.mnemon`。 |
| subagent | curator | 执行低频 review、合并、proposal 和 report。 | 默认 proposal-first；批准后通过 `skill_manage` 修改状态。 |
