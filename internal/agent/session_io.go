package agent

// Session I/O: command handling, prompt processing, and tool formatting.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	domainerrors "github.com/alayacore/alayacore/internal/errors"
	"github.com/alayacore/alayacore/internal/llm"
)

// ============================================================================
// Command Handling
// ============================================================================

func (s *Session) handleCommandSync(ctx context.Context, cmd string) {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		s.writeError(domainerrors.ErrEmptyCommand.Error())
		return
	}

	if s.dispatchCommand(ctx, cmd) {
		return
	}

	s.writeError(domainerrors.NewSessionErrorf("command", "unknown cmd <%s>", parts[0]).Error())
}

func (s *Session) cancelTask() {
	s.mu.Lock()
	inProgress := s.inProgress
	cancelCurrent := s.cancelCurrent
	s.mu.Unlock()
	if inProgress && cancelCurrent != nil {
		cancelCurrent()
		return
	}
	s.writeError(domainerrors.ErrNothingToCancel.Error())
}

func (s *Session) summarize(ctx context.Context) {
	prompt := "Please summarize the conversation above in a concise manner. Return ONLY the summary, no introductions or explanations."

	beforeCount := len(s.Messages)

	outputTokens, err := s.processPrompt(ctx, prompt, s.Messages)
	if err != nil {
		s.writeError(err.Error())
		return
	}

	var lastAssistantMsg llm.Message
	for i := beforeCount; i < len(s.Messages); i++ {
		if s.Messages[i].Role == llm.RoleAssistant {
			lastAssistantMsg = s.Messages[i]
		}
	}

	s.Messages = []llm.Message{lastAssistantMsg}
	if outputTokens > 0 {
		s.mu.Lock()
		s.ContextTokens = outputTokens
		s.mu.Unlock()
	}
	s.sendSystemInfo()
}

func (s *Session) saveSession(args []string) {
	var path string
	switch len(args) {
	case 0:
		if s.SessionFile == "" {
			s.writeError(domainerrors.ErrNoSessionFile.Error())
			return
		}
		path = s.SessionFile
	case 1:
		path = expandPath(args[0])
	default:
		s.writeError("usage: :save [filename]")
		return
	}

	if err := s.saveSessionToFile(path); err != nil {
		s.writeError(domainerrors.Wrapf("save", err, "failed to save session").Error())
	} else {
		s.writeNotifyf("Session saved to %s", path)
	}
}

func (s *Session) handleModelSet(args []string) {
	if s.ModelManager == nil {
		s.writeError(domainerrors.ErrModelManagerNotInitialized.Error())
		return
	}

	if len(args) == 0 {
		s.writeError("usage: :model_set <id>")
		return
	}

	s.mu.Lock()
	inProgress := s.inProgress
	s.mu.Unlock()
	if inProgress {
		s.writeError("Cannot switch model while a task is running. Please wait or cancel the current task.")
		return
	}

	modelID := args[0]
	model := s.ModelManager.GetModel(modelID)
	if model == nil {
		s.writeError(domainerrors.NewSessionErrorf("model_set", "model not found: %s", modelID).Error())
		return
	}

	if err := s.ModelManager.SetActive(modelID); err != nil {
		s.writeError(err.Error())
		return
	}

	if s.RuntimeManager != nil {
		_ = s.RuntimeManager.SetActiveModel(model.Name)
	}

	if err := s.SwitchModel(model); err != nil {
		s.writeError("Failed to switch model: " + err.Error())
		return
	}

	s.writeNotifyf("Switched to model: %s (%s)", model.Name, model.ModelName)
}

func (s *Session) handleModelLoad() {
	if s.ModelManager == nil {
		s.writeError(domainerrors.ErrModelManagerNotInitialized.Error())
		return
	}

	path := s.ModelManager.GetFilePath()
	if path == "" {
		s.writeError(domainerrors.ErrNoModelFilePath.Error())
		return
	}

	if err := s.ModelManager.LoadFromFile(path); err != nil {
		s.writeError(domainerrors.Wrapf("model_load", err, "failed to load models").Error())
		return
	}

	s.initModelManager()
	s.sendSystemInfo()
	s.writeNotify("Models reloaded from configuration file")
}

func (s *Session) handleTaskQueueGetAll() {
	s.sendSystemInfo()
}

func (s *Session) handleTaskQueueDel(args []string) {
	if len(args) == 0 {
		s.writeError("usage: :taskqueue_del <queue_id>")
		return
	}

	queueID := args[0]
	if s.DeleteQueueItem(queueID) {
		s.sendSystemInfo()
	} else {
		s.writeError(domainerrors.NewSessionErrorf("taskqueue_del", "queue item %s not found", queueID).Error())
	}
}

// ============================================================================
// Tool Formatting
// ============================================================================

//nolint:gocyclo // tool formatting requires handling many tool types
func formatToolCall(toolName, input string) string {
	var fields map[string]interface{}
	if err := json.Unmarshal([]byte(input), &fields); err != nil {
		return ""
	}

	switch toolName {
	case "posix_shell":
		if cmd, ok := fields["command"].(string); ok {
			return fmt.Sprintf("%s: %s", toolName, escapeNewlines(cmd))
		}
	case "activate_skill":
		if name, ok := fields["name"].(string); ok {
			return fmt.Sprintf("%s: %s", toolName, name)
		}
	case "read_file":
		args := []string{}
		if path, ok := fields["path"].(string); ok {
			args = append(args, path)
		}
		if startLine, ok := fields["start_line"].(string); ok && startLine != "" {
			args = append(args, startLine)
		}
		if endLine, ok := fields["end_line"].(string); ok && endLine != "" {
			args = append(args, endLine)
		}
		if len(args) > 0 {
			return fmt.Sprintf("%s: %s", toolName, strings.Join(args, ", "))
		}
	case "write_file":
		path, _ := fields["path"].(string)
		content, _ := fields["content"].(string)
		if path == "" || content == "" {
			return ""
		}
		return fmt.Sprintf("%s: %s\n%s", toolName, path, content)
	case "edit_file":
		path, _ := fields["path"].(string)
		oldStr, _ := fields["old_string"].(string)
		newStr, _ := fields["new_string"].(string)
		if path == "" || oldStr == "" || newStr == "" {
			return ""
		}

		var lines []string
		lines = append(lines, fmt.Sprintf("%s: %s", toolName, path))

		oldLines := strings.Split(oldStr, "\n")
		newLines := strings.Split(newStr, "\n")

		diffPairs := computeDiff(oldLines, newLines)

		for _, pair := range diffPairs {
			oldPart := strings.ReplaceAll(pair.old, "\n", "\\n")
			newPart := strings.ReplaceAll(pair.new, "\n", "\\n")
			lines = append(lines, fmt.Sprintf("\x00%s\x00%s", oldPart, newPart))
		}

		return strings.Join(lines, "\n")
	}
	return ""
}

func escapeNewlines(s string) string {
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\t", "\\t")
	return s
}

// diffPair represents a pair of old/new lines in a diff
type diffPair struct {
	old string
	new string
}

// computeDiff computes the LCS-based diff between old and new lines
func computeDiff(oldLines, newLines []string) []diffPair {
	lcs := computeLCS(oldLines, newLines)

	var result []diffPair
	i, j := 0, 0

	for _, lcsLine := range lcs {
		for i < len(oldLines) && oldLines[i] != lcsLine {
			result = append(result, diffPair{old: oldLines[i], new: ""})
			i++
		}

		for j < len(newLines) && newLines[j] != lcsLine {
			result = append(result, diffPair{old: "", new: newLines[j]})
			j++
		}

		if i < len(oldLines) && j < len(newLines) {
			result = append(result, diffPair{old: oldLines[i], new: newLines[j]})
			i++
			j++
		}
	}

	for i < len(oldLines) {
		result = append(result, diffPair{old: oldLines[i], new: ""})
		i++
	}
	for j < len(newLines) {
		result = append(result, diffPair{old: "", new: newLines[j]})
		j++
	}

	return result
}

// computeLCS computes the Longest Common Subsequence of two string slices
func computeLCS(a, b []string) []string {
	if len(a) == 0 || len(b) == 0 {
		return nil
	}

	m, n := len(a), len(b)
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}

	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if a[i-1] == b[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else {
				dp[i][j] = max(dp[i-1][j], dp[i][j-1])
			}
		}
	}

	var lcs []string
	i, j := m, n
	for i > 0 && j > 0 {
		switch {
		case a[i-1] == b[j-1]:
			lcs = append([]string{a[i-1]}, lcs...)
			i--
			j--
		case dp[i-1][j] > dp[i][j-1]:
			i--
		default:
			j--
		}
	}

	return lcs
}
