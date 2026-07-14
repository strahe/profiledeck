package update

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha512"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/updater"
)

func TestStrictProviderAcceptsSignedUpgrade(t *testing.T) {
	fixture := newFeedFixture(t, "0.1.0-alpha.2", []byte("signed artifact"))
	server := fixture.server(t, false)
	defer server.Close()
	fixture.setURLs(t, server.URL)

	provider := fixture.provider(t, server.URL)
	release, err := provider.Check(context.Background(), checkRequest("0.1.0-alpha.1"))
	if err != nil {
		t.Fatalf("check signed upgrade: %v", err)
	}
	if release == nil || release.Version != fixture.manifest.Version || release.Verification == nil {
		t.Fatalf("unexpected release: %#v", release)
	}
	if release.Verification.SignatureAlgo != SignatureAlgorithm || len(release.Verification.Signature) == 0 {
		t.Fatalf("artifact signature was not preserved: %#v", release.Verification)
	}
}

func TestStrictProviderTreatsSameVersionAsUpToDate(t *testing.T) {
	fixture := newFeedFixture(t, "0.1.0-alpha.2", []byte("artifact"))
	server := fixture.server(t, false)
	defer server.Close()
	fixture.setURLs(t, server.URL)
	release, err := fixture.provider(t, server.URL).Check(context.Background(), checkRequest(fixture.manifest.Version))
	if err != nil || release != nil {
		t.Fatalf("same version should be up to date: release=%#v err=%v", release, err)
	}
}

func TestStrictProviderRejectsTamperedOrUnsignedFeed(t *testing.T) {
	fixture := newFeedFixture(t, "0.1.0-alpha.2", []byte("artifact"))

	tests := []struct {
		name string
		edit func(*FeedEnvelope)
		code string
	}{
		{
			name: "payload tampered",
			edit: func(envelope *FeedEnvelope) { envelope.Payload[0] ^= 1 },
			code: ErrorFeedSignatureInvalid,
		},
		{
			name: "signature missing",
			edit: func(envelope *FeedEnvelope) { envelope.Signature = nil },
			code: ErrorFeedSignatureMissing,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var envelope FeedEnvelope
			if err := json.Unmarshal(fixture.feed, &envelope); err != nil {
				t.Fatal(err)
			}
			test.edit(&envelope)
			raw, err := json.Marshal(envelope)
			if err != nil {
				t.Fatal(err)
			}
			_, err = verifyFeed(raw, fixture.publicKey)
			if ErrorCode(err) != test.code {
				t.Fatalf("error code = %q, want %q (err=%v)", ErrorCode(err), test.code, err)
			}
		})
	}
}

func TestStrictProviderRejectsWrongPublicKey(t *testing.T) {
	fixture := newFeedFixture(t, "0.1.0-alpha.2", []byte("artifact"))
	wrongPublic, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, err = verifyFeed(fixture.feed, wrongPublic)
	if ErrorCode(err) != ErrorFeedSignatureInvalid {
		t.Fatalf("expected signature failure, got %v", err)
	}
}

func TestStrictProviderRejectsRollbackAndInvalidManifestTargets(t *testing.T) {
	base := newFeedFixture(t, "0.1.0-alpha.2", []byte("artifact")).manifest
	tests := []struct {
		name    string
		current string
		edit    func(*FeedManifest)
		code    string
	}{
		{name: "old replay", current: "0.1.0-alpha.3", edit: func(*FeedManifest) {}, code: ErrorFeedRollback},
		{name: "channel", current: "0.1.0-alpha.1", edit: func(m *FeedManifest) { m.Channel = "stable" }, code: ErrorFeedInvalid},
		{name: "architecture", current: "0.1.0-alpha.1", edit: func(m *FeedManifest) { m.Artifact.Arch = "amd64" }, code: ErrorFeedInvalid},
		{name: "platform", current: "0.1.0-alpha.1", edit: func(m *FeedManifest) { m.Artifact.Platform = "windows" }, code: ErrorFeedInvalid},
		{name: "release url", current: "0.1.0-alpha.1", edit: func(m *FeedManifest) { m.Artifact.URL = "https://example.com/app.zip" }, code: ErrorFeedInvalid},
		{name: "tag", current: "0.1.0-alpha.1", edit: func(m *FeedManifest) { m.ReleaseTag = "v9.9.9" }, code: ErrorFeedInvalid},
		{name: "filename", current: "0.1.0-alpha.1", edit: func(m *FeedManifest) { m.Artifact.Filename = "other.zip" }, code: ErrorFeedInvalid},
		{name: "artifact signature", current: "0.1.0-alpha.1", edit: func(m *FeedManifest) { m.Artifact.Signature = nil }, code: ErrorArtifactSignatureMissing},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := base
			manifest.Artifact.Digest = append([]byte(nil), base.Artifact.Digest...)
			manifest.Artifact.Signature = append([]byte(nil), base.Artifact.Signature...)
			test.edit(&manifest)
			_, err := validateManifest(manifest, test.current, false)
			if ErrorCode(err) != test.code {
				t.Fatalf("error code = %q, want %q (err=%v)", ErrorCode(err), test.code, err)
			}
		})
	}
}

func TestWailsUpdaterRejectsArtifactThatDoesNotMatchSignedFeed(t *testing.T) {
	fixture := newFeedFixture(t, "0.1.0-alpha.2", []byte("expected artifact"))
	server := fixture.server(t, true)
	defer server.Close()
	fixture.setURLs(t, server.URL)
	provider := fixture.provider(t, server.URL)
	host := &testUpdaterHost{}
	wailsUpdater := updater.New(host)
	if err := wailsUpdater.Init(updater.Config{
		CurrentVersion: "0.1.0-alpha.1",
		Providers:      []updater.Provider{provider},
		PublicKey:      fixture.publicKey,
		Platform:       UpdatePlatform,
		Arch:           UpdateArch,
		Channel:        UpdateChannel,
		Window:         updater.WindowNone,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := wailsUpdater.Check(context.Background()); err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := wailsUpdater.DownloadAndInstall(context.Background()); err == nil {
		t.Fatal("expected Wails artifact verification to fail")
	}
	if wailsUpdater.State() != updater.StateError {
		t.Fatalf("unexpected Wails updater state: %s", wailsUpdater.State())
	}
}

func TestWailsUpdaterRejectsWrongArtifactSignature(t *testing.T) {
	fixture := newFeedFixture(t, "0.1.0-alpha.2", []byte("signed artifact"))
	fixture.manifest.Artifact.Signature[0] ^= 1
	feed, err := MarshalSignedFeed(fixture.manifest, fixture.privateKey)
	if err != nil {
		t.Fatal(err)
	}
	fixture.feed = feed
	server := fixture.server(t, false)
	defer server.Close()
	fixture.setURLs(t, server.URL)

	provider := fixture.provider(t, server.URL)
	wailsUpdater := updater.New(&testUpdaterHost{})
	if err := wailsUpdater.Init(updater.Config{
		CurrentVersion: "0.1.0-alpha.1",
		Providers:      []updater.Provider{provider},
		PublicKey:      fixture.publicKey,
		Platform:       UpdatePlatform,
		Arch:           UpdateArch,
		Channel:        UpdateChannel,
		Window:         updater.WindowNone,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := wailsUpdater.Check(context.Background()); err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := wailsUpdater.DownloadAndInstall(context.Background()); err == nil {
		t.Fatal("expected the artifact signature to fail")
	}
}

type feedFixture struct {
	publicKey  ed25519.PublicKey
	privateKey ed25519.PrivateKey
	artifact   []byte
	manifest   FeedManifest
	feed       []byte
}

func newFeedFixture(t *testing.T, version string, artifact []byte) *feedFixture {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha512.Sum512(artifact)
	signature, err := SignPrehashed(privateKey, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	filename := "ProfileDeck_" + version + "_darwin_arm64.zip"
	manifest := FeedManifest{
		SchemaVersion: FeedSchemaVersion,
		Version:       version,
		Channel:       UpdateChannel,
		ReleaseTag:    "v" + version,
		Artifact: ManifestArtifact{
			URL:           "https://github.com/strahe/profiledeck/releases/download/v" + version + "/" + filename,
			Filename:      filename,
			Platform:      UpdatePlatform,
			Arch:          UpdateArch,
			Size:          int64(len(artifact)),
			DigestAlgo:    DigestAlgorithm,
			Digest:        digest[:],
			SignatureAlgo: SignatureAlgorithm,
			Signature:     signature,
		},
	}
	feed, err := MarshalSignedFeed(manifest, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return &feedFixture{publicKey: publicKey, privateKey: privateKey, artifact: artifact, manifest: manifest, feed: feed}
}

func (fixture *feedFixture) setURLs(t *testing.T, serverURL string) {
	t.Helper()
	fixture.manifest.Artifact.URL = serverURL + "/artifact.zip"
	feed, err := MarshalSignedFeed(fixture.manifest, fixture.privateKey)
	if err != nil {
		t.Fatal(err)
	}
	fixture.feed = feed
}

func (fixture *feedFixture) server(t *testing.T, corruptArtifact bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/feed.json":
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write(fixture.feed)
		case "/artifact.zip":
			artifact := fixture.artifact
			if corruptArtifact {
				artifact = []byte("tampered artifact")
			}
			response.Header().Set("Content-Length", strconv.Itoa(len(artifact)))
			_, _ = response.Write(artifact)
		default:
			http.NotFound(response, request)
		}
	}))
}

func (fixture *feedFixture) provider(t *testing.T, serverURL string) *StrictProvider {
	t.Helper()
	provider, err := NewStrictProvider(ProviderConfig{
		FeedURL: serverURL + "/feed.json", PublicKey: fixture.publicKey, AllowTestSource: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func checkRequest(current string) updater.CheckRequest {
	return updater.CheckRequest{CurrentVersion: current, Platform: UpdatePlatform, Arch: UpdateArch}
}

type testUpdaterHost struct{}

func (*testUpdaterHost) Emit(string, ...any) bool                              { return false }
func (*testUpdaterHost) OnEvent(string, func(any)) func()                      { return func() {} }
func (*testUpdaterHost) OpenWindow(updater.WindowOptions) updater.WindowHandle { return testWindow{} }
func (*testUpdaterHost) Quit()                                                 {}

type testWindow struct{}

func (testWindow) EmitEvent(string, ...any) bool { return false }
func (testWindow) Show()                         {}
func (testWindow) Close()                        {}
