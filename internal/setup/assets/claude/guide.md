### Recall — before responding

**Default: recall on every new user message**, unless ALL of these apply:
- Direct follow-up within a topic already fully in context
- No reference to past sessions, decisions, or preferences
- No knowledge dependency beyond the current conversation

**Before web search**: always recall first — stored context sharpens queries.

To recall: `mnemon recall "<query>" --limit 5`.
Craft a focused, keyword-rich query — do not pass the raw user prompt.

### Remember — after responding

Run this decision tree after every substantive response.
**Bias toward storing**: when in doubt, store it. Low-importance memories are cheap; missing context is expensive.

**Step 1 — Does this exchange contain any of the following?**

Tier A (importance 4-5, always store):
- User directive — explicit preference, decision, correction, or "remember this"
- Reasoning conclusion — non-trivial judgment from multi-source synthesis
- Durable system/architectural fact discovered during this session
- User-specific context that no search engine can recover

Tier B (importance 2-3, store unless trivial):
- Casual preference revealed in passing ("I usually...", "I prefer...", "I don't like...")
- Topic the user is currently exploring or curious about
- Useful framing or analogy the user offered
- Background context about the user's projects, tools, or setup
- Interesting question the user raised, even if not fully resolved

Tier C (importance 1, store only if genuinely reusable):
- Conversational context that might help future sessions feel continuous
- Soft signal about communication style or mood

→ None of the above → STOP.

**Step 2 — Does a highly overlapping memory already exist?**
→ Yes, incremental new info → UPDATE (merge into existing)
→ Yes, but contradicts/supersedes → REPLACE
→ No significant overlap → CREATE

**Step 3 — Importance calibration**
Use the full 1-5 scale intentionally:
- 5: Cross-session core fact, architectural decision, strong user preference
- 4: Important context, significant finding, clear user preference
- 3: Useful background, project context, topic of interest
- 2: Passing mention, soft preference, conversational color
- 1: Ephemeral but potentially useful continuity

Aim for a rough distribution: ~20% at 4-5, ~50% at 2-3, ~30% at 1.
Avoid defaulting everything to 4-5 — that defeats the scoring system.

**What to store**: both conclusions AND context. Prefer storing a little too much over missing something useful.
**How to store**: delegate to a Task sub-agent (`subagent_type="general-purpose"`, `model="sonnet"`).
Only provide what to store — content, category, importance, entities, and create/update intent.
The sub-agent will read the mnemon skill and execute the correct commands itself.

Do NOT: write CLI commands or workflow steps in the sub-agent prompt (the sub-agent has access to the skill docs and will use the correct flags).
Do NOT run memory writes in the main conversation, or remember operational/public/git-tracked/transient info.