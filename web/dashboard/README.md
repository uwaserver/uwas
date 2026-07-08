# UWAS Dashboard

React 19 + TypeScript + Vite single-page admin UI for UWAS. The production build is embedded into the Go binary via `internal/admin/dashboard/embed.go`.

## Stack

- React 19
- Vite 8
- TypeScript 5.9 project references
- Tailwind CSS 4 via `@tailwindcss/vite`
- React Router 7
- Playwright E2E tests

## Scripts

```bash
npm run dev      # local Vite dev server
npm run build    # tsc -b && vite build
npm run lint     # ESLint
npm run preview  # preview production build
```

## Development notes

- API helpers live in `src/lib/api.ts` and call the UWAS admin REST API.
- Page components live in `src/pages/` (42 pages currently).
- Shared UI lives in `src/components/`.
- Keep TypeScript strict-mode clean; `npm run build` is the primary validation gate.

## E2E

```bash
npx playwright test
```

CI uses the direct Playwright binary with cached browser installs; local development may require `npx playwright install` once.
