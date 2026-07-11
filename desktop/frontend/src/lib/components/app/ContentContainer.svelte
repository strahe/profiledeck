<script lang="ts">
	import type { Snippet } from "svelte";
	import type { HTMLAttributes } from "svelte/elements";

	import { cn } from "$lib/utils";

	let {
		class: className,
		children,
		...restProps
	}: HTMLAttributes<HTMLDivElement> & { children?: Snippet } = $props();
</script>

<div class={cn("mx-auto flex w-full max-w-5xl flex-col gap-4", className)} {...restProps}>
	{@render children?.()}
</div>
