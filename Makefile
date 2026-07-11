BINARY := profiledeck
BIN_DIR := bin
TOOLS_DIR := $(BIN_DIR)/tools
CMD := ./cmd/profiledeck
DESKTOP_CMD := ./desktop
DESKTOP_FRONTEND := desktop/frontend
DOCS_DIR := docs
CORE_PKGS := ./cmd/... ./internal/...
DESKTOP_PKGS := ./desktop/...
GO_PKGS := $(CORE_PKGS) $(DESKTOP_PKGS)
GOLANGCI_LINT ?= golangci-lint
WAILS3 ?= wails3
GOLANGCI_LINT_VERSION := v2.12.2
WAILS3_VERSION := v3.0.0-alpha2.115
CI_GOLANGCI_LINT_DIR := $(TOOLS_DIR)/golangci-lint/$(GOLANGCI_LINT_VERSION)
CI_GOLANGCI_LINT := $(CI_GOLANGCI_LINT_DIR)/golangci-lint
CI_WAILS3_DIR := $(TOOLS_DIR)/wails3/$(WAILS3_VERSION)
CI_WAILS3 := $(CI_WAILS3_DIR)/wails3
DESKTOP_GOOS ?= $(or $(GOOS),$(shell go env GOOS))
DESKTOP_GOARCH ?= $(or $(GOARCH),$(shell go env GOARCH))
MACOS_MIN_VERSION ?= 14.0
CGO_CFLAGS ?= -O2 -g
CGO_CXXFLAGS ?= -O2 -g
CGO_LDFLAGS ?= -O2 -g
DESKTOP_GO_ENV := GOOS=$(DESKTOP_GOOS) GOARCH=$(DESKTOP_GOARCH)
ifeq ($(DESKTOP_GOOS),darwin)
DESKTOP_GO_ENV += MACOSX_DEPLOYMENT_TARGET=$(MACOS_MIN_VERSION)
DESKTOP_GO_ENV += CGO_CFLAGS="$(CGO_CFLAGS) -mmacosx-version-min=$(MACOS_MIN_VERSION)"
DESKTOP_GO_ENV += CGO_CXXFLAGS="$(CGO_CXXFLAGS) -mmacosx-version-min=$(MACOS_MIN_VERSION)"
DESKTOP_GO_ENV += CGO_LDFLAGS="$(CGO_LDFLAGS) -mmacosx-version-min=$(MACOS_MIN_VERSION)"
endif

.PHONY: fmt vet lint lint-core lint-desktop test build core-check check clean wails-boundary desktop-go-fmt desktop-bindings desktop-bindings-check desktop-frontend-install desktop-frontend-check desktop-frontend-build desktop-build desktop-check docs-install docs-dev docs-build docs-preview docs-check ci-core-check ci-desktop-check

fmt:
	$(GOLANGCI_LINT) fmt $(GO_PKGS)

vet: lint-core

lint: lint-core lint-desktop

lint-core:
	$(GOLANGCI_LINT) run $(CORE_PKGS)

lint-desktop:
	$(DESKTOP_GO_ENV) $(GOLANGCI_LINT) run $(DESKTOP_PKGS)

test:
	go test $(CORE_PKGS)

build:
	mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/$(BINARY) $(CMD)

core-check: lint-core test build

check: core-check desktop-check docs-check

wails-boundary:
	! rg -n 'github.com/wailsapp/wails|@wailsio/runtime' cmd internal

desktop-go-fmt:
	$(GOLANGCI_LINT) fmt $(DESKTOP_PKGS)

desktop-bindings:
	$(WAILS3) generate bindings -d $(DESKTOP_FRONTEND)/bindings -ts -i $(DESKTOP_PKGS)

desktop-bindings-check:
	@temp_dir=$$(mktemp -d); \
	trap 'rm -rf "$$temp_dir"' EXIT; \
	$(WAILS3) generate bindings -d "$$temp_dir" -ts -i $(DESKTOP_PKGS); \
	diff -ru $(DESKTOP_FRONTEND)/bindings "$$temp_dir"

desktop-frontend-install:
	npm --prefix $(DESKTOP_FRONTEND) ci

desktop-frontend-check: desktop-frontend-install
	npm --prefix $(DESKTOP_FRONTEND) run check

desktop-frontend-build: desktop-frontend-install
	npm --prefix $(DESKTOP_FRONTEND) run build

desktop-build: desktop-frontend-build
	mkdir -p $(BIN_DIR)
	$(DESKTOP_GO_ENV) go build -tags production -o $(BIN_DIR)/profiledeck-desktop $(DESKTOP_CMD)

desktop-check: wails-boundary lint-desktop desktop-bindings-check desktop-frontend-check desktop-build
	$(DESKTOP_GO_ENV) go test ./desktop/...

docs-install:
	npm --prefix $(DOCS_DIR) ci

docs-dev: docs-install
	npm --prefix $(DOCS_DIR) run dev

docs-build: docs-install
	npm --prefix $(DOCS_DIR) run build

docs-preview: docs-build
	npm --prefix $(DOCS_DIR) run preview

docs-check: docs-build

$(CI_GOLANGCI_LINT):
	mkdir -p $(CI_GOLANGCI_LINT_DIR)
	curl -sSfL https://golangci-lint.run/install.sh | sh -s -- -b $(CI_GOLANGCI_LINT_DIR) $(GOLANGCI_LINT_VERSION)

$(CI_WAILS3):
	mkdir -p $(CI_WAILS3_DIR)
	GOBIN=$(abspath $(CI_WAILS3_DIR)) go install github.com/wailsapp/wails/v3/cmd/wails3@$(WAILS3_VERSION)

ci-core-check: $(CI_GOLANGCI_LINT)
	$(MAKE) core-check GOLANGCI_LINT=$(abspath $(CI_GOLANGCI_LINT))

ci-desktop-check: $(CI_GOLANGCI_LINT) $(CI_WAILS3)
	$(MAKE) desktop-check GOLANGCI_LINT=$(abspath $(CI_GOLANGCI_LINT)) WAILS3=$(abspath $(CI_WAILS3))

clean:
	rm -rf $(BIN_DIR)
