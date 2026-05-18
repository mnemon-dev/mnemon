# Eval Loop MVP Design

英文版本：[DESIGN.md](../../../harness/eval-loop/DESIGN.md)

可安装 MVP 资产：[harness/modules/eval-loop](../../../../harness/modules/eval-loop/README.md)

Eval loop 是 Mnemon 的 feedback-facing harness module。它定义如何通过真实
scenario 测试 HostAgent，如何收集证据，以及如何把稳定失败转化为经过治理的
改进候选。

## 定位

Eval loop 与 memory-loop、skill-loop 是平级模块，不是它们的父模块。
memory-loop 和 skill-loop 直接影响 HostAgent interface：前者影响记忆上下文，
后者影响可复用工作方法。eval-loop 通过 scenario 执行观察这些影响，并把发现
反馈回项目。

```text
harness/modules/
├── memory-loop
├── skill-loop
└── eval-loop
```

## 核心模型

```text
scenario
   |
   v
isolated workspace + .mnemon + host projection
   |
   v
Codex app server HostAgent
   |
   v
artifacts: transcript, diff, memory state, skill evidence, logs
   |
   v
rubric judgement
   |
   v
report and improvement candidate
```

Codex app server 是当前 primary HostAgent。通用 HostAgent requirement 应该从
Codex-first 场景中持续归纳，而不是一开始就前置设计。

## 资产

| Asset | 作用 |
| --- | --- |
| Scenario | 可复现的任务压力场景，包含 target、setup、prompt、evidence 和预期观察。 |
| Suite | 一组 scenarios 和 loop configuration。 |
| Rubric | 行为判断和 eval asset 质量判断标准。 |
| Skill | eval plan、run、analyze、improve 的 protocol 方法。 |
| Evaluator | 后台 curation worker，用于去重 candidates、总结趋势。 |

## 生命周期

Eval assets 的生命周期应比 skills 更严格，因为它们定义项目如何判断自己是否
变好。

```text
ephemeral -> candidate -> promoted -> canonical -> retired
```

- `ephemeral`：临时探索，不需要审计。
- `candidate`：有初步证据的候选资产。
- `promoted`：经过整理，可用于本地回归。
- `canonical`：稳定，可用于长期对比或 gate。
- `retired`：过时、不稳定或被替代的资产。

这样可以降低 review 压力：agent 可以自由探索，但只有稳定且有价值的资产才进入
promotion 审阅。

## 第一阶段范围

第一批场景聚焦 Mnemon 当前的自迭代工作：

- memory preference recall
- skill creation and reuse
- bilingual documentation synchronization
- host projection smoke checks

这些场景当前主要评估 memory-loop 和 skill-loop，但 eval-loop 框架本身更通用。
它也可以评估 setup、host adapter、docs workflow、commit discipline，以及
eval-loop 自身。
