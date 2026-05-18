# Remind Hook

## Runtime Moment

Run before planning or executing a user task only if the host lacks native skill
discovery.

## Output To HostAgent

No-op by default.

If this host needs a reminder, tell the HostAgent to use its native skill
discovery mechanism. Do not repeat the full skill guide every turn.

## Expected Effect

The default skill loop avoids noisy per-prompt reminders.
