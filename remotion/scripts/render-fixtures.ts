/**
 * Renders a still (frame 0) of the 5-second fixture composition per format.
 * Output: remotion/fixtures/<id>.jpeg
 */
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { spawnSync } from 'node:child_process'
import { mkdirSync } from 'node:fs'

const remotionRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const cli = path.join(remotionRoot, 'node_modules', '@remotion', 'cli', 'remotion-cli.js')
mkdirSync(path.join(remotionRoot, 'fixtures'), { recursive: true })

const fixtures = [
  'explained-60s-fixture',
  'myth-vs-fact-fixture',
  'top-n-fixture',
  'thumbnail',
] as const

for (const id of fixtures) {
  const outRel = path.join('fixtures', `${id}.jpeg`)
  const args = ['still', id, outRel, '--frame=0']
  console.log(`\n→ node remotion-cli.js ${args.join(' ')}`)
  const result = spawnSync(process.execPath, [cli, ...args], {
    cwd: remotionRoot,
    stdio: 'inherit',
    env: process.env,
  })
  if (result.status !== 0) {
    throw new Error(`Fixture still failed for ${id}`)
  }
}

console.log('\nFixtures written to remotion/fixtures/')
