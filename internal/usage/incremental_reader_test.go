package usage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/strahe/profiledeck/internal/store"
)

type openHookUsageFileSystem struct {
	hook func()
}

func (fileSystem openHookUsageFileSystem) Lstat(path string) (os.FileInfo, error) {
	return os.Lstat(path)
}

func (fileSystem openHookUsageFileSystem) Open(path string) (usageOpenFile, error) {
	handle, err := os.Open(path)
	if err == nil && fileSystem.hook != nil {
		fileSystem.hook()
	}
	return handle, err
}

func TestCheckpointParsersRejectConcurrentAppend(t *testing.T) {
	for _, test := range []struct {
		name    string
		content string
		parse   func(context.Context, SourceFile, usageFileSystem) error
	}{
		{
			name:    "Codex",
			content: `{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":10,"output_tokens":2}}}}`,
			parse: func(ctx context.Context, file SourceFile, fileSystem usageFileSystem) error {
				_, err := parseCodexCheckpointFile(
					ctx,
					file,
					0,
					newCodexParserState(file),
					store.UsageKey{},
					store.UsageKey{},
					fileSystem,
					nil,
				)
				return err
			},
		},
		{
			name:    "Grok Build",
			content: `{"timestamp":0,"method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"user_message_chunk"}}}`,
			parse: func(ctx context.Context, file SourceFile, fileSystem usageFileSystem) error {
				_, err := parseGrokBuildCheckpointFile(
					ctx,
					file,
					0,
					store.UsageKey{},
					store.UsageKey{},
					fileSystem,
					nil,
				)
				return err
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "session.jsonl")
			if err := os.WriteFile(path, []byte(test.content), 0o600); err != nil {
				t.Fatalf("write fixture: %v", err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatalf("stat fixture: %v", err)
			}
			file := sourceFileFromInfo(path, usageTestEventKey(test.name), info)
			fileSystem := openHookUsageFileSystem{hook: func() {
				handle, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
				if err != nil {
					t.Fatalf("open append fixture: %v", err)
				}
				if _, err := handle.WriteString("\n" + test.content); err != nil {
					_ = handle.Close()
					t.Fatalf("append fixture: %v", err)
				}
				if err := handle.Close(); err != nil {
					t.Fatalf("close append fixture: %v", err)
				}
			}}
			if err := test.parse(context.Background(), file, fileSystem); err == nil {
				t.Fatal("concurrent append unexpectedly advanced the checkpoint")
			}
		})
	}
}

func TestCheckpointParsersResumeUnterminatedRecord(t *testing.T) {
	t.Run("Codex", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.jsonl")
		partial := `{"type":"session_meta","session_id":"`
		if err := os.WriteFile(path, []byte(partial), 0o600); err != nil {
			t.Fatalf("write partial fixture: %v", err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat partial fixture: %v", err)
		}
		file := sourceFileFromInfo(path, usageTestEventKey("Codex partial"), info)
		first, err := parseCodexCheckpointFile(
			context.Background(), file, 0, newCodexParserState(file),
			store.UsageKey{}, store.UsageKey{}, nil, nil,
		)
		if err != nil || first.ProcessedBytes != 0 || first.InvalidLines != 0 {
			t.Fatalf("partial Codex checkpoint = %#v, err = %v", first, err)
		}
		appendUsageCheckpointFixture(t, path, `session-tail"}`+"\n")
		info, err = os.Stat(path)
		if err != nil {
			t.Fatalf("stat completed fixture: %v", err)
		}
		file = sourceFileFromInfo(path, file.SourceKey, info)
		state, err := decodeCodexParserState(first.ParserStateJSON)
		if err != nil {
			t.Fatalf("decode partial parser state: %v", err)
		}
		completed, err := parseCodexCheckpointFile(
			context.Background(), file, first.ProcessedBytes, state,
			first.CheckpointEventDigest, first.BoundaryDigest, nil, nil,
		)
		if err != nil || completed.ProcessedBytes != info.Size() || completed.InvalidLines != 0 {
			t.Fatalf("completed Codex checkpoint = %#v, err = %v", completed, err)
		}
	})

	t.Run("Grok Build", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "updates.jsonl")
		partial := `{"timestamp":1,"method":"session/update","params":{"sessionId":"session-tail","update":`
		if err := os.WriteFile(path, []byte(partial), 0o600); err != nil {
			t.Fatalf("write partial fixture: %v", err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat partial fixture: %v", err)
		}
		file := sourceFileFromInfo(path, usageTestEventKey("Grok partial"), info)
		first, err := parseGrokBuildCheckpointFile(
			context.Background(), file, 0, store.UsageKey{}, store.UsageKey{}, nil, nil,
		)
		if err != nil || first.ProcessedBytes != 0 || first.InvalidLines != 0 {
			t.Fatalf("partial Grok Build checkpoint = %#v, err = %v", first, err)
		}
		appendUsageCheckpointFixture(t, path, `{"sessionUpdate":"agent_message_chunk"}}}`+"\n")
		info, err = os.Stat(path)
		if err != nil {
			t.Fatalf("stat completed fixture: %v", err)
		}
		file = sourceFileFromInfo(path, file.SourceKey, info)
		completed, err := parseGrokBuildCheckpointFile(
			context.Background(), file, first.ProcessedBytes,
			first.CheckpointEventDigest, first.BoundaryDigest, nil, nil,
		)
		if err != nil || completed.ProcessedBytes != info.Size() || completed.InvalidLines != 0 {
			t.Fatalf("completed Grok Build checkpoint = %#v, err = %v", completed, err)
		}
	})
}

func appendUsageCheckpointFixture(t *testing.T, path, content string) {
	t.Helper()
	handle, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open fixture for append: %v", err)
	}
	if _, err := handle.WriteString(content); err != nil {
		_ = handle.Close()
		t.Fatalf("append fixture: %v", err)
	}
	if err := handle.Close(); err != nil {
		t.Fatalf("close appended fixture: %v", err)
	}
}

func TestEncodeCodexParserStateRejectsOversizedCheckpoint(t *testing.T) {
	state := newCodexParserState(SourceFile{
		Path:      "session.jsonl",
		SourceKey: usageTestEventKey("oversized-state"),
	})
	for index := range 12_000 {
		session := fmt.Sprintf("session-%05d", index)
		state.PreviousTotals[session] = TokenCounts{InputTokens: 1, TotalTokens: 1}
		state.UsageOrdinals[session] = 1
	}
	if _, err := encodeCodexParserState(state); err == nil {
		t.Fatal("oversized parser checkpoint unexpectedly encoded")
	}
}
