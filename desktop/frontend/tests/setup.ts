import "@testing-library/jest-dom/vitest";
import { act, cleanup, setup, waitFor } from "@testing-library/svelte";
import { locale } from "svelte-i18n";
import { afterEach, beforeEach, expect } from "vitest";

import { setupI18n } from "$lib/i18n";

setupI18n();

beforeEach(async () => {
	await setup();
	locale.set("en-US");
	document.documentElement.lang = "en-US";
});

afterEach(async () => {
	await act();
	cleanup();
	// Bits UI releases its global body scroll lock asynchronously, so keep jsdom alive until restoration completes.
	await waitFor(() => expect(document.body.style.overflow).not.toBe("hidden"));
});
