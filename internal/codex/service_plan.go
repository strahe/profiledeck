package codex

import codexprofile "github.com/strahe/profiledeck/internal/codex/profile"

const (
	codexCaptureKindCredential = "credential"
	codexCaptureKindConfigSet  = "config-set"
)

func codexAuthPayloadsEqual(left, right string) bool {
	return codexprofile.AuthPayloadsEqual(left, right)
}
