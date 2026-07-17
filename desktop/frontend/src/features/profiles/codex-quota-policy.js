/**
 * Keeps the automatic Codex quota read to one startup attempt after the
 * initial runtime snapshot is loaded. Navigation and later refreshes do not
 * enter this policy.
 */
export class CodexStartupQuotaReadCoordinator {
	#handled = false;

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
}
