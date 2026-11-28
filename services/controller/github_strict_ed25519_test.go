package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"

	"filippo.io/edwards25519"
)

func TestGitHubStrictEd25519Key(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if !githubStrictEd25519Key(public) || !githubStrictEd25519Key(private.Public().(ed25519.PublicKey)) {
		t.Fatal("valid generated key rejected")
	}
	identity := edwards25519.NewIdentityPoint().Bytes()
	torsion := make([]byte, 32)
	low, err := new(edwards25519.Point).SetBytes(torsion)
	if err != nil {
		t.Fatal(err)
	}
	mixed := new(edwards25519.Point).Add(edwards25519.NewGeneratorPoint(), low).Bytes()
	noncanonical := make([]byte, 32)
	for i := range noncanonical {
		noncanonical[i] = 0xff
	}
	noncanonical[0], noncanonical[31] = 0xee, 0x7f
	negativeZero := append([]byte(nil), identity...)
	negativeZero[31] |= 0x80
	for _, key := range [][]byte{nil, make([]byte, 31), make([]byte, 33), identity, torsion, mixed, noncanonical, negativeZero} {
		if githubStrictEd25519Key(key) {
			t.Fatalf("invalid key accepted: %x", key)
		}
	}
}
