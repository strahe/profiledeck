package quota

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	defaultTimeout    = 30 * time.Second
	shutdownTimeout   = 2 * time.Second
	maxProtocolBytes  = 4 * 1024 * 1024
	internalErrorCode = -32603
	// ACP extension methods add an underscore to their internal name on the JSON-RPC wire.
	billingWireMethod = "_x.ai/billing"
)

type ACPClient struct {
	timeout     time.Duration
	now         func() time.Time
	start       processStarter
	environ     func() []string
	lookPath    func(string) (string, error)
	userHomeDir func() (string, error)
}

type commandSpec struct {
	Command string
	Args    []string
	Dir     string
	Env     []string
}

type processStarter func(context.Context, commandSpec) (*runningProcess, error)

type commandStartError struct {
	err error
}

func (e *commandStartError) Error() string {
	return "Grok Build could not be started"
}

func (e *commandStartError) Unwrap() error {
	return e.err
}

type runningProcess struct {
	stdin      io.WriteCloser
	stdout     io.ReadCloser
	wait       func() error
	kill       func() error
	stopOnce   sync.Once
	stdinOnce  sync.Once
	stdoutOnce sync.Once
}

func NewACPClient() *ACPClient {
	client := &ACPClient{
		timeout:     defaultTimeout,
		now:         time.Now,
		environ:     os.Environ,
		lookPath:    exec.LookPath,
		userHomeDir: os.UserHomeDir,
	}
	client.start = client.startCommand
	return client
}

func (c *ACPClient) Read(ctx context.Context, grokHome string) (Snapshot, error) {
	if c == nil || strings.TrimSpace(grokHome) == "" {
		return Snapshot{}, &Error{Kind: ErrorUnavailable}
	}
	timeout := c.timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	environ := os.Environ
	if c.environ != nil {
		environ = c.environ
	}
	currentEnvironment := environ()
	if hasAuthEnvironmentOverride(currentEnvironment) {
		return Snapshot{}, &Error{Kind: ErrorUnsupported}
	}
	if err := requestCtx.Err(); err != nil {
		return Snapshot{}, &Error{Kind: ErrorUnavailable, Err: err}
	}
	command, err := c.resolveExecutable(grokHome, currentEnvironment)
	if err != nil {
		return Snapshot{}, err
	}
	spec := commandSpec{
		Command: command,
		Args:    commandArgs(),
		Dir:     grokHome,
		Env:     grokEnvironment(currentEnvironment, grokHome),
	}
	starter := c.start
	if starter == nil {
		starter = c.startCommand
	}
	process, err := starter(requestCtx, spec)
	if err != nil {
		if requestErr := requestCtx.Err(); requestErr != nil {
			return Snapshot{}, &Error{Kind: ErrorUnavailable, Err: requestErr}
		}
		var startErr *commandStartError
		if errors.As(err, &startErr) {
			return Snapshot{}, &Error{Kind: ErrorRuntimeUnavailable, Err: startErr}
		}
		return Snapshot{}, &Error{Kind: ErrorUnavailable, Err: err}
	}
	defer process.stop()
	exchangeDone := make(chan struct{})
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		select {
		case <-requestCtx.Done():
			process.closeIO()
		case <-exchangeDone:
		}
	}()
	raw, err := exchange(requestCtx, process.stdin, process.stdout)
	close(exchangeDone)
	<-watchDone
	if err != nil {
		return Snapshot{}, err
	}
	now := time.Now
	if c.now != nil {
		now = c.now
	}
	return decodeBilling(raw, now().UTC())
}

func (c *ACPClient) startCommand(ctx context.Context, spec commandSpec) (*runningProcess, error) {
	cmd := exec.CommandContext(ctx, spec.Command, spec.Args...)
	cmd.Dir = spec.Dir
	cmd.Env = spec.Env
	// Grok diagnostics may contain upstream response details. ProfileDeck never
	// logs or returns this stream across an output boundary.
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
		return nil, &commandStartError{err: err}
	}
	return &runningProcess{
		stdin:  stdin,
		stdout: stdout,
		wait:   cmd.Wait,
		kill: func() error {
			if cmd.Process == nil {
				return nil
			}
			return cmd.Process.Kill()
		},
	}, nil
}

func (c *ACPClient) resolveExecutable(grokHome string, currentEnvironment []string) (string, error) {
	lookPath := exec.LookPath
	if c.lookPath != nil {
		lookPath = c.lookPath
	}
	candidates := []string{filepath.Join(grokHome, "bin", grokExecutableName())}
	if binDir := environmentValue(currentEnvironment, "GROK_BIN_DIR"); filepath.IsAbs(binDir) {
		candidates = append(candidates, filepath.Join(binDir, grokExecutableName()))
	}
	candidates = append(candidates, "grok")
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		userHomeDir := os.UserHomeDir
		if c.userHomeDir != nil {
			userHomeDir = c.userHomeDir
		}
		if userHome, err := userHomeDir(); err == nil && strings.TrimSpace(userHome) != "" {
			candidates = append(candidates, filepath.Join(userHome, ".local", "bin", "grok"))
		}
	}
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if _, duplicate := seen[candidate]; duplicate {
			continue
		}
		seen[candidate] = struct{}{}
		if resolved, err := lookPath(candidate); err == nil {
			return resolved, nil
		}
	}
	return "", &Error{Kind: ErrorRuntimeUnavailable}
}

func grokExecutableName() string {
	if runtime.GOOS == "windows" {
		return "grok.exe"
	}
	return "grok"
}

func commandArgs() []string {
	return []string{"--no-auto-update", "agent", "--no-leader", "stdio"}
}

func grokEnvironment(current []string, home string) []string {
	result := make([]string, 0, len(current)+1)
	for _, entry := range current {
		name := environmentName(entry)
		if environmentNameMatches(name, "GROK_HOME") ||
			environmentNameMatches(name, "GROK_AUTH") ||
			environmentNameMatches(name, "GROK_AUTH_PATH") {
			continue
		}
		result = append(result, entry)
	}
	return append(result, "GROK_HOME="+home)
}

func hasAuthEnvironmentOverride(current []string) bool {
	for _, entry := range current {
		name := environmentName(entry)
		if environmentNameMatches(name, "GROK_AUTH") || environmentNameMatches(name, "GROK_AUTH_PATH") {
			return true
		}
	}
	return false
}

func environmentName(entry string) string {
	name, _, _ := strings.Cut(entry, "=")
	return name
}

func environmentValue(current []string, expected string) string {
	result := ""
	for _, entry := range current {
		name, value, found := strings.Cut(entry, "=")
		if found && environmentNameMatches(name, expected) {
			result = strings.TrimSpace(value)
		}
	}
	return result
}

func environmentNameMatches(name, expected string) bool {
	return strings.EqualFold(name, expected)
}

func (p *runningProcess) stop() {
	if p == nil {
		return
	}
	p.stopOnce.Do(func() {
		p.closeIO()
		if p.wait == nil {
			return
		}
		// Start Wait only after the protocol reader has stopped; os/exec closes
		// StdoutPipe from Wait and must not race the active scanner.
		wait := make(chan error, 1)
		go func() {
			wait <- p.wait()
			close(wait)
		}()
		timer := time.NewTimer(shutdownTimeout)
		defer timer.Stop()
		select {
		case <-wait:
		case <-timer.C:
			if p.kill != nil {
				_ = p.kill()
			}
			select {
			case <-wait:
			case <-time.After(shutdownTimeout):
			}
		}
	})
}

func (p *runningProcess) closeIO() {
	if p == nil {
		return
	}
	p.stdinOnce.Do(func() {
		if p.stdin != nil {
			_ = p.stdin.Close()
		}
	})
	p.stdoutOnce.Do(func() {
		if p.stdout != nil {
			_ = p.stdout.Close()
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

func exchange(ctx context.Context, writer io.Writer, reader io.Reader) (json.RawMessage, error) {
	encoder := json.NewEncoder(writer)
	initialize := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": 1,
			"clientCapabilities": map[string]any{
				"fs":       map[string]bool{"readTextFile": false, "writeTextFile": false},
				"terminal": false,
			},
			"_meta": map[string]any{
				"startupHints": map[string]bool{
					"nonInteractive":    true,
					"skipGitStatus":     true,
					"skipProjectLayout": true,
				},
				"clientType":    "profiledeck",
				"clientVersion": "1",
			},
		},
	}
	if err := encoder.Encode(initialize); err != nil {
		return nil, &Error{Kind: ErrorUnavailable, Err: err}
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), maxProtocolBytes)
	if _, err := readResponse(ctx, scanner, "1", true); err != nil {
		return nil, err
	}
	if err := encoder.Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  billingWireMethod,
		"params":  map[string]any{},
	}); err != nil {
		return nil, &Error{Kind: ErrorUnavailable, Err: err}
	}
	return readResponse(ctx, scanner, "2", false)
}

func readResponse(ctx context.Context, scanner *bufio.Scanner, expectedID string, initialize bool) (json.RawMessage, error) {
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, &Error{Kind: ErrorUnavailable, Err: err}
		}
		var message protocolMessage
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			return nil, &Error{Kind: ErrorUnavailable, Err: err}
		}
		if len(message.ID) == 0 {
			continue
		}
		if message.Method != "" {
			return nil, &Error{Kind: ErrorUnavailable}
		}
		if normalizedID(message.ID) != expectedID {
			continue
		}
		if message.Error != nil {
			return nil, classifyProtocolError(message.Error, initialize)
		}
		if len(message.Result) == 0 || string(message.Result) == "null" {
			return nil, &Error{Kind: ErrorUnavailable}
		}
		return message.Result, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, &Error{Kind: ErrorUnavailable, Err: err}
	}
	if err := scanner.Err(); err != nil {
		return nil, &Error{Kind: ErrorUnavailable, Err: err}
	}
	return nil, &Error{Kind: ErrorUnavailable, Err: io.ErrUnexpectedEOF}
}

func classifyProtocolError(value *protocolError, initialize bool) error {
	if value == nil {
		return &Error{Kind: ErrorUnavailable}
	}
	message := strings.ToLower(strings.TrimSpace(value.Message))
	switch {
	case value.Code == -32601:
		return &Error{Kind: ErrorUnsupported}
	case initialize:
		return &Error{Kind: ErrorUnavailable}
	case value.Code == -32001,
		strings.Contains(message, "auth_required"),
		strings.Contains(message, "authentication required"),
		billingAuthFailure(value):
		return &Error{Kind: ErrorAuthRequired}
	default:
		return &Error{Kind: ErrorUnavailable}
	}
}

func billingAuthFailure(value *protocolError) bool {
	if value == nil || value.Code != internalErrorCode || len(value.Data) == 0 {
		return false
	}
	var detail string
	if err := json.Unmarshal(value.Data, &detail); err != nil {
		return false
	}
	detail = strings.TrimSpace(detail)
	switch detail {
	case "Billing service error: HTTP 401", "Billing service error: HTTP 403":
		return true
	default:
		return strings.EqualFold(detail, "Billing service error: unauthorized")
	}
}

func normalizedID(raw json.RawMessage) string {
	return strings.Trim(strings.TrimSpace(string(raw)), `"`)
}
