import { afterEach, describe, expect, it, vi } from "vitest";

describe("quota formatting", () => {
	afterEach(() => {
		vi.restoreAllMocks();
		vi.resetModules();
	});

	it("loads when supported currency discovery is unavailable", async () => {
		vi.spyOn(Intl, "supportedValuesOf").mockImplementation(() => {
			throw new Error("unsupported");
		});
		vi.resetModules();

		const { formatQuotaValue } = await import("../src/features/profiles/quota-format");

		expect(formatQuotaValue(12.5, "USD", "en-US")).toBe("12.5 USD");
	});
});
