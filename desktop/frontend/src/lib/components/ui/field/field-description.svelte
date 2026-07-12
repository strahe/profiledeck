<script lang="ts">
	import { cn, type WithElementRef } from "$lib/utils.js";
	import { tv, type VariantProps } from "tailwind-variants";
	import type { HTMLAttributes } from "svelte/elements";

	const fieldDescriptionVariants = tv({
		base: "text-muted-foreground text-left [[data-variant=legend]+&]:-mt-1.5 font-normal group-has-[[data-orientation=horizontal]]/field:text-balance",
		variants: {
			size: {
				default: "text-sm leading-normal",
				compact: "text-xs leading-relaxed",
			},
		},
		defaultVariants: {
			size: "default",
		},
	});

	let {
		ref = $bindable(null),
		class: className,
		size = "default",
		children,
		...restProps
	}: WithElementRef<HTMLAttributes<HTMLParagraphElement>> & {
		size?: VariantProps<typeof fieldDescriptionVariants>["size"];
	} = $props();
</script>

<p
	bind:this={ref}
	data-slot="field-description"
	class={cn(
		fieldDescriptionVariants({ size }),
		"last:mt-0 nth-last-2:-mt-1",
		"[&>a:hover]:text-primary [&>a]:underline [&>a]:underline-offset-4",
		className
	)}
	{...restProps}
>
	{@render children?.()}
</p>
