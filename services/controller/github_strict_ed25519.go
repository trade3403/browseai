package main

import (
	"bytes"
	"crypto/ed25519"

	"filippo.io/edwards25519"
)

// Require a canonical, non-identity point in the prime-order subgroup.
// SetBytes alone accepts noncanonical encodings and points with torsion.
// Multiplication by 8 removes torsion; multiplying the result by 8^-1
// modulo the subgroup order reconstructs only the prime-order component.
func githubStrictEd25519Key(key []byte) bool {
	if len(key) != ed25519.PublicKeySize {
		return false
	}
	p, err := new(edwards25519.Point).SetBytes(key)
	if err != nil || !bytes.Equal(p.Bytes(), key) || p.Equal(edwards25519.NewIdentityPoint()) == 1 {
		return false
	}
	var raw [32]byte
	raw[0] = 8
	eight, err := new(edwards25519.Scalar).SetCanonicalBytes(raw[:])
	if err != nil {
		return false
	}
	inverse := new(edwards25519.Scalar).Invert(eight)
	cleared := new(edwards25519.Point).MultByCofactor(p)
	return new(edwards25519.Point).ScalarMult(inverse, cleared).Equal(p) == 1
}
