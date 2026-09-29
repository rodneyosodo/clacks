package crypto

import (
	"bytes"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	k, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	aad := AAD("id1", "host1", "opencode", 0)
	nonce, ct, err := Encrypt(k, aad, []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	pt, err := Decrypt(k, aad, nonce, ct)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pt, []byte("hello")) {
		t.Fatalf("round trip mismatch: %q", pt)
	}
}

func TestWrongKeyRejected(t *testing.T) {
	k1, _ := NewKey()
	k2, _ := NewKey()
	aad := AAD("id1", "host1", "opencode", 3)
	nonce, ct, err := Encrypt(k1, aad, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decrypt(k2, aad, nonce, ct); err == nil {
		t.Fatal("expected decryption with wrong key to fail")
	}
}

func TestWrongAADRejected(t *testing.T) {
	k, _ := NewKey()
	nonce, ct, err := Encrypt(k, AAD("a", "h", "t", 0), []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decrypt(k, AAD("b", "h", "t", 0), nonce, ct); err == nil {
		t.Fatal("expected AAD mismatch to fail")
	}
}

func TestMnemonicRoundTrip(t *testing.T) {
	k, _ := NewKey()
	m, err := ToMnemonic(k)
	if err != nil {
		t.Fatal(err)
	}
	k2, err := KeyFromMnemonic(m)
	if err != nil {
		t.Fatal(err)
	}
	if k != k2 {
		t.Fatal("mnemonic round trip mismatch")
	}
	if _, err := KeyFromMnemonic("not a valid mnemonic phrase at all xyz"); err == nil {
		t.Fatal("expected bad mnemonic to fail")
	}
}
