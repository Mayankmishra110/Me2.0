import React from 'react'
import { AbsoluteFill, Img, staticFile } from 'remotion'

import type { BrandKit, CaptionStyle, Orientation, WordTiming } from '../types'
import { resolvePublicPath } from './assets'
import { activeWordIndex, captionSafeBox } from './timing'

export function resolveAsset(path: string): string {
  return staticFile(resolvePublicPath(path))
}

export const BeatBackground: React.FC<{
  clipPath: string
  brandKit: BrandKit
}> = ({ clipPath, brandKit }) => {
  return (
    <AbsoluteFill style={{ backgroundColor: brandKit.secondary }}>
      <Img
        src={resolveAsset(clipPath)}
        style={{ width: '100%', height: '100%', objectFit: 'cover', opacity: 0.85 }}
      />
      <AbsoluteFill
        style={{
          background: `linear-gradient(180deg, transparent 40%, ${brandKit.secondary}cc 100%)`,
        }}
      />
    </AbsoluteFill>
  )
}

export const BrandTitle: React.FC<{
  text: string
  brandKit: BrandKit
  orientation: Orientation
}> = ({ text, brandKit, orientation }) => {
  const size = orientation === '9:16' ? 64 : 56
  return (
    <div
      style={{
        fontFamily: brandKit.fontHeading,
        fontWeight: 800,
        fontSize: size,
        color: '#FFFFFF',
        textShadow: `0 4px 24px ${brandKit.secondary}`,
        lineHeight: 1.15,
        maxWidth: '90%',
      }}
    >
      {text}
    </div>
  )
}

export const ProgressBar: React.FC<{
  progress: number
  brandKit: BrandKit
  orientation: Orientation
}> = ({ progress, brandKit, orientation }) => {
  if (orientation !== '9:16') return null
  return (
    <div
      style={{
        position: 'absolute',
        top: 48,
        left: '6%',
        width: '70%',
        height: 6,
        borderRadius: 999,
        background: '#ffffff33',
        overflow: 'hidden',
      }}
    >
      <div
        style={{
          width: `${Math.min(100, Math.max(0, progress * 100))}%`,
          height: '100%',
          background: brandKit.primary,
        }}
      />
    </div>
  )
}

const CaptionWords: React.FC<{
  words: WordTiming[]
  timeMs: number
  style: CaptionStyle
  brandKit: BrandKit
}> = ({ words, timeMs, style, brandKit }) => {
  const active = activeWordIndex(words, timeMs)

  if (style === 'block') {
    const windowStart = Math.max(0, active < 0 ? 0 : active - 2)
    const chunk = words.slice(windowStart, windowStart + 6)
    return (
      <div
        style={{
          fontFamily: brandKit.fontBody,
          fontSize: 42,
          fontWeight: 700,
          color: '#fff',
          background: `${brandKit.secondary}e6`,
          padding: '12px 18px',
          borderRadius: 12,
          textAlign: 'center',
        }}
      >
        {chunk.map((w) => w.word).join(' ')}
      </div>
    )
  }

  return (
    <div
      style={{
        fontFamily: brandKit.fontBody,
        fontSize: 40,
        fontWeight: 700,
        color: '#fff',
        textAlign: 'center',
        display: 'flex',
        flexWrap: 'wrap',
        justifyContent: 'center',
        gap: 10,
        lineHeight: 1.35,
      }}
    >
      {words.map((w, i) => {
        const isActive = i === active
        const passed = i < active
        if (style === 'karaoke') {
          return (
            <span
              key={`${w.word}-${i}`}
              style={{
                color: isActive || passed ? brandKit.primary : '#ffffff99',
              }}
            >
              {w.word}
            </span>
          )
        }
        // word-highlight
        return (
          <span
            key={`${w.word}-${i}`}
            style={{
              color: '#fff',
              background: isActive ? brandKit.primary : 'transparent',
              padding: isActive ? '2px 8px' : 0,
              borderRadius: 8,
            }}
          >
            {w.word}
          </span>
        )
      })}
    </div>
  )
}

export const CaptionsLayer: React.FC<{
  words: WordTiming[]
  timeMs: number
  brandKit: BrandKit
  orientation: Orientation
}> = ({ words, timeMs, brandKit, orientation }) => {
  const box = captionSafeBox(orientation)
  return (
    <div
      style={{
        position: 'absolute',
        left: box.left,
        top: box.top,
        width: box.width,
        height: box.height,
        display: 'flex',
        alignItems: orientation === '9:16' ? 'flex-start' : 'center',
        justifyContent: 'center',
        pointerEvents: 'none',
      }}
    >
      <CaptionWords
        words={words}
        timeMs={timeMs}
        style={brandKit.captionStyle}
        brandKit={brandKit}
      />
    </div>
  )
}

export const LowerThird: React.FC<{
  label: string
  brandKit: BrandKit
}> = ({ label, brandKit }) => (
  <div
    style={{
      position: 'absolute',
      left: 64,
      bottom: 64,
      background: brandKit.primary,
      color: brandKit.secondary,
      fontFamily: brandKit.fontBody,
      fontWeight: 700,
      fontSize: 28,
      padding: '10px 18px',
      borderRadius: 8,
    }}
  >
    {label}
  </div>
)
