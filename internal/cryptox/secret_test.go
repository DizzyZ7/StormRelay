package cryptox

import (
	"bytes"
	"testing"
)

func TestBoxRoundTripAndAAD(t *testing.T) {
	box, err := NewBox(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := box.Encrypt([]byte("secret"), []byte("source-1"))
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := box.Decrypt(ciphertext, []byte("source-1"))
	if err != nil {
		t.Fatal(err)
	}
	if string(plaintext) != "secret" {
		t.Fatalf("got %q", plaintext)
	}
	if _, err := box.Decrypt(ciphertext, []byte("source-2")); err == nil {
		t.Fatal("expected AAD mismatch to fail")
	}
}
