<script lang="ts">
	import { _ } from "svelte-i18n";

	import * as Tooltip from "$lib/components/ui/tooltip";
	import { currentDesktopLocale } from "$lib/i18n";

	let {
		checkedAtUnixMS,
		checkOutcome,
		nowUnixMS,
	}: {
		checkedAtUnixMS: number;
		checkOutcome: "updated" | "checked" | "failed" | "never";
		nowUnixMS: number;
	} = $props();

	function formatFullCheck(unixMS: number): string {
		return new Date(unixMS).toLocaleString(currentDesktopLocale(), {
			year: "numeric",
			month: "long",
			day: "numeric",
			hour: "2-digit",
			minute: "2-digit",
			second: "2-digit",
			timeZoneName: "short",
		});
	}

	function relativeTime(value: number): string {
		const elapsed = Math.max(0, nowUnixMS - value);
		if (elapsed < 60_000) return $_("time.justNow");
		return $_("time.minutesAgo", { values: { count: Math.max(1, Math.floor(elapsed / 60_000)) } });
	}

	function freshness(value: string): string {
		if (checkOutcome === "updated") return $_("quota.updatedAt", { values: { value } });
		if (checkOutcome === "failed") return $_("quota.checkFailedAt", { values: { value } });
		return $_("quota.checkedAt", { values: { value } });
	}
</script>

{#if checkedAtUnixMS && checkOutcome !== "never"}
	<Tooltip.Root>
		<Tooltip.Trigger>
			{#snippet child({ props })}
				<button
					{...props}
					type="button"
					class="shrink-0 rounded-sm text-xs text-muted-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring"
				>
					{freshness(relativeTime(checkedAtUnixMS))}
				</button>
			{/snippet}
		</Tooltip.Trigger>
		<Tooltip.Content>{freshness(formatFullCheck(checkedAtUnixMS))}</Tooltip.Content>
	</Tooltip.Root>
{/if}
