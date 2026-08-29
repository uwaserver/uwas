import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { execFileSync } from 'node:child_process'
import { readFileSync } from 'node:fs'
import path from 'node:path'

/**
 * The released version, resolved at build time.
 *
 * The site used to carry a hand-written version string in the hero, which is
 * exactly the kind of thing nobody remembers to touch during a release — it
 * still said v0.8.9 three releases later.
 *
 * The repo's VERSION file is the source of truth: `chore(release): prepare
 * vX.Y.Z` bumps it in the same commit the tag points at, so reading it gives
 * the released version without a network call.
 *
 * It is preferred over `git describe` because the Pages deploy checks out with
 * actions/checkout's default shallow fetch, which brings no tags — describe
 * would fail there and succeed locally, which is the worst of both. Describe
 * stays as a fallback for a tree without a VERSION file.
 */
function releaseVersion(): string {
  try {
    const raw = readFileSync(path.resolve(__dirname, '../../VERSION'), 'utf8').trim()
    if (raw) return raw
  } catch {
    // fall through to git
  }
  try {
    return execFileSync('git', ['describe', '--tags', '--abbrev=0'], {
      cwd: __dirname,
      encoding: 'utf8',
      stdio: ['ignore', 'pipe', 'ignore'],
    }).trim()
  } catch {
    return 'dev'
  }
}

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  define: {
    __UWAS_VERSION__: JSON.stringify(releaseVersion()),
  },
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
})
