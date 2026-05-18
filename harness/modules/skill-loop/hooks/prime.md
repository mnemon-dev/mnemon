# Prime Hook

## Runtime Moment

Run at session start, agent bootstrap, or first system prompt assembly.

## Output To HostAgent

Apply `GUIDE.md` and sync canonical active skills into the host-native skill
surface.

Only active skills should become host-visible. Keep stale and archived skills
outside the normal discovery path.

Do not inject all skill bodies into the prompt. Let the HostAgent discover and
invoke skills through its native skill mechanism.

## Expected Effect

The HostAgent starts with current skill policy and a refreshed native skill
surface, while `.mnemon` remains the canonical skill library.
