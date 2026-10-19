import { describe, expect, it } from 'vitest'
import { QODERWORK_REVIEW_POLICY, scoreReviewActivity } from "../src/host/review-activity.ts"

describe('QoderWork-compatible background review score', () => {
  it('uses the original constants without provider token accounting', () => {
    expect(QODERWORK_REVIEW_POLICY).toEqual({
      reviewThreshold: 5,
      textLengthScoreUnit: 50,
      textLengthScoreCap: 3,
      toolCountScoreUnit: 5,
      toolCountScoreCap: 2,
      toolDiversityThreshold: 3,
      toolDiversityScoreCap: 2,
      turnScore: 1,
    })
  })

  it('caps text and tool volume while scoring turns and tool diversity independently', () => {
    expect(scoreReviewActivity({
      totalUserTextLength: 1_000,
      turnCount: 2,
      toolCallCount: 100,
      uniqueToolCount: 8,
    })).toMatchObject({
      textLengthScore: 3,
      turnScore: 2,
      toolCallScore: 2,
      toolDiversityScore: 2,
      score: 9,
      threshold: 5,
      eligible: true,
    })
  })

  it('matches QoderWork threshold examples exactly', () => {
    expect(scoreReviewActivity({ totalUserTextLength: 150, turnCount: 1, toolCallCount: 0, uniqueToolCount: 0 }).score).toBe(4)
    expect(scoreReviewActivity({ totalUserTextLength: 150, turnCount: 2, toolCallCount: 0, uniqueToolCount: 0 }).score).toBe(5)
    expect(scoreReviewActivity({ totalUserTextLength: 100, turnCount: 1, toolCallCount: 5, uniqueToolCount: 3 }).score).toBe(5)
    expect(scoreReviewActivity({ totalUserTextLength: 0, turnCount: 5, toolCallCount: 0, uniqueToolCount: 0 }).score).toBe(5)
  })
})
