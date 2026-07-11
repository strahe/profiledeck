package appserver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	codexquota "github.com/strahe/profiledeck/internal/codex/quota"
)

const (
	defaultTimeout        = 30 * time.Second
	shutdownTimeout       = 2 * time.Second
	maxProtocolBytes      = 4 * 1024 * 1024
	externalRefreshMethod = "account/chatgptAuthTokens/refresh"
)

type ErrorKind string

const (
	ErrorUnavailable   ErrorKind = "unavailable"
	ErrorIncompatible  ErrorKind = "incompatible"
	ErrorAuthRequired  ErrorKind = "auth_required"
	ErrorAuthPermanent ErrorKind = "auth_permanent"
	ErrorTransient     ErrorKind = "transient"
)

type Error struct {
	Kind ErrorKind
	Err  error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	switch e.Kind {
	case ErrorUnavailable:
		return "Codex app-server is unavailable"
	case ErrorIncompatible:
		return "Codex app-server protocol is incompatible"
	case ErrorAuthRequired:
		return "Codex authentication is required"
	case ErrorAuthPermanent:
		return "Codex refresh token is no longer usable"
	default:
		return "Codex app-server request failed"
	}
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func KindOf(err error) ErrorKind {
	var appServerErr *Error
	if errors.As(err, &appServerErr) {
		return appServerErr.Kind
	}
	return ErrorTransient
}

type Runner struct {
	Command string
	Timeout time.Duration
	now     func() time.Time
	start   processStarter
}

type processStarter func(context.Context, string, string) (*runningProcess, error)

type runningProcess struct {
	stdin  io.WriteCloser
	stdout io.ReadCloser
	wait   <-chan error
	kill   func() error
	once   sync.Once
}

func NewRunner() *Runner {
	runner := &Runner{Command: "codex", Timeout: defaultTimeout, now: time.Now}
	runner.start = runner.startCommand
	return runner
}

func (r *Runner) Available() bool {
	if r == nil {
		return false
	}
	command := strings.TrimSpace(r.Command)
	if command == "" {
		command = "codex"
	}
	_, err := exec.LookPath(command)
	return err == nil
}

func (r *Runner) ReadRateLimits(ctx context.Context, codexHome string) (codexquota.Snapshot, error) {
	raw, err := r.call(ctx, codexHome, "account/rateLimits/read", nil)
	if err != nil {
		return codexquota.Snapshot{}, err
	}
	return decodeRateLimits(raw, r.clock().UTC())
}

func (r *Runner) RefreshAccount(ctx context.Context, codexHome string) error {
	_, err := r.call(ctx, codexHome, "account/read", map[string]any{"refreshToken": true})
	return err
}

func (r *Runner) clock() time.Time {
	if r != nil && r.now != nil {
		return r.now()
	}
	return time.Now()
}

func (r *Runner) call(ctx context.Context, codexHome, method string, params any) (json.RawMessage, error) {
	if r == nil {
		return nil, &Error{Kind: ErrorUnavailable}
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	starter := r.start
	if starter == nil {
		starter = r.startCommand
	}
	process, err := starter(requestCtx, strings.TrimSpace(r.Command), codexHome)
	if err != nil {
		if requestCtx.Err() != nil {
			return nil, &Error{Kind: ErrorTransient, Err: requestCtx.Err()}
		}
		return nil, &Error{Kind: ErrorUnavailable, Err: err}
	}
	defer process.stop()
	result, err := exchange(requestCtx, process.stdin, process.stdout, method, params)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (r *Runner) startCommand(ctx context.Context, command, codexHome string) (*runningProcess, error) {
	if command == "" {
		command = "codex"
	}
	cmd := exec.CommandContext(ctx, command, commandArgs()...)
	cmd.Env = codexEnvironment(os.Environ(), codexHome)
	// App-server diagnostics can contain upstream response details. ProfileDeck
	// never logs or returns this stream across an output boundary.
	cmd.Stderr = io.Discard
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, err
	}
	wait := make(chan error, 1)
	go func() {
		wait <- cmd.Wait()
		close(wait)
	}()
	return &runningProcess{
		stdin: stdin, stdout: stdout, wait: wait,
		kill: func() error {
			if cmd.Process == nil {
				return nil
			}
			return cmd.Process.Kill()
		},
	}, nil
}

func commandArgs() []string {
	return []string{
		"app-server", "--stdio",
		"--disable", "remote_plugin",
		"--disable", "apps",
		"-c", `cli_auth_credentials_store="file"`,
		"-c", "analytics.enabled=false",
		"-c", "memories.use_memories=false",
		"-c", "memories.generate_memories=false",
		"-c", "include_apps_instructions=false",
	}
}

func codexEnvironment(current []string, home string) []string {
	result := make([]string, 0, len(current)+1)
	for _, entry := range current {
		if strings.HasPrefix(entry, "CODEX_HOME=") {
			continue
		}
		result = append(result, entry)
	}
	return append(result, "CODEX_HOME="+home)
}

func (p *runningProcess) stop() {
	if p == nil {
		return
	}
	p.once.Do(func() {
		if p.stdin != nil {
			_ = p.stdin.Close()
		}
		if p.stdout != nil {
			_ = p.stdout.Close()
		}
		if p.wait == nil {
			return
		}
		timer := time.NewTimer(shutdownTimeout)
		defer timer.Stop()
		select {
		case <-p.wait:
			return
		case <-timer.C:
			if p.kill != nil {
				_ = p.kill()
			}
		}
		select {
		case <-p.wait:
		case <-time.After(shutdownTimeout):
		}
	})
}

type protocolMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Result json.RawMessage `json:"result"`
	Error  *protocolError  `json:"error"`
}

type protocolError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func exchange(ctx context.Context, writer io.Writer, reader io.Reader, method string, params any) (json.RawMessage, error) {
	encoder := json.NewEncoder(writer)
	initialize := map[string]any{
		"method": "initialize",
		"id":     1,
		"params": map[string]any{
			"clientInfo": map[string]string{"name": "profiledeck", "title": "ProfileDeck", "version": "1"},
			"capabilities": map[string]any{
				"optOutNotificationMethods": []string{"account/updated", "account/rateLimits/updated", "app/list/updated", "remoteControl/status/changed"},
			},
		},
	}
	if err := encoder.Encode(initialize); err != nil {
		return nil, &Error{Kind: ErrorTransient, Err: err}
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), maxProtocolBytes)
	if _, err := readResponse(ctx, scanner, "1", true); err != nil {
		return nil, err
	}
	if err := encoder.Encode(map[string]any{"method": "initialized"}); err != nil {
		return nil, &Error{Kind: ErrorTransient, Err: err}
	}
	request := map[string]any{"method": method, "id": 2}
	if params != nil {
		request["params"] = params
	}
	if err := encoder.Encode(request); err != nil {
		return nil, &Error{Kind: ErrorTransient, Err: err}
	}
	return readResponse(ctx, scanner, "2", false)
}

func readResponse(ctx context.Context, scanner *bufio.Scanner, expectedID string, initialize bool) (json.RawMessage, error) {
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, &Error{Kind: ErrorTransient, Err: err}
		}
		var message protocolMessage
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			return nil, &Error{Kind: ErrorIncompatible, Err: err}
		}
		if len(message.ID) > 0 && message.Method != "" {
			// External token mode delegates refresh back to its controlling client.
			// ProfileDeck has no token provider for that mode, so fail immediately
			// instead of leaving the app-server request pending until timeout.
			if message.Method == externalRefreshMethod {
				return nil, &Error{Kind: ErrorAuthRequired}
			}
			return nil, &Error{Kind: ErrorIncompatible}
		}
		if strings.TrimSpace(string(message.ID)) != expectedID {
			// Notifications and unrelated server messages may be interleaved with
			// the response. The quota runner has no methods that require replies.
			continue
		}
		if message.Error != nil {
			return nil, classifyProtocolError(message.Error, initialize)
		}
		if len(message.Result) == 0 || string(message.Result) == "null" {
			return nil, &Error{Kind: ErrorIncompatible}
		}
		return message.Result, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, &Error{Kind: ErrorTransient, Err: err}
	}
	if err := scanner.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return nil, &Error{Kind: ErrorIncompatible, Err: err}
		}
		return nil, &Error{Kind: ErrorTransient, Err: err}
	}
	return nil, &Error{Kind: ErrorUnavailable, Err: io.ErrUnexpectedEOF}
}

func classifyProtocolError(value *protocolError, initialize bool) error {
	if value == nil {
		return &Error{Kind: ErrorTransient}
	}
	message := strings.ToLower(value.Message)
	switch {
	case initialize, value.Code == -32601, value.Code == -32602, strings.Contains(message, "not initialized"):
		return &Error{Kind: ErrorIncompatible}
	case strings.Contains(message, "refresh token has expired"), strings.Contains(message, "refresh token was already used"), strings.Contains(message, "refresh token was revoked"), strings.Contains(message, "refresh_token_expired"), strings.Contains(message, "refresh_token_reused"), strings.Contains(message, "refresh_token_invalidated"):
		return &Error{Kind: ErrorAuthPermanent}
	case strings.Contains(message, "authentication"), strings.Contains(message, "not logged in"), strings.Contains(message, "unauthorized"), strings.Contains(message, "sign in again"):
		return &Error{Kind: ErrorAuthRequired}
	default:
		return &Error{Kind: ErrorTransient}
	}
}

type rateLimitsResponse struct {
	RateLimits            rateLimitSnapshot            `json:"rateLimits"`
	RateLimitsByLimitID   map[string]rateLimitSnapshot `json:"rateLimitsByLimitId"`
	RateLimitResetCredits *resetCreditsSummary         `json:"rateLimitResetCredits"`
}

type rateLimitSnapshot struct {
	LimitID              *string                  `json:"limitId"`
	LimitName            *string                  `json:"limitName"`
	Primary              *rateLimitWindow         `json:"primary"`
	Secondary            *rateLimitWindow         `json:"secondary"`
	Credits              *creditsSnapshot         `json:"credits"`
	IndividualLimit      *individualLimitSnapshot `json:"individualLimit"`
	PlanType             *string                  `json:"planType"`
	RateLimitReachedType *string                  `json:"rateLimitReachedType"`
}

type rateLimitWindow struct {
	UsedPercent        float64 `json:"usedPercent"`
	WindowDurationMins *int64  `json:"windowDurationMins"`
	ResetsAt           *int64  `json:"resetsAt"`
}

type creditsSnapshot struct {
	HasCredits bool    `json:"hasCredits"`
	Unlimited  bool    `json:"unlimited"`
	Balance    *string `json:"balance"`
}

type individualLimitSnapshot struct {
	Limit            string  `json:"limit"`
	Used             string  `json:"used"`
	RemainingPercent float64 `json:"remainingPercent"`
	ResetsAt         int64   `json:"resetsAt"`
}

type resetCreditsSummary struct {
	AvailableCount int64 `json:"availableCount"`
}

func decodeRateLimits(raw json.RawMessage, fetchedAt time.Time) (codexquota.Snapshot, error) {
	var response rateLimitsResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return codexquota.Snapshot{}, &Error{Kind: ErrorIncompatible, Err: err}
	}
	result := codexquota.Snapshot{FetchedAt: fetchedAt, AdditionalRateLimits: []codexquota.RateLimit{}}
	defaultLimit := response.RateLimits
	if defaultLimit.PlanType != nil {
		result.PlanType = *defaultLimit.PlanType
	}
	if hasRateLimit(defaultLimit) {
		mapped, err := mapRateLimit(defaultLimit, "codex", fetchedAt)
		if err != nil {
			return codexquota.Snapshot{}, err
		}
		result.RateLimit = &mapped
		result.Credits = mapCredits(defaultLimit.Credits)
		result.SpendControl = mapSpendControl(defaultLimit.IndividualLimit, fetchedAt)
		if defaultLimit.RateLimitReachedType != nil {
			result.RateLimitReachedType = *defaultLimit.RateLimitReachedType
		}
	}
	keys := make([]string, 0, len(response.RateLimitsByLimitID))
	for key := range response.RateLimitsByLimitID {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	defaultID := "codex"
	if defaultLimit.LimitID != nil && strings.TrimSpace(*defaultLimit.LimitID) != "" {
		defaultID = strings.TrimSpace(*defaultLimit.LimitID)
	}
	for _, key := range keys {
		value := response.RateLimitsByLimitID[key]
		id := key
		if value.LimitID != nil && strings.TrimSpace(*value.LimitID) != "" {
			id = strings.TrimSpace(*value.LimitID)
		}
		if id == defaultID {
			continue
		}
		mapped, err := mapRateLimit(value, id, fetchedAt)
		if err != nil {
			return codexquota.Snapshot{}, err
		}
		result.AdditionalRateLimits = append(result.AdditionalRateLimits, mapped)
		if result.PlanType == "" && value.PlanType != nil {
			result.PlanType = *value.PlanType
		}
	}
	if response.RateLimitResetCredits != nil {
		count := response.RateLimitResetCredits.AvailableCount
		result.ResetCreditsAvailable = &count
	}
	return result, nil
}

func hasRateLimit(value rateLimitSnapshot) bool {
	return value.Primary != nil || value.Secondary != nil || value.LimitID != nil || value.PlanType != nil || value.Credits != nil || value.IndividualLimit != nil || value.RateLimitReachedType != nil
}

func mapRateLimit(value rateLimitSnapshot, fallbackID string, fetchedAt time.Time) (codexquota.RateLimit, error) {
	id := fallbackID
	if value.LimitID != nil && strings.TrimSpace(*value.LimitID) != "" {
		id = strings.TrimSpace(*value.LimitID)
	}
	name := ""
	if value.LimitName != nil {
		name = *value.LimitName
	}
	limitReached := value.RateLimitReachedType != nil && strings.TrimSpace(*value.RateLimitReachedType) != ""
	primary, err := mapWindow(value.Primary, fetchedAt)
	if err != nil {
		return codexquota.RateLimit{}, fmt.Errorf("primary rate-limit window: %w", err)
	}
	secondary, err := mapWindow(value.Secondary, fetchedAt)
	if err != nil {
		return codexquota.RateLimit{}, fmt.Errorf("secondary rate-limit window: %w", err)
	}
	return codexquota.RateLimit{
		ID: id, Name: name, Allowed: !limitReached, LimitReached: limitReached,
		PrimaryWindow: primary, SecondaryWindow: secondary,
	}, nil
}

func mapWindow(value *rateLimitWindow, fetchedAt time.Time) (*codexquota.Window, error) {
	if value == nil {
		return nil, nil
	}
	if value.WindowDurationMins == nil || value.ResetsAt == nil || *value.WindowDurationMins <= 0 || *value.ResetsAt <= 0 {
		return nil, &Error{Kind: ErrorIncompatible}
	}
	used := clampPercent(value.UsedPercent)
	resetAfter := *value.ResetsAt - fetchedAt.Unix()
	if resetAfter < 0 {
		resetAfter = 0
	}
	return &codexquota.Window{
		UsedPercent: used, RemainingPercent: clampPercent(100 - used),
		LimitWindowSeconds: *value.WindowDurationMins * 60,
		ResetAfterSeconds:  resetAfter, ResetAtUnixSeconds: *value.ResetsAt,
	}, nil
}

func mapCredits(value *creditsSnapshot) *codexquota.Credits {
	if value == nil {
		return nil
	}
	return &codexquota.Credits{HasCredits: value.HasCredits, Unlimited: value.Unlimited, Balance: value.Balance}
}

func mapSpendControl(value *individualLimitSnapshot, fetchedAt time.Time) *codexquota.SpendControl {
	if value == nil {
		return nil
	}
	remaining := clampPercent(value.RemainingPercent)
	resetAfter := value.ResetsAt - fetchedAt.Unix()
	if resetAfter < 0 {
		resetAfter = 0
	}
	return &codexquota.SpendControl{
		Reached: remaining <= 0,
		IndividualLimit: &codexquota.SpendControlLimit{
			Limit: value.Limit, Used: value.Used,
			UsedPercent: clampPercent(100 - remaining), RemainingPercent: remaining,
			ResetAfterSeconds: resetAfter, ResetAtUnixSeconds: value.ResetsAt,
		},
	}
}

func clampPercent(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}
