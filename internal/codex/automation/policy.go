package automation

import "github.com/strahe/profiledeck/internal/apperror"

const UsageSyncIntervalDefault = 15

// NormalizeUsageSyncInterval accepts the supported local usage-refresh cadence.
func NormalizeUsageSyncInterval(value int) (int, *apperror.Error) {
	switch value {
	case 5, 15, 30, 60:
		return value, nil
	default:
		return 0, apperror.New(apperror.SettingInvalid, "unsupported Codex usage sync interval").WithDetail("usage_sync_interval_seconds", value)
	}
}

// NormalizeQuotaRefreshInterval accepts opt-in quota automation intervals.
func NormalizeQuotaRefreshInterval(value int) (int, *apperror.Error) {
	switch value {
	case 0, 300, 600, 1800, 3600:
		return value, nil
	default:
		return 0, apperror.New(apperror.SettingInvalid, "unsupported Codex quota refresh interval").WithDetail("quota_refresh_interval_seconds", value)
	}
}

// ValidateProfileSettings prevents unsupported login modes from enabling automation.
func ValidateProfileSettings(quotaInterval int, keepalive, quotaSupported, keepaliveSupported bool) *apperror.Error {
	if quotaInterval > 0 && !quotaSupported {
		return apperror.New(apperror.SettingInvalid, "Codex quota refresh is unsupported for this login mode")
	}
	if keepalive && !keepaliveSupported {
		return apperror.New(apperror.SettingInvalid, "Codex auth keepalive is unsupported for this login mode")
	}
	return nil
}
