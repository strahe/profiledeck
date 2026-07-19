import { describe, expect, it } from "vitest";

import {
	MAX_SYNC_JS_CHUNK_BYTES,
	WORKSPACE_VIEW_ENTRIES,
	checkBuildManifest,
} from "../scripts/check-build-output.mjs";

type ManifestEntry = {
	file: string;
	src?: string;
	isEntry?: boolean;
	isDynamicEntry?: boolean;
	imports?: string[];
	dynamicImports?: string[];
};

function manifestWithViews(entry: ManifestEntry = { file: "assets/app.js", isEntry: true }) {
	return Object.fromEntries([
		["index.html", entry],
		...WORKSPACE_VIEW_ENTRIES.map((source, index) => [source, {
			file: `assets/view-${index}.js`,
			src: source,
			isDynamicEntry: true,
		}]),
	]) as Record<string, ManifestEntry>;
}

function sizeReader(sizes: Record<string, number>) {
	return (file: string) => sizes[file] ?? 1;
}

describe("production bundle policy", () => {
	it("rejects one oversized synchronous chunk", () => {
		const manifest = manifestWithViews({
			file: "assets/app.js",
			isEntry: true,
			imports: ["shared.js"],
		});
		manifest["shared.js"] = { file: "assets/shared.js" };

		expect(() => checkBuildManifest(manifest, sizeReader({
			"assets/shared.js": MAX_SYNC_JS_CHUNK_BYTES + 1,
		}))).toThrow(/exceeds 500000 bytes/);
	});

	it("allows multiple synchronous chunks whose total exceeds 500 kB", () => {
		const manifest = manifestWithViews({
			file: "assets/app.js",
			isEntry: true,
			imports: ["shared.js"],
		});
		manifest["shared.js"] = { file: "assets/shared.js" };

		expect(() => checkBuildManifest(manifest, sizeReader({
			"assets/app.js": 300_000,
			"assets/shared.js": 300_000,
		}))).not.toThrow();
	});

	it("does not include asynchronous chunks in the size gate", () => {
		const manifest = manifestWithViews({
			file: "assets/app.js",
			isEntry: true,
			dynamicImports: ["async.js"],
		});
		manifest["async.js"] = { file: "assets/async.js", isDynamicEntry: true };

		expect(() => checkBuildManifest(manifest, sizeReader({
			"assets/async.js": 900_000,
		}))).not.toThrow();
	});

	it("requires all seven Workspace views to remain dynamic entries", () => {
		const manifest = manifestWithViews();
		manifest[WORKSPACE_VIEW_ENTRIES[0]].isDynamicEntry = false;

		expect(() => checkBuildManifest(manifest, sizeReader({}))).toThrow(
			`Workspace view is not a dynamic entry: ${WORKSPACE_VIEW_ENTRIES[0]}`,
		);
	});

	it("rejects a dynamic Workspace view in the App Shell static graph", () => {
		const manifest = manifestWithViews({
			file: "assets/app.js",
			isEntry: true,
			imports: [WORKSPACE_VIEW_ENTRIES[0]],
		});

		expect(() => checkBuildManifest(manifest, sizeReader({}))).toThrow(/App Shell static graph/);
	});
});
