package automation

import "testing"

func TestProfileSettingsPolicy(t *testing.T) {
	if got, err := NormalizeUsageSyncInterval(5); err != nil || got != 15 {
		t.Fatalf("retired usage interval = %d, err = %v", got, err)
	}
	if _, err := NormalizeUsageSyncInterval(300); err != nil {
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
