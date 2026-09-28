import type { Beat, Orientation } from '../types'
import { FPS, SHORTS_SAFE, SIZES } from '../types'

export function totalDurationInFrames(beats: Beat[]): number {
  const seconds = beats.reduce((sum, b) => sum + Math.max(0, b.durationInSeconds), 0)
  return Math.max(1, Math.round(seconds * FPS))
}

export function frameToMs(frame: number, fps = FPS): number {
  return (frame / fps) * 1000
}

export function beatStartFrames(beats: Beat[]): number[] {
  const starts: number[] = []
  let cursor = 0
  for (const beat of beats) {
    starts.push(cursor)
    cursor += Math.round(beat.durationInSeconds * FPS)
  }
  return starts
}

export function activeBeatIndex(frame: number, beats: Beat[]): number {
  const starts = beatStartFrames(beats)
  let idx = 0
  for (let i = 0; i < starts.length; i++) {
    if (frame >= starts[i]) idx = i
  }
  return idx
}

export function compositionSize(orientation: Orientation) {
  return SIZES[orientation]
}

export type SafeZoneBox = {
  left: number
  top: number
  width: number
  height: number
}

/** Caption area inside the platform-safe region for 9:16; full lower third for 16:9. */
export function captionSafeBox(orientation: Orientation): SafeZoneBox {
  const { width, height } = SIZES[orientation]
  if (orientation === '9:16') {
    const rightPad = width * SHORTS_SAFE.rightFraction
    const bottomPad = height * SHORTS_SAFE.bottomFraction
    const top = height * 0.45
    return {
      left: width * 0.06,
      top,
      width: width - width * 0.06 - rightPad,
      height: height - top - bottomPad,
    }
  }
  return {
    left: width * 0.08,
    top: height * 0.72,
    width: width * 0.84,
    height: height * 0.2,
  }
}

export function activeWordIndex(
  words: { startMs: number; endMs: number }[],
  timeMs: number,
): number {
  let idx = -1
  for (let i = 0; i < words.length; i++) {
    if (timeMs >= words[i].startMs && timeMs < words[i].endMs) return i
    if (timeMs >= words[i].startMs) idx = i
  }
  return idx
}
