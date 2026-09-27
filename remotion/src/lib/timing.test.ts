import { describe, expect, it } from 'vitest'

import { resolvePublicPath } from './assets'
import { activeBeatIndex, activeWordIndex, captionSafeBox, totalDurationInFrames } from './timing'
import { SHORTS_SAFE, type Beat } from '../types'

describe('timing helpers', () => {
  const beats: Beat[] = [
    { text: 'a', clipPath: 'samples/beat-a.svg', durationInSeconds: 2 },
    { text: 'b', clipPath: 'samples/beat-b.svg', durationInSeconds: 3 },
  ]

  it('sums beat durations into frames at 30fps', () => {
    expect(totalDurationInFrames(beats)).toBe(150)
  })

  it('picks the active beat by frame', () => {
    expect(activeBeatIndex(0, beats)).toBe(0)
    expect(activeBeatIndex(59, beats)).toBe(0)
    expect(activeBeatIndex(60, beats)).toBe(1)
  })

  it('finds the active caption word', () => {
    const words = [
      { word: 'Hello', startMs: 0, endMs: 400 },
      { word: 'world', startMs: 400, endMs: 900 },
    ]
    expect(activeWordIndex(words, 100)).toBe(0)
    expect(activeWordIndex(words, 500)).toBe(1)
  })
})

describe('safe zone (DESIGN §3)', () => {
  it('keeps 9:16 captions clear of bottom 20% and right 15%', () => {
    const box = captionSafeBox('9:16')
    const width = 1080
    const height = 1920
    expect(box.left + box.width).toBeLessThanOrEqual(width * (1 - SHORTS_SAFE.rightFraction) + 0.01)
    expect(box.top + box.height).toBeLessThanOrEqual(
      height * (1 - SHORTS_SAFE.bottomFraction) + 0.01,
    )
  })

  it('places 16:9 captions in the lower third', () => {
    const box = captionSafeBox('16:9')
    expect(box.top).toBeGreaterThan(1080 * 0.6)
  })
})

describe('asset paths', () => {
  it('normalizes local public paths and rejects network URLs', () => {
    expect(resolvePublicPath('/samples/beat-a.svg')).toBe('samples/beat-a.svg')
    expect(() => resolvePublicPath('https://example.com/x.mp4')).toThrow(/Network/)
  })
})
