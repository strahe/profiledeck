//go:build darwin && cgo

package main

/*
#cgo LDFLAGS: -framework CoreFoundation
#include <CoreFoundation/CoreFoundation.h>
#include <stdlib.h>

static char *profiledeckPreferredLanguage(void) {
	CFArrayRef languages = CFLocaleCopyPreferredLanguages();
	if (languages == NULL || CFArrayGetCount(languages) == 0) {
		if (languages != NULL) {
			CFRelease(languages);
		}
		return NULL;
	}
	CFStringRef language = (CFStringRef)CFArrayGetValueAtIndex(languages, 0);
	CFIndex size = CFStringGetMaximumSizeForEncoding(
		CFStringGetLength(language),
		kCFStringEncodingUTF8
	) + 1;
	char *result = malloc((size_t)size);
	if (result == NULL || !CFStringGetCString(language, result, size, kCFStringEncodingUTF8)) {
		free(result);
		result = NULL;
	}
	CFRelease(languages);
	return result;
}
*/
import "C"

import "unsafe"

func systemPreferredTrayLanguage() string {
	value := C.profiledeckPreferredLanguage()
	if value == nil {
		return environmentPreferredTrayLanguage()
	}
	defer C.free(unsafe.Pointer(value))
	return C.GoString(value)
}
