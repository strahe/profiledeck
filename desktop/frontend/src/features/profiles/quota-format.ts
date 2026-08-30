function loadSupportedCurrencyUnits(): Set<string> {
	try {
		return typeof Intl.supportedValuesOf === "function"
			? new Set(Intl.supportedValuesOf("currency"))
			: new Set<string>();
	} catch {
		return new Set<string>();
	}
}

const supportedCurrencyUnits = loadSupportedCurrencyUnits();

export function formatQuotaValue(value: number | null | undefined, unit: string, locale: string): string {
	if (value == null) return "—";
	if (/^[A-Z]{3}$/.test(unit) && supportedCurrencyUnits.has(unit)) {
		return new Intl.NumberFormat(locale, { style: "currency", currency: unit }).format(value);
	}
	const formatted = new Intl.NumberFormat(locale, { maximumFractionDigits: 4 }).format(value);
	return unit ? `${formatted} ${unit}` : formatted;
}
