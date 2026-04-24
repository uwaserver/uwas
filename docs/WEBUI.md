> **This prompt defines how the embedded WebUI dashboard for this Go project must be built.**
> **Every WebUI task must adhere to this prompt. No exceptions.**

---

## 🎯 PURPOSE

This Go project's Web UI will be built FROM SCRATCH using the technology stack and design rules defined below. No rule can be skipped with "we'll fix it later" or "keep it simple for now." Every component, every page, every line of code must comply.

---

## 📦 TECHNOLOGY STACK — MANDATORY VERSIONS

| Technology | Minimum Version | Notes |
|---|---|---|
| **React** | 19.x (latest stable) | `use()` hook, Suspense-first, automatic batching enabled |
| **TypeScript** | 5.7+ | `strict: true`, `noUncheckedIndexedAccess: true` required |
| **Tailwind CSS** | 4.1+ | `@theme` directive, new `@import "tailwindcss"` syntax, CSS-first config |
| **Shadcn/UI** | Latest (installed via CLI) | `npx shadcn@latest init` — components under `src/components/ui/` |
| **Lucide React** | Latest | The ONLY icon library. Any other icon package is FORBIDDEN |
| **Vite** | 6.x+ | Build tool. Webpack/CRA FORBIDDEN |
| **React Router** | 7.x | File-based routing NOT used, code-based route config |
| **Zustand** | 5.x | Global state management. Redux, MobX, Jotai FORBIDDEN |
| **TanStack Query** | 5.x | Server state, API calls, cache, polling |
| **React Hook Form** | 7.x + Zod 3.x | Form management + validation |

> ⚠️ **RULE:** Every package must use the LATEST stable version at `npm install` time. Pinning older versions is FORBIDDEN.

---

## 🎨 COLOR SYSTEM — "COMFORTABLE CONTRAST" PRINCIPLE

### Philosophy

- **Dark mode must NOT be pitch black** — pure black values like `#000000` or `#09090b` are FORBIDDEN.
- **Light mode must NOT be washed out** — invisible combos like `#e5e5e5` text on `#ffffff` background are FORBIDDEN.
- Both modes must meet **WCAG AA minimum contrast ratio (4.5:1 for normal text, 3:1 for large text)**.
- The target is a professional, eye-friendly palette that can be stared at for hours.

### Dark Mode Palette

```css
/* Dark Mode — Warm Charcoal, NOT Pure Black */
--background:         #131316;    /* Main surface — dark but NOT black */
--background-subtle:  #1a1a1f;    /* Card, sidebar surface */
--background-muted:   #222228;    /* Hover, secondary areas */
--surface:            #2a2a32;    /* Elevated card, modal, dropdown */
--border:             #32323c;    /* Subtle border */
--border-strong:      #45454f;    /* Prominent border */
--text-primary:       #ededf0;    /* Primary text — NOT pure white */
--text-secondary:     #a0a0ab;    /* Secondary text */
--text-muted:         #6e6e7a;    /* Placeholder, disabled */
--ring:               #4f6df5;    /* Focus ring */
```

### Light Mode Palette

```css
/* Light Mode — Off-White, Warm, Readable */
--background:         #f8f8fa;    /* Main surface — NOT pure white */
--background-subtle:  #f0f0f4;    /* Card, sidebar surface */
--background-muted:   #e8e8ee;    /* Hover, secondary areas */
--surface:            #ffffff;    /* Elevated card, modal */
--border:             #d4d4dc;    /* Subtle border */
--border-strong:      #b0b0bc;    /* Prominent border */
--text-primary:       #1a1a22;    /* Primary text — NOT pure black */
--text-secondary:     #55556a;    /* Secondary text — must be readable! */
--text-muted:         #8888a0;    /* Placeholder, disabled — still legible */
--ring:               #3b5ce4;    /* Focus ring */
```

### Accent / Brand Color Integration

```css
/* Each project's brand color maps to these variables */
--accent:             /* Project-specific primary color */
--accent-hover:       /* Accent hover state — 10-15% shift */
--accent-foreground:  #ffffff;    /* Text on accent background */
--accent-muted:       /* Accent at 10% opacity — badge, tag background */

/* Semantic Colors — must be legible in BOTH modes */
--success:            #22c55e;
--success-bg:         /* dark: #22c55e/10, light: #22c55e/8 */
--warning:            #f59e0b;
--warning-bg:         /* dark: #f59e0b/10, light: #f59e0b/8 */
--error:              #ef4444;
--error-bg:           /* dark: #ef4444/10, light: #ef4444/8 */
--info:               #3b82f6;
--info-bg:            /* dark: #3b82f6/10, light: #3b82f6/8 */
```

### Tailwind 4.1 Theme Configuration

```css
/* src/index.css */
@import "tailwindcss";

@theme {
  /* Color system defined as CSS custom properties */
  --color-background: var(--background);
  --color-surface: var(--surface);
  --color-foreground: var(--text-primary);
  --color-muted: var(--text-muted);
  --color-accent: var(--accent);
  --color-border: var(--border);

  /* Fonts */
  --font-sans: 'Inter Variable', 'Inter', system-ui, -apple-system, sans-serif;
  --font-mono: 'JetBrains Mono Variable', 'JetBrains Mono', 'Fira Code', monospace;

  /* Spacing scale (Tailwind 4 @theme) */
  --radius-lg: 0.75rem;
  --radius-md: 0.5rem;
  --radius-sm: 0.375rem;

  /* Shadows — different opacity per mode */
  --shadow-sm: 0 1px 2px 0 rgb(0 0 0 / 0.04);
  --shadow-md: 0 4px 6px -1px rgb(0 0 0 / 0.07), 0 2px 4px -2px rgb(0 0 0 / 0.05);
  --shadow-lg: 0 10px 15px -3px rgb(0 0 0 / 0.08), 0 4px 6px -4px rgb(0 0 0 / 0.04);
}
```

---

## 🔤 TYPOGRAPHY — FONT RULES

### Font Selection

| Usage | Font | Installation |
|---|---|---|
| **UI Text** | Inter Variable | `@fontsource-variable/inter` |
| **Code / Mono** | JetBrains Mono Variable | `@fontsource-variable/jetbrains-mono` |

> ⚠️ Google Fonts CDN MUST NOT be used. Fonts are installed as NPM packages and bundled. The embedded WebUI must work offline.

### Font Size Scale

```
text-xs:   0.75rem / 1rem     — Badge, caption, timestamp
text-sm:   0.875rem / 1.25rem — Secondary text, table cell, sidebar item
text-base: 1rem / 1.5rem      — Body text, form label
text-lg:   1.125rem / 1.75rem — Section title, card header
text-xl:   1.25rem / 1.75rem  — Page subtitle
text-2xl:  1.5rem / 2rem      — Page title
text-3xl:  1.875rem / 2.25rem — Hero, dashboard big number
```

### Font Weight Usage

- `font-normal (400)` — Body text
- `font-medium (500)` — Labels, sidebar items, table headers
- `font-semibold (600)` — Card titles, section headings, buttons
- `font-bold (700)` — Page titles and hero numbers ONLY. Overuse is FORBIDDEN.

### Typographic Rules

- Use `tracking-tight` on headings.
- Use `leading-relaxed` or `leading-normal` on body text.
- Use `space-y-4` between paragraphs.
- Monospace font is ONLY for: code snippets, API paths, IDs, hashes, terminal output.

---

## 🌗 DARK / LIGHT MODE SYSTEM

### Implementation

```tsx
// src/hooks/use-theme.ts
type Theme = 'light' | 'dark' | 'system';

// 1. Read from localStorage on init, default to 'system' if absent
// 2. If 'system', listen to prefers-color-scheme media query
// 3. Apply class="dark" or class="light" to <html> element
// 4. Style using Tailwind 4.1's dark: variant
// 5. Transition: 150ms ease color transition on theme change
```

### Mandatory Rules

1. **Three options must be provided:** Light, Dark, System. A 2-way toggle is NOT enough.
2. **LocalStorage key:** `{project-name}-theme` (e.g., `monsoon-theme`).
3. **Flash prevention:** An inline `<script>` inside `<head>` applies the theme BEFORE page render — a white flash is FORBIDDEN.
4. **Transition:** `transition-colors duration-150` on all color-changing elements.
5. **Shadcn/UI compatibility:** Must integrate with Shadcn's CSS variables system — writing a separate override layer is FORBIDDEN.
6. **Every component MUST be tested in both modes.** Developing only in dark mode and forgetting light mode is FORBIDDEN.

### Theme Toggle Component

- Shadcn `DropdownMenu` with 3 options (Sun / Moon / Monitor icons from Lucide).
- Positioned on the right side of the Header/Navbar.
- Wrapped with `<Tooltip>` showing "Switch theme" label.

---

## 📱 RESPONSIVE DESIGN — MANDATORY RULES

### Breakpoint Strategy — Mobile First

```
DEFAULT:   0px+     — Mobile (single column, stacked layout)
sm:        640px+   — Large phone / small tablet
md:        768px+   — Tablet portrait
lg:        1024px+  — Tablet landscape / small desktop
xl:        1280px+  — Desktop
2xl:       1536px+  — Large desktop
```

### Layout Rules

1. **Write mobile-first.** Start with mobile styles, then scale up with `sm:`, `md:`, `lg:`.
2. **Sidebar:** Desktop — fixed sidebar (w-64). Tablet — collapsible (icon-only w-16). Mobile — hamburger with slide-over sheet.
3. **Dashboard grid:** Mobile 1 col, `md:` 2 cols, `lg:` 3 cols, `xl:` 4 cols.
4. **Tables:** On mobile, convert to card-based layout OR use horizontal scroll with `overflow-x-auto`.
5. **Forms:** Mobile — full-width inputs. Desktop — 2-column grid layout allowed.
6. **Modal/Dialog:** Mobile — full-screen sheet. Desktop — centered dialog.
7. **Font size:** Responsive font sizes are NOT used. The same type scale applies everywhere.
8. **Touch targets:** Minimum 44×44px hit area — buttons, links, icon buttons included.
9. **Padding:** Container padding is `px-4 sm:px-6 lg:px-8`.

### Container Structure

```tsx
<div className="min-h-screen bg-background text-foreground">
  {/* Sidebar — responsive */}
  <aside className="fixed inset-y-0 left-0 z-40 w-64 border-r border-border
                     transform -translate-x-full lg:translate-x-0 transition-transform">
    {/* Sidebar content */}
  </aside>

  {/* Main content — sidebar offset */}
  <main className="lg:pl-64">
    <header className="sticky top-0 z-30 h-16 border-b border-border bg-background/80
                        backdrop-blur-sm flex items-center px-4 sm:px-6 lg:px-8">
      {/* Header: hamburger (mobile), breadcrumb, search, theme toggle, user */}
    </header>

    <div className="p-4 sm:p-6 lg:p-8">
      {/* Page content */}
    </div>
  </main>
</div>
```

---

## 🧩 COMPONENT ARCHITECTURE

### Directory Structure

```
src/
├── components/
│   ├── ui/              # Shadcn/UI components (added via CLI, DO NOT MODIFY)
│   ├── layout/          # Sidebar, Header, Footer, PageContainer
│   ├── shared/          # DataTable, StatusBadge, ConfirmDialog, EmptyState
│   └── {feature}/       # Feature-specific components (e.g., DhcpLeaseTable)
├── hooks/               # Custom hooks (use-theme, use-debounce, use-media-query)
├── lib/                 # Utility functions, API client, cn() helper
├── pages/               # Route-level page components
├── stores/              # Zustand stores
├── types/               # TypeScript type definitions
└── index.css            # Tailwind imports + theme variables
```

### Component Rules

1. **Every component is TypeScript + function component.** Class components are FORBIDDEN.
2. **Props interface is mandatory.** Use a separate `interface` or `type`, not inline annotation.
3. **`cn()` helper is mandatory:** `clsx` + `tailwind-merge` combo for conditional classes.
4. **Shadcn/UI takes priority:** If a UI element exists in Shadcn, do NOT build a custom one.
5. **Composition pattern:** Preserve Shadcn's slot/composition patterns.
6. **Default exports are FORBIDDEN.** Use named exports. Only exception: page-level lazy components.
7. **One component per file.** Sub-components exceeding 50 lines must be extracted to their own file.
8. **Barrel exports (`index.ts`) are FORBIDDEN.** Import directly from the file.

### Required Shadcn Components (must be installed in every project)

```bash
npx shadcn@latest add button card dialog dropdown-menu input label \
  select separator sheet sidebar skeleton table tabs textarea \
  tooltip badge alert avatar command scroll-area switch popover \
  sonner breadcrumb collapsible chart
```

---

## 🔌 API COMMUNICATION

### HTTP Client

```tsx
// src/lib/api-client.ts
// TanStack Query + fetch-based client

const API_BASE = '/api/v1';  // Go backend relative path — no CORS needed

// Every request function must be typed:
// - Request body: validated with Zod schema
// - Response: typed with TypeScript generics
// - Error: standard error shape { code, message, details? }
```

### TanStack Query Rules

1. **Every API endpoint gets a custom hook:** `useLeases()`, `useCreateLease()`, etc.
2. **Query key standard:** `[resource, ...params]` — e.g., `['leases', { page: 1 }]`
3. **Stale time:** Default 30 seconds. For real-time data, define a polling interval.
4. **Optimistic updates:** UI updates instantly on mutation, rolls back on error.
5. **Error handling:** Query errors are displayed via `Sonner` toast.
6. **Loading states:** Every query shows a `Skeleton` component — spinners are FORBIDDEN.

---

## 🛡️ FORMS & VALIDATION

```tsx
// Every form: React Hook Form + Zod
const schema = z.object({
  name: z.string().min(1, 'Name is required').max(255),
  ip: z.string().ip({ version: 'v4', message: 'Invalid IPv4 address' }),
});

type FormValues = z.infer<typeof schema>;
```

### Form Rules

1. **Inline validation:** Error message below each field. Do NOT show all errors in a toast.
2. **Disabled submit:** Button is disabled + `cursor-not-allowed` when form is invalid.
3. **Loading state:** Show `Loader2` icon spinning inside the button + disabled during submission.
4. **Success feedback:** `Sonner` toast — green success variant. Form resets after success.
5. **Keyboard:** `Enter` must trigger submit. Tab order must be correct.

---

## ✨ UX STANDARDS

### Loading States

- **Page load:** Full-page `Skeleton` layout (sidebar + content area).
- **Data table:** Skeleton rows matching expected row count.
- **Button action:** `Loader2` icon animation (Lucide).
- **Inline fetch:** Shadcn `Skeleton` component.
- **NEVER:** Use spinners (CircularProgress). Skeleton > Spinner, always.

### Empty States

- Every list/table must have an empty state component.
- Content: Icon (Lucide) + Title + Description + CTA button.
- Example: "No leases found" + "Create your first DHCP lease" button.

### Error States

- **API error:** Sonner toast (destructive variant).
- **Page error:** Error boundary component — "Something went wrong" + "Retry" button.
- **Form error:** Field-level inline error (red text + red border).
- **Network error:** Connection lost banner (sticky top, dismissible).

### Confirmation Dialogs

- `AlertDialog` is mandatory for all destructive actions (delete, revoke, reset).
- Red "Delete" button + "Cancel" button.
- Dialog text must include the name of the item being deleted.

### Notifications / Toast

- **Sonner** (Shadcn integration) must be used.
- Position: `bottom-right`.
- Auto-dismiss: 5 seconds.
- Variants: success, error, warning, info.
- Maximum 3 toasts visible simultaneously.

---

## ♿ ACCESSIBILITY (A11Y) — MANDATORY

1. **ARIA labels:** `aria-label` is mandatory on all icon-only buttons.
2. **Focus visible:** `focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2`.
3. **Keyboard navigation:** All interactive elements must be reachable via Tab.
4. **Skip to content:** Hidden "Skip to main content" link at the top of the page.
5. **Screen reader:** Use `aria-live` regions for status changes.
6. **Color alone:** Color must NEVER be the sole carrier of information — pair with icon or text.
7. **Contrast:** WCAG AA minimum (4.5:1 for normal text, 3:1 for large text + UI components).
8. **Reduced motion:** Use `motion-reduce:` variant to disable animations.

---

## ⚡ PERFORMANCE RULES

1. **Code splitting:** Route-based lazy loading — `React.lazy()` + `Suspense`.
2. **Bundle size:** Single vendor chunk must stay under 250KB gzipped.
3. **Images:** Prefer SVG. If PNG/JPG is needed, use WebP + lazy loading.
4. **Virtualization:** Use `@tanstack/react-virtual` for tables with 100+ rows.
5. **Memoization:** Prevent unnecessary re-renders with `React.memo`, `useMemo`, `useCallback`.
6. **Debounce:** 300ms debounce on search inputs.
7. **Prefetch:** Prefetch routes on sidebar link hover.

---

## 🏗️ BUILD & EMBED RULES (GO INTEGRATION)

```go
// Go side embed:
//go:embed all:frontend/dist
var frontendFS embed.FS

// Vite build output: frontend/dist/
// Single page app — Go router serves index.html for all non-API paths
// API prefix: /api/v1/... — Go handlers
// Static: /assets/... — embedded FS
```

### Vite Config

```ts
// vite.config.ts
export default defineConfig({
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    sourcemap: false,        // Sourcemaps OFF in production
    rollupOptions: {
      output: {
        manualChunks: {
          vendor: ['react', 'react-dom'],
          ui: ['@radix-ui/react-dialog', '@radix-ui/react-dropdown-menu', /* ... */],
        },
      },
    },
  },
  server: {
    proxy: {
      '/api': 'http://localhost:PORT', // Dev mode — proxy to Go backend
    },
  },
});
```

---

## 📋 PAGE TEMPLATE — STANDARD PAGE STRUCTURE

```tsx
export function DashboardPage() {
  return (
    <div className="space-y-6">
      {/* Page Header */}
      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight text-foreground">
            Dashboard
          </h1>
          <p className="text-sm text-muted-foreground">
            Overview of your system status.
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm">
            <RefreshCw className="mr-2 h-4 w-4" />
            Refresh
          </Button>
          <Button size="sm">
            <Plus className="mr-2 h-4 w-4" />
            Create New
          </Button>
        </div>
      </div>

      {/* Stats Grid */}
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <StatsCard />
        <StatsCard />
        <StatsCard />
        <StatsCard />
      </div>

      {/* Content */}
      <Card>
        <CardHeader>
          <CardTitle>Recent Activity</CardTitle>
          <CardDescription>Last 24 hours</CardDescription>
        </CardHeader>
        <CardContent>
          {/* Table or content */}
        </CardContent>
      </Card>
    </div>
  );
}
```

---

## 🚨 ABSOLUTE PROHIBITIONS

| # | FORBIDDEN | REASON |
|---|---|---|
| 1 | `any` type usage | Type safety violation |
| 2 | Inline `style={}` usage | Use Tailwind classes instead |
| 3 | CSS Modules or styled-components | Tailwind 4.1 is sufficient |
| 4 | `!important` usage | Starts specificity wars |
| 5 | Hardcoded color values (`#fff`, `rgb()`) | Use CSS variables |
| 6 | `console.log` in production | Dev-only, must be stripped in build |
| 7 | Barrel exports (`index.ts`) | Causes circular deps + breaks tree-shaking |
| 8 | Google Fonts CDN links | Offline capability — use NPM font packages |
| 9 | Direct `localStorage` access | Wrap in a custom hook (`useLocalStorage`) |
| 10 | `// @ts-ignore` or `// @ts-expect-error` | Fix the type, don't silence it |
| 11 | Hardcoded UI strings inside components | Use `constants.ts` or i18n file |
| 12 | `useEffect` for data fetching | Use TanStack Query |
| 13 | Class components | Function components + hooks only |
| 14 | Default exports (except page lazy import) | Named exports required |
| 15 | `moment.js` or full `lodash` import | Use `date-fns` + native JS |
| 16 | Spinners / CircularProgress | Use Skeleton components |
| 17 | `<div>` as a button | Use `<button>` or Shadcn `<Button>` |
| 18 | Font Awesome, Material Icons, etc. | Lucide React ONLY |
| 19 | `px` units for spacing (outside Tailwind) | Use Tailwind spacing scale |
| 20 | Z-index wars (`z-[9999]`) | Use the layered z-index scale |

---

## 📐 Z-INDEX SCALE

```css
--z-base:      0;
--z-dropdown:  50;
--z-sticky:    100;
--z-overlay:   200;
--z-modal:     300;
--z-popover:   400;
--z-toast:     500;
--z-tooltip:   600;
--z-max:       999;
```

---

## 🎯 CHECKLIST — BEFORE EVERY PR / COMMIT

- [ ] TypeScript compiles with zero errors in `strict` mode
- [ ] All pages verified in dark mode
- [ ] All pages verified in light mode
- [ ] Mobile (375px) responsive test passed
- [ ] Tablet (768px) responsive test passed
- [ ] Desktop (1280px) responsive test passed
- [ ] All interactive elements are keyboard-accessible
- [ ] Loading state (skeleton) implemented
- [ ] Empty state implemented
- [ ] Error state implemented
- [ ] Form validation works inline
- [ ] Sonner toast notifications applied
- [ ] All `console.log` statements removed
- [ ] Lucide icon imports are tree-shakeable (named imports)
- [ ] Contrast ratios meet WCAG AA
- [ ] Vite build succeeds, dist/ size is acceptable

---

## 💬 NOTE TO CLAUDE CODE

The rules in this prompt are **ABSOLUTE**. They cannot be skipped with justifications like "we'll fix it later", "keep it simple for now", or "for the sake of brevity." Every component, every page, every pixel must comply. Production quality from the first commit.

Read the project's `SPECIFICATION.md` and `BRANDING.md` files to obtain project-specific accent colors, terminology, and domain details. This prompt defines the universal WebUI standards; project-specific details live in those files.

**Build command:** `cd frontend && npm run build` — output goes to `frontend/dist/`, ready for Go embed.