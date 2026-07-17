BINARY := profiledeck
BIN_DIR := bin
TOOLS_DIR := $(BIN_DIR)/tools
CMD := ./cmd/profiledeck
DESKTOP_FRONTEND := desktop/frontend
RELEASE_TOOL_PKGS := ./scripts/releasetool ./scripts/updatee2e/runner
UPDATE_E2E_PKG := ./scripts/updatee2e/client
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
DESKTOP_GO_ENV := GOOS=$(DESKTOP_GOOS) GOARCH=$(DESKTOP_GOARCH)
VERSION ?=
BUILD_NUMBER ?=
RELEASE_REPO ?= strahe/profiledeck-private
SIGN_IDENTITY ?=

.PHONY: fmt vet lint lint-core lint-desktop test build core-boundary core-check check clean wails-boundary desktop-bindings desktop-bindings-check desktop-taskfile-check desktop-frontend-install desktop-frontend-check desktop-build release-build release-draft release-publish verify-update-e2e desktop-check docs-install docs-dev docs-build docs-preview docs-check ci-core-check ci-desktop-check

fmt:
	$(GOLANGCI_LINT) fmt $(GO_PKGS)
	$(GOLANGCI_LINT) fmt ./scripts/...

vet: lint-core

lint: lint-core lint-desktop

lint-core:
	$(GOLANGCI_LINT) run $(CORE_PKGS)

lint-desktop:
	$(DESKTOP_GO_ENV) $(GOLANGCI_LINT) run $(DESKTOP_PKGS) $(RELEASE_TOOL_PKGS)
	$(DESKTOP_GO_ENV) $(GOLANGCI_LINT) run --build-tags updatee2e $(UPDATE_E2E_PKG)

test:
	go test $(CORE_PKGS)

build:
	mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/$(BINARY) $(CMD)

core-boundary:
	go test ./internal/architecture

core-check: lint-core core-boundary test build

check: core-check desktop-check docs-check

wails-boundary:
	! rg -n 'github.com/wailsapp/wails|@wailsio/runtime' cmd internal

desktop-bindings:
	$(WAILS3) task common:bindings

desktop-bindings-check:
	@temp_dir=$$(mktemp -d); \
	trap 'rm -rf "$$temp_dir"' EXIT; \
	$(WAILS3) generate bindings -d "$$temp_dir" -ts -i $(DESKTOP_PKGS); \
	diff -ru $(DESKTOP_FRONTEND)/bindings "$$temp_dir"

desktop-taskfile-check:
	$(WAILS3) task --list >/dev/null
	$(WAILS3) task common:generate:icons -dry >/dev/null
	$(WAILS3) task build GOOS=darwin -dry >/dev/null
	$(WAILS3) task build GOOS=darwin DEV=true EXTRA_TAGS=taskfilecheck -dry >/dev/null
	$(WAILS3) task build GOOS=windows DEV=true EXTRA_TAGS=taskfilecheck -dry >/dev/null
	$(WAILS3) task build GOOS=linux DEV=true EXTRA_TAGS=taskfilecheck -dry >/dev/null
	$(WAILS3) task darwin:build:universal VERSION=0.1.0-beta.1 COMMIT=0123456789abcdef0123456789abcdef01234567 BUILD_DATE=2026-07-16T00:00:00Z -dry >/dev/null
	$(WAILS3) task darwin:package:universal VERSION=0.1.0-beta.1 BUILD_NUMBER=1 COMMIT=0123456789abcdef0123456789abcdef01234567 BUILD_DATE=2026-07-16T00:00:00Z -dry >/dev/null

desktop-frontend-install:
	$(WAILS3) task common:frontend:install

desktop-frontend-check: desktop-frontend-install
	npm --prefix $(DESKTOP_FRONTEND) run check
	npm --prefix $(DESKTOP_FRONTEND) run test:unit

desktop-build:
	$(WAILS3) task build GOOS=$(DESKTOP_GOOS) ARCH=$(DESKTOP_GOARCH)

release-build:
	$(WAILS3) task darwin:release VERSION="$(VERSION)" BUILD_NUMBER="$(BUILD_NUMBER)" SIGN_IDENTITY="$(SIGN_IDENTITY)"

release-draft:
	go run ./scripts/releasetool draft --version "$(VERSION)" --repo "$(RELEASE_REPO)"

release-publish:
	go run ./scripts/releasetool publish --version "$(VERSION)" --repo "$(RELEASE_REPO)"

verify-update-e2e:
	go run ./scripts/updatee2e/runner

desktop-check: wails-boundary lint-desktop desktop-bindings-check desktop-taskfile-check desktop-frontend-check desktop-build
	$(DESKTOP_GO_ENV) go test $(DESKTOP_PKGS) $(RELEASE_TOOL_PKGS)
	$(DESKTOP_GO_ENV) go test -tags updatee2e $(UPDATE_E2E_PKG)

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
