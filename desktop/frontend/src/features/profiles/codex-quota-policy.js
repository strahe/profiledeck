const codexProviderID = "codex";

export class CodexStartupQuotaReadCoordinator {
	#handled = false;
	#handledSwitchOperations = new Set();

	/**
	 * @param {string} profileID
	 * @param {Promise<unknown>} runtimeReady
	 * @param {(profileID: string) => void} readQuota
	 */
	async start(profileID, runtimeReady, readQuota) {
		if (this.#handled) return;
		this.#handled = true;
		await runtimeReady;
		if (profileID) readQuota(profileID);
	}

	/**
	 * @param {{ kind?: string, status?: string, provider_id?: string, profile_id?: string, operation_id?: string } | null | undefined} event
	 */
	afterSwitch(event) {
		if (event?.kind !== "switch-applied" || event.status !== "success" || event.provider_id !== codexProviderID) return "";
		const profileID = event.profile_id || "";
		if (!profileID) return "";
		const operationID = event.operation_id || "";
		if (operationID && this.#handledSwitchOperations.has(operationID)) return "";
		if (operationID) this.#handledSwitchOperations.add(operationID);
		return profileID;
	}
}

/**
 * @param {{ credential_id?: string, config_set_id?: string }} summary
 * @param {{ credential_id?: string, config_set_id?: string, source?: string } | null | undefined} quota
 */
export function codexQuotaMatchesSummary(summary, quota) {
	if (!quota || (quota.credential_id ?? "") !== (summary.credential_id ?? "")) return false;
	return quota.source !== "sub2api" || (quota.config_set_id ?? "") === (summary.config_set_id ?? "");
}
