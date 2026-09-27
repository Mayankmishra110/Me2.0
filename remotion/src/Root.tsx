import React from 'react'
import { Composition } from 'remotion'

import { Explained60s } from './compositions/explained_60s/Explained60s'
import { MythVsFact } from './compositions/myth_vs_fact/MythVsFact'
import { Thumbnail } from './compositions/thumbnail/Thumbnail'
import { TopN } from './compositions/top_n/TopN'
import { totalDurationInFrames } from './lib/timing'
import type { FormatCompositionProps, ThumbnailProps } from './types'
import { FPS, SIZES } from './types'

import explainedSample from '../samples/explained_60s.json'
import mythSample from '../samples/myth_vs_fact.json'
import topNSample from '../samples/top_n.json'
import thumbnailSample from '../samples/thumbnail.json'

const formats = [
  {
    id: 'explained-60s',
    component: Explained60s,
    sample: explainedSample as FormatCompositionProps,
  },
  {
    id: 'myth-vs-fact',
    component: MythVsFact,
    sample: mythSample as FormatCompositionProps,
  },
  {
    id: 'top-n',
    component: TopN,
    sample: topNSample as FormatCompositionProps,
  },
] as const

const orientations = ['9:16', '16:9'] as const

export const RemotionRoot: React.FC = () => {
  return (
    <>
      {formats.flatMap((fmt) =>
        orientations.map((orientation) => {
          const props: FormatCompositionProps = { ...fmt.sample, orientation }
          const { width, height } = SIZES[orientation]
          const durationInFrames = totalDurationInFrames(props.beats)
          const compositionId = `${fmt.id}-${orientation === '9:16' ? 'portrait' : 'landscape'}`
          return (
            <Composition
              key={compositionId}
              id={compositionId}
              component={fmt.component}
              durationInFrames={durationInFrames}
              fps={FPS}
              width={width}
              height={height}
              defaultProps={props}
              calculateMetadata={async ({ props: p }) => ({
                durationInFrames: totalDurationInFrames(p.beats),
                props: p,
              })}
            />
          )
        }),
      )}

      <Composition
        id="thumbnail"
        component={Thumbnail}
        durationInFrames={1}
        fps={FPS}
        width={SIZES.thumbnail.width}
        height={SIZES.thumbnail.height}
        defaultProps={thumbnailSample as ThumbnailProps}
      />

      {formats.map((fmt) => {
        const props: FormatCompositionProps = { ...fmt.sample, orientation: '9:16' }
        return (
          <Composition
            key={`${fmt.id}-fixture`}
            id={`${fmt.id}-fixture`}
            component={fmt.component}
            durationInFrames={FPS * 5}
            fps={FPS}
            width={SIZES['9:16'].width}
            height={SIZES['9:16'].height}
            defaultProps={props}
          />
        )
      })}
    </>
  )
}
