import type { Context } from '@deepseek-ai/cordis'
import { prepareStarterResolution } from './starter-resolution.ts'

export const name = 'dsh-mnemon-starter'
export const provide = ['mnemonStarterReady']

/**
 * The separate readiness Entry of 0.5.18 and 0.5.19, kept for compositions that
 * still insert it; the Starter's own group (`dsh-mnemon/bundle`) now prepares itself.
 */
export async function apply(ctx: Context): Promise<void> {
  await prepareStarterResolution(ctx)
  ctx.provide('mnemonStarterReady', true)
}
