# Design mode

A dedicated **designer agent** that shares the F1 window with the edit agent. F1 toggles between Edit (nvim + edit agent) and Design (image preview + designer agent). Both agents persist across toggles — nvim preserves its session, the designer agent keeps its Claude conversation. The designer agent generates React components from prompts, serves them on localhost, captures screenshots, renders them in the left pane, and **automatically validates** the output against the original prompt — iterating until the design is accurate without user intervention.

## Context

### Current state

| Aspect | Current behavior |
|--------|-----------------|
| F1 window | `edit` — permanent, created at launch |
| Layout | Split-left: nvim editor (left, pane 0) + Claude Code agent (right, pane 1) |
| Left pane | Neovim with `NVIM_APPNAME=muxcode` |
| Toggle | None — F1 is always the editor |
| Agents | 9 window agents + modal agents (api) |

### Problem

Designing UI components requires switching between a code editor, a browser, and a terminal. There's no way to visually iterate on a design without leaving the muxcode session. Claude can generate React code but there's no feedback loop — the developer has to manually run a dev server, open a browser, and compare the output to the prompt.

### Goal

A separate designer agent with its own persistent Claude session, toggled via F1, that autonomously generates, renders, validates, and refines UI designs. The agent reads its own screenshots, compares them to the user's prompt, and automatically re-generates if the output doesn't match — producing a validated design with zero manual iteration. Both the editor and designer sessions survive toggles so context is never lost.

## Design

### Architecture: two agents, one window

The F1 window hosts **two agents** that share the window but never run simultaneously in the visible panes. F1 toggles which agent's panes are active. Both agents persist in hidden panes when not active.

```
F1 Window
┌─────────────────────────────────────────────┐
│  Edit mode (default)                        │
│  ┌──────────┬──────────────┐                │
│  │  nvim    │  edit agent  │  ← visible     │
│  │  pane 0  │  pane 1      │                │
│  └──────────┴──────────────┘                │
│  ┌──────────┬──────────────┐                │
│  │  image   │  designer    │  ← hidden      │
│  │  viewer  │  agent       │                │
│  └──────────┴──────────────┘                │
│                                             │
│  Design mode (toggled)                      │
│  ┌──────────┬──────────────┐                │
│  │  image   │  designer    │  ← visible     │
│  │  viewer  │  agent       │                │
│  └──────────┴──────────────┘                │
│  ┌──────────┬──────────────┐                │
│  │  nvim    │  edit agent  │  ← hidden      │
│  └──────────┴──────────────┘                │
└─────────────────────────────────────────────┘
```

The **designer** is a distinct agent role (`designer`) with its own:
- Persistent Claude Code session (survives toggles)
- Agent definition file (`agents/designer.md`)
- Tool profile in `bus/profile.go`
- Bus inbox (receives messages as role `designer`)
- System prompt focused on React generation and visual validation

### Session persistence

Both agents and their left panes persist across toggles using tmux pane management:

**Mechanism**: the toggle command uses `swap-pane` or `join-pane`/`break-pane` to move panes between visible and a hidden holding window. Both processes (nvim, Claude Code, image viewer, designer agent) continue running — only their visibility changes.

```
/tmp/muxcode-bus-{session}/design-hold   # hidden tmux window for inactive panes
```

| Toggle direction | Visible panes | Hidden panes |
|-----------------|---------------|--------------|
| Edit → Design | image viewer + designer agent | nvim + edit agent |
| Design → Edit | nvim + edit agent | image viewer + designer agent |

**Key constraint**: `swap-pane` swaps individual panes, not pairs. The toggle must swap pane 0 and pane 1 independently, maintaining the left/right split layout in both states.

### F1 toggle

F1 behavior changes from simple window selection to a toggle:

```
F1 pressed → is current window F1?
  YES → toggle between edit/design panes
  NO  → switch to F1 window (show whichever mode is active)
```

Implementation: F1 keybinding calls `muxcode design toggle` when already on the F1 window, otherwise does the normal `select-window -t:1`.

```bash
# In tmux.conf
bind -n F1 if-shell '[ "$(tmux display-message -p "#I")" = "1" ]' \
  'run-shell "muxcode design toggle"' \
  'select-window -t:1'
```

### Designer agent

The designer agent is a full Claude Code agent, not a mode of the edit agent. It has its own personality and toolset optimized for UI generation.

**Agent definition** (`agents/designer.md`):

```yaml
---
description: UI designer — generates React components, captures screenshots, and validates designs
---
```

**Key behaviors:**
- Generates self-contained React components with CSS modules or inline styles
- Manages its own dev server lifecycle
- Captures screenshots after each generation
- **Reads its own screenshots** via the Read tool (Claude Code is multimodal)
- Compares the rendered output to the user's original prompt
- Autonomously re-generates if the design doesn't match
- Persists conversation context across toggles — remembers prior design iterations

**Tool profile** (in `bus/profile.go`):

```go
"designer": {
  Tools: []string{
    "Read(*)", "Write(*)", "Edit(*)", "Glob(*)", "Grep(*)",
    "Bash(npm *)", "Bash(npx *)",
    "Bash(muxcode design *)", "Bash(muxcode send *)",
    "Bash(muxcode inbox *)", "Bash(muxcode memory *)",
  },
},
```

### Auto-validation loop

The designer agent's core workflow is an autonomous generate → capture → validate → refine loop. This is driven by the agent's system prompt, not by external hook infrastructure.

```
User prompt: "Create a login page with email/password fields and a blue submit button"
     │
     ▼
┌─────────────┐
│  Generate   │  Write React component to docs/designs/workspace/src/App.tsx
└──────┬──────┘
       ▼
┌─────────────┐
│  Save Draft │  muxcode design save <view> → drafts/<view>/NNN.tsx + NNN.png
└──────┬──────┘
       ▼
┌─────────────┐
│  Capture    │  muxcode design capture → saves screenshot
└──────┬──────┘
       ▼
┌─────────────┐
│  Render     │  Screenshot appears in left pane (image viewer)
└──────┬──────┘
       ▼
┌─────────────┐
│  Validate   │  Read screenshot via Read tool (multimodal)
│             │  Compare against original prompt requirements
└──────┬──────┘
       │
       ├── matches → save final draft, report to user, wait for feedback
       │
       └── doesn't match → identify issues, loop back to Generate
           (max N auto-iterations, default 3, configurable)
```

Every iteration is automatically saved as a numbered draft, so the full design evolution is preserved even during auto-refinement.

**System prompt instructions** (in agent definition):

```
When the user describes a UI design:
1. Read the project theme at docs/designs/workspace/theme/styles.css to understand
   the design system (colors, typography, spacing, component patterns).
2. Write a complete React component to docs/designs/workspace/src/App.tsx.
   Use CSS modules or inline styles — never use utility-first CSS frameworks.
   Follow the theme's CSS custom properties (e.g. var(--color-primary), var(--font-heading)).
3. Save the draft: muxcode design save <view-name> --note "description of this iteration"
4. Capture a screenshot: muxcode design capture
5. Read the screenshot file docs/designs/workspace/captures/latest.png
6. Evaluate whether the rendered design matches the user's request:
   - Check layout, colors, typography, component presence, spacing
   - Verify the design follows the project's theme and style guide
   - If the design is accurate: describe what was built and ask for feedback
   - If the design has issues: describe what's wrong, fix the component, and repeat from step 2
7. Maximum MUXCODE_DESIGN_MAX_ITERATIONS automatic refinement iterations (default 3).
   After reaching the limit, present the current state and ask the user for guidance.

To load a previous design for reference or further iteration:
- muxcode design list                    # see all views
- muxcode design list <view>             # see drafts for a view
- muxcode design load <view> [<draft>]   # load draft into workspace
- muxcode design load-final <view>       # load final version
- muxcode design promote <view>          # promote current workspace to final
```

### Theme and style guide

Designs are **not** built with utility-first CSS frameworks (Tailwind, etc.). Instead, each project provides its own theme via CSS custom properties in `docs/designs/workspace/theme/styles.css`. This supports multi-tenant applications where each tenant has its own brand, colors, and typography.

**Default theme scaffold** — uses Tailwind CSS's design tokens as the starting point. These are CSS custom properties derived from Tailwind's default configuration, giving a well-tested, comprehensive set of design tokens without requiring the Tailwind framework itself:

```css
/* docs/designs/workspace/theme/styles.css */

/* ==========================================================================
   Design System — based on Tailwind CSS default configuration
   Customize these tokens to match your project's brand and style guide.
   The designer agent reads this file before generating components.
   ========================================================================== */

:root {
  /* --- Colors (Tailwind slate/blue/red/green palette) --- */

  /* Primary */
  --color-primary-50: #eff6ff;
  --color-primary-100: #dbeafe;
  --color-primary-200: #bfdbfe;
  --color-primary-300: #93c5fd;
  --color-primary-400: #60a5fa;
  --color-primary-500: #3b82f6;
  --color-primary-600: #2563eb;
  --color-primary-700: #1d4ed8;
  --color-primary-800: #1e40af;
  --color-primary-900: #1e3a8a;
  --color-primary: var(--color-primary-600);
  --color-primary-hover: var(--color-primary-700);

  /* Neutral */
  --color-neutral-50: #f8fafc;
  --color-neutral-100: #f1f5f9;
  --color-neutral-200: #e2e8f0;
  --color-neutral-300: #cbd5e1;
  --color-neutral-400: #94a3b8;
  --color-neutral-500: #64748b;
  --color-neutral-600: #475569;
  --color-neutral-700: #334155;
  --color-neutral-800: #1e293b;
  --color-neutral-900: #0f172a;
  --color-neutral-950: #020617;

  /* Semantic */
  --color-background: #ffffff;
  --color-surface: var(--color-neutral-50);
  --color-border: var(--color-neutral-200);
  --color-text: var(--color-neutral-900);
  --color-text-secondary: var(--color-neutral-600);
  --color-text-muted: var(--color-neutral-400);
  --color-error: #dc2626;
  --color-warning: #d97706;
  --color-success: #16a34a;
  --color-info: var(--color-primary-500);

  /* --- Typography (Tailwind default scale) --- */
  --font-sans: ui-sans-serif, system-ui, sans-serif, "Apple Color Emoji", "Segoe UI Emoji";
  --font-serif: ui-serif, Georgia, Cambria, "Times New Roman", Times, serif;
  --font-mono: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  --font-family: var(--font-sans);
  --font-heading: var(--font-sans);

  --font-size-xs: 0.75rem;     /* 12px */
  --font-size-sm: 0.875rem;    /* 14px */
  --font-size-base: 1rem;      /* 16px */
  --font-size-lg: 1.125rem;    /* 18px */
  --font-size-xl: 1.25rem;     /* 20px */
  --font-size-2xl: 1.5rem;     /* 24px */
  --font-size-3xl: 1.875rem;   /* 30px */
  --font-size-4xl: 2.25rem;    /* 36px */

  --line-height-tight: 1.25;
  --line-height-normal: 1.5;
  --line-height-relaxed: 1.75;

  --font-weight-normal: 400;
  --font-weight-medium: 500;
  --font-weight-semibold: 600;
  --font-weight-bold: 700;

  /* --- Spacing (Tailwind 4px base scale) --- */
  --spacing-0: 0;
  --spacing-px: 1px;
  --spacing-0-5: 0.125rem;   /* 2px */
  --spacing-1: 0.25rem;      /* 4px */
  --spacing-1-5: 0.375rem;   /* 6px */
  --spacing-2: 0.5rem;       /* 8px */
  --spacing-2-5: 0.625rem;   /* 10px */
  --spacing-3: 0.75rem;      /* 12px */
  --spacing-3-5: 0.875rem;   /* 14px */
  --spacing-4: 1rem;         /* 16px */
  --spacing-5: 1.25rem;      /* 20px */
  --spacing-6: 1.5rem;       /* 24px */
  --spacing-8: 2rem;         /* 32px */
  --spacing-10: 2.5rem;      /* 40px */
  --spacing-12: 3rem;        /* 48px */
  --spacing-16: 4rem;        /* 64px */
  --spacing-20: 5rem;        /* 80px */
  --spacing-24: 6rem;        /* 96px */

  /* --- Borders (Tailwind defaults) --- */
  --radius-none: 0;
  --radius-sm: 0.125rem;     /* 2px */
  --radius-base: 0.25rem;    /* 4px */
  --radius-md: 0.375rem;     /* 6px */
  --radius-lg: 0.5rem;       /* 8px */
  --radius-xl: 0.75rem;      /* 12px */
  --radius-2xl: 1rem;        /* 16px */
  --radius-full: 9999px;

  --border-width: 1px;

  /* --- Shadows (Tailwind defaults) --- */
  --shadow-sm: 0 1px 2px 0 rgb(0 0 0 / 0.05);
  --shadow-base: 0 1px 3px 0 rgb(0 0 0 / 0.1), 0 1px 2px -1px rgb(0 0 0 / 0.1);
  --shadow-md: 0 4px 6px -1px rgb(0 0 0 / 0.1), 0 2px 4px -2px rgb(0 0 0 / 0.1);
  --shadow-lg: 0 10px 15px -3px rgb(0 0 0 / 0.1), 0 4px 6px -4px rgb(0 0 0 / 0.1);
  --shadow-xl: 0 20px 25px -5px rgb(0 0 0 / 0.1), 0 8px 10px -6px rgb(0 0 0 / 0.1);

  /* --- Transitions --- */
  --transition-fast: 150ms cubic-bezier(0.4, 0, 0.2, 1);
  --transition-base: 200ms cubic-bezier(0.4, 0, 0.2, 1);
  --transition-slow: 300ms cubic-bezier(0.4, 0, 0.2, 1);

  /* --- Breakpoints (reference only — used for responsive component logic) --- */
  --breakpoint-sm: 640px;
  --breakpoint-md: 768px;
  --breakpoint-lg: 1024px;
  --breakpoint-xl: 1280px;
}

/* --- Base reset --- */
*, *::before, *::after {
  box-sizing: border-box;
  margin: 0;
  padding: 0;
}

body {
  font-family: var(--font-family);
  font-size: var(--font-size-base);
  line-height: var(--line-height-normal);
  color: var(--color-text);
  background-color: var(--color-background);
  -webkit-font-smoothing: antialiased;
  -moz-osx-font-smoothing: grayscale;
}
```

The user customizes this file to match their project's design system — replace colors with brand palette, swap fonts, adjust spacing scale. For multi-tenant apps, swap in a tenant-specific theme file to preview designs under that tenant's brand. The designer agent reads it before each generation to ensure components use the correct tokens.

**Theme in components** — the agent uses CSS custom properties in its generated styles:

```tsx
// Example: designer-generated component using theme tokens
const styles = {
  button: {
    backgroundColor: 'var(--color-primary)',
    color: '#fff',
    padding: 'var(--spacing-sm) var(--spacing-md)',
    borderRadius: 'var(--radius-md)',
    fontFamily: 'var(--font-family)',
    fontSize: 'var(--font-size-base)',
  }
};
```

**Theme per view**: each saved design in `drafts/` and `final/` includes the theme file that was active when it was created, so designs are reproducible even after the theme changes:

```
docs/designs/drafts/login/
├── 001.tsx
├── 001.png
├── 001.css        # theme snapshot at time of draft
├── prompt.md
```

### Output directory

All design artifacts live in `docs/designs/` — a project-level directory committed to version control. The directory has two concerns: a **workspace** (tooling + active component) for the live dev server, and an **archive** of saved designs organized by view name with draft and final stages.

```
docs/designs/
├── workspace/                # active dev server + working component
│   ├── package.json          # minimal React + Vite setup
│   ├── vite.config.ts        # dev server config (fixed port)
│   ├── tsconfig.json         # TypeScript config
│   ├── index.html            # HTML shell
│   ├── theme/
│   │   └── styles.css        # project theme/style guide (colors, typography, spacing)
│   ├── src/
│   │   ├── main.tsx          # React mount point (static)
│   │   └── App.tsx           # Designer-generated component (active work)
│   ├── captures/
│   │   └── latest.png        # most recent screenshot (rendered in left pane + read by Claude)
│   ├── node_modules/         # (gitignored)
│   └── .gitignore            # ignore node_modules
│
├── drafts/                   # saved design drafts, organized by view
│   ├── login/
│   │   ├── 001.tsx           # first draft component
│   │   ├── 001.png           # first draft screenshot
│   │   ├── 001.css           # theme snapshot at time of draft
│   │   ├── 002.tsx           # second iteration
│   │   ├── 002.png           # second iteration screenshot
│   │   ├── 002.css           # theme snapshot
│   │   └── prompt.md         # original design prompt + notes
│   ├── dashboard/
│   │   ├── 001.tsx
│   │   ├── 001.png
│   │   ├── 001.css
│   │   └── prompt.md
│   └── ...
│
└── final/                    # promoted final designs
    ├── login/
    │   ├── App.tsx            # final component
    │   ├── screenshot.png     # final screenshot
    │   ├── styles.css         # theme at time of promotion
    │   └── prompt.md          # original prompt + revision notes
    ├── dashboard/
    │   ├── App.tsx
    │   ├── screenshot.png
    │   ├── styles.css
    │   └── prompt.md
    └── ...
```

- **workspace/**: the live Vite project. Only one component (`App.tsx`) at a time. Contains `theme/styles.css` with CSS custom properties for the project's design system. `node_modules/` is gitignored; everything else is transient working state.
- **drafts/**: every saved iteration of a design, grouped by view name. Each draft is a numbered pair (`.tsx` + `.png`) plus a `prompt.md` capturing the original request.
- **final/**: promoted designs — the validated, approved version of a view. One component + screenshot + prompt per view.

### Design management commands

The designer agent (and user) can save, load, list, and promote designs:

| Command | Description |
|---------|-------------|
| `muxcode design save <view> [--note "..."]` | Save current `App.tsx` + `latest.png` + `theme/styles.css` as next draft for `<view>`. Creates `drafts/<view>/` if needed. Writes `prompt.md` on first save. |
| `muxcode design load <view> [<draft>]` | Load a design into the workspace. Without `<draft>`, loads the latest draft. With a number (e.g. `3`), loads that specific draft. Copies `.tsx` → `App.tsx`, triggers capture + render. |
| `muxcode design load-final <view>` | Load the final version of a view into the workspace for further iteration. |
| `muxcode design promote <view>` | Promote the current workspace to `final/<view>/`. Copies `App.tsx` + `latest.png` + `theme/styles.css` + `prompt.md`. Overwrites any existing final for that view. |
| `muxcode design list` | List all views with draft count and final status. |
| `muxcode design list <view>` | List all drafts for a view with timestamps. |
| `muxcode design diff <view> [<a>] [<b>]` | Show diff between two drafts (default: latest two). |

**Automatic save**: each auto-validation iteration saves a draft automatically. The iteration loop becomes: generate → save draft → capture → validate → refine. This means every intermediate attempt is preserved without manual intervention.

**`prompt.md` format**:

```markdown
# login

> Create a login page with email and password fields, a blue submit button,
> and a "forgot password" link below the form. Use a centered card layout
> with a subtle shadow.

## Revisions
- Draft 001: initial generation
- Draft 002: fixed button color from green to blue
- Draft 003: added forgot password link, adjusted spacing
- Final: promoted from draft 003
```

The designer agent appends to the revisions list on each save, noting what changed.

### Dev server lifecycle

The Vite dev server runs as a managed background process via `muxcode proc`:

| Event | Action |
|-------|--------|
| Enter Design mode | Start dev server if not running: `muxcode proc start design-server "cd docs/designs/workspace && npx vite --port $PORT"` |
| Generate new component | Vite HMR auto-reloads — no restart needed |
| Exit Design mode | Server keeps running (lightweight, reused on next toggle) |
| Session end | Cleaned up by normal proc cleanup |

Port: `MUXCODE_DESIGN_PORT` env var, default `5173`.

### Screenshot capture

The `muxcode design capture` command:

1. Waits for Vite dev server to be ready (health check on port)
2. Runs headless browser screenshot with fixed viewport:

```bash
npx playwright screenshot \
  --browser chromium \
  http://localhost:${port} \
  docs/designs/workspace/captures/latest.png \
  --viewport-size 1280,1280 \
  --wait-for-timeout 1500
```

3. Outputs the path to `latest.png` so the agent can Read it

Draft history is managed separately via `muxcode design save`, which copies both the component and screenshot to `drafts/<view>/NNN.tsx` + `NNN.png`.

**Fixed viewport**: 1280×1280 for all captures. Consistent sizing makes Claude's visual validation more reliable — the same layout renders identically every time regardless of terminal dimensions.

The agent then uses Claude Code's multimodal Read tool on the PNG file — Claude sees the actual rendered pixels and can evaluate the design visually.

### Image rendering in left pane

The left pane runs an image viewer that watches `docs/designs/workspace/captures/latest.png` and re-renders on change:

```bash
muxcode design view   # runs in left pane, watches for file changes
```

| Terminal | Renderer | Notes |
|----------|----------|-------|
| Kitty | `kitty +kitten icat` | Native protocol, best quality |
| iTerm2 | `imgcat` | iTerm2 inline images |
| Sixel-capable | `img2sixel` | xterm, foot, WezTerm |
| Fallback | `chafa` | Unicode block art, works everywhere |

Detection order: check `$TERM_PROGRAM`, `$KITTY_PID`, sixel capability, fall back to chafa.

Uses `fswatch` (macOS) or inotify (Linux) to detect file changes. On each change, clears the pane and re-renders the image scaled to fit the pane dimensions (the source image is always 1280×1280).

### Scaffold initialization

The design workspace is initialized at **session start** (inside `LaunchSession`), not on first toggle. When muxcode starts in a project directory, it runs `muxcode design init` as part of the launch sequence. This ensures the workspace, dependencies, and Playwright browser are ready before the user ever toggles to Design mode — no waiting on first use.

```bash
muxcode design init
```

If `docs/designs/workspace/package.json` already exists, the command is a no-op (fast skip).

**First-time setup** (~90s, runs once per project):
- Creates `docs/designs/workspace/`, `docs/designs/drafts/`, `docs/designs/final/`
- Copies template files to `workspace/` from `~/.config/muxcode/templates/design/`
- Writes the default theme to `workspace/theme/styles.css`
- Writes the Hello World starter component to `workspace/src/App.tsx`
- Runs `npm install` in `workspace/` (~30s)
- Installs Playwright browsers: `npx playwright install chromium` (~60s)
- Creates `workspace/captures/` directory

**Subsequent sessions**: skips everything (package.json exists).

Templates are bundled in `templates/design/` and installed to `~/.config/muxcode/templates/design/` by the Makefile.

### Hello World starter

The initial `App.tsx` is a simple Hello World component that uses the theme tokens. This gives the designer agent a working starting point and confirms the dev server + capture pipeline work on first toggle:

```tsx
/* docs/designs/workspace/src/App.tsx */
export default function App() {
  return (
    <div style={{
      display: 'flex',
      flexDirection: 'column',
      alignItems: 'center',
      justifyContent: 'center',
      minHeight: '100vh',
      fontFamily: 'var(--font-family)',
      backgroundColor: 'var(--color-background)',
      color: 'var(--color-text)',
    }}>
      <h1 style={{
        fontSize: 'var(--font-size-4xl)',
        fontWeight: 'var(--font-weight-bold)',
        color: 'var(--color-primary)',
        marginBottom: 'var(--spacing-4)',
      }}>
        Hello World
      </h1>
      <p style={{
        fontSize: 'var(--font-size-lg)',
        color: 'var(--color-text-secondary)',
      }}>
        Design mode is ready. Describe a UI to get started.
      </p>
    </div>
  );
}
```

When the user toggles to Design mode for the first time, the dev server starts, the Hello World page renders, and a screenshot appears in the left pane — confirming the full pipeline works before any design prompts are given.

### State management

```
/tmp/muxcode-bus-{session}/design-mode.state    # "edit" or "design"
/tmp/muxcode-bus-{session}/design-port           # dev server port
/tmp/muxcode-bus-{session}/design-hold           # hidden window name for inactive panes
/tmp/muxcode-bus-{session}/design-iteration      # current iteration count (reset per prompt)
```

Default state is `edit`. State does not persist across session restarts (agents are relaunched).

### Mode transitions

**First toggle (Edit → Design, no designer agent yet):**

1. Read current mode state (default: `edit`)
2. Create hidden holding window for edit panes
3. Start dev server via `muxcode proc` if not running (scaffold already initialized at session start)
4. Create designer panes: image viewer (pane 0) + designer agent (pane 1)
5. Swap edit panes to holding window, designer panes to F1 window
6. Write `design` to state file
7. Update tmux window name to `design`

**Subsequent toggle (Edit → Design, designer agent exists):**

1. Swap edit panes to holding window
2. Swap designer panes from holding window to F1 window
3. Write `design` to state file
4. Update tmux window name to `design`

**Design → Edit:**

1. Swap designer panes to holding window
2. Swap edit panes from holding window to F1 window
3. Write `edit` to state file
4. Update tmux window name to `edit`

All pane processes (nvim, edit agent, image viewer, designer agent) continue running throughout. No process is killed or restarted during toggles.

### Keybindings

| Key | Action |
|-----|--------|
| `F1` | Toggle edit/design when on F1 window, switch to F1 otherwise |
| `prefix + d` | Toggle edit/design regardless of current window |

### Configuration

| Env var | Default | Description |
|---------|---------|-------------|
| `MUXCODE_DESIGN_PORT` | `5173` | Vite dev server port |
| `MUXCODE_DESIGN_MAX_ITERATIONS` | `3` | Max auto-validation iterations before asking user |
| `MUXCODE_DESIGN_VIEWPORT` | `1280,1280` | Fixed screenshot viewport (width,height) |

### Relationship to edit agent

The designer and edit agents are fully independent:

| Aspect | Edit agent | Designer agent |
|--------|-----------|---------------|
| Role | `edit` | `designer` |
| Claude session | Persistent (survives toggles) | Persistent (survives toggles) |
| System prompt | Code editing, orchestration | React generation, visual validation |
| Left pane | Neovim | Image viewer |
| Tool profile | Full edit tools, delegation | React/npm/design tools |
| Bus inbox | `edit` | `designer` |
| Orchestration | Yes (delegates to build/test/review) | No (self-contained design loop) |

The designer agent does **not** participate in build/test/review chains. It is a self-contained design loop. The user can switch to Edit mode to integrate the generated components from `docs/designs/final/` into the project. Final designs serve as reference implementations — the edit agent can read them to understand the intended UI.

### Console view

When Design mode is active, the designer agent does not need a console view in the left pane — the image viewer serves that purpose. The console infrastructure is not used for the designer role.

## Implementation

### Phase 0: install.sh prerequisites

Updated files:

| File | Change |
|------|--------|
| `install.sh` | Add node, chafa, fswatch to prerequisites check; add Node.js version check |

Success criteria:
- [ ] `install.sh` checks for `node` (>= 18), `chafa`, and `fswatch`
- [ ] `install.sh` warns on Node.js < 18 with version mismatch message
- [ ] Missing design deps use the same non-blocking pattern (warning + "Continue anyway?")

### Phase 1: toggle infrastructure and persistence

New files:

| File | Purpose |
|------|---------|
| `bus/design.go` | Mode state read/write, toggle logic, pane swap via tmux swap-pane, holding window management, design archive CRUD (save/load/promote/list) |
| `bus/design_test.go` | Unit tests for state transitions, toggle logic, iteration counter, archive operations |
| `cmd/design.go` | CLI subcommand: `muxcode design {toggle,init,capture,view,status,save,load,load-final,promote,list,diff}` |
| `agents/designer.md` | Designer agent definition with system prompt |

Updated files:

| File | Change |
|------|--------|
| `main.go` | Add `design` subcommand dispatch |
| `config/tmux.conf` | F1 toggle keybinding, `prefix + d` keybinding |
| `bus/launcher.go` | Initialize design state to `edit` on session start, run `muxcode design init` during launch, create holding window |
| `bus/profile.go` | Add `designer` tool profile |
| `bus/config.go` | Add `designer` to role lists if needed |

Success criteria:
- [ ] `muxcode design toggle` swaps both panes between edit and design
- [ ] Designer agent launches with its own Claude Code session
- [ ] Nvim session preserved across toggles (process stays alive in hidden pane)
- [ ] Designer agent session preserved across toggles (Claude conversation intact)
- [ ] Edit agent session preserved across toggles (Claude conversation intact)
- [ ] F1 toggle works: same-window toggles mode, other-window switches to F1
- [ ] `muxcode design status` shows current mode, agent status, server status

### Phase 2: scaffold and dev server

New files:

| File | Purpose |
|------|---------|
| `templates/design/package.json` | React + Vite + Playwright |
| `templates/design/vite.config.ts` | Vite dev server config |
| `templates/design/index.html` | HTML shell |
| `templates/design/src/main.tsx` | React mount point |
| `templates/design/src/App.tsx` | Hello World starter component |
| `templates/design/tsconfig.json` | TypeScript config |
| `templates/design/theme/styles.css` | Default theme (Tailwind CSS token scale) |
| `templates/design/.gitignore` | Ignore node_modules only |

Updated files:

| File | Change |
|------|--------|
| `Makefile` | Install templates to `~/.config/muxcode/templates/design/` |

Success criteria:
- [ ] `muxcode design init` runs at session start during `LaunchSession`
- [ ] First run creates `docs/designs/{workspace,drafts,final}/`, installs deps, writes Hello World `App.tsx`
- [ ] Subsequent sessions skip init (package.json exists, fast no-op)
- [ ] Default theme uses Tailwind CSS design tokens as CSS custom properties
- [ ] Dev server starts and serves on configured port
- [ ] Vite HMR works — component changes reflected without restart
- [ ] Dev server managed via `muxcode proc` (start/stop/status)
- [ ] Hello World renders correctly on first toggle, confirming pipeline works
- [ ] Only `node_modules/` is gitignored — workspace source, drafts, and finals are committable

### Phase 3: capture pipeline, image viewer, and design archive

Success criteria:
- [ ] `muxcode design capture` screenshots localhost at fixed 1280×1280 viewport
- [ ] Screenshot saved to `docs/designs/workspace/captures/latest.png`
- [ ] Image viewer in left pane auto-updates when `latest.png` changes
- [ ] Terminal image protocol auto-detection (kitty, sixel, chafa fallback)
- [ ] `muxcode design save <view>` saves component + screenshot + theme to `drafts/<view>/NNN.{tsx,png,css}`
- [ ] `muxcode design save` creates `prompt.md` on first save for a view
- [ ] `muxcode design load <view> [<draft>]` loads draft into workspace and triggers render
- [ ] `muxcode design load-final <view>` loads final version into workspace
- [ ] `muxcode design promote <view>` copies workspace to `final/<view>/`
- [ ] `muxcode design list` shows all views with draft count and final status
- [ ] `muxcode design list <view>` shows all drafts with timestamps
- [ ] `muxcode design diff <view> [<a>] [<b>]` diffs two drafts

### Phase 4: auto-validation loop

Success criteria:
- [ ] Designer agent generates React component from user prompt using project theme
- [ ] Agent captures screenshot and reads it via Read tool (multimodal)
- [ ] Agent compares rendered output to original prompt requirements
- [ ] Agent automatically re-generates and re-captures if design doesn't match
- [ ] Default 3 auto-iterations, configurable via `MUXCODE_DESIGN_MAX_ITERATIONS`
- [ ] Iteration counter resets on each new user prompt
- [ ] Full loop works end-to-end: prompt → generate → capture → validate → refine → present

### Phase 5: polish and docs

Success criteria:
- [ ] `muxcode design` added to quick menu (`prefix + b`)
- [ ] Designer agent definition documented
- [ ] Architecture docs updated with design mode
- [ ] Configuration docs updated (env vars, templates)
- [ ] README updated with design mode feature

## Dependencies

| Dependency | Purpose | Install | Category |
|------------|---------|---------|----------|
| Node.js 18+ | React dev server, npm | `brew install node` / `nvm install 18` | Required |
| chafa | Terminal image viewer (fallback renderer) | `brew install chafa` / `apt install chafa` | Required |
| fswatch | File change detection for image viewer | `brew install fswatch` / `apt install inotify-tools` | Required |
| Vite | Fast dev server with HMR | `npm install` (workspace, auto) | Auto (npm) |
| React 18+ | UI component framework | `npm install` (workspace, auto) | Auto (npm) |
| Playwright | Headless screenshot capture | `npm install` (workspace, auto) | Auto (npm) |
| Chromium | Browser engine for Playwright | `npx playwright install chromium` (auto) | Auto (npm) |

**Required** dependencies are checked in `install.sh` and must be present on the system. **Auto** dependencies are installed automatically by `muxcode design init` (runs `npm install` + `npx playwright install chromium` in the workspace).

### install.sh changes

Add design mode dependencies to the prerequisites check in `install.sh`:

```bash
# --- Check prerequisites (existing) ---
command -v tmux   >/dev/null 2>&1 || missing+=("tmux (>= 3.0)")
command -v go     >/dev/null 2>&1 || missing+=("go (>= 1.22)")
command -v claude >/dev/null 2>&1 || missing+=("claude (Claude Code CLI)")
command -v jq     >/dev/null 2>&1 || missing+=("jq")
command -v nvim   >/dev/null 2>&1 || missing+=("nvim")
command -v fzf    >/dev/null 2>&1 || missing+=("fzf")

# --- Design mode dependencies (new) ---
command -v node   >/dev/null 2>&1 || missing+=("node (>= 18, for design mode)")
command -v chafa  >/dev/null 2>&1 || missing+=("chafa (for design mode image preview)")
command -v fswatch >/dev/null 2>&1 || missing+=("fswatch (for design mode file watching)")
```

Node.js version check (after the missing array check):

```bash
# --- Check Node.js version ---
if command -v node >/dev/null 2>&1; then
  NODE_VERSION=$(node -v | sed 's/^v//' | cut -d. -f1)
  if [ "$NODE_VERSION" -ge 18 ] 2>/dev/null; then
    ok "Node.js v$(node -v | sed 's/^v//') found"
  else
    warn "Node.js v$(node -v | sed 's/^v//') found — design mode requires >= 18"
  fi
fi
```
