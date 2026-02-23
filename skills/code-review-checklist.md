---
name: code-review-checklist
description: Code review quality checklist
roles: [review]
tags: [review, quality]
---

## Review checklist

### Correctness
- Does the code do what the PR description says?
- Are edge cases handled?
- Are error paths covered?

### Security
- No hardcoded secrets or credentials
- Input validation on all external data
- No SQL injection, XSS, or path traversal risks

### Style
- Follows existing codebase conventions
- Consistent naming and formatting
- No unnecessary complexity or abstraction

### Testing
- Are new paths tested?
- Do existing tests still pass?
- Are test descriptions clear?

### Performance
- No unnecessary allocations in hot paths
- Database queries are efficient
- No N+1 query patterns
