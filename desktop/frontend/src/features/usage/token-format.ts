import { translate } from "$lib/i18n";

export function usageTokenLabel(formatted: string, status: string): string {
	if (status === "unknown") return translate("usage.tokensUnknown");
	if (status === "partial") return translate("usage.tokensAtLeast", { value: formatted });
	return formatted;
}
