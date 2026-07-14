package automation

import "testing"

func TestProfileSettingsPolicy(t *testing.T) {
	if _, err := NormalizeUsageSyncInterval(15); err != nil {
		t.Fatal(err)
	}
	if _, err := NormalizeQuotaRefreshInterval(600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateProfileSettings(300, false, false, true); err == nil {
		t.Fatal("expected unsupported quota interval to fail")
	}
	if err := ValidateProfileSettings(0, true, true, false); err == nil {
		t.Fatal("expected unsupported keepalive to fail")
	}
}
