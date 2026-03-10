---
description: Save session context to memory and compress the conversation
---

Perform a combined session compact:

1. **Save to memory first**: Write a concise summary of the current session's key work, decisions, file changes, and important state to muxcode memory:
   ```bash
   muxcode-agent-bus session compact "<summary>"
   ```
   The summary should capture what was accomplished, key decisions made, and any important context that should survive a session restart.

2. **Then compress the conversation**: Run the built-in `/compact` command to reduce the Claude Code context window.

Always do step 1 before step 2 — `/compact` only trims the conversation, it does not persist anything to memory.
