const grokBuildProviderID = "grok-build";

export class GrokBuildQuotaReadPolicy {
	#startupHandled = false;
	#handledSwitchOperations = new Set();

	/**
	 * @param {string} profileID
	 * @param {boolean} enabled
	 */
	startup(profileID, enabled) {
		if (this.#startupHandled) return "";
		this.#startupHandled = true;
		return enabled ? profileID || "" : "";
	}

	/**
	 * @param {{ kind?: string, status?: string, provider_id?: string, profile_id?: string, operation_id?: string } | null | undefined} event
	 * @param {boolean} enabled
	 */
	afterSwitch(event, enabled) {
		if (event?.kind !== "switch-applied" || event.status !== "success" || event.provider_id !== grokBuildProviderID) return "";
		const profileID = event.profile_id || "";
		if (!profileID) return "";
		const operationID = event.operation_id || "";
		if (operationID && this.#handledSwitchOperations.has(operationID)) return "";
		if (operationID) this.#handledSwitchOperations.add(operationID);
		return enabled ? profileID : "";
	}
}

/** @param {{ credential_id?: string, config_set_id?: string }} summary */
export function grokBuildQuotaBindingKey(summary) {
	const credentialID = summary.credential_id ?? "";
	const configSetID = summary.config_set_id ?? "";
	return credentialID && configSetID ? `${credentialID}\u0000${configSetID}` : "";
}

/**
 * @param {Array<{ credential_id?: string, config_set_id?: string }>} profiles
 * @param {Record<string, unknown>} records
 */
export function pruneGrokBuildQuotaRecords(profiles, records) {
	const bindingKeys = new Set(profiles.map(grokBuildQuotaBindingKey).filter(Boolean));
	return Object.fromEntries(Object.entries(records).filter(([bindingKey]) => bindingKeys.has(bindingKey)));
}

/**
 * @param {Array<{ profile: { id: string }, active: boolean }>} current
 * @param {Array<{ profile: { id: string }, active: boolean }> | null | undefined} incoming
 * @param {string} activeProfileID
 */
export function reconcileGrokBuildProfileSummaries(current, incoming, activeProfileID) {
	if (incoming !== null && incoming !== undefined) return incoming;
	return current.map((summary) => {
		const active = summary.profile.id === activeProfileID;
		return summary.active === active ? summary : { ...summary, active };
	});
}
