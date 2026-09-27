import React from 'react'
import {
  AbsoluteFill,
  Audio,
  Sequence,
  staticFile,
  useCurrentFrame,
  useVideoConfig,
} from 'remotion'

import { BeatBackground, BrandTitle, CaptionsLayer, ProgressBar } from '../../lib/chrome'
import { activeBeatIndex, beatStartFrames, frameToMs } from '../../lib/timing'
import type { FormatCompositionProps } from '../../types'
import { FPS } from '../../types'

export const TopN: React.FC<FormatCompositionProps> = (props) => {
  const frame = useCurrentFrame()
  const { durationInFrames } = useVideoConfig()
  const beatIdx = activeBeatIndex(frame, props.beats)
  const beat = props.beats[beatIdx] ?? props.beats[0]
  const starts = beatStartFrames(props.beats)
  const rank = beatIdx + 1

  return (
    <AbsoluteFill style={{ backgroundColor: props.brandKit.secondary }}>
      {props.beats.map((b, i) => (
        <Sequence
          key={`${b.text}-${i}`}
          from={starts[i]}
          durationInFrames={Math.round(b.durationInSeconds * FPS)}
          name={`beat-${i}`}
        >
          <BeatBackground clipPath={b.clipPath} brandKit={props.brandKit} />
        </Sequence>
      ))}

      <AbsoluteFill
        style={{
          padding: props.orientation === '9:16' ? '140px 64px' : '96px 96px',
          flexDirection: 'row',
          alignItems: 'flex-start',
          gap: 28,
        }}
      >
        <div
          style={{
            minWidth: 120,
            height: 120,
            borderRadius: 24,
            background: props.brandKit.primary,
            color: props.brandKit.secondary,
            fontFamily: props.brandKit.fontHeading,
            fontWeight: 900,
            fontSize: 64,
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
          }}
        >
          #{rank}
        </div>
        <div style={{ flex: 1 }}>
          <div
            style={{
              fontFamily: props.brandKit.fontBody,
              color: props.brandKit.primary,
              fontWeight: 700,
              fontSize: 28,
              marginBottom: 12,
            }}
          >
            TOP {props.beats.length}
          </div>
          <BrandTitle
            text={beat?.text ?? ''}
            brandKit={props.brandKit}
            orientation={props.orientation}
          />
        </div>
      </AbsoluteFill>

      <ProgressBar
        progress={frame / Math.max(1, durationInFrames - 1)}
        brandKit={props.brandKit}
        orientation={props.orientation}
      />

      <CaptionsLayer
        words={props.captions.words}
        timeMs={frameToMs(frame)}
        brandKit={props.brandKit}
        orientation={props.orientation}
      />

      {props.audioSrc ? <Audio src={staticFile(props.audioSrc.replace(/^\//, ''))} /> : null}
    </AbsoluteFill>
  )
}
