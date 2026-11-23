Summarize the conversation for continuation, for an agent with no memory of this work. The summary replaces the conversation, so it has to stand on its own. Do not call tools: answer from the conversation only.

Provide:
1. **Task** — the user's request and success criteria
2. **Done** — what is finished, one line each, with the command or test that proves it; items finished before this compaction as a count
3. **State** — files changed and why; name the file or symbol, do not paste code that can be re-read
4. **Blocked** — unresolved errors, failing tests, open questions
5. **Next** — ordered actions to resume; the first confirms the current state with a tool before acting on this summary

Rules:
- Mark anything you did not verify as `assumed`, in every section
- Keep it short by dropping how the work was done — the what and the why stay
- Quote the user's requirements, constraints and error messages verbatim — the message, not the whole log
- Keep the approaches you tried and rejected, and why
- If the conversation already begins with a summary, carry its Task, constraints and rejected approaches forward in the user's own words — do not paraphrase them
