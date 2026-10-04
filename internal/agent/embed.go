package agent

import _ "embed"

// summarizePrompt is the prompt sent to the LLM to summarize the
// conversation for continuation. Shared by every summarize path — the
// task-start auto-summarize, the between-steps compaction, and the
// :summarize command — so behavior stays consistent.
//
//go:embed summarize_prompt.md
var summarizePrompt string
