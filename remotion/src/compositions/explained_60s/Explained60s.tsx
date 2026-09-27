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

export const Explained60s: React.FC<FormatCompositionProps> = (props) => {
  const frame = useCurrentFrame()
  const { durationInFrames } = useVideoConfig()
  const beatIdx = activeBeatIndex(frame, props.beats)
  const beat = props.beats[beatIdx] ?? props.beats[0]
  const starts = beatStartFrames(props.beats)

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
          padding: props.orientation === '9:16' ? '120px 64px' : '80px 96px',
          justifyContent: 'flex-start',
        }}
      >
        <div
          style={{
            fontFamily: props.brandKit.fontBody,
            color: props.brandKit.primary,
            fontSize: 28,
            fontWeight: 700,
            marginBottom: 16,
            letterSpacing: 1,
          }}
        >
          EXPLAINED IN 60s
        </div>
        <BrandTitle
          text={beat?.text ?? ''}
          brandKit={props.brandKit}
          orientation={props.orientation}
        />
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
