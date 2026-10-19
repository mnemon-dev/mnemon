import type { HostSession, HostSessionEvent } from '../../src/host/dsh.ts'

const MESSAGE_EVENTS = new Set(['system/message', 'user/message', 'assistant/message', 'tool/result'])

/**
 * DSH Session log readers over a live event array; later pushes stay visible.
 * The surface is append-only: every message event stays model-visible, as in
 * a Session that was never rewound or compacted.
 */
export function sessionLog(events: HostSessionEvent[] = []): Pick<HostSession, 'snapshotEvents' | 'eventAt' | 'surface'> {
  return {
    snapshotEvents: (fromSeq = 0, toSeqExclusive = events.length) => events.slice(fromSeq, toSeqExclusive),
    eventAt: seq => events[seq],
    surface: {
      get nodes() { return events.flatMap((event, seq) => MESSAGE_EVENTS.has(event.type) ? [seq] : []) },
    },
  }
}
