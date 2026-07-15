//go:build livesmoke

package quota_test

import (
	"context"
	"testing"
	"time"

	keyring "github.com/zalando/go-keyring"

	agyauth "github.com/strahe/profiledeck/internal/antigravity/auth"
	agyconfig "github.com/strahe/profiledeck/internal/antigravity/config"
	agyquota "github.com/strahe/profiledeck/internal/antigravity/quota"
)

func TestLiveAntigravityQuotaRead(t *testing.T) {
	raw, err := keyring.Get(agyconfig.KeyringService, agyconfig.KeyringAccount)
	if err != nil {
		t.Fatal("current Antigravity login is unavailable")
	}
	_, payload, err := agyauth.Normalize([]byte(raw))
	if err != nil || !time.UnixMilli(agyauth.ExpiryUnixMS(payload)).After(time.Now().Add(30*time.Second)) {
		t.Fatal("current Antigravity login is not fresh enough for the smoke check")
	}
	snapshot, err := agyquota.NewClient().Read(context.Background(), payload.Token.AccessToken)
	if err != nil {
		t.Fatal("Antigravity quota smoke check is unavailable")
	}
	if snapshot.FetchedAt.IsZero() || len(snapshot.Groups) == 0 {
		t.Fatal("Antigravity quota smoke check returned no groups")
	}
	for _, group := range snapshot.Groups {
		if group.DisplayName == "" || len(group.Buckets) == 0 {
			t.Fatal("Antigravity quota smoke check returned an incomplete group")
		}
	}
}
