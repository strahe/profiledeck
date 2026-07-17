/**
 * @template {{ revision: number }} T
 * @param {T} current
 * @param {T} next
 * @returns {T}
 */
export function selectLatestUpdateStatus(current, next) {
	return next.revision < current.revision ? current : next;
}
