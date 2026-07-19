import { resolve } from "node:path";
import { svelte } from "@sveltejs/vite-plugin-svelte";
import { svelteTesting } from "@testing-library/svelte/vite";
import { defineConfig } from "vitest/config";

export default defineConfig({
	resolve: {
		alias: {
			$lib: resolve("./src/lib"),
		},
	},
	plugins: [svelte(), svelteTesting()],
	test: {
		environment: "jsdom",
		globals: false,
		setupFiles: ["./tests/setup.ts"],
	},
});
