const antigravityProviderID = "antigravity";

/**
 * Keeps automatic quota reads limited to one startup read and one read per
 * successful Antigravity switch. Navigation and ordinary data refreshes do not
 * enter this policy.
 */
export class AntigravityQuotaReadPolicy {
	#startupHandled = false;
	#handledSwitchOperations = new Set();

	/** @param {string} profileID */
	startup(profileID) {
		if (this.#startupHandled) return "";
		this.#startupHandled = true;
		return profileID || "";
	}

	/**
	 * @param {{ kind?: string, status?: string, provider_id?: string, profile_id?: string, operation_id?: string } | null | undefined} event
	 */
	afterSwitch(event) {
		if (event?.kind !== "switch-applied" || event.status !== "success" || event.provider_id !== antigravityProviderID) return "";
		const profileID = event.profile_id || "";
		if (!profileID) return "";
		const operationID = event.operation_id || "";
		if (operationID && this.#handledSwitchOperations.has(operationID)) return "";
		if (operationID) this.#handledSwitchOperations.add(operationID);
		return profileID;
	}
}

export class AntigravityQuotaRequestGate {
	#profileIDs = new Set();

	/** @param {string} profileID */
	start(profileID) {
		if (!profileID || this.#profileIDs.has(profileID)) return false;
		this.#profileIDs.add(profileID);
		return true;
	}

	/** @param {string} profileID */
	finish(profileID) {
		this.#profileIDs.delete(profileID);
	}

	clear() {
		this.#profileIDs.clear();
	}
}

/**
 * @param {Array<{ profile: { id: string }, credential_id?: string }>} profiles
 * @param {Record<string, { credential_id?: string }>} records
 */
export function pruneAntigravityQuotaRecords(profiles, records) {
	const credentials = new Map(profiles.map((summary) => [summary.profile.id, summary.credential_id ?? ""]));
	return Object.fromEntries(
		Object.entries(records).filter(([profileID, quota]) => credentials.get(profileID) === (quota.credential_id ?? "")),
	);
}

/**
 * @param {Array<{ profile: { id: string }, credential_id?: string }>} profiles
 * @param {Record<string, { credentialID: string }>} records
 */
export function pruneAntigravityQuotaChecks(profiles, records) {
	const credentials = new Map(profiles.map((summary) => [summary.profile.id, summary.credential_id ?? ""]));
	return Object.fromEntries(
		Object.entries(records).filter(([profileID, check]) => credentials.get(profileID) === check.credentialID),
	);
}
