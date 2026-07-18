package architecture

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/strahe/profiledeck/internal/agent"
)

const moduleInternal = "github.com/strahe/profiledeck/internal/"

// Shared core and CLI code must remain buildable without Wails or the Desktop layer.
func TestSharedCoreDoesNotImportWails(t *testing.T) {
	root := repositoryRoot(t)
	for _, directory := range []string{"cmd", "internal"} {
		err := walkGo(filepath.Join(root, directory), func(path string, file *ast.File) error {
			for _, imported := range importPaths(file) {
				if imported == "github.com/wailsapp/wails" || strings.HasPrefix(imported, "github.com/wailsapp/wails/") {
					return boundaryFailure(path, "shared core imports Wails "+imported)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestLowerPackagesDoNotImportApp(t *testing.T) {
	internalRoot := filepath.Join(repositoryRoot(t), "internal")
	err := walkProductionGo(internalRoot, func(path string, file *ast.File) error {
		relative, err := filepath.Rel(internalRoot, path)
		if err != nil {
			return err
		}
		topPackage := strings.Split(filepath.ToSlash(relative), "/")[0]
		if topPackage == "app" || topPackage == "cli" {
			return nil
		}
		if importsPath(file, moduleInternal+"app") {
			return boundaryFailure(path, "lower package imports internal/app")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAppContainsOnlyCompositionAndBuildInfoAPI(t *testing.T) {
	root := filepath.Join(repositoryRoot(t), "internal", "app")
	allowedFunctions := map[string]struct{}{
		"New": {}, "NewWithDependencies": {}, "NewDependencies": {}, "defaultDependencies": {},
		"DefaultInfo": {}, "NewInfo": {},
	}
	allowedApplicationMethods := map[string]struct{}{
		"Runtime": {}, "Backups": {}, "Agents": {}, "Providers": {}, "Profiles": {}, "Targets": {}, "Switching": {},
		"Doctor": {}, "Usage": {}, "Settings": {}, "Codex": {}, "Antigravity": {}, "ClaudeCode": {},
		"Initialize": {}, "Close": {},
	}
	err := walkProductionGo(root, func(path string, file *ast.File) error {
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok {
				continue
			}
			if function.Recv == nil {
				if _, allowed := allowedFunctions[function.Name.Name]; !allowed {
					return boundaryFailure(path, "app declares non-composition function "+function.Name.Name)
				}
				continue
			}
			if receiverType(function) != "Application" {
				return boundaryFailure(path, "app declares methods outside Application")
			}
			if _, allowed := allowedApplicationMethods[function.Name.Name]; !allowed {
				return boundaryFailure(path, "Application declares use-case method "+function.Name.Name)
			}
			parameterCount := 0
			if function.Type.Params != nil {
				parameterCount = len(function.Type.Params.List)
			}
			if function.Name.Name == "Initialize" {
				if parameterCount != 1 {
					return boundaryFailure(path, "Application Initialize must accept one context parameter")
				}
				continue
			}
			if parameterCount != 0 {
				return boundaryFailure(path, "Application accessor accepts arguments: "+function.Name.Name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestProviderAdaptersUseOnlyPlanTargetAndTheirOwnDomain(t *testing.T) {
	root := repositoryRoot(t)
	for _, domain := range []string{"codex", "antigravity", "claudecode"} {
		directory := filepath.Join(root, "internal", domain, "adapter")
		err := walkProductionGo(directory, func(path string, file *ast.File) error {
			for _, imported := range importPaths(file) {
				if !strings.HasPrefix(imported, moduleInternal) {
					continue
				}
				allowed := imported == moduleInternal+"apperror" ||
					imported == moduleInternal+"switching/plan" ||
					imported == moduleInternal+"switching/target" ||
					strings.HasPrefix(imported, moduleInternal+domain+"/")
				if !allowed {
					return boundaryFailure(path, fmt.Sprintf("%s adapter imports %s", domain, imported))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestSwitchingOrchestrationDoesNotImportProviderDomains(t *testing.T) {
	directory := filepath.Join(repositoryRoot(t), "internal", "switching")
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	bannedPrefixes := []string{
		moduleInternal + "codex",
		moduleInternal + "antigravity",
		moduleInternal + "claudecode",
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, imported := range importPaths(file) {
			for _, banned := range bannedPrefixes {
				if imported == banned || strings.HasPrefix(imported, banned+"/") {
					t.Fatalf("%s: switching orchestration imports Provider domain %s", path, imported)
				}
			}
		}
	}
}

func TestProfileTargetServiceUsesAgentOwnershipInsteadOfProviderHardcoding(t *testing.T) {
	root := filepath.Join(repositoryRoot(t), "internal", "profiletarget")
	bannedPrefixes := []string{
		moduleInternal + "codex",
		moduleInternal + "antigravity",
		moduleInternal + "claudecode",
	}
	err := walkProductionGo(root, func(path string, file *ast.File) error {
		for _, imported := range importPaths(file) {
			for _, banned := range bannedPrefixes {
				if imported == banned || strings.HasPrefix(imported, banned+"/") {
					return boundaryFailure(path, "profiletarget hardcodes Provider domain "+imported)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTransactionHasNoStoreOrLockDependency(t *testing.T) {
	root := filepath.Join(repositoryRoot(t), "internal", "switching", "transaction")
	err := walkProductionGo(root, func(path string, file *ast.File) error {
		for _, banned := range []string{moduleInternal + "store", moduleInternal + "targetfs", moduleInternal + "maintenance"} {
			if importsPath(file, banned) {
				return boundaryFailure(path, "transaction imports persistence or lock package "+banned)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestProviderRootServicesDoNotInvokeTargetMutations(t *testing.T) {
	root := repositoryRoot(t)
	for _, domain := range []string{"codex", "antigravity", "claudecode"} {
		directory := filepath.Join(root, "internal", domain)
		entries, err := os.ReadDir(directory)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			path := filepath.Join(directory, entry.Name())
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			var violation string
			ast.Inspect(file, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				switch selector.Sel.Name {
				case "Apply", "Restore", "Remove", "Backup":
					violation = selector.Sel.Name
					return false
				default:
					return true
				}
			})
			if violation != "" {
				t.Fatalf("%s: Provider root invokes external target mutation %s", path, violation)
			}
		}
	}
}

func TestNoMutableGlobalAgentAdapterOrBackendMaps(t *testing.T) {
	root := filepath.Join(repositoryRoot(t), "internal")
	err := walkProductionGo(root, func(path string, file *ast.File) error {
		for _, declaration := range file.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok || general.Tok != token.VAR {
				continue
			}
			for _, rawSpec := range general.Specs {
				spec, ok := rawSpec.(*ast.ValueSpec)
				if !ok || !valueSpecContainsMap(spec) {
					continue
				}
				for _, name := range spec.Names {
					normalized := strings.ToLower(name.Name)
					if strings.Contains(normalized, "adapter") || strings.Contains(normalized, "backend") ||
						strings.Contains(normalized, "agentregistry") || strings.Contains(normalized, "targetregistry") {
						return boundaryFailure(path, "mutable global registry map "+name.Name)
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAgentRegistryRejectsOwnershipConflictsAndReturnsCopies(t *testing.T) {
	_, err := agent.NewRegistry(
		agent.Manifest{ID: "one", DisplayName: "One", ProviderIDs: []string{"shared"}},
		agent.Manifest{ID: "two", DisplayName: "Two", ProviderIDs: []string{"shared"}},
	)
	if err == nil {
		t.Fatal("Agent Registry accepted duplicate Provider ownership")
	}
	registry := agent.MustRegistry(agent.Manifest{ID: "one", DisplayName: "One", ProviderIDs: []string{"provider"}})
	manifests := registry.Manifests()
	manifests[0].ProviderIDs[0] = "mutated"
	if owner, ok := registry.AgentForProvider("provider"); !ok || owner != "one" {
		t.Fatalf("returned Manifest mutated Registry ownership: owner=%q ok=%v", owner, ok)
	}
}

func walkProductionGo(root string, visit func(string, *ast.File) error) error {
	return walkGo(root, func(path string, file *ast.File) error {
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		return visit(path, file)
	})
}

func walkGo(root string, visit func(string, *ast.File) error) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		return visit(path, file)
	})
}

func importPaths(file *ast.File) []string {
	paths := make([]string, 0, len(file.Imports))
	for _, imported := range file.Imports {
		paths = append(paths, strings.Trim(imported.Path.Value, `"`))
	}
	return paths
}

func importsPath(file *ast.File, path string) bool {
	for _, imported := range importPaths(file) {
		if imported == path {
			return true
		}
	}
	return false
}

func receiverType(function *ast.FuncDecl) string {
	if function.Recv == nil || len(function.Recv.List) != 1 {
		return ""
	}
	typeExpression := function.Recv.List[0].Type
	if pointer, ok := typeExpression.(*ast.StarExpr); ok {
		typeExpression = pointer.X
	}
	identifier, _ := typeExpression.(*ast.Ident)
	if identifier == nil {
		return ""
	}
	return identifier.Name
}

func valueSpecContainsMap(spec *ast.ValueSpec) bool {
	if _, ok := spec.Type.(*ast.MapType); ok {
		return true
	}
	for _, value := range spec.Values {
		literal, ok := value.(*ast.CompositeLit)
		if ok {
			if _, isMap := literal.Type.(*ast.MapType); isMap {
				return true
			}
		}
	}
	return false
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to resolve architecture test path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func boundaryFailure(path, message string) error {
	return fmt.Errorf("%s: %s", path, message)
}
