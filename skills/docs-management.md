---
name: docs-management
description: Manage documentation lifecycle — move specs, update status, check off phases
roles:
  - plan
  - edit
tags:
  - docs
  - requirements
  - specs
---

## Documentation lifecycle management

Manage requirements specs through their lifecycle: backlog -> drafts -> completed.

### Move a spec between directories

Move specs to reflect their current state:

```bash
# Move from backlog to drafts (starting work)
git mv docs/requirements/my-feature.md docs/requirements/drafts/my-feature.md

# Move from drafts to completed (fully implemented)
git mv docs/requirements/drafts/my-feature.md docs/requirements/completed/my-feature.md
```

After moving, update cross-references in other docs that link to the old path.

### Update status field

Find and update the `## Status` section at the bottom of a spec:

- `Draft` — initial design, not yet started
- `In Progress` — actively being implemented
- `Complete` — fully implemented and verified

### Check off acceptance criteria

Acceptance criteria use markdown checkboxes. Check them off as phases complete:

```markdown
### Phase 1: Core implementation

- [x] Feature A implemented
- [x] Tests written and passing
- [ ] Documentation updated
```

Change `- [ ]` to `- [x]` for completed items.

### Update phase status tables

Some specs use tables to track phase status:

```markdown
| Phase | Status |
|-------|--------|
| Phase 1 | Complete |
| Phase 2 | In Progress |
| Phase 3 | Not Started |
```

### Cross-reference verification

When updating docs, verify that:
- File paths in "Key files" tables still exist (`ls` or `Glob` to check)
- Cross-links to other docs use correct relative paths
- Code examples match current function signatures
