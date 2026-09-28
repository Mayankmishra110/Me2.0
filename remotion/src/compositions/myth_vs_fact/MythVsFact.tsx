import React from 'react'
import {
  AbsoluteFill,
  Audio,
  Sequence,
  staticFile,
  useCurrentFrame,
  useVideoConfig,
} from 'remotion'

import {
  BeatBackground,
  BrandTitle,
  CaptionsLayer,
  LowerThird,
  ProgressBar,
} from '../../lib/chrome'
import { activeBeatIndex, beatStartFrames, frameToMs } from '../../lib/timing'
import type { FormatCompositionProps } from '../../types'
import { FPS } from '../../types'

export const MythVsFact: React.FC<FormatCompositionProps> = (props) => {
  const frame = useCurrentFrame()
  const { durationInFrames } = useVideoConfig()
  const beatIdx = activeBeatIndex(frame, props.beats)
  const beat = props.beats[beatIdx] ?? props.beats[0]
  const starts = beatStartFrames(props.beats)
  const isMyth = beatIdx % 2 === 0
  const label = isMyth ? 'MYTH' : 'FACT'

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
        }}
      >
        <div
          style={{
            display: 'inline-flex',
            alignSelf: 'flex-start',
            background: isMyth ? '#F87171' : props.brandKit.primary,
            color: props.brandKit.secondary,
            fontFamily: props.brandKit.fontHeading,
            fontWeight: 800,
            fontSize: 36,
            padding: '10px 20px',
            borderRadius: 10,
            marginBottom: 24,
          }}
        >
          {label}
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

      {props.orientation === '16:9' ? (
        <LowerThird label={`Source: beat ${beatIdx + 1}`} brandKit={props.brandKit} />
      ) : null}

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
