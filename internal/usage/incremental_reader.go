package usage

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"

	grokconfig "github.com/strahe/profiledeck/internal/grokbuild/config"
	"github.com/strahe/profiledeck/internal/store"
)

const (
	usageCheckpointRevision = int64(1)
	usageBoundaryBytes      = int64(64 * 1024)
	maxCodexParserStateSize = 1024 * 1024
)

type UsageSyncObserver interface {
	UsageFileOpened()
	UsageBytesRead(int64)
}

type usageFileSystem interface {
	Lstat(string) (os.FileInfo, error)
	Open(string) (usageOpenFile, error)
}

type usageOpenFile interface {
	io.Reader
	io.ReaderAt
	io.Seeker
	io.Closer
	Stat() (os.FileInfo, error)
}

type osUsageFileSystem struct{}

func (osUsageFileSystem) Lstat(path string) (os.FileInfo, error) {
	return os.Lstat(path)
}

func (osUsageFileSystem) Open(path string) (usageOpenFile, error) {
	return os.Open(path)
}

type observedReaderAt struct {
	reader   io.ReaderAt
	observer UsageSyncObserver
}

func (reader observedReaderAt) ReadAt(buffer []byte, offset int64) (int, error) {
	count, err := reader.reader.ReadAt(buffer, offset)
	if reader.observer != nil && count > 0 {
		reader.observer.UsageBytesRead(int64(count))
	}
	return count, err
}

type checkpointParseResult struct {
	FileParseResult
	ProcessedBytes        int64
	BoundaryDigest        store.UsageKey
	CheckpointEventDigest store.UsageKey
	ParserStateJSON       string
}

func parseCodexCheckpointFile(
	ctx context.Context,
	file SourceFile,
	start int64,
	state codexParserState,
	eventDigest store.UsageKey,
	expectedBoundary store.UsageKey,
	fileSystem usageFileSystem,
	observer UsageSyncObserver,
) (checkpointParseResult, error) {
	if fileSystem == nil {
		fileSystem = osUsageFileSystem{}
	}
	handle, err := openStableUsageFile(file, fileSystem, observer)
	if err != nil {
		return checkpointParseResult{}, err
	}
	defer handle.Close()
	if start < 0 || start > file.SizeBytes {
		return checkpointParseResult{}, errors.New("Codex usage checkpoint is outside the session file")
	}
	boundary, err := readUsageBoundaryWindow(handle, start, observer)
	if err != nil {
		return checkpointParseResult{}, err
	}
	if start > 0 {
		if boundary.digest() != expectedBoundary {
			return checkpointParseResult{}, errUsageBoundaryChanged
		}
	}
	if eventDigest.IsZero() {
		eventDigest = initialCheckpointEventDigest(ProviderCodex)
	}
	result := checkpointParseResult{ProcessedBytes: start, CheckpointEventDigest: eventDigest}
	section := io.NewSectionReader(observedReaderAt{reader: handle, observer: observer}, start, file.SizeBytes-start)
	reader := bufio.NewReaderSize(section, 64*1024)
	for {
		if err := ctx.Err(); err != nil {
			return checkpointParseResult{}, err
		}
		rawLine, boundarySuffix, tooLong, terminated, consumed, err := readCheckpointLine(
			ctx,
			reader,
			maxCodexSessionLineBytes,
		)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return checkpointParseResult{}, err
		}
		if tooLong {
			result.InvalidLines++
			result.ProcessedBytes += consumed
			boundary.appendSuffix(boundarySuffix, consumed)
			continue
		}
		line := bytes.TrimSpace(rawLine)
		if len(line) == 0 {
			result.ProcessedBytes += consumed
			boundary.appendSuffix(boundarySuffix, consumed)
			continue
		}
		event, invalid, unsupported := state.parseLine(line)
		if invalid && !terminated {
			break
		}
		if invalid {
			result.InvalidLines++
		} else if unsupported {
			result.UnsupportedLines++
		} else if event != nil {
			result.Events = append(result.Events, *event)
			result.CheckpointEventDigest = advanceCheckpointEventDigest(
				ProviderCodex,
				result.CheckpointEventDigest,
				*event,
			)
		}
		result.ProcessedBytes += consumed
		boundary.appendSuffix(boundarySuffix, consumed)
	}
	stateJSON, err := encodeCodexParserState(state)
	if err != nil {
		return checkpointParseResult{}, err
	}
	result.ParserStateJSON = stateJSON
	result.BoundaryDigest = boundary.digest()
	if err := verifyStableUsageFile(file, handle, fileSystem); err != nil {
		return checkpointParseResult{}, err
	}
	return result, nil
}

func parseGrokBuildCheckpointFile(
	ctx context.Context,
	file SourceFile,
	start int64,
	eventDigest store.UsageKey,
	expectedBoundary store.UsageKey,
	fileSystem usageFileSystem,
	observer UsageSyncObserver,
) (checkpointParseResult, error) {
	if fileSystem == nil {
		fileSystem = osUsageFileSystem{}
	}
	handle, err := openStableUsageFile(file, fileSystem, observer)
	if err != nil {
		return checkpointParseResult{}, err
	}
	defer handle.Close()
	if start < 0 || start > file.SizeBytes {
		return checkpointParseResult{}, errors.New("usage checkpoint is outside the Grok Build session file")
	}
	boundary, err := readUsageBoundaryWindow(handle, start, observer)
	if err != nil {
		return checkpointParseResult{}, err
	}
	if start > 0 {
		if boundary.digest() != expectedBoundary {
			return checkpointParseResult{}, errUsageBoundaryChanged
		}
	}
	if eventDigest.IsZero() {
		eventDigest = initialCheckpointEventDigest(grokconfig.ProviderID)
	}
	result := checkpointParseResult{
		ProcessedBytes:        start,
		CheckpointEventDigest: eventDigest,
		ParserStateJSON:       "{}",
	}
	section := io.NewSectionReader(observedReaderAt{reader: handle, observer: observer}, start, file.SizeBytes-start)
	reader := bufio.NewReaderSize(section, 64*1024)
	for {
		if err := ctx.Err(); err != nil {
			return checkpointParseResult{}, err
		}
		rawLine, boundarySuffix, tooLong, terminated, consumed, err := readCheckpointLine(
			ctx,
			reader,
			maxGrokBuildSessionLineBytes,
		)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return checkpointParseResult{}, err
		}
		if tooLong {
			return checkpointParseResult{}, errors.New("session file contains an oversized record")
		}
		line := bytes.TrimSpace(rawLine)
		if len(line) == 0 {
			result.ProcessedBytes += consumed
			boundary.appendSuffix(boundarySuffix, consumed)
			continue
		}
		events, unsupported, parseErr := parseGrokBuildSessionLine(line)
		if parseErr != nil && !terminated {
			break
		}
		if parseErr != nil {
			return checkpointParseResult{}, parseErr
		}
		for _, event := range events {
			result.Events = append(result.Events, event)
			result.CheckpointEventDigest = advanceCheckpointEventDigest(
				grokconfig.ProviderID,
				result.CheckpointEventDigest,
				event,
			)
		}
		if unsupported {
			result.UnsupportedLines++
		}
		result.ProcessedBytes += consumed
		boundary.appendSuffix(boundarySuffix, consumed)
	}
	result.BoundaryDigest = boundary.digest()
	if err := verifyStableUsageFile(file, handle, fileSystem); err != nil {
		return checkpointParseResult{}, err
	}
	return result, nil
}

var errUsageBoundaryChanged = errors.New("usage file changed before the saved checkpoint")

func openStableUsageFile(
	file SourceFile,
	fileSystem usageFileSystem,
	observer UsageSyncObserver,
) (usageOpenFile, error) {
	if fileSystem == nil {
		fileSystem = osUsageFileSystem{}
	}
	before, err := fileSystem.Lstat(file.Path)
	if err != nil {
		return nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() ||
		sourceFileFromInfo(file.Path, file.SourceKey, before).MetadataDigest != file.MetadataDigest {
		return nil, errors.New("usage file changed before it was read")
	}
	handle, err := fileSystem.Open(file.Path)
	if err != nil {
		return nil, err
	}
	if observer != nil {
		observer.UsageFileOpened()
	}
	opened, err := handle.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		_ = handle.Close()
		if err != nil {
			return nil, err
		}
		return nil, errors.New("usage file identity changed before it was read")
	}
	return handle, nil
}

func verifyStableUsageFile(file SourceFile, handle usageOpenFile, fileSystem usageFileSystem) error {
	opened, err := handle.Stat()
	if err != nil {
		return err
	}
	after, err := fileSystem.Lstat(file.Path)
	if err != nil {
		return err
	}
	if after.Mode()&os.ModeSymlink != 0 || !after.Mode().IsRegular() ||
		!os.SameFile(opened, after) ||
		sourceFileFromInfo(file.Path, file.SourceKey, opened).MetadataDigest != file.MetadataDigest ||
		sourceFileFromInfo(file.Path, file.SourceKey, after).MetadataDigest != file.MetadataDigest {
		return errors.New("usage file changed while it was read")
	}
	return nil
}

type usageBoundaryWindow struct {
	data []byte
}

func readUsageBoundaryWindow(
	reader io.ReaderAt,
	processedBytes int64,
	observer UsageSyncObserver,
) (usageBoundaryWindow, error) {
	if processedBytes < 0 {
		return usageBoundaryWindow{}, errors.New("usage checkpoint offset is invalid")
	}
	start := processedBytes - usageBoundaryBytes
	if start < 0 {
		start = 0
	}
	length := processedBytes - start
	window := usageBoundaryWindow{data: make([]byte, length)}
	if length == 0 {
		return window, nil
	}
	section := io.NewSectionReader(observedReaderAt{reader: reader, observer: observer}, start, length)
	if _, err := io.ReadFull(section, window.data); err != nil {
		return usageBoundaryWindow{}, err
	}
	return window, nil
}

func (window *usageBoundaryWindow) appendSuffix(suffix []byte, consumed int64) {
	if consumed <= 0 {
		return
	}
	if consumed >= usageBoundaryBytes {
		window.data = append(window.data[:0], suffix...)
		return
	}
	window.data = append(window.data, suffix...)
	if excess := int64(len(window.data)) - usageBoundaryBytes; excess > 0 {
		copy(window.data, window.data[excess:])
		window.data = window.data[:usageBoundaryBytes]
	}
}

func (window usageBoundaryWindow) digest() store.UsageKey {
	hash := sha256.New()
	_, _ = hash.Write([]byte("profiledeck-usage-file-boundary-v1\x00"))
	_, _ = hash.Write(window.data)
	var digest store.UsageKey
	copy(digest[:], hash.Sum(nil))
	return digest
}

func readCheckpointLine(
	ctx context.Context,
	reader *bufio.Reader,
	limit int,
) ([]byte, []byte, bool, bool, int64, error) {
	if limit <= 0 {
		limit = maxCodexSessionLineBytes
	}
	var line []byte
	var boundarySuffix []byte
	var consumed int64
	tooLong := false
	for {
		if err := ctx.Err(); err != nil {
			return nil, nil, false, false, consumed, err
		}
		fragment, err := reader.ReadSlice('\n')
		consumed += int64(len(fragment))
		boundarySuffix = appendBoundarySuffix(boundarySuffix, fragment)
		if !tooLong && len(line)+len(fragment) <= limit {
			line = append(line, fragment...)
		} else if len(fragment) > 0 {
			tooLong = true
			line = nil
		}
		switch {
		case err == nil:
			return line, boundarySuffix, tooLong, true, consumed, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF) && consumed == 0:
			return nil, nil, false, false, 0, io.EOF
		case errors.Is(err, io.EOF):
			return line, boundarySuffix, tooLong, false, consumed, nil
		default:
			return nil, nil, false, false, consumed, err
		}
	}
}

func appendBoundarySuffix(suffix, fragment []byte) []byte {
	if int64(len(fragment)) >= usageBoundaryBytes {
		return append(suffix[:0], fragment[len(fragment)-int(usageBoundaryBytes):]...)
	}
	suffix = append(suffix, fragment...)
	if excess := int64(len(suffix)) - usageBoundaryBytes; excess > 0 {
		copy(suffix, suffix[excess:])
		suffix = suffix[:usageBoundaryBytes]
	}
	return suffix
}

func initialCheckpointEventDigest(providerID string) store.UsageKey {
	hash := sha256.Sum256([]byte("profiledeck-usage-event-checkpoint-v1\x00" + providerID))
	return store.UsageKey(hash)
}

func advanceCheckpointEventDigest(
	providerID string,
	previous store.UsageKey,
	event Event,
) store.UsageKey {
	hash := sha256.New()
	_, _ = hash.Write([]byte("profiledeck-usage-event-checkpoint-v1\x00"))
	_, _ = hash.Write([]byte(providerID))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(previous[:])
	_, _ = hash.Write(event.EventKey[:])
	var encoded [8]byte
	for _, value := range []int64{
		event.InputTokens,
		event.CachedInputTokens,
		event.OutputTokens,
		event.TotalTokens,
	} {
		binary.BigEndian.PutUint64(encoded[:], uint64(value))
		_, _ = hash.Write(encoded[:])
	}
	var digest store.UsageKey
	copy(digest[:], hash.Sum(nil))
	return digest
}

func checkpointEventDigest(providerID string, events []Event, limit int64) store.UsageKey {
	if limit < 0 || limit > int64(len(events)) {
		limit = int64(len(events))
	}
	digest := initialCheckpointEventDigest(providerID)
	for _, event := range events[:limit] {
		digest = advanceCheckpointEventDigest(providerID, digest, event)
	}
	return digest
}

func encodeCodexParserState(state codexParserState) (string, error) {
	if err := validateCodexParserState(state); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return "", err
	}
	if len(encoded) > maxCodexParserStateSize {
		return "", errors.New("Codex usage parser checkpoint is invalid")
	}
	return string(encoded), nil
}

func decodeCodexParserState(encoded string) (codexParserState, error) {
	if len(encoded) == 0 || len(encoded) > maxCodexParserStateSize {
		return codexParserState{}, errors.New("Codex usage parser checkpoint is invalid")
	}
	decoder := json.NewDecoder(strings.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var state codexParserState
	if err := decoder.Decode(&state); err != nil {
		return codexParserState{}, errors.New("Codex usage parser checkpoint is invalid")
	}
	if err := validateCodexParserState(state); err != nil {
		return codexParserState{}, err
	}
	return state, nil
}

func validateCodexParserState(state codexParserState) error {
	if len(state.CurrentSessionKey) == 0 || len(state.CurrentSessionKey) > 256 ||
		store.NormalizeUsageModelKey(state.ModelForStorage) != state.ModelForStorage ||
		len(state.ModelForPricing) > 200 || state.PreviousTotals == nil || state.UsageOrdinals == nil {
		return errors.New("Codex usage parser checkpoint is invalid")
	}
	for session, totals := range state.PreviousTotals {
		if len(session) == 0 || len(session) > 256 || !totals.valid() {
			return errors.New("Codex usage parser checkpoint is invalid")
		}
	}
	for session, ordinal := range state.UsageOrdinals {
		if len(session) == 0 || len(session) > 256 || ordinal <= 0 {
			return errors.New("Codex usage parser checkpoint is invalid")
		}
	}
	return nil
}
