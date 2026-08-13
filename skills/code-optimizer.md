---
name: code-optimizer
description: Remove duplication, dead code, and needless indirection — fewer lines as a result, never as the goal
roles: [edit, review]
tags: [refactor, simplification, readability]
---

## Reducing code

Priority order: **correctness > readability > size.** Fewer lines is a *symptom* of removing duplication and indirection, never the objective. Code optimized for line count becomes dense, loses its guards, and costs more to read than it saved.

The question is never "can this be shorter?" It is **"does this carry its weight?"** — does every line, parameter, and abstraction earn the cost of being read.

### Reduce these

| Target | Why it pays |
|--------|-------------|
| **Duplication** | Extract the shared helper. Four copies of arg parsing become one; the next change happens in one place |
| **Dead code** | Unused exports, unreachable branches, functions kept alive only by their own test |
| **Redundant work** | Two reads of the same source, a value recomputed per loop, a second pass that could fold into the first |
| **Pass-through indirection** | A wrapper that only forwards, an interface with one implementation and no seam value |
| **Unused surface** | Parameters no caller sets, options nothing selects, returns nobody reads |
| **Reimplemented stdlib** | Hand-rolled `contains`, `min`, sorting, string building |
| **Over-nesting** | Early returns and guard clauses flatten arrow code without deleting a single check |

The best reductions delete a *concept*, not lines: one fewer thing to hold in your head, one fewer place to change.

### Never trade these for lines

| Keep | Because |
|------|---------|
| **Guard clauses and validation** | Code that looks removable is the most dangerous thing to remove — the guard exists because something failed once |
| **Tests** | Never shrink a test to save lines. Coverage is the license to refactor at all |
| **Doc comments at boundaries** | They cost lines and save readers; that trade is already won |
| **Named intermediates** | A well-named variable that "could be inlined" is often the only thing explaining the expression |
| **Error context** | Collapsing distinct errors into one generic message costs debuggability |
| **Separate responsibilities** | Two similar functions with different jobs stay two functions |

### When smaller is worse

Merging two near-identical handlers that mean different things saves lines and hurts clarity — the reader now has to work out which mode they are in. If a merge needs a boolean parameter to select behaviour, it was two functions.

Likewise, a clever chain that replaces six readable lines with one dense expression is not a win. If it needs a comment to explain what it does, it was better long.

### Verify before deleting

- **Grep every caller** across the whole repo before removing anything exported — including tests, other modules, and build-tagged files
- Names can be reached by reflection, struct tags, JSON keys, config strings, or another process parsing CLI output — a symbol with no Go caller is not necessarily unused
- If something is kept alive only by its own test, that is a real deletion candidate: remove both
- When unsure whether a guard is load-bearing, keep it and say why in a doc comment

### Process

- **Behaviour-preserving by default.** A refactor that changes behaviour is not a refactor
- **Tests pass before and after**, and the run must be real — a refactor validated by an assumed-green suite is a rewrite
- **One concern per change.** Do not mix reduction with a bug fix or a feature; the diff stops being reviewable
- **Stay in scope.** Refactor the code the task already touches. Tidying adjacent code inflates the diff and buries the actual change
- **Report what was dropped.** If a reduction removes a capability, even an unused one, say so rather than letting it vanish silently

### Agent-specific failure modes

Check for these before finishing — they are the characteristic ways generated refactors go wrong:

- **Rewriting a whole file** when three lines needed changing
- **Deleting "unused" code** that a grep would have shown is used elsewhere
- **Collapsing readable steps** into a clever one-liner
- **Refactoring adjacent code** nobody asked about
- **Removing a guard** because the happy path makes it look redundant
- **Claiming a reduction without measuring** — state what actually shrank

### Review checklist

| Finding | Severity |
|---------|----------|
| Refactor changed behaviour, or was not covered by a real test run | must-fix |
| Guard, validation, or error context removed to save lines | must-fix |
| Deleted symbol still referenced anywhere in the repo | must-fix |
| Duplicated logic left in place when a helper was already extracted nearby | should-fix |
| Redundant work (double read, recompute in loop) | should-fix |
| Dense expression that needs a comment to explain it | nit |
| Unrelated code refactored, inflating the diff | nit |
