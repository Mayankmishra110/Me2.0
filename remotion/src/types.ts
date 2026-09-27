/** Composition props — pure data; every asset path is a string from props (no network). */

export type Orientation = '16:9' | '9:16'

export type CaptionStyle = 'word-highlight' | 'karaoke' | 'block'

export type BrandKit = {
  primary: string
  secondary: string
  fontHeading: string
  fontBody: string
  captionStyle: CaptionStyle
}

export type Beat = {
  /** On-screen title / spoken beat text */
  text: string
  /** Path relative to `public/` (passed through `staticFile`) */
  clipPath: string
  durationInSeconds: number
}

export type WordTiming = {
  word: string
  startMs: number
  endMs: number
}

export type Captions = {
  words: WordTiming[]
}

export type FormatCompositionProps = {
  brandKit: BrandKit
  beats: Beat[]
  /** Voice-over path relative to `public/` */
  audioSrc: string
  captions: Captions
  orientation: Orientation
}

export type ThumbnailProps = {
  brandKit: BrandKit
  /** ≤ 4 words per DESIGN §3 */
  title: string
  focalClipPath: string
  orientation?: '16:9'
}

export const FPS = 30

export const SIZES = {
  '9:16': { width: 1080, height: 1920 },
  '16:9': { width: 1920, height: 1080 },
  thumbnail: { width: 1280, height: 720 },
} as const

/** Shorts safe zone: avoid bottom 20% and right 15% (DESIGN §3). */
export const SHORTS_SAFE = {
  bottomFraction: 0.2,
  rightFraction: 0.15,
} as const
