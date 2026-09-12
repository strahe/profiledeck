package usage

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	grokconfig "github.com/strahe/profiledeck/internal/grokbuild/config"
	"github.com/strahe/profiledeck/internal/store"
)

const (
	SourceGrokBuildSessionJSONL    = "grok-build-session-jsonl"
	GrokBuildUsageParserRevision   = int64(2)
	GrokBuildUsageIdentityRevision = int64(1)
	maxGrokBuildSessionLineBytes   = 16 * 1024 * 1024

	grokBuildSessionUpdateMethod = "_x.ai/session/update"
	acpSessionUpdateMethod       = "session/update"
	grokBuildTurnCompleted       = "turn_completed"
)

func DefaultGrokBuildDir() (string, error) {
	home, err := grokconfig.ResolveHome("")
	if err != nil {
		return "", err
	}
	return home.Dir, nil
}

func ListGrokBuildSessionFiles(grokHome string) ([]SourceFile, error) {
	return ListGrokBuildSessionFilesContext(context.Background(), grokHome)
}

func ListGrokBuildSessionFilesContext(ctx context.Context, grokHome string) ([]SourceFile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	home, err := grokconfig.ResolveHome(grokHome)
	if err != nil {
		return nil, err
	}
	sessionsDir := filepath.Join(home.Dir, "sessions")
	rootInfo, err := os.Lstat(sessionsDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return nil, errors.New("sessions directory under Grok Home is not a regular directory")
	}

	workspaceEntries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return nil, err
	}
	var files []SourceFile
	for _, workspaceEntry := range workspaceEntries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if workspaceEntry.Type()&os.ModeSymlink != 0 || !workspaceEntry.IsDir() {
			continue
		}
		workspacePath := filepath.Join(sessionsDir, workspaceEntry.Name())
		workspaceInfo, err := os.Lstat(workspacePath)
		if err != nil {
			return nil, err
		}
		if workspaceInfo.Mode()&os.ModeSymlink != 0 || !workspaceInfo.IsDir() {
			continue
		}
		sessionEntries, err := os.ReadDir(workspacePath)
		if err != nil {
			return nil, err
		}
		for _, sessionEntry := range sessionEntries {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if sessionEntry.Name() == "subagents" {
				continue
			}
			if sessionEntry.Type()&os.ModeSymlink != 0 || !sessionEntry.IsDir() {
				continue
			}
			sessionPath := filepath.Join(workspacePath, sessionEntry.Name())
			sessionInfo, err := os.Lstat(sessionPath)
			if err != nil {
				return nil, err
			}
			if sessionInfo.Mode()&os.ModeSymlink != 0 || !sessionInfo.IsDir() {
				continue
			}
			updatePath := filepath.Join(sessionPath, "updates.jsonl")
			updateInfo, err := os.Lstat(updatePath)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if updateInfo.Mode()&os.ModeSymlink != 0 || !updateInfo.Mode().IsRegular() {
				continue
			}
			sourceKey, err := SourceKey(updatePath)
			if err != nil {
				return nil, err
			}
			files = append(files, sourceFileFromInfo(updatePath, sourceKey, updateInfo))
		}
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].Path < files[j].Path
	})
	return files, nil
}

func ParseGrokBuildSessionFile(file SourceFile) (FileParseResult, error) {
	return ParseGrokBuildSessionFileContext(context.Background(), file)
}

func ParseGrokBuildSessionFileContext(ctx context.Context, file SourceFile) (FileParseResult, error) {
	return parseGrokBuildSessionFile(ctx, file, maxGrokBuildSessionLineBytes)
}

func parseGrokBuildSessionFile(
	ctx context.Context,
	file SourceFile,
	maxLineBytes int,
) (FileParseResult, error) {
	return parseGrokBuildSessionFileWithOpenHook(ctx, file, maxLineBytes, nil)
}

func parseGrokBuildSessionFileWithOpenHook(
	ctx context.Context,
	file SourceFile,
	maxLineBytes int,
	afterOpen func(),
) (FileParseResult, error) {
	if err := ctx.Err(); err != nil {
		return FileParseResult{}, err
	}
	before, err := os.Lstat(file.Path)
	if err != nil {
		return FileParseResult{}, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() ||
		before.Size() != file.SizeBytes || before.ModTime().UnixMilli() != file.ModifiedUnixMS {
		return FileParseResult{}, errors.New("session file changed before it was read")
	}
	handle, err := os.Open(file.Path)
	if err != nil {
		return FileParseResult{}, err
	}
	defer handle.Close()
	opened, err := handle.Stat()
	if err != nil {
		return FileParseResult{}, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return FileParseResult{}, errors.New("session file identity changed before it was read")
	}
	if afterOpen != nil {
		afterOpen()
	}

	result := FileParseResult{}
	reader := bufio.NewReaderSize(handle, 64*1024)
	for {
		if err := ctx.Err(); err != nil {
			return FileParseResult{}, err
		}
		rawLine, tooLong, err := readCodexSessionLine(ctx, reader, maxLineBytes)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return FileParseResult{}, err
		}
		if tooLong {
			return FileParseResult{}, errors.New("session file contains an oversized record")
		}
		line := bytes.TrimSpace(rawLine)
		if len(line) == 0 {
			continue
		}
		events, unsupported, err := parseGrokBuildSessionLine(line)
		if err != nil {
			return FileParseResult{}, err
		}
		result.Events = append(result.Events, events...)
		if unsupported {
			result.UnsupportedLines++
		}
	}

	openedAfter, err := handle.Stat()
	if err != nil {
		return FileParseResult{}, err
	}
	after, err := os.Lstat(file.Path)
	if err != nil {
		return FileParseResult{}, err
	}
	if after.Mode()&os.ModeSymlink != 0 || !after.Mode().IsRegular() ||
		!os.SameFile(before, openedAfter) || !os.SameFile(before, after) ||
		openedAfter.Size() != before.Size() || after.Size() != before.Size() ||
		openedAfter.ModTime() != before.ModTime() || after.ModTime() != before.ModTime() {
		return FileParseResult{}, errors.New("session file changed while it was read")
	}
	return result, nil
}

type grokBuildEnvelopeClassifier struct {
	Timestamp uint64                    `json:"timestamp"`
	Method    string                    `json:"method"`
	Params    grokBuildParamsClassifier `json:"params"`
}

type grokBuildParamsClassifier struct {
	SessionID string          `json:"sessionId"`
	Update    json.RawMessage `json:"update"`
	Meta      json.RawMessage `json:"_meta"`
}

func parseGrokBuildSessionLine(line []byte) ([]Event, bool, error) {
	var classifier grokBuildEnvelopeClassifier
	if err := decodeJSON(line, &classifier, false); err != nil {
		return nil, false, errors.New("session file contains a malformed record")
	}
	classifier.Method = strings.TrimSpace(classifier.Method)
	classifier.Params.SessionID = strings.TrimSpace(classifier.Params.SessionID)
	if (classifier.Method != acpSessionUpdateMethod &&
		classifier.Method != grokBuildSessionUpdateMethod) ||
		classifier.Params.SessionID == "" ||
		len(classifier.Params.Update) == 0 ||
		!validOptionalJSONObject(classifier.Params.Meta) {
		return nil, false, errors.New("session file contains an unrecognized record")
	}
	var updateTag struct {
		SessionUpdate string `json:"sessionUpdate"`
	}
	if err := decodeJSON(classifier.Params.Update, &updateTag, false); err != nil ||
		strings.TrimSpace(updateTag.SessionUpdate) == "" {
		return nil, false, errors.New("session file contains an unrecognized update")
	}
	if updateTag.SessionUpdate != grokBuildTurnCompleted {
		return nil, false, nil
	}
	if classifier.Method != grokBuildSessionUpdateMethod {
		return nil, false, errors.New("terminal update is in an unexpected location")
	}

	var envelope grokBuildTerminalEnvelope
	if err := decodeJSON(line, &envelope, false); err != nil {
		return nil, false, errors.New("terminal update has an unsupported structure")
	}
	return grokBuildEventsFromTerminal(envelope)
}

type grokBuildTerminalEnvelope struct {
	Timestamp requiredUint64          `json:"timestamp"`
	Method    string                  `json:"method"`
	Params    grokBuildTerminalParams `json:"params"`
}

type grokBuildTerminalParams struct {
	SessionID string                  `json:"sessionId"`
	Update    grokBuildTerminalUpdate `json:"update"`
	Meta      json.RawMessage         `json:"_meta"`
}

type grokBuildTerminalUpdate struct {
	SessionUpdate string          `json:"sessionUpdate"`
	PromptID      string          `json:"prompt_id"`
	StopReason    string          `json:"stop_reason"`
	AgentResult   *string         `json:"agent_result,omitempty"`
	Usage         *grokBuildUsage `json:"usage,omitempty"`
}

type requiredUint64 struct {
	value uint64
	set   bool
}

func (value *requiredUint64) UnmarshalJSON(raw []byte) error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errors.New("integer is required")
	}
	if err := json.Unmarshal(raw, &value.value); err != nil {
		return err
	}
	value.set = true
	return nil
}

type optionalBool struct {
	value bool
	set   bool
}

type optionalUint64 struct {
	value uint64
	set   bool
}

func (value *optionalUint64) UnmarshalJSON(raw []byte) error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errors.New("integer must not be null")
	}
	if err := json.Unmarshal(raw, &value.value); err != nil {
		return err
	}
	value.set = true
	return nil
}

func (value *optionalBool) UnmarshalJSON(raw []byte) error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errors.New("boolean must not be null")
	}
	if err := json.Unmarshal(raw, &value.value); err != nil {
		return err
	}
	value.set = true
	return nil
}

type grokBuildModelUsage struct {
	values  map[string]grokBuildUsageModel
	present bool
}

func (value *grokBuildModelUsage) UnmarshalJSON(raw []byte) error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errors.New("model usage must be an object")
	}
	var encoded map[string]json.RawMessage
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return err
	}
	value.values = make(map[string]grokBuildUsageModel, len(encoded))
	for model, encodedRow := range encoded {
		var row grokBuildUsageModel
		if err := decodeJSON(encodedRow, &row, false); err != nil {
			return err
		}
		value.values[model] = row
	}
	value.present = true
	return nil
}

type grokBuildUsage struct {
	grokBuildUsageModel
	ModelUsage      grokBuildModelUsage `json:"modelUsage"`
	NumTurns        requiredUint64      `json:"numTurns"`
	UsageIncomplete optionalBool        `json:"usageIsIncomplete"`
}

type grokBuildUsageModel struct {
	InputTokens         requiredUint64 `json:"inputTokens"`
	OutputTokens        requiredUint64 `json:"outputTokens"`
	TotalTokens         requiredUint64 `json:"totalTokens"`
	CachedReadTokens    requiredUint64 `json:"cachedReadTokens"`
	CacheCreationTokens optionalUint64 `json:"cacheCreationTokens"`
	ReasoningTokens     requiredUint64 `json:"reasoningTokens"`
	ModelCalls          requiredUint64 `json:"modelCalls"`
	APIDurationMS       requiredUint64 `json:"apiDurationMs"`
	CostUSDTicks        *int64         `json:"costUsdTicks,omitempty"`
	CostIsPartial       optionalBool   `json:"costIsPartial"`
}

func grokBuildEventsFromTerminal(
	envelope grokBuildTerminalEnvelope,
) ([]Event, bool, error) {
	if envelope.Method != grokBuildSessionUpdateMethod ||
		!validOptionalJSONObject(envelope.Params.Meta) ||
		envelope.Params.Update.SessionUpdate != grokBuildTurnCompleted ||
		strings.TrimSpace(envelope.Params.SessionID) == "" ||
		strings.TrimSpace(envelope.Params.Update.PromptID) == "" ||
		strings.TrimSpace(envelope.Params.Update.StopReason) == "" {
		return nil, false, errors.New("terminal update is invalid")
	}
	if !envelope.Timestamp.set || envelope.Timestamp.value > math.MaxInt64/1000 {
		return nil, false, errors.New("terminal timestamp is out of range")
	}
	usage := envelope.Params.Update.Usage
	if usage == nil {
		return nil, true, nil
	}
	if err := validateGrokBuildUsage(*usage); err != nil {
		return nil, false, err
	}
	if usage.UsageIncomplete.set && usage.UsageIncomplete.value {
		return nil, true, nil
	}
	if usage.InputTokens.value == 0 && usage.OutputTokens.value == 0 &&
		usage.CachedReadTokens.value == 0 && usage.CacheCreationTokens.value == 0 {
		return nil, true, nil
	}
	if !usage.ModelUsage.present || len(usage.ModelUsage.values) == 0 {
		return nil, false, errors.New("terminal usage has no model breakdown")
	}

	models := make([]string, 0, len(usage.ModelUsage.values))
	for model := range usage.ModelUsage.values {
		models = append(models, model)
	}
	sort.Strings(models)
	events := make([]Event, 0, len(models))
	promptID := strings.TrimSpace(envelope.Params.Update.PromptID)
	sessionKey := grokBuildSessionKey(strings.TrimSpace(envelope.Params.SessionID))
	occurredAt := int64(envelope.Timestamp.value) * 1000
	for _, rawModel := range models {
		model := pricingModelID(rawModel)
		if model == "" || store.NormalizeUsageModelKey(model) != model {
			return nil, false, errors.New("terminal usage contains an invalid model")
		}
		row := usage.ModelUsage.values[rawModel]
		if row.InputTokens.value == 0 && row.OutputTokens.value == 0 &&
			row.CachedReadTokens.value == 0 && row.CacheCreationTokens.value == 0 {
			continue
		}
		tokens := TokenCounts{
			InputTokens:       int64(row.InputTokens.value),
			CachedInputTokens: int64(row.CachedReadTokens.value),
			OutputTokens:      int64(row.OutputTokens.value),
			TotalTokens:       int64(row.TotalTokens.value),
		}
		cost, status := EstimateGrokBuildCostMicros(model, tokens)
		if cost != nil && row.CacheCreationTokens.value > 0 {
			status = CostStatusPartial
		}
		events = append(events, Event{
			EventKey:            GrokBuildEventID(promptID, model),
			SessionID:           sessionKey,
			Model:               model,
			OccurredAtUnixMS:    occurredAt,
			InputTokens:         tokens.InputTokens,
			CachedInputTokens:   tokens.CachedInputTokens,
			OutputTokens:        tokens.OutputTokens,
			TotalTokens:         tokens.TotalTokens,
			EstimatedCostMicros: cost,
			CostStatus:          status,
		})
	}
	if len(events) == 0 {
		return nil, true, nil
	}
	return events, false, nil
}

func validateGrokBuildUsage(usage grokBuildUsage) error {
	if !usage.NumTurns.set {
		return errors.New("terminal usage is missing turn totals")
	}
	if err := validateGrokBuildUsageModel(usage.grokBuildUsageModel); err != nil {
		return err
	}
	if usage.NumTurns.value > usage.ModelCalls.value {
		return errors.New("terminal usage turn totals are inconsistent")
	}
	if !usage.ModelUsage.present {
		if usage.InputTokens.value == 0 && usage.OutputTokens.value == 0 &&
			usage.CachedReadTokens.value == 0 && usage.TotalTokens.value == 0 &&
			usage.CacheCreationTokens.value == 0 &&
			usage.ReasoningTokens.value == 0 && usage.ModelCalls.value == 0 &&
			usage.APIDurationMS.value == 0 {
			return nil
		}
		return errors.New("terminal usage is missing its model breakdown")
	}

	var sums grokBuildUsageSums
	normalizedModels := make(map[string]struct{}, len(usage.ModelUsage.values))
	for rawModel, row := range usage.ModelUsage.values {
		model := pricingModelID(rawModel)
		if model == "" || store.NormalizeUsageModelKey(model) != model {
			return errors.New("terminal usage contains an invalid model")
		}
		if _, duplicate := normalizedModels[model]; duplicate {
			return errors.New("terminal usage contains duplicate models")
		}
		normalizedModels[model] = struct{}{}
		if err := validateGrokBuildUsageModel(row); err != nil {
			return err
		}
		if !sums.add(row) {
			return errors.New("terminal usage totals overflow")
		}
	}
	if sums.input != usage.InputTokens.value ||
		sums.output != usage.OutputTokens.value ||
		sums.total != usage.TotalTokens.value ||
		sums.cached != usage.CachedReadTokens.value ||
		(usage.CacheCreationTokens.set && sums.cacheCreation != usage.CacheCreationTokens.value) ||
		sums.reasoning != usage.ReasoningTokens.value ||
		sums.calls != usage.ModelCalls.value ||
		sums.duration != usage.APIDurationMS.value {
		return errors.New("terminal usage totals are inconsistent")
	}
	return nil
}

func validateGrokBuildUsageModel(model grokBuildUsageModel) error {
	fields := []requiredUint64{
		model.InputTokens,
		model.OutputTokens,
		model.TotalTokens,
		model.CachedReadTokens,
		model.ReasoningTokens,
		model.ModelCalls,
		model.APIDurationMS,
	}
	for _, field := range fields {
		if !field.set || field.value > math.MaxInt64 {
			return errors.New("terminal usage contains an invalid total")
		}
	}
	if model.CacheCreationTokens.set && model.CacheCreationTokens.value > math.MaxInt64 {
		return errors.New("terminal usage contains an invalid total")
	}
	total, ok := addUint64(model.InputTokens.value, model.OutputTokens.value)
	if !ok || model.TotalTokens.value != total ||
		model.CachedReadTokens.value > model.InputTokens.value ||
		model.ReasoningTokens.value > model.OutputTokens.value {
		return errors.New("terminal usage totals are inconsistent")
	}
	return nil
}

type grokBuildUsageSums struct {
	input, output, total, cached, cacheCreation, reasoning, calls, duration uint64
}

func (sums *grokBuildUsageSums) add(model grokBuildUsageModel) bool {
	values := []struct {
		target *uint64
		value  uint64
	}{
		{&sums.input, model.InputTokens.value},
		{&sums.output, model.OutputTokens.value},
		{&sums.total, model.TotalTokens.value},
		{&sums.cached, model.CachedReadTokens.value},
		{&sums.cacheCreation, model.CacheCreationTokens.value},
		{&sums.reasoning, model.ReasoningTokens.value},
		{&sums.calls, model.ModelCalls.value},
		{&sums.duration, model.APIDurationMS.value},
	}
	for _, item := range values {
		value, ok := addUint64(*item.target, item.value)
		if !ok || value > math.MaxInt64 {
			return false
		}
		*item.target = value
	}
	return true
}

func addUint64(left, right uint64) (uint64, bool) {
	if left > math.MaxUint64-right {
		return 0, false
	}
	return left + right, true
}

func decodeJSON(raw []byte, destination any, strict bool) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if strict {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err != nil {
			return err
		}
		return errors.New("multiple JSON values")
	}
	return nil
}

func validOptionalJSONObject(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return true
	}
	var object map[string]json.RawMessage
	return decodeJSON(raw, &object, false) == nil && object != nil
}

func GrokBuildEventID(promptID, model string) store.UsageKey {
	promptID = strings.TrimSpace(promptID)
	model = pricingModelID(model)
	if promptID == "" || model == "" {
		return store.UsageKey{}
	}
	return domainSeparatedUsageKey(
		"profiledeck-grok-build-usage-event-v1",
		grokconfig.ProviderID,
		SourceGrokBuildSessionJSONL,
		fmt.Sprintf("%d", GrokBuildUsageIdentityRevision),
		promptID,
		model,
	)
}

func GrokBuildEventDigest(events []Event, limit int64) store.UsageKey {
	if limit < 0 || limit > int64(len(events)) {
		limit = int64(len(events))
	}
	hash := sha256.New()
	writeHashField(hash, "profiledeck-grok-build-usage-prefix-v1")
	var encoded [8]byte
	for _, event := range events[:limit] {
		_, _ = hash.Write(event.EventKey[:])
		for _, value := range []int64{
			event.InputTokens,
			event.CachedInputTokens,
			event.OutputTokens,
			event.TotalTokens,
		} {
			binary.BigEndian.PutUint64(encoded[:], uint64(value))
			_, _ = hash.Write(encoded[:])
		}
	}
	var digest store.UsageKey
	copy(digest[:], hash.Sum(nil))
	return digest
}

func grokBuildSessionKey(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ""
	}
	key := domainSeparatedUsageKey(
		"profiledeck-grok-build-usage-session-v1",
		sessionID,
	)
	return "derived-" + hex.EncodeToString(key[:])
}

func domainSeparatedUsageKey(domain string, fields ...string) store.UsageKey {
	hash := sha256.New()
	writeHashField(hash, domain)
	for _, field := range fields {
		writeHashField(hash, field)
	}
	var result store.UsageKey
	copy(result[:], hash.Sum(nil))
	return result
}

type byteWriter interface {
	Write([]byte) (int, error)
}

func writeHashField(writer byteWriter, value string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = writer.Write(length[:])
	_, _ = writer.Write([]byte(value))
}
