---
name: code-comments
description: Keep comments minimal and structurally invisible — code shape comes first, rationale lives at the boundary, delete the rest
roles: [edit, review]
tags: [style, comments, readability]
---

## Comment discipline

> **When to apply this.** Before every `Edit`/`Write` that adds a comment, re-read your own added lines against the rules below — not the file's existing comments, the ones you just wrote. This is an authoring step, not only a review standard: the failure mode is knowing these rules and still writing a paragraph into a function body twenty tool calls into a task, because the rule was never checked at the moment of writing.

The default is **no comment**. Code says what it does; a comment earns its place only by saying something the code cannot.

Most comments are a net loss: they restate the line below, drift out of sync, and train readers to skip prose — so the one comment that mattered gets skipped too.

### Structure comes first

**The shape of the code is what a reader scans. A comment must never break it.** Control flow, blocks, and indentation carry meaning; prose wedged between statements destroys the reader's ability to see that shape at a glance.

- Rationale belongs on the **function or type doc comment**, not between statements
- Inside a body: at most **one short line**, above a group of statements — never between tightly coupled lines
- Never split a cohesive unit: a struct literal, `switch`, `if`/`else` chain, or argument list
- **Never comment inside an expression.** Prose spliced into a multi-line string concatenation, argument list, chained call, or collection literal is worse than prose between statements: it breaks a single unit of meaning mid-thought, and the reader has to reassemble the expression around it. Put it above the statement, or at the boundary
- **A multi-line comment inside a function is a smell.** Hoist it to the doc comment, or extract a named function and let the name carry it

```go
// Bad — six lines of prose bury a two-line guard
if repoDir == "" {
    // Session directory unresolvable (transient tmux failure). Skip the tick
    // rather than letting CurrentBranchIn("") fall back to the daemon's own
    // working directory: that reads a foreign repo's branch, which either
    // misattributes time or silently stops tracking entirely.
    return
}

// Good — rationale at the boundary, body stays scannable
//
// A tick is skipped when the session directory cannot be resolved: falling
// back to the process working directory would read a foreign repo's branch
// and misattribute time.
func (d *Daemon) checkBranchTime() {
    ...
    if repoDir == "" {
        return // unresolvable session dir — see doc comment
    }
```

The test: **squint at the function.** If prose interrupts the outline of the logic, move it.

### Make the code not need the comment

Before writing a comment, try removing the need for it. Renaming, extracting a function, or naming a constant is almost always better than explaining.

```go
if now-last <= 300 { // check if the user was active in the last 5 minutes
if userActiveWithin(fiveMinutes) {
```

### Delete these

| Pattern | Why it goes |
|---------|-------------|
| Restating the code | `// increment i` above `i++` |
| Narrating the change | `// Added this to fix the race`, `// Changed from X to Y` — that is the commit message |
| Step-numbering prose | `// Step 1: validate`, `// First, we...` |
| Orientation markers | `// ... rest unchanged`, `// (existing code)` — never let these reach a real edit |
| Explaining the language | `// defer runs at function exit` |
| Section banners | `// ---- helpers ----` — a file needing signposts needs splitting |
| Commented-out code | Version control has it; dead code rots silently |
| Changelog / attribution | `// added by X on 2026-03-01` — that is `git blame` |
| TODOs with no owner or action | Either do it, or file it with a link |

The first five are the habits that show up most in generated code. Check for them specifically before finishing an edit.

### Keep these

**Why, not what** — the reason a reader cannot reconstruct from the mechanism.

**The non-obvious guard** — code that looks removable but is not, so nobody deletes it and reintroduces the bug.

**Invariants and contracts** — ordering, locking, idempotency, anything a caller must uphold.

**The failure that motivated the code** — naming the real incident is the most durable kind; it survives refactors and tells the next reader what breaks if they undo it.

**Consumer-visible contracts** — a JSON shape, on-disk format, or CLI output another process parses, so a rename is understood as a breaking change.

**Length scales with consequence.** A comment is as long as the damage it prevents: a bounds guard earns one line; a hazard that silently corrupts data earns a short paragraph. Anything longer than the code it explains belongs in a doc comment or a spec.

**Length never buys you a place in the body.** Earning a paragraph earns it *at the boundary* — the function or type doc comment. The two decisions are independent: how much to write, and where it goes. Deciding a hazard deserves a paragraph is not permission to put that paragraph between statements. If it will not fit at the boundary, extract a named function so the boundary moves to it.

### Doc comments on exported API

Exported identifiers get a doc comment; unexported ones only when non-obvious. Describe behaviour at the boundary — arguments, returns, errors, side effects — not the implementation.

- **Go**: start with the identifier name (`// SeedBranchTime raises …`)
- **Python**: docstrings on public functions and classes
- **TypeScript**: TSDoc on exported functions and types
- **Bash**: one line above the function; document non-obvious globals and exit codes

### When editing existing code

- Do not comment unchanged lines just because you are nearby
- If a comment beside your change is now wrong, fix or delete it — stale is worse than absent
- A comment promising behaviour the code lacks is a bug; correct it in the same change
- When unsure a comment earns its place, delete it and reread the code

### Review checklist

| Finding | Severity |
|---------|----------|
| Comment contradicts the code, or promises behaviour it lacks | should-fix |
| Multi-line prose fragmenting a function body | should-fix |
| Comment spliced inside an expression (string concat, argument list, literal) | should-fix |
| Non-obvious guard, ordering rule, or consumer-visible contract undocumented | should-fix |
| Restatement, narration, step-numbering, banner, commented-out code, unowned TODO | nit |
| Exported identifier missing a doc comment | nit |
