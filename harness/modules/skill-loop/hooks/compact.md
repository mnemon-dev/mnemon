# Compact Hook

## Runtime Moment

Run before context compaction, summarization, release handoff, or another
low-frequency maintenance boundary.

## Output To HostAgent

Apply `GUIDE.md`; if accumulated evidence needs review, load
`skills/skill_curate.md` or spawn the curator subagent.

Do not apply lifecycle mutations directly from this hook.

## Expected Effect

The HostAgent treats compaction as a natural review boundary while keeping
proposal generation separate from online task work.
