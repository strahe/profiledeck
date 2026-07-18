//go:build darwin && cgo

package keychain

/*
#cgo LDFLAGS: -framework CoreFoundation -framework Security -framework LocalAuthentication
#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>
#include <stdlib.h>
#include "authentication_context_darwin.h"

static CFMutableDictionaryRef pd_dictionary(void) {
	return CFDictionaryCreateMutable(kCFAllocatorDefault, 0,
		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
}

static CFStringRef pd_string(const char *value) {
	return CFStringCreateWithCString(kCFAllocatorDefault, value, kCFStringEncodingUTF8);
}

static CFMutableDictionaryRef pd_find_query(CFStringRef service, CFStringRef account, int allowInteraction) {
	CFMutableDictionaryRef query = pd_dictionary();
	CFDictionarySetValue(query, kSecClass, kSecClassGenericPassword);
	CFDictionarySetValue(query, kSecAttrService, service);
	CFDictionarySetValue(query, kSecAttrAccount, account);
	CFDictionarySetValue(query, kSecAttrSynchronizable, kCFBooleanFalse);
	CFDictionarySetValue(query, kSecMatchLimit, kSecMatchLimitAll);
	CFDictionarySetValue(query, kSecReturnAttributes, kCFBooleanTrue);
	CFDictionarySetValue(query, kSecReturnPersistentRef, kCFBooleanTrue);
	pd_apply_interaction_policy(query, allowInteraction);
	// Password discovery must not combine MatchLimitAll with ReturnData.
	return query;
}

static OSStatus pd_find(const char *service, const char *account, int allowInteraction, CFTypeRef *result) {
	CFStringRef serviceValue = pd_string(service);
	CFStringRef accountValue = pd_string(account);
	CFMutableDictionaryRef query = pd_find_query(serviceValue, accountValue, allowInteraction);
	OSStatus status = SecItemCopyMatching(query, result);
	CFRelease(accountValue);
	CFRelease(serviceValue);
	CFRelease(query);
	return status;
}

static CFIndex pd_array_count(CFTypeRef value) {
	if (value == NULL || CFGetTypeID(value) != CFArrayGetTypeID()) return -1;
	return CFArrayGetCount((CFArrayRef)value);
}

static int pd_is_dictionary(CFTypeRef value) {
	return value != NULL && CFGetTypeID(value) == CFDictionaryGetTypeID();
}

static CFDictionaryRef pd_array_dictionary(CFTypeRef value, CFIndex index) {
	if (pd_array_count(value) <= index || index < 0) return NULL;
	CFTypeRef item = CFArrayGetValueAtIndex((CFArrayRef)value, index);
	if (item == NULL || CFGetTypeID(item) != CFDictionaryGetTypeID()) return NULL;
	return (CFDictionaryRef)item;
}

static CFDataRef pd_dictionary_data(CFDictionaryRef value, CFTypeRef key) {
	if (value == NULL) return NULL;
	CFTypeRef item = CFDictionaryGetValue(value, key);
	if (item == NULL || CFGetTypeID(item) != CFDataGetTypeID()) return NULL;
	return (CFDataRef)item;
}

static CFStringRef pd_dictionary_string(CFDictionaryRef value, CFTypeRef key) {
	if (value == NULL) return NULL;
	CFTypeRef item = CFDictionaryGetValue(value, key);
	if (item == NULL || CFGetTypeID(item) != CFStringGetTypeID()) return NULL;
	return (CFStringRef)item;
}

static CFDataRef pd_persistent_ref(CFDictionaryRef value) {
	return pd_dictionary_data(value, kSecValuePersistentRef);
}

static CFStringRef pd_service(CFDictionaryRef value) {
	return pd_dictionary_string(value, kSecAttrService);
}

static CFStringRef pd_account(CFDictionaryRef value) {
	return pd_dictionary_string(value, kSecAttrAccount);
}

static CFDataRef pd_value_data(CFDictionaryRef value) {
	return pd_dictionary_data(value, kSecValueData);
}

static CFMutableDictionaryRef pd_resolve_query(CFArrayRef persistentList, int allowInteraction) {
	CFMutableDictionaryRef query = pd_dictionary();
	// macOS requires the password class when converting persistent references
	// into SecKeychainItemRef values through kSecMatchItemList.
	CFDictionarySetValue(query, kSecClass, kSecClassGenericPassword);
	CFDictionarySetValue(query, kSecMatchItemList, persistentList);
	CFDictionarySetValue(query, kSecMatchLimit, kSecMatchLimitOne);
	CFDictionarySetValue(query, kSecReturnRef, kCFBooleanTrue);
	pd_apply_interaction_policy(query, allowInteraction);
	return query;
}

static CFTypeRef pd_resolve(CFDataRef persistent, int allowInteraction, OSStatus *status) {
	const void *references[] = { persistent };
	CFArrayRef persistentList = CFArrayCreate(kCFAllocatorDefault, references, 1, &kCFTypeArrayCallBacks);
	CFMutableDictionaryRef query = pd_resolve_query(persistentList, allowInteraction);
	CFTypeRef result = NULL;
	*status = SecItemCopyMatching(query, &result);
	CFRelease(query);
	CFRelease(persistentList);
	if (*status != errSecSuccess) return NULL;
	if (result == NULL) {
		*status = errSecInvalidItemRef;
		return NULL;
	}
	return result;
}

static CFMutableDictionaryRef pd_read_query(CFArrayRef itemList, int allowInteraction) {
	CFMutableDictionaryRef query = pd_dictionary();
	CFDictionarySetValue(query, kSecClass, kSecClassGenericPassword);
	CFDictionarySetValue(query, kSecMatchItemList, itemList);
	CFDictionarySetValue(query, kSecMatchLimit, kSecMatchLimitOne);
	CFDictionarySetValue(query, kSecReturnAttributes, kCFBooleanTrue);
	CFDictionarySetValue(query, kSecReturnData, kCFBooleanTrue);
	// Passive detection must never trigger a Keychain authorization dialog.
	pd_apply_interaction_policy(query, allowInteraction);
	return query;
}

static CFMutableDictionaryRef pd_update_query(CFArrayRef itemList) {
	CFMutableDictionaryRef query = pd_dictionary();
	CFDictionarySetValue(query, kSecClass, kSecClassGenericPassword);
	CFDictionarySetValue(query, kSecMatchItemList, itemList);
	return query;
}

static CFMutableDictionaryRef pd_update_values(CFDataRef data) {
	CFMutableDictionaryRef update = pd_dictionary();
	CFDictionarySetValue(update, kSecValueData, data);
	return update;
}

static OSStatus pd_read(const UInt8 *bytes, CFIndex length, int allowInteraction, CFTypeRef *result) {
	CFDataRef persistent = CFDataCreate(kCFAllocatorDefault, bytes, length);
	OSStatus status = errSecSuccess;
	CFTypeRef item = pd_resolve(persistent, allowInteraction, &status);
	CFRelease(persistent);
	if (status != errSecSuccess) {
		return status;
	}
	const void *items[] = { item };
	CFArrayRef itemList = CFArrayCreate(kCFAllocatorDefault, items, 1, &kCFTypeArrayCallBacks);
	CFMutableDictionaryRef query = pd_read_query(itemList, allowInteraction);
	status = SecItemCopyMatching(query, result);
	CFRelease(query);
	CFRelease(itemList);
	CFRelease(item);
	return status;
}

static OSStatus pd_update(const UInt8 *referenceBytes, CFIndex referenceLength,
	const UInt8 *dataBytes, CFIndex dataLength) {
	CFDataRef persistent = CFDataCreate(kCFAllocatorDefault, referenceBytes, referenceLength);
	OSStatus status = errSecSuccess;
	CFTypeRef item = pd_resolve(persistent, 1, &status);
	CFRelease(persistent);
	if (status != errSecSuccess) return status;
	const void *items[] = { item };
	CFArrayRef itemList = CFArrayCreate(kCFAllocatorDefault, items, 1, &kCFTypeArrayCallBacks);
	CFMutableDictionaryRef query = pd_update_query(itemList);
	CFDataRef data = CFDataCreate(kCFAllocatorDefault, dataBytes, dataLength);
	CFMutableDictionaryRef update = pd_update_values(data);
	status = SecItemUpdate(query, update);
	CFRelease(update);
	CFRelease(data);
	CFRelease(query);
	CFRelease(itemList);
	CFRelease(item);
	return status;
}

static int pd_dictionary_value_is(CFDictionaryRef dictionary, CFTypeRef key, CFTypeRef expected) {
	CFTypeRef value = CFDictionaryGetValue(dictionary, key);
	return value != NULL && CFEqual(value, expected);
}

static int pd_find_query_contract(void) {
	CFStringRef service = pd_string("service");
	CFStringRef account = pd_string("account");
	CFMutableDictionaryRef query = pd_find_query(service, account, 1);
	int valid = CFDictionaryGetCount(query) == 7
		&& pd_dictionary_value_is(query, kSecClass, kSecClassGenericPassword)
		&& pd_dictionary_value_is(query, kSecAttrService, service)
		&& pd_dictionary_value_is(query, kSecAttrAccount, account)
		&& pd_dictionary_value_is(query, kSecAttrSynchronizable, kCFBooleanFalse)
		&& pd_dictionary_value_is(query, kSecMatchLimit, kSecMatchLimitAll)
		&& pd_dictionary_value_is(query, kSecReturnAttributes, kCFBooleanTrue)
		&& pd_dictionary_value_is(query, kSecReturnPersistentRef, kCFBooleanTrue)
		&& !CFDictionaryContainsKey(query, kSecUseAuthenticationContext)
		&& !CFDictionaryContainsKey(query, kSecReturnData);
	CFRelease(query);
	CFRelease(account);
	CFRelease(service);
	return valid;
}

static int pd_passive_find_query_contract(void) {
	CFStringRef service = pd_string("service");
	CFStringRef account = pd_string("account");
	CFMutableDictionaryRef query = pd_find_query(service, account, 0);
	int valid = CFDictionaryGetCount(query) == 8
		&& pd_query_uses_authentication_context(query)
		&& !CFDictionaryContainsKey(query, kSecUseAuthenticationUI)
		&& !CFDictionaryContainsKey(query, kSecReturnData);
	CFRelease(query);
	CFRelease(account);
	CFRelease(service);
	return valid;
}

static int pd_read_query_contract(void) {
	const void *items[] = { kCFNull };
	CFArrayRef itemList = CFArrayCreate(kCFAllocatorDefault, items, 1, &kCFTypeArrayCallBacks);
	CFMutableDictionaryRef query = pd_read_query(itemList, 1);
	int valid = CFDictionaryGetCount(query) == 5
		&& pd_dictionary_value_is(query, kSecClass, kSecClassGenericPassword)
		&& pd_dictionary_value_is(query, kSecMatchItemList, itemList)
		&& pd_dictionary_value_is(query, kSecMatchLimit, kSecMatchLimitOne)
		&& pd_dictionary_value_is(query, kSecReturnAttributes, kCFBooleanTrue)
		&& pd_dictionary_value_is(query, kSecReturnData, kCFBooleanTrue)
		&& !CFDictionaryContainsKey(query, kSecUseAuthenticationContext)
		&& !CFDictionaryContainsKey(query, kSecReturnPersistentRef);
	CFRelease(query);
	CFRelease(itemList);
	return valid;
}

static int pd_passive_read_query_contract(void) {
	const void *items[] = { kCFNull };
	CFArrayRef itemList = CFArrayCreate(kCFAllocatorDefault, items, 1, &kCFTypeArrayCallBacks);
	CFMutableDictionaryRef query = pd_read_query(itemList, 0);
	int valid = CFDictionaryGetCount(query) == 6
		&& pd_dictionary_value_is(query, kSecClass, kSecClassGenericPassword)
		&& pd_dictionary_value_is(query, kSecMatchItemList, itemList)
		&& pd_dictionary_value_is(query, kSecMatchLimit, kSecMatchLimitOne)
		&& pd_dictionary_value_is(query, kSecReturnAttributes, kCFBooleanTrue)
		&& pd_dictionary_value_is(query, kSecReturnData, kCFBooleanTrue)
		&& pd_query_uses_authentication_context(query)
		&& !CFDictionaryContainsKey(query, kSecUseAuthenticationUI);
	CFRelease(query);
	CFRelease(itemList);
	return valid;
}

static int pd_resolve_query_contract(void) {
	const void *items[] = { kCFNull };
	CFArrayRef persistentList = CFArrayCreate(kCFAllocatorDefault, items, 1, &kCFTypeArrayCallBacks);
	CFMutableDictionaryRef query = pd_resolve_query(persistentList, 1);
	int valid = CFDictionaryGetCount(query) == 4
		&& pd_dictionary_value_is(query, kSecClass, kSecClassGenericPassword)
		&& pd_dictionary_value_is(query, kSecMatchItemList, persistentList)
		&& pd_dictionary_value_is(query, kSecMatchLimit, kSecMatchLimitOne)
		&& pd_dictionary_value_is(query, kSecReturnRef, kCFBooleanTrue)
		&& !CFDictionaryContainsKey(query, kSecUseAuthenticationContext)
		&& !CFDictionaryContainsKey(query, kSecReturnData);
	CFRelease(query);
	CFRelease(persistentList);
	return valid;
}

static int pd_passive_resolve_query_contract(void) {
	const void *items[] = { kCFNull };
	CFArrayRef persistentList = CFArrayCreate(kCFAllocatorDefault, items, 1, &kCFTypeArrayCallBacks);
	CFMutableDictionaryRef query = pd_resolve_query(persistentList, 0);
	int valid = CFDictionaryGetCount(query) == 5
		&& pd_query_uses_authentication_context(query)
		&& !CFDictionaryContainsKey(query, kSecUseAuthenticationUI)
		&& !CFDictionaryContainsKey(query, kSecReturnData);
	CFRelease(query);
	CFRelease(persistentList);
	return valid;
}

static int pd_update_query_contract(void) {
	const void *items[] = { kCFNull };
	CFArrayRef itemList = CFArrayCreate(kCFAllocatorDefault, items, 1, &kCFTypeArrayCallBacks);
	CFMutableDictionaryRef query = pd_update_query(itemList);
	const UInt8 byte = 1;
	CFDataRef data = CFDataCreate(kCFAllocatorDefault, &byte, 1);
	CFMutableDictionaryRef update = pd_update_values(data);
	int valid = CFDictionaryGetCount(query) == 2
		&& pd_dictionary_value_is(query, kSecClass, kSecClassGenericPassword)
		&& pd_dictionary_value_is(query, kSecMatchItemList, itemList)
		&& CFDictionaryGetCount(update) == 1
		&& pd_dictionary_value_is(update, kSecValueData, data);
	CFRelease(update);
	CFRelease(data);
	CFRelease(query);
	CFRelease(itemList);
	return valid;
}

static char *pd_copy_string(CFStringRef value) {
	if (value == NULL) return NULL;
	CFIndex length = CFStringGetLength(value);
	CFIndex capacity = CFStringGetMaximumSizeForEncoding(length, kCFStringEncodingUTF8) + 1;
	char *buffer = malloc((size_t)capacity);
	if (buffer == NULL) return NULL;
	if (!CFStringGetCString(value, buffer, capacity, kCFStringEncodingUTF8)) {
		free(buffer);
		return NULL;
	}
	return buffer;
}
*/
import "C"

import (
	"fmt"
	"sync"
	"unsafe"
)

type securityDriver struct{}

// Preserve the driver's existing native call ordering while passive and
// interactive Keychain operations use separate authentication policies.
var driverInteractionMu sync.Mutex

type nativeDriverQueryContract struct {
	FindAttributesAndReferenceOnly bool
	FindWithoutAuthenticationUI    bool
	ResolvePersistentReference     bool
	ResolveWithoutAuthenticationUI bool
	ReadExactItemWithData          bool
	ReadWithoutAuthenticationUI    bool
	UpdateExactItemDataOnly        bool
}

func inspectNativeDriverQueryContract() nativeDriverQueryContract {
	return nativeDriverQueryContract{
		FindAttributesAndReferenceOnly: C.pd_find_query_contract() != 0,
		FindWithoutAuthenticationUI:    C.pd_passive_find_query_contract() != 0,
		ResolvePersistentReference:     C.pd_resolve_query_contract() != 0,
		ResolveWithoutAuthenticationUI: C.pd_passive_resolve_query_contract() != 0,
		ReadExactItemWithData:          C.pd_read_query_contract() != 0,
		ReadWithoutAuthenticationUI:    C.pd_passive_read_query_contract() != 0,
		UpdateExactItemDataOnly:        C.pd_update_query_contract() != 0,
	}
}

func newDriver() Driver { return securityDriver{} }

func (securityDriver) Find(service, account string, allowInteraction bool) ([]Reference, error) {
	driverInteractionMu.Lock()
	defer driverInteractionMu.Unlock()
	cService := C.CString(service)
	cAccount := C.CString(account)
	defer C.free(unsafe.Pointer(cService))
	defer C.free(unsafe.Pointer(cAccount))
	var result C.CFTypeRef
	interaction := C.int(0)
	if allowInteraction {
		interaction = 1
	}
	status := C.pd_find(cService, cAccount, interaction, &result)
	if status == C.errSecItemNotFound {
		return []Reference{}, nil
	}
	if status == C.errSecInteractionNotAllowed || status == C.errSecInteractionRequired || status == C.errSecAuthFailed || status == C.errSecUserCanceled {
		return nil, ErrInteractionRequired
	}
	if status != C.errSecSuccess {
		return nil, statusError("find", status)
	}
	if result == 0 {
		return nil, fmt.Errorf("keychain find returned an empty result")
	}
	defer C.CFRelease(result)
	count := int(C.pd_array_count(result))
	if count < 0 {
		return nil, fmt.Errorf("keychain find returned an unexpected result")
	}
	references := make([]Reference, 0, count)
	for index := 0; index < count; index++ {
		dictionary := C.pd_array_dictionary(result, C.CFIndex(index))
		persistent := copyData(C.pd_persistent_ref(dictionary))
		itemService, okService := copyString(C.pd_service(dictionary))
		itemAccount, okAccount := copyString(C.pd_account(dictionary))
		if len(persistent) == 0 || !okService || !okAccount {
			return nil, fmt.Errorf("keychain find returned incomplete attributes")
		}
		references = append(references, Reference{Persistent: persistent, Service: itemService, Account: itemAccount})
	}
	return references, nil
}

func (securityDriver) Read(persistent []byte, allowInteraction bool) (Item, error) {
	driverInteractionMu.Lock()
	defer driverInteractionMu.Unlock()
	if len(persistent) == 0 {
		return Item{}, ErrNotFound
	}
	var result C.CFTypeRef
	interaction := C.int(0)
	if allowInteraction {
		interaction = 1
	}
	status := C.pd_read((*C.UInt8)(unsafe.Pointer(&persistent[0])), C.CFIndex(len(persistent)), interaction, &result)
	if status == C.errSecItemNotFound || status == C.errSecInvalidItemRef {
		return Item{}, ErrNotFound
	}
	if status == C.errSecInteractionNotAllowed || status == C.errSecInteractionRequired || status == C.errSecAuthFailed || status == C.errSecUserCanceled {
		return Item{}, ErrInteractionRequired
	}
	if status != C.errSecSuccess {
		return Item{}, statusError("read", status)
	}
	if result == 0 {
		return Item{}, fmt.Errorf("keychain read returned an empty result")
	}
	defer C.CFRelease(result)
	if C.pd_is_dictionary(result) == 0 {
		return Item{}, fmt.Errorf("keychain read returned an unexpected result")
	}
	dictionary := C.CFDictionaryRef(result)
	service, okService := copyString(C.pd_service(dictionary))
	account, okAccount := copyString(C.pd_account(dictionary))
	data := copyData(C.pd_value_data(dictionary))
	if !okService || !okAccount || data == nil {
		return Item{}, fmt.Errorf("keychain read returned incomplete attributes")
	}
	return Item{Service: service, Account: account, Data: data}, nil
}

func (securityDriver) Update(persistent, data []byte) error {
	driverInteractionMu.Lock()
	defer driverInteractionMu.Unlock()
	if len(persistent) == 0 {
		return ErrNotFound
	}
	var dataPointer *C.UInt8
	if len(data) != 0 {
		dataPointer = (*C.UInt8)(unsafe.Pointer(&data[0]))
	}
	status := C.pd_update((*C.UInt8)(unsafe.Pointer(&persistent[0])), C.CFIndex(len(persistent)), dataPointer, C.CFIndex(len(data)))
	if status == C.errSecItemNotFound || status == C.errSecInvalidItemRef {
		return ErrNotFound
	}
	if status != C.errSecSuccess {
		return statusError("update", status)
	}
	return nil
}

func copyData(value C.CFDataRef) []byte {
	if value == 0 {
		return nil
	}
	length := C.CFDataGetLength(value)
	if length == 0 {
		return []byte{}
	}
	return C.GoBytes(unsafe.Pointer(C.CFDataGetBytePtr(value)), C.int(length))
}

func copyString(value C.CFStringRef) (string, bool) {
	text := C.pd_copy_string(value)
	if text == nil {
		return "", false
	}
	defer C.free(unsafe.Pointer(text))
	return C.GoString(text), true
}

func statusError(operation string, status C.OSStatus) error {
	return fmt.Errorf("security framework %s failed with status %d", operation, int32(status))
}
