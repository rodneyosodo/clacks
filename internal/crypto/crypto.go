package crypto

import (
	"crypto/rand"
	"errors"
	"fmt"

	"github.com/tyler-smith/go-bip39"
	"golang.org/x/crypto/chacha20poly1305"
)

const KeyLen = 32

// NewKey generates a random 32-byte key.
func NewKey() ([32]byte, error) {
	var k [32]byte
	if _, err := rand.Read(k[:]); err != nil {
		return k, err
	}
	return k, nil
}

// ToMnemonic encodes a key as a BIP39 mnemonic (24 words for 256 bits).
func ToMnemonic(key [32]byte) (string, error) {
	return bip39.NewMnemonic(key[:])
}

// KeyFromMnemonic decodes a mnemonic back to a 32-byte key.
func KeyFromMnemonic(mnemonic string) ([32]byte, error) {
	var k [32]byte
	entropy, err := bip39.EntropyFromMnemonic(mnemonic)
	if err != nil {
		return k, err
	}
	if len(entropy) != KeyLen {
		return k, fmt.Errorf("bad entropy length %d", len(entropy))
	}
	copy(k[:], entropy)
	return k, nil
}

// Encrypt encrypts plaintext with AAD, returning nonce and ciphertext.
func Encrypt(key [32]byte, aad, plaintext []byte) (nonce, ciphertext []byte, err error) {
	a, err := chacha20poly1305.NewX(key[:])
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, a.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	ciphertext = a.Seal(nil, nonce, plaintext, aad)
	return nonce, ciphertext, nil
}

// Decrypt verifies AAD and decrypts.
func Decrypt(key [32]byte, aad, nonce, ciphertext []byte) ([]byte, error) {
	a, err := chacha20poly1305.NewX(key[:])
	if err != nil {
		return nil, err
	}
	if len(nonce) != a.NonceSize() {
		return nil, errors.New("bad nonce size")
	}
	return a.Open(nil, nonce, ciphertext, aad)
}

// AAD builds the additional authenticated data for a record:
// id|host|tag|idx (decimal).
func AAD(id, host, tag string, idx uint64) []byte {
	return []byte(fmt.Sprintf("%s|%s|%s|%d", id, host, tag, idx))
}
