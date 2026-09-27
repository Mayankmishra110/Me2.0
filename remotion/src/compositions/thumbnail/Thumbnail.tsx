import React from 'react'
import { AbsoluteFill, Img } from 'remotion'

import { resolveAsset } from '../../lib/chrome'
import type { ThumbnailProps } from '../../types'

/** 1280×720 thumbnail still — ≤ 4 words, brand color block, one focal element (DESIGN §3). */
export const Thumbnail: React.FC<ThumbnailProps> = ({ brandKit, title, focalClipPath }) => {
  const words = title.trim().split(/\s+/).filter(Boolean)
  if (words.length > 4) {
    throw new Error(`Thumbnail title must be ≤ 4 words (got ${words.length}): "${title}"`)
  }

  return (
    <AbsoluteFill style={{ backgroundColor: brandKit.secondary }}>
      <Img
        src={resolveAsset(focalClipPath)}
        style={{
          position: 'absolute',
          right: 0,
          top: 0,
          width: '58%',
          height: '100%',
          objectFit: 'cover',
        }}
      />
      <div
        style={{
          position: 'absolute',
          left: 0,
          top: 0,
          width: '48%',
          height: '100%',
          background: brandKit.primary,
          clipPath: 'polygon(0 0, 100% 0, 82% 100%, 0 100%)',
        }}
      />
      <div
        style={{
          position: 'absolute',
          left: 56,
          top: '50%',
          transform: 'translateY(-50%)',
          maxWidth: '40%',
          fontFamily: brandKit.fontHeading,
          fontWeight: 900,
          fontSize: 72,
          lineHeight: 1.05,
          color: brandKit.secondary,
        }}
      >
        {title}
      </div>
    </AbsoluteFill>
  )
}
