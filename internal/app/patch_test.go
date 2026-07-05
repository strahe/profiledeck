package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"github.com/strahe/profiledeck/internal/store"
)

func TestJSONMergePatchContentAndPlanActions(t *testing.T) {
	target := testPatchTarget(t, targetFormatJSON, targetStrategyJSONMerge, `{
		"array":[2],
		"nested":{"b":2},
		"nil":null,
		"scalar":"new"
	}`)
	before := targetPlanRead{
		FileExists: true,
		Content:    `{"array":[1],"keep":true,"nested":{"a":1},"scalar":"old"}`,
	}

	content, warnings, err := desiredTargetContent(target, before)
	if err != nil {
		t.Fatalf("expected JSON merge content to succeed, got %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no JSON merge warnings, got %#v", warnings)
	}
	want := `{
  "array": [
    2
  ],
  "keep": true,
  "nested": {
    "a": 1,
    "b": 2
  },
  "nil": null,
  "scalar": "new"
}
`
	if content != want {
		t.Fatalf("unexpected JSON merge content:\nwant:\n%q\ngot:\n%q", want, content)
	}

	createOp := buildTestPatchOperation(t, targetFormatJSON, targetStrategyJSONMerge, `{"model":"x"}`, nil)
	if createOp.Action != planActionCreate || createOp.StatusReason != planReasonTargetMissing {
		t.Fatalf("expected JSON merge create, got %#v", createOp)
	}
	noopContent := `{
  "model": "x"
}
`
	noopOp := buildTestPatchOperation(t, targetFormatJSON, targetStrategyJSONMerge, `{"model":"x"}`, &noopContent)
	if noopOp.Action != planActionNoop || noopOp.StatusReason != planReasonTargetSameContent {
		t.Fatalf("expected JSON merge noop, got %#v", noopOp)
	}
	formatOnlyBefore := `{"model":"x"}
`
	updateOp := buildTestPatchOperation(t, targetFormatJSON, targetStrategyJSONMerge, `{"model":"x"}`, &formatOnlyBefore)
	if updateOp.Action != planActionUpdate || updateOp.StatusReason != planReasonTargetDifferentContent {
		t.Fatalf("expected JSON format-only update, got %#v", updateOp)
	}
}

func TestJSONMergeRejectsInvalidTargetContent(t *testing.T) {
	for _, raw := range []string{
		`{"model":"x" // comment}`,
		`{"model":"x"} {"extra":true}`,
		`["not-object"]`,
	} {
		target := testPatchTarget(t, targetFormatJSON, targetStrategyJSONMerge, `{"model":"x"}`)
		_, _, err := desiredTargetContent(target, targetPlanRead{FileExists: true, Content: raw})
		assertAppErrorCode(t, err, ErrorTargetInvalid)
	}
}

func TestJSONMergeTreatsEmptyTargetAsEmptyObject(t *testing.T) {
	target := testPatchTarget(t, targetFormatJSON, targetStrategyJSONMerge, `{"model":"x"}`)
	content, _, err := desiredTargetContent(target, targetPlanRead{FileExists: true, Content: ""})
	if err != nil {
		t.Fatalf("expected empty JSON target to be treated as empty object, got %v", err)
	}
	want := `{
  "model": "x"
}
`
	if content != want {
		t.Fatalf("unexpected empty JSON merge content: want %q got %q", want, content)
	}
}

func TestMergeRejectsReservedEnvRefObjects(t *testing.T) {
	for _, tc := range []struct {
		name     string
		format   string
		strategy string
		value    string
	}{
		{name: "json", format: targetFormatJSON, strategy: targetStrategyJSONMerge, value: `{"api_key":{"ref_type":"env","name":"OPENAI_API_KEY"}}`},
		{name: "toml", format: targetFormatTOML, strategy: targetStrategyTOMLMerge, value: `{"nested":{"token":{"ref_type":"env","name":"TOKEN"}}}`},
		{name: "env", format: targetFormatEnv, strategy: targetStrategyEnvMerge, value: `{"TOKEN":{"ref_type":"env","name":"TOKEN"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := testPatchTarget(t, tc.format, tc.strategy, tc.value)
			_, _, err := desiredTargetContent(target, targetPlanRead{})
			assertAppErrorCode(t, err, ErrorTargetInvalid)
		})
	}
}

func TestTOMLMergePatchContentAndPlanActions(t *testing.T) {
	target := testPatchTarget(t, targetFormatTOML, targetStrategyTOMLMerge, `{
		"array":["x"],
		"scalar":"new",
		"table":{"b":2}
	}`)
	before := targetPlanRead{
		FileExists: true,
		Content:    "scalar = \"old\"\n\n[table]\na = 1\n",
	}

	first, warnings, err := desiredTargetContent(target, before)
	if err != nil {
		t.Fatalf("expected TOML merge content to succeed, got %v", err)
	}
	second, _, err := desiredTargetContent(target, before)
	if err != nil {
		t.Fatalf("expected repeated TOML merge content to succeed, got %v", err)
	}
	if first != second {
		t.Fatalf("expected TOML merge output to be stable:\nfirst:\n%q\nsecond:\n%q", first, second)
	}
	if !strings.HasSuffix(first, "\n") {
		t.Fatalf("expected TOML merge output to end with newline, got %q", first)
	}
	if len(warnings) != 1 || warnings[0] != tomlSemanticRewriteWarning {
		t.Fatalf("expected TOML semantic rewrite warning, got %#v", warnings)
	}
	var decoded map[string]any
	if err := toml.Unmarshal([]byte(first), &decoded); err != nil {
		t.Fatalf("expected merged TOML to parse, got %v: %q", err, first)
	}
	if decoded["scalar"] != "new" {
		t.Fatalf("expected scalar overwrite, got %#v", decoded)
	}
	table, ok := decoded["table"].(map[string]any)
	if !ok || table["a"] == nil || table["b"] == nil {
		t.Fatalf("expected nested table merge, got %#v", decoded)
	}

	createOp := buildTestPatchOperation(t, targetFormatTOML, targetStrategyTOMLMerge, `{"model":"x"}`, nil)
	if createOp.Action != planActionCreate || createOp.StatusReason != planReasonTargetMissing {
		t.Fatalf("expected TOML merge create, got %#v", createOp)
	}
	desired := createOp.AfterPreview.Content
	noopOp := buildTestPatchOperation(t, targetFormatTOML, targetStrategyTOMLMerge, `{"model":"x"}`, &desired)
	if noopOp.Action != planActionNoop || noopOp.StatusReason != planReasonTargetSameContent {
		t.Fatalf("expected TOML merge noop, got %#v", noopOp)
	}
	beforeUpdate := "model = \"old\"\n"
	updateOp := buildTestPatchOperation(t, targetFormatTOML, targetStrategyTOMLMerge, `{"model":"x"}`, &beforeUpdate)
	if updateOp.Action != planActionUpdate || updateOp.StatusReason != planReasonTargetDifferentContent {
		t.Fatalf("expected TOML merge update, got %#v", updateOp)
	}
	if len(updateOp.Warnings) != 1 || updateOp.Warnings[0] != tomlSemanticRewriteWarning {
		t.Fatalf("expected TOML update warning, got %#v", updateOp.Warnings)
	}
}

func TestTOMLMergeRejectsInvalidInput(t *testing.T) {
	target := testPatchTarget(t, targetFormatTOML, targetStrategyTOMLMerge, `{"model":"x"}`)
	_, _, err := desiredTargetContent(target, targetPlanRead{FileExists: true, Content: "not valid toml ="})
	assertAppErrorCode(t, err, ErrorTargetInvalid)

	nullTarget := testPatchTarget(t, targetFormatTOML, targetStrategyTOMLMerge, `{"model":null}`)
	_, _, err = desiredTargetContent(nullTarget, targetPlanRead{})
	assertAppErrorCode(t, err, ErrorTargetInvalid)

	hugeNumberTarget := testPatchTarget(t, targetFormatTOML, targetStrategyTOMLMerge, `{"model":1e10000}`)
	_, _, err = desiredTargetContent(hugeNumberTarget, targetPlanRead{})
	assertAppErrorCode(t, err, ErrorTargetInvalid)
}

func TestEnvMergePatchContentAndPlanActions(t *testing.T) {
	target := testPatchTarget(t, targetFormatEnv, targetStrategyEnvMerge, `{"A":"new","C":"3"}`)
	before := targetPlanRead{
		FileExists: true,
		Content:    "# top\nexport A = old\nB=2\n",
	}
	content, warnings, err := desiredTargetContent(target, before)
	if err != nil {
		t.Fatalf("expected env merge content to succeed, got %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no env merge warnings, got %#v", warnings)
	}
	want := "# top\nexport A = new\nB=2\nC=3\n"
	if content != want {
		t.Fatalf("unexpected env merge content: want %q got %q", want, content)
	}

	quotedTarget := testPatchTarget(t, targetFormatEnv, targetStrategyEnvMerge, `{"TOKEN":"new#raw","URL":"\"https://example.com/#new\""}`)
	quotedBefore := targetPlanRead{
		FileExists: true,
		Content:    "URL=\"https://example.com/#old\" # keep\nTOKEN=old # keep token\n",
	}
	quotedContent, _, err := desiredTargetContent(quotedTarget, quotedBefore)
	if err != nil {
		t.Fatalf("expected env merge with quoted hash and trailing comments to succeed, got %v", err)
	}
	quotedWant := "URL=\"https://example.com/#new\" # keep\nTOKEN=new#raw # keep token\n"
	if quotedContent != quotedWant {
		t.Fatalf("unexpected quoted env merge content: want %q got %q", quotedWant, quotedContent)
	}

	apostropheTarget := testPatchTarget(t, targetFormatEnv, targetStrategyEnvMerge, `{"NAME":"updated"}`)
	apostropheContent, _, err := desiredTargetContent(apostropheTarget, targetPlanRead{FileExists: true, Content: "NAME=don't-break\n"})
	if err != nil {
		t.Fatalf("expected unquoted apostrophe env value to be supported, got %v", err)
	}
	if apostropheContent != "NAME=updated\n" {
		t.Fatalf("unexpected apostrophe env merge content: got %q", apostropheContent)
	}

	missingTarget := testPatchTarget(t, targetFormatEnv, targetStrategyEnvMerge, `{"B":"2","A":"1"}`)
	missingContent, _, err := desiredTargetContent(missingTarget, targetPlanRead{})
	if err != nil {
		t.Fatalf("expected missing env merge content to succeed, got %v", err)
	}
	if missingContent != "A=1\nB=2\n" {
		t.Fatalf("expected sorted env creation content, got %q", missingContent)
	}

	noFinalNewlineTarget := testPatchTarget(t, targetFormatEnv, targetStrategyEnvMerge, `{"B":"2"}`)
	noFinalNewlineContent, _, err := desiredTargetContent(noFinalNewlineTarget, targetPlanRead{FileExists: true, Content: "A=1"})
	if err != nil {
		t.Fatalf("expected env append without final newline to succeed, got %v", err)
	}
	if noFinalNewlineContent != "A=1\nB=2\n" {
		t.Fatalf("expected env append to add separator newline, got %q", noFinalNewlineContent)
	}

	createOp := buildTestPatchOperation(t, targetFormatEnv, targetStrategyEnvMerge, `{"A":"1"}`, nil)
	if createOp.Action != planActionCreate || createOp.StatusReason != planReasonTargetMissing {
		t.Fatalf("expected env merge create, got %#v", createOp)
	}
	noopBefore := "A=1\n"
	noopOp := buildTestPatchOperation(t, targetFormatEnv, targetStrategyEnvMerge, `{"A":"1"}`, &noopBefore)
	if noopOp.Action != planActionNoop || noopOp.StatusReason != planReasonTargetSameContent {
		t.Fatalf("expected env merge noop, got %#v", noopOp)
	}
	updateBefore := "A=old\n"
	updateOp := buildTestPatchOperation(t, targetFormatEnv, targetStrategyEnvMerge, `{"A":"1"}`, &updateBefore)
	if updateOp.Action != planActionUpdate || updateOp.StatusReason != planReasonTargetDifferentContent {
		t.Fatalf("expected env merge update, got %#v", updateOp)
	}
}

func TestEnvMergeRejectsInvalidInput(t *testing.T) {
	for _, tc := range []struct {
		name      string
		valueJSON string
		before    string
	}{
		{name: "duplicate", valueJSON: `{"A":"1"}`, before: "A=old\nA=older\n"},
		{name: "invalid-line", valueJSON: `{"A":"1"}`, before: "not an assignment\n"},
		{name: "invalid-key", valueJSON: `{"1BAD":"1"}`, before: ""},
		{name: "invalid-value-newline", valueJSON: "{\"A\":\"bad\\nvalue\"}", before: ""},
		{name: "non-string-value", valueJSON: `{"A":1}`, before: ""},
		{name: "malformed-quote", valueJSON: `{"A":"1"}`, before: "A=\"unterminated # not-comment\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := testPatchTarget(t, targetFormatEnv, targetStrategyEnvMerge, tc.valueJSON)
			_, _, err := desiredTargetContent(target, targetPlanRead{FileExists: tc.before != "", Content: tc.before})
			assertAppErrorCode(t, err, ErrorTargetInvalid)
		})
	}
}

func buildTestPatchOperation(t *testing.T, format string, strategy string, valueJSON string, before *string) PlanOperation {
	t.Helper()

	path := filepath.Join(t.TempDir(), "target")
	if before != nil {
		if err := os.WriteFile(path, []byte(*before), 0o600); err != nil {
			t.Fatalf("expected target setup to succeed, got %v", err)
		}
	}
	op, err := buildGenericPlanOperation(
		context.Background(),
		store.Provider{ID: "provider-a", AdapterID: "generic"},
		store.Profile{ID: "profile-a"},
		store.ProfileTarget{
			ProfileID:  "profile-a",
			ProviderID: "provider-a",
			TargetID:   "target-a",
			Path:       path,
			Format:     format,
			Strategy:   strategy,
			ValueJSON:  valueJSON,
			Enabled:    true,
		},
	)
	if err != nil {
		t.Fatalf("expected build operation to succeed, got %v", err)
	}
	return op
}

func testPatchTarget(t *testing.T, format string, strategy string, valueJSON string) store.ProfileTarget {
	t.Helper()

	return store.ProfileTarget{
		ProfileID:  "profile-a",
		ProviderID: "provider-a",
		TargetID:   "target-a",
		Path:       filepath.Join(t.TempDir(), "target"),
		Format:     format,
		Strategy:   strategy,
		ValueJSON:  valueJSON,
		Enabled:    true,
	}
}
