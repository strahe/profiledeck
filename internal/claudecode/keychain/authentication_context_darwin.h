#ifndef PROFILEDECK_AUTHENTICATION_CONTEXT_DARWIN_H
#define PROFILEDECK_AUTHENTICATION_CONTEXT_DARWIN_H

#include <CoreFoundation/CoreFoundation.h>

void pd_apply_interaction_policy(CFMutableDictionaryRef query, int allow_interaction);
int pd_query_uses_authentication_context(CFDictionaryRef query);

#endif
