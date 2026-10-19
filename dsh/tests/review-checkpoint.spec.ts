import { describe, expect, it } from 'vitest'
import { reviewCheckpoint } from '../src/host/review-checkpoint.ts'
import type { HostSessionEvent } from '../src/host/dsh.ts'
import { sessionLog } from './fixtures/session-log.ts'

describe('bounded review checkpoint', () => {
  it('uses whole visible messages and excludes rewound, plugin and unfinished evidence', () => {
    const message = (seq: number, text: string, type = 'user/message', kind = 'user'): HostSessionEvent => {
      const body = { source: { kind }, content: [{ type: 'text', text }] }
      return { seq, type, data: type === 'user/message' ? body : { message: body } }
    }
    const events = [message(0, 'rewound fact'), message(1, 'plugin assertion', 'user/message', 'plugin'),
      message(2, 'Please remember the explicit decision to use SQLite.'), message(3, 'oversized '.repeat(500)),
      message(4, 'bounded artifact', 'assistant/message'), message(5, 'successful tool evidence', 'tool/result'),
      { seq: 6, type: 'turn/end', data: {} }, message(7, 'unfinished fact')]
    const prompt = reviewCheckpoint({ ...sessionLog(events), surface: { nodes: [1, 2, 3, 4, 5, 7] } }, 1_000)
    expect(prompt.length).toBeLessThanOrEqual(1_000)
    expect(prompt).toContain('Please remember the explicit decision to use SQLite.')
    expect(prompt).toContain('bounded artifact')
    expect(prompt).toContain('successful tool evidence')
    expect(prompt).not.toMatch(/rewound fact|plugin assertion|oversized|unfinished fact/)
    expect(prompt).toContain('skip candidates needing missing evidence')
  })

  it('reads published flat user events without promoting injected or unknown sources (issue 275)', () => {
    const event = (seq: number, text: string, source?: object): HostSessionEvent => ({ seq, type: 'user/message',
      data: { id: `user-${seq}`, role: 'user', content: [{ type: 'text', text }], ...(source === undefined ? {} : { source }) },
    })
    const events = [
      event(0, 'Remember the explicit SQLite project decision.', { kind: 'user', rpcId: 'synthetic-rpc' }),
      event(1, 'Do not write this checkpoint to memory. 不记录本轮的临时诊断信息。', { kind: 'user' }),
      event(2, 'Injected runtime snapshot.', { kind: 'dsh-mnemon', form: 'recall' }),
      event(3, 'Old plugin wrapper.', { kind: 'plugin', plugin: 'dsh-mnemon' }),
      event(4, 'Namespaced plugin wrapper.', { kind: 'plugin:dsh-mnemon' }),
      event(5, 'Runtime policy.', { kind: 'runtime-context' }),
      event(6, 'Compacted summary.', { kind: 'summary' }),
      event(7, 'No known source.'),
      { seq: 8, type: 'turn/end', data: {} },
    ]
    const checkpoint = reviewCheckpoint({ ...sessionLog(events), surface: { nodes: events.map(event => event.seq!) } }, 2_000)
    expect(checkpoint).toContain('[user/message; checkpoint event 0]\nRemember the explicit SQLite project decision.')
    expect(checkpoint).toContain('[user/message; checkpoint event 1]\nDo not write this checkpoint to memory. 不记录本轮的临时诊断信息。')
    expect(checkpoint).not.toMatch(/Injected|wrapper|Runtime policy|Compacted|No known source/u)
  })

  it('skips review before any completed checkpoint', () => {
    expect(reviewCheckpoint(sessionLog(), 1_000)).toContain('No completed checkpoint')
  })
})
