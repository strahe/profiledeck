// Package doctor provides the reusable, read-only health-check execution engine.
package doctor

import "context"

const (
	LevelOK      = "OK"
	LevelWarning = "WARNING"
	LevelError   = "ERROR"
)

// Finding is a structured diagnostic result shared by all entrypoints.
type Finding struct {
	ID      string         `json:"id"`
	Level   string         `json:"level"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// Checker performs a read-only domain health check.
type Checker interface {
	Check(context.Context) ([]Finding, error)
}

// CheckerFunc adapts a function into a Checker.
type CheckerFunc func(context.Context) ([]Finding, error)

func (checker CheckerFunc) Check(ctx context.Context) ([]Finding, error) {
	return checker(ctx)
}

// Engine composes provider checks without knowing providers, SQLite, or DTOs.
type Engine struct {
	checkers []Checker
}

func New(checkers ...Checker) Engine {
	valid := make([]Checker, 0, len(checkers))
	for _, checker := range checkers {
		if checker != nil {
			valid = append(valid, checker)
		}
	}
	return Engine{checkers: valid}
}

func (engine Engine) Run(ctx context.Context) ([]Finding, error) {
	findings := []Finding{}
	for _, checker := range engine.checkers {
		result, err := checker.Check(ctx)
		if err != nil {
			return nil, err
		}
		findings = append(findings, result...)
	}
	return findings, nil
}

// OverallLevel returns the highest severity supplied by independent checks.
func OverallLevel(levels ...string) string {
	level := LevelOK
	for _, candidate := range levels {
		if rank(candidate) > rank(level) {
			level = candidate
		}
	}
	return level
}

func rank(level string) int {
	switch level {
	case LevelError:
		return 2
	case LevelWarning:
		return 1
	default:
		return 0
	}
}
