<script lang="ts">
	import type { Snippet } from "svelte";

	import { Badge } from "$lib/components/ui/badge";
	import { cn } from "$lib/utils";

	let {
		tone = "neutral",
		class: className,
		children,
	}: {
		tone?: "current" | "warning" | "success" | "running" | "neutral";
		class?: string;
		children?: Snippet;
	} = $props();
</script>

<Badge
	variant={tone === "warning" ? "destructive" : tone === "neutral" ? "outline" : "secondary"}
	class={cn(
		tone === "current" && "border-primary bg-primary text-primary-foreground hover:bg-primary/90",
		tone === "success" && "border-transparent bg-primary/10 text-foreground",
		tone === "running" && "border-transparent bg-secondary text-secondary-foreground",
		className,
	)}
>
	{@render children?.()}
</Badge>
