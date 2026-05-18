# Codex App-Server Eval

这个 eval 模式使用真实的 Codex app-server，而不是 mock server。它会在
`.testdata` 下创建一次性的隔离运行目录，把 Mnemon loop module 投影到生成的
workspace 中，然后启动：

```bash
codex app-server --listen stdio://
```

默认 smoke 流程会通过 JSON-RPC 调用 `initialize`、`skills/list` 和
`thread/start`，验证真实 Codex app-server 能读取被 harness 注入的 `.codex`
技能和 `.mnemon` 状态：

```bash
make codex-app-eval
```

memory/skill 场景套件会启动真实 Codex turn，并断言 loop 行为：

```bash
make codex-app-eval-suite
```

当前套件覆盖：本地上下文应跳过 memory recall、相关长期记忆应被 recall、持久
决策应写入 `MEMORY.md`、临时信息不应污染 memory，以及 skill evidence
应写入 JSONL。

更长的 memory loop 回归可以运行：

```bash
make codex-memory-deep-eval
```

deep memory suite 会额外覆盖：带噪声的相关 recall、过期 memory 覆盖、
不确定偏好拒绝、疑似 secret 值拒绝，以及通过持久化 `MEMORY.md` 完成多轮连续性。

更长的 skill loop 回归可以运行：

```bash
make codex-skill-deep-eval
```

deep skill suite 会额外覆盖：跳过临时 evidence、记录 missing-skill evidence、
执行已批准的 active skill 创建、保护 host skill surface，以及 proposal-first
curation 不直接激活 skill，并验证 reviewable skill draft 的 authoring。

如果需要触发真实 Codex turn，可以显式开启：

```bash
python3 scripts/codex_app_server_eval.py --agent-turn
```

真实 turn 会使用本机 Codex 认证，并可能消耗模型额度。

每次运行都会生成：

```text
.testdata/codex-app-eval/<timestamp>/
├── workspace/          # Codex 看到的隔离项目目录
├── workspace/.codex/   # Codex host projection
├── .mnemon/            # Mnemon canonical harness state
├── logs/               # app-server stderr
└── reports/            # JSON eval report
```
