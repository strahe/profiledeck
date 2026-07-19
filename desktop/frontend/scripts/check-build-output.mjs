import fs from "node:fs";
import path from "node:path";
import { pathToFileURL } from "node:url";

export const MAX_SYNC_JS_CHUNK_BYTES = 500_000;

export const WORKSPACE_VIEW_ENTRIES = [
	"src/features/profiles/CodexProfiles.svelte",
	"src/features/profiles/AntigravityProfiles.svelte",
	"src/features/profiles/ClaudeCodeProfiles.svelte",
	"src/features/usage/UsagePage.svelte",
	"src/features/settings/CodexSettings.svelte",
	"src/features/workspace/GlobalSettings.svelte",
	"src/features/workspace/DiagnosticsPage.svelte",
];

export function checkBuildManifest(
	manifest,
	readFileSize,
	{
		maxChunkBytes = MAX_SYNC_JS_CHUNK_BYTES,
		workspaceViewEntries = WORKSPACE_VIEW_ENTRIES,
	} = {},
) {
	const manifestEntries = Object.entries(manifest);
	const entryKeys = manifestEntries
		.filter(([, entry]) => entry.isEntry)
		.map(([key]) => key);
	if (entryKeys.length === 0) {
		throw new Error("Build manifest does not contain an application entry.");
	}

	const synchronousGraph = collectSynchronousGraph(manifest, entryKeys);
	for (const source of workspaceViewEntries) {
		const match = manifestEntries.find(([key, entry]) => key === source || entry.src === source);
		if (!match || !match[1].isDynamicEntry) {
			throw new Error(`Workspace view is not a dynamic entry: ${source}`);
		}
		if (synchronousGraph.has(match[0])) {
			throw new Error(`Workspace view is part of the App Shell static graph: ${source}`);
		}
	}

	for (const key of synchronousGraph) {
		const entry = manifest[key];
		if (!entry.file.endsWith(".js")) continue;
		const size = readFileSize(entry.file);
		if (size > maxChunkBytes) {
			throw new Error(`Synchronous JavaScript chunk exceeds ${maxChunkBytes} bytes: ${entry.file} (${size} bytes)`);
		}
	}
}

function collectSynchronousGraph(manifest, entryKeys) {
	const visited = new Set();
	const pending = [...entryKeys];
	while (pending.length > 0) {
		const key = pending.pop();
		if (visited.has(key)) continue;
		const entry = manifest[key];
		if (!entry) {
			throw new Error(`Build manifest references a missing synchronous import: ${key}`);
		}
		visited.add(key);
		for (const imported of entry.imports ?? []) pending.push(imported);
	}
	return visited;
}

function checkProductionBuild() {
	const distRoot = path.resolve("dist");
	const manifestDirectory = path.join(distRoot, ".vite");
	const manifestPath = path.join(manifestDirectory, "manifest.json");
	try {
		const manifest = JSON.parse(fs.readFileSync(manifestPath, "utf8"));
		checkBuildManifest(manifest, (file) => {
			const builtFile = path.resolve(distRoot, file);
			if (!builtFile.startsWith(`${distRoot}${path.sep}`)) {
				throw new Error(`Build manifest contains an invalid output path: ${file}`);
			}
			return fs.statSync(builtFile).size;
		});
	} finally {
		fs.rmSync(manifestPath, { force: true });
		try {
			fs.rmdirSync(manifestDirectory);
		} catch (error) {
			if (error?.code !== "ENOENT" && error?.code !== "ENOTEMPTY") throw error;
		}
	}
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
	checkProductionBuild();
}
