/** Asset path helpers — no network at render time. */

export function resolvePublicPath(path: string): string {
  if (path.startsWith('http://') || path.startsWith('https://') || path.startsWith('data:')) {
    throw new Error(`Network/data URLs are not allowed at render time: ${path}`)
  }
  return path.replace(/^\//, '')
}
