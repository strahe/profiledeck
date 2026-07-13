//go:build darwin && cgo

package keychain

import "testing"

func TestNativeDriverQueryContract(t *testing.T) {
	contract := inspectNativeDriverQueryContract()
	if !contract.FindAttributesAndReferenceOnly {
		t.Fatal("multi-result discovery must return attributes and persistent references without password data")
	}
	if !contract.FindWithoutAuthenticationUI {
		t.Fatal("passive discovery must fail instead of showing Keychain authentication UI")
	}
	if !contract.ResolvePersistentReference {
		t.Fatal("persistent references must resolve through kSecMatchItemList and kSecReturnRef")
	}
	if !contract.ResolveWithoutAuthenticationUI {
		t.Fatal("passive reference resolution must fail instead of showing Keychain authentication UI")
	}
	if !contract.ReadExactItemWithData {
		t.Fatal("password data must be read separately through one resolved item reference")
	}
	if !contract.ReadWithoutAuthenticationUI {
		t.Fatal("passive password reads must fail instead of showing Keychain authentication UI")
	}
	if !contract.UpdateExactItemDataOnly {
		t.Fatal("Keychain update must select one item reference and update only its value data")
	}
}
