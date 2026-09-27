# Mayank 2.0 Remotion compositions

Formats: `explained_60s`, `myth_vs_fact`, `top_n`, plus `thumbnail` still.

```powershell
cd remotion
npm install
npm run dev          # Remotion Studio
npm run test
npm run render:fixtures   # 5s still fixtures per format
npm run render            # sample of each format × orientation (+ thumbnail)
```

Compositions are pure functions of `{ brandKit, beats, audioSrc, captions, orientation }`.
Asset paths come only from props (files under `public/`). No network at render time.
