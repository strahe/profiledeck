import "@testing-library/jest-dom/vitest";
import { locale } from "svelte-i18n";
import { beforeEach } from "vitest";

import { setupI18n } from "$lib/i18n";

setupI18n();

beforeEach(() => {
	locale.set("en-US");
	document.documentElement.lang = "en-US";
});
