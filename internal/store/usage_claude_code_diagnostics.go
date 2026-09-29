package store

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
)

const (
	ClaudeCodeInvalidJSON       = "malformed_json"
	ClaudeCodeRecordTooLarge    = "record_too_large"
	ClaudeCodeInvalidFields     = "invalid_fields"
	ClaudeCodeInvalidTokens     = "invalid_tokens"
	ClaudeCodeCacheMismatch     = "cache_tokens_mismatch"
	ClaudeCodeIterationMismatch = "iteration_tokens_mismatch"
	ClaudeCodeInvalidIdentity   = "invalid_identity"
	ClaudeCodeInvalidTimestamp  = "invalid_timestamp"
	ClaudeCodeTokenOverflow     = "token_overflow"
)

func DecodeClaudeCodeUsageInvalidReasons(state string, invalidLines int64) (map[string]int64, error) {
	if len(state) > 4096 || !strings.HasPrefix(strings.TrimSpace(state), "{") || invalidLines < 0 {
		return nil, errors.New("invalid Claude Code usage diagnostics")
	}
	decoder := json.NewDecoder(strings.NewReader(state))
	decoder.UseNumber()
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil, errors.New("invalid Claude Code usage diagnostics")
	}
	counts := make(map[string]int64)
	var total int64
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return nil, errors.New("invalid Claude Code usage diagnostics")
		}
		reason, ok := key.(string)
		if !ok {
			return nil, errors.New("invalid Claude Code usage diagnostics")
		}
		if _, duplicate := counts[reason]; duplicate {
			return nil, errors.New("invalid Claude Code usage diagnostics")
		}
		switch reason {
		case ClaudeCodeInvalidJSON, ClaudeCodeRecordTooLarge, ClaudeCodeInvalidFields,
			ClaudeCodeInvalidTokens, ClaudeCodeCacheMismatch, ClaudeCodeIterationMismatch,
			ClaudeCodeInvalidIdentity, ClaudeCodeInvalidTimestamp, ClaudeCodeTokenOverflow:
		default:
			return nil, errors.New("invalid Claude Code usage diagnostics")
		}
		value, err := decoder.Token()
		if err != nil {
			return nil, errors.New("invalid Claude Code usage diagnostics")
		}
		number, ok := value.(json.Number)
		if !ok {
			return nil, errors.New("invalid Claude Code usage diagnostics")
		}
		count, err := number.Int64()
		if err != nil {
			return nil, errors.New("invalid Claude Code usage diagnostics")
		}
		if count < 0 || count > invalidLines-total {
			return nil, errors.New("invalid Claude Code usage diagnostics")
		}
		total += count
		counts[reason] = count
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, errors.New("invalid Claude Code usage diagnostics")
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("invalid Claude Code usage diagnostics")
	}
	return counts, nil
}

const claudeCodeUsageDiagnosticsViolationQuery = `SELECT COUNT(1)
	FROM claude_code_usage_import_files AS f
	WHERE CASE WHEN NOT json_valid(f.parser_state_json) THEN 1
	ELSE json_type(f.parser_state_json) <> 'object'
		OR length(CAST(f.parser_state_json AS BLOB)) > 4096
		OR EXISTS (SELECT 1 FROM json_each(f.parser_state_json) AS reason
			WHERE reason.key NOT IN ('malformed_json','record_too_large','invalid_fields',
				'invalid_tokens','cache_tokens_mismatch','iteration_tokens_mismatch',
				'invalid_identity','invalid_timestamp','token_overflow')
				OR reason.type <> 'integer' OR reason.value < 0
				OR reason.value > f.invalid_lines)
		OR EXISTS (SELECT 1 FROM json_each(f.parser_state_json) GROUP BY key HAVING COUNT(1)>1)
		OR (SELECT COALESCE(SUM(value),0) FROM json_each(f.parser_state_json)) > f.invalid_lines
	END`
