package usage

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	claudeconfig "github.com/strahe/profiledeck/internal/claudecode/config"
	"github.com/strahe/profiledeck/internal/store"
)

const (
	SourceClaudeCodeSessionJSONL    = "claude-code-session-jsonl"
	ClaudeCodeUsageParserRevision   = int64(3)
	ClaudeCodeUsageIdentityRevision = int64(1)
	maxClaudeCodeSessionLineBytes   = 16 * 1024 * 1024
)

func ListClaudeCodeSessionFilesContext(ctx context.Context, dir string) ([]SourceFile, error) {
	files, err := listClaudeCodeSessionFiles(ctx, dir)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("claude code session files could not be read")
	}
	return files, nil
}

func listClaudeCodeSessionFiles(ctx context.Context, dir string) ([]SourceFile, error) {
	root, err := claudeconfig.ResolveConfigDir(dir)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, errors.New("claude code log directory is unavailable")
	}
	projects := filepath.Join(root, "projects")
	info, err = os.Lstat(projects)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, errors.New("claude code projects directory is unavailable")
	}
	var files []SourceFile
	err = filepath.WalkDir(projects, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		relative, err := filepath.Rel(projects, path)
		if err != nil {
			return err
		}
		parts := strings.Split(relative, string(filepath.Separator))
		if entry.IsDir() {
			if len(parts) > 3 || len(parts) == 3 && parts[2] != "subagents" {
				return filepath.SkipDir
			}
			return nil
		}
		main := len(parts) == 2 && strings.HasSuffix(parts[1], ".jsonl")
		child := len(parts) == 4 && parts[2] == "subagents" && strings.HasPrefix(parts[3], "agent-") && strings.HasSuffix(parts[3], ".jsonl")
		if !main && !child {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		key, err := SourceKey(path)
		if err != nil {
			return err
		}
		files = append(files, sourceFileFromInfo(path, key, info))
		return nil
	})
	return files, err
}

type claudeCacheCreation struct {
	FiveMinutes *int64 `json:"ephemeral_5m_input_tokens"`
	OneHour     *int64 `json:"ephemeral_1h_input_tokens"`
}

type claudeUsage struct {
	Input      *int64               `json:"input_tokens"`
	Output     *int64               `json:"output_tokens"`
	Read       *int64               `json:"cache_read_input_tokens"`
	Write      *int64               `json:"cache_creation_input_tokens"`
	Creation   *claudeCacheCreation `json:"cache_creation"`
	Iterations []claudeIteration    `json:"iterations"`
	Speed      string               `json:"speed"`
	Geo        string               `json:"inference_geo"`
	ServerTool map[string]int64     `json:"server_tool_use"`
}

type claudeIteration struct {
	claudeUsage
	Type  string `json:"type"`
	Model string `json:"model"`
}

func parseClaudeCodeSessionLine(line []byte) (*Event, bool, bool, error) {
	event, reason, unsupported, err := parseClaudeCodeSessionObservation(line)
	return event, reason != "", unsupported, err
}

func parseClaudeCodeSessionObservation(line []byte) (*Event, string, bool, error) {
	return parseClaudeCodeSessionObservationForRevision(line, ClaudeCodeUsageParserRevision)
}

func parseClaudeCodeSessionObservationForRevision(line []byte, revision int64) (*Event, string, bool, error) {
	var record struct {
		Type      string `json:"type"`
		RequestID string `json:"requestId"`
		SessionID string `json:"sessionId"`
		Timestamp string `json:"timestamp"`
		Message   struct {
			ID         string          `json:"id"`
			Model      string          `json:"model"`
			StopReason *string         `json:"stop_reason"`
			Usage      json.RawMessage `json:"usage"`
		} `json:"message"`
	}
	if !json.Valid(line) {
		return nil, store.ClaudeCodeInvalidJSON, false, errors.New("session record is invalid")
	}
	if err := json.Unmarshal(line, &record); err != nil {
		return nil, store.ClaudeCodeInvalidFields, false, nil
	}
	if record.Type != "assistant" || record.Message.Model == "<synthetic>" {
		return nil, "", false, nil
	}
	if len(record.Message.Usage) == 0 || string(record.Message.Usage) == "null" || record.RequestID == "" || record.Message.ID == "" || record.SessionID == "" {
		return nil, "", true, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(record.Message.Usage, &fields); err != nil {
		return nil, store.ClaudeCodeInvalidFields, false, nil
	}
	if iterations := fields["iterations"]; len(iterations) > 0 && string(iterations) != "null" && iterations[0] != '[' {
		return nil, "", true, nil
	}
	usage := &claudeUsage{}
	if err := json.Unmarshal(record.Message.Usage, usage); err != nil {
		return nil, store.ClaudeCodeInvalidFields, false, nil
	}
	if len(record.RequestID) > 256 || len(record.Message.ID) > 256 || len(record.SessionID) > 256 {
		return nil, store.ClaudeCodeInvalidIdentity, false, nil
	}
	model := store.NormalizeUsageModelKey(record.Message.Model)
	if model == store.UsageUnknownModelKey {
		return nil, "", true, nil
	}
	final := record.Message.StopReason != nil && *record.Message.StopReason != ""
	if final {
		switch *record.Message.StopReason {
		case "end_turn", "tool_use", "max_tokens", "stop_sequence", "refusal", "pause_turn":
		case "model_context_window_exceeded":
			if revision < 3 {
				return nil, "", true, nil
			}
		default:
			return nil, "", true, nil
		}
	}
	// Claude Code clears these counts when restoring history, leaving nested usage intact.
	if revision >= 3 && final && usage.Input != nil && *usage.Input == 0 && usage.Read != nil && *usage.Read == 0 && usage.Write != nil && *usage.Write == 0 && usage.Output != nil && *usage.Output == 0 {
		return nil, "", false, nil
	}
	if reason := invalidClaudeUsageReason(usage, final); reason != "" {
		return nil, reason, false, nil
	}
	if len(usage.Iterations) > 0 {
		if len(usage.Iterations) != 1 || usage.Iterations[0].Type != "message" || usage.Iterations[0].Model != "" && usage.Iterations[0].Model != model {
			return nil, "", true, nil
		}
		iteration := &usage.Iterations[0].claudeUsage
		if invalidClaudeUsageReason(iteration, final) != "" || *iteration.Input != *usage.Input || *iteration.Read != *usage.Read || *iteration.Write != *usage.Write || final && *iteration.Output != *usage.Output {
			return nil, store.ClaudeCodeIterationMismatch, false, nil
		}
		if usage.Creation != nil && iteration.Creation != nil && (*usage.Creation.FiveMinutes != *iteration.Creation.FiveMinutes || *usage.Creation.OneHour != *iteration.Creation.OneHour) {
			return nil, store.ClaudeCodeIterationMismatch, false, nil
		}
		if usage.Creation == nil {
			usage.Creation = iteration.Creation
		}
	}
	occurred, err := time.Parse(time.RFC3339Nano, record.Timestamp)
	if err != nil || occurred.UnixMilli() <= 0 {
		return nil, store.ClaudeCodeInvalidTimestamp, false, nil
	}
	input, ok := addClaudeTokens(*usage.Input, *usage.Read, *usage.Write)
	if !ok {
		return nil, store.ClaudeCodeTokenOverflow, false, nil
	}
	event := Event{EventKey: claudeCodeIdentity("request", record.RequestID, record.Message.ID), SessionID: "derived-" + claudeCodeIdentity("session", record.SessionID).String(), Model: model, OccurredAtUnixMS: occurred.UnixMilli(), InputTokens: input, CachedInputTokens: *usage.Read, CacheWriteInputTokens: usage.Write, CacheCreationInputTokens: usage.Write, TokenStatus: store.UsageTokensPartial, PricingEligible: true}
	if usage.Creation != nil {
		event.CacheWrite5mTokens = usage.Creation.FiveMinutes
		event.CacheWrite1hTokens = usage.Creation.OneHour
	}
	if final {
		event.TokenStatus = store.UsageTokensComplete
		event.OutputTokens = *usage.Output
	}
	event.TotalTokens, ok = addClaudeTokens(input, event.OutputTokens)
	if !ok {
		return nil, store.ClaudeCodeTokenOverflow, false, nil
	}
	if usage.Speed != "" && usage.Speed != "standard" || usage.Geo != "" && usage.Geo != "not_available" && usage.Geo != "global" {
		event.PricingEligible = false
	}
	for _, count := range usage.ServerTool {
		if count != 0 {
			event.PricingEligible = false
		}
	}
	return &event, "", false, nil
}

func invalidClaudeUsageReason(usage *claudeUsage, final bool) string {
	if usage.Input == nil || usage.Read == nil || usage.Write == nil || final && usage.Output == nil {
		return store.ClaudeCodeInvalidTokens
	}
	for _, value := range []*int64{usage.Input, usage.Read, usage.Write, usage.Output} {
		if value != nil && *value < 0 {
			return store.ClaudeCodeInvalidTokens
		}
	}
	if usage.Creation != nil {
		if usage.Creation.FiveMinutes == nil || usage.Creation.OneHour == nil {
			return store.ClaudeCodeInvalidTokens
		}
		sum, ok := addClaudeTokens(*usage.Creation.FiveMinutes, *usage.Creation.OneHour)
		if !ok || sum != *usage.Write {
			return store.ClaudeCodeCacheMismatch
		}
	}
	return ""
}

func addClaudeTokens(values ...int64) (int64, bool) {
	var total int64
	for _, value := range values {
		if value < 0 || value > math.MaxInt64-total {
			return 0, false
		}
		total += value
	}
	return total, true
}

func claudeCodeIdentity(kind string, values ...string) store.UsageKey {
	hash := sha256.New()
	_, _ = hash.Write([]byte("profiledeck-claude-code-" + kind + "-v1\x00" + claudeconfig.ProviderID + "\x00" + SourceClaudeCodeSessionJSONL))
	for _, value := range values {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(value)))
		_, _ = hash.Write(size[:])
		_, _ = hash.Write([]byte(value))
	}
	var key store.UsageKey
	copy(key[:], hash.Sum(nil))
	return key
}
