# Compact Hook

## Runtime Moment

Run before context compaction, summarization, or any boundary where important
session context may be lost.

## Output To HostAgent

Apply `GUIDE.md` and decide whether any critical continuity should survive the
context boundary.

If so, load `skills/memory_set.md` and write only the minimal necessary update
to `MEMORY.md`. Preserve decisions, constraints, unresolved continuity, and
state that would otherwise be lost.

Do not save the whole conversation. Do not perform full working-memory cleanup
from this hook. Full cleanup belongs to the dreaming subagent.

## Expected Effect

The HostAgent preserves important continuity before compaction without
performing offline consolidation.
