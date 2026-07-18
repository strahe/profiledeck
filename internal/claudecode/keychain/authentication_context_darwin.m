#import <LocalAuthentication/LocalAuthentication.h>
#import <Security/Security.h>

#include "authentication_context_darwin.h"

void pd_apply_interaction_policy(CFMutableDictionaryRef query, int allow_interaction) {
	if (allow_interaction) {
		return;
	}
	LAContext *context = [[LAContext alloc] init];
	// A per-query policy keeps passive reads non-interactive without changing
	// process-wide Keychain behavior for other clients.
	context.interactionNotAllowed = YES;
	CFDictionarySetValue(query, kSecUseAuthenticationContext, context);
	[context release];
}

int pd_query_uses_authentication_context(CFDictionaryRef query) {
	id value = (id)CFDictionaryGetValue(query, kSecUseAuthenticationContext);
	return [value isKindOfClass:[LAContext class]];
}
