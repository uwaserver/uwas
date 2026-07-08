# UWAS Documentation Site

Vite + React documentation/marketing site for UWAS. Source lives under `docs/site/src`; the production build is emitted to `docs/site/dist`.

## Stack

- React 19
- Vite 8
- TypeScript 5.9 project references
- Tailwind CSS 4 via `@tailwindcss/vite`
- Lucide icons

## Scripts

```bash
npm run dev      # local docs site dev server
npm run build    # tsc -b && vite build
npm run lint     # ESLint
npm run preview  # preview production build
```

## Notes

- Static assets live in `public/`.
- Main app code lives in `src/App.tsx`, with supporting components in `src/components/`.
- Keep this site aligned with the root `README.md`, `ARCHITECTURE.md`, and `docs/SPECIFICATION.md` when product capabilities change.
