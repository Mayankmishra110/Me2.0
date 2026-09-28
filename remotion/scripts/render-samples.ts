import { mkdirSync, writeFileSync, readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { spawnSync } from 'node:child_process'

const remotionRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const cli = path.join(remotionRoot, 'node_modules', '@remotion', 'cli', 'remotion-cli.js')
const tmpDir = path.join(remotionRoot, 'out', '.props')
mkdirSync(tmpDir, { recursive: true })
mkdirSync(path.join(remotionRoot, 'out'), { recursive: true })

const jobs: { composition: string; propsFile: string; outName: string }[] = [
  {
    composition: 'explained-60s-portrait',
    propsFile: 'samples/explained_60s.json',
    outName: 'explained-60s-portrait.mp4',
  },
  {
    composition: 'explained-60s-landscape',
    propsFile: 'samples/explained_60s.json',
    outName: 'explained-60s-landscape.mp4',
  },
  {
    composition: 'myth-vs-fact-portrait',
    propsFile: 'samples/myth_vs_fact.json',
    outName: 'myth-vs-fact-portrait.mp4',
  },
  {
    composition: 'myth-vs-fact-landscape',
    propsFile: 'samples/myth_vs_fact.json',
    outName: 'myth-vs-fact-landscape.mp4',
  },
  {
    composition: 'top-n-portrait',
    propsFile: 'samples/top_n.json',
    outName: 'top-n-portrait.mp4',
  },
  {
    composition: 'top-n-landscape',
    propsFile: 'samples/top_n.json',
    outName: 'top-n-landscape.mp4',
  },
  {
    composition: 'thumbnail',
    propsFile: 'samples/thumbnail.json',
    outName: 'thumbnail.png',
  },
]

function runRemotion(args: string[]) {
  console.log(`\n→ node remotion-cli.js ${args.join(' ')}`)
  const result = spawnSync(process.execPath, [cli, ...args], {
    cwd: remotionRoot,
    stdio: 'inherit',
    env: process.env,
  })
  if (result.status !== 0) {
    throw new Error(`Remotion failed (exit ${result.status}) args=${args.join(' ')}`)
  }
}

for (const job of jobs) {
  const props = JSON.parse(readFileSync(path.join(remotionRoot, job.propsFile), 'utf8')) as Record<
    string,
    unknown
  >
  if (job.composition.endsWith('-portrait')) props.orientation = '9:16'
  if (job.composition.endsWith('-landscape')) props.orientation = '16:9'

  const propsRel = path.join('out', '.props', `${job.composition}.json`)
  writeFileSync(path.join(remotionRoot, propsRel), JSON.stringify(props, null, 2))
  const outRel = path.join('out', job.outName)

  if (job.composition === 'thumbnail') {
    runRemotion(['still', job.composition, outRel, `--props=${propsRel}`])
  } else {
    runRemotion(['render', job.composition, outRel, `--props=${propsRel}`, '--frames=0-29'])
  }
}

console.log('\nAll sample renders finished → remotion/out/')
