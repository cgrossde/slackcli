//go:build darwin && cgo

package keychain

// Security.framework binding (cgo). Items written here are created by the
// slackcli process itself, so macOS records this binary's designated
// requirement as the item's only trusted application. The signed release
// binary reads them silently across updates; every other program, including
// /usr/bin/security, gets the system password prompt.

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <stdlib.h>
#include <string.h>
#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>

static CFStringRef jc_str(const char *s) {
	return CFStringCreateWithCString(kCFAllocatorDefault, s, kCFStringEncodingUTF8);
}

static CFMutableDictionaryRef jc_query(const char *service, const char *account) {
	CFMutableDictionaryRef q = CFDictionaryCreateMutable(kCFAllocatorDefault, 0,
		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	CFStringRef svc = jc_str(service);
	CFStringRef acct = jc_str(account);
	CFDictionarySetValue(q, kSecClass, kSecClassGenericPassword);
	CFDictionarySetValue(q, kSecAttrService, svc);
	CFDictionarySetValue(q, kSecAttrAccount, acct);
	CFRelease(svc);
	CFRelease(acct);
	return q;
}

// jc_get copies the secret into a malloc'd buffer the caller frees.
static OSStatus jc_get(const char *service, const char *account, void **out, long *outLen) {
	CFMutableDictionaryRef q = jc_query(service, account);
	CFDictionarySetValue(q, kSecReturnData, kCFBooleanTrue);
	CFDictionarySetValue(q, kSecMatchLimit, kSecMatchLimitOne);
	CFTypeRef result = NULL;
	OSStatus st = SecItemCopyMatching(q, &result);
	CFRelease(q);
	if (st != errSecSuccess) {
		return st;
	}
	CFDataRef data = (CFDataRef)result;
	long n = (long)CFDataGetLength(data);
	*out = malloc(n > 0 ? n : 1);
	memcpy(*out, CFDataGetBytePtr(data), n);
	*outLen = n;
	CFRelease(result);
	return errSecSuccess;
}

// jc_set adds the item, or updates its data in place when it already exists.
// An update keeps the item's access list, so ownership never changes hands.
static OSStatus jc_set(const char *service, const char *account, const void *secret, long n) {
	CFDataRef data = CFDataCreate(kCFAllocatorDefault, (const UInt8 *)secret, n);
	CFMutableDictionaryRef q = jc_query(service, account);
	CFMutableDictionaryRef add = CFDictionaryCreateMutableCopy(kCFAllocatorDefault, 0, q);
	CFDictionarySetValue(add, kSecValueData, data);
	OSStatus st = SecItemAdd(add, NULL);
	CFRelease(add);
	if (st == errSecDuplicateItem) {
		CFMutableDictionaryRef upd = CFDictionaryCreateMutable(kCFAllocatorDefault, 0,
			&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
		CFDictionarySetValue(upd, kSecValueData, data);
		st = SecItemUpdate(q, upd);
		CFRelease(upd);
	}
	CFRelease(q);
	CFRelease(data);
	return st;
}

static OSStatus jc_del(const char *service, const char *account) {
	CFMutableDictionaryRef q = jc_query(service, account);
	OSStatus st = SecItemDelete(q);
	CFRelease(q);
	return st;
}

// jc_self_satisfies reports whether the running process's code signature is
// valid and satisfies the given code requirement.
static OSStatus jc_self_satisfies(const char *requirement) {
	CFStringRef text = jc_str(requirement);
	SecRequirementRef req = NULL;
	OSStatus st = SecRequirementCreateWithString(text, kSecCSDefaultFlags, &req);
	CFRelease(text);
	if (st != errSecSuccess) {
		return st;
	}
	SecCodeRef self = NULL;
	st = SecCodeCopySelf(kSecCSDefaultFlags, &self);
	if (st == errSecSuccess) {
		st = SecCodeCheckValidity(self, kSecCSDefaultFlags, req);
		CFRelease(self);
	}
	CFRelease(req);
	return st;
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// errSecItemNotFound is the OSStatus Security.framework returns for a miss.
const errSecItemNotFound = -25300

// frameworkStore keeps secrets as generic-password items created through
// Security.framework by the running binary.
type frameworkStore struct {
	service string
}

func (f frameworkStore) get(account string) (string, error) {
	svc, acct := C.CString(f.service), C.CString(account)
	defer C.free(unsafe.Pointer(svc))
	defer C.free(unsafe.Pointer(acct))

	var buf unsafe.Pointer
	var n C.long
	st := C.jc_get(svc, acct, &buf, &n)
	if err := statusErr("SecItemCopyMatching", st); err != nil {
		return "", err
	}
	defer C.free(buf)
	return C.GoStringN((*C.char)(buf), C.int(n)), nil
}

func (f frameworkStore) set(account, secret string) error {
	svc, acct := C.CString(f.service), C.CString(account)
	defer C.free(unsafe.Pointer(svc))
	defer C.free(unsafe.Pointer(acct))
	data := C.CBytes([]byte(secret))
	defer C.free(data)
	return statusErr("SecItemAdd", C.jc_set(svc, acct, data, C.long(len(secret))))
}

func (f frameworkStore) del(account string) error {
	svc, acct := C.CString(f.service), C.CString(account)
	defer C.free(unsafe.Pointer(svc))
	defer C.free(unsafe.Pointer(acct))
	return statusErr("SecItemDelete", C.jc_del(svc, acct))
}

// selfSatisfies reports whether this binary carries a valid signature that
// satisfies requirement (code-signing requirement language).
func selfSatisfies(requirement string) bool {
	r := C.CString(requirement)
	defer C.free(unsafe.Pointer(r))
	return C.jc_self_satisfies(r) == C.errSecSuccess
}

// frameworkAvailable reports whether this build can bind items to the binary.
const frameworkAvailable = true

func statusErr(op string, st C.OSStatus) error {
	switch st {
	case C.errSecSuccess:
		return nil
	case errSecItemNotFound:
		return ErrNotFound
	}
	msg := C.SecCopyErrorMessageString(st, nil)
	if msg == 0 {
		return fmt.Errorf("%s: OSStatus %d", op, int(st))
	}
	defer C.CFRelease(C.CFTypeRef(msg))
	var text [256]C.char
	C.CFStringGetCString(msg, &text[0], C.CFIndex(len(text)), C.kCFStringEncodingUTF8)
	return fmt.Errorf("%s: %s (OSStatus %d)", op, C.GoString(&text[0]), int(st))
}
