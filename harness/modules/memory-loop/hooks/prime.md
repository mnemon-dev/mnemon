# Prime Hook

## Runtime Moment

Run at session start, agent bootstrap, or first system prompt assembly.

## Output To HostAgent

Load the current `MEMORY.md` and `GUIDE.md` into the system prompt.

`MEMORY.md` is working memory: compact, prompt-facing context for this project.
`GUIDE.md` is policy: it explains when memory should be read or written.

Do not recall Mnemon during Prime. Do not load long-term memory wholesale. Use
`memory_get.md` later only if the task appears to need prior memory.

## Expected Effect

The HostAgent starts the session with current working memory and memory
judgment rules, but without performing long-term recall or writeback.
