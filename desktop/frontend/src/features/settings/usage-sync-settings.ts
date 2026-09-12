export const usageIntervals = [15, 30, 60, 120, 300] as const;

export const usageSyncIntervalDefault = 60;

export function usageIntervalLabel(seconds: number): { key: "usageSettings.seconds" | "usageSettings.minutes"; count: number } {
	if (seconds >= 60 && seconds % 60 === 0) {
		return { key: "usageSettings.minutes", count: seconds / 60 };
	}
	return { key: "usageSettings.seconds", count: seconds };
}
