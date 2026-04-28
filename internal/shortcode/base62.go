// Package shortcode generates and validates Base-62 short codes.
//
// Alphabet : 0-9 A-Z a-z  (62 characters)
// Length   : 6 characters
// Space    : 62^6 = 56,800,235,584 ≈ 56 billion unique codes
package shortcode

import (
	"crypto/rand"
	"math/big"
	"strings"
)

const (
	alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	codeLen  = 6

	// validChars are accepted in user-supplied custom aliases (base-62 + hyphen/underscore).
	validChars = alphabet + "-_"
)

var bigBase = big.NewInt(62)

// Random returns a cryptographically random 6-character Base-62 code.
func Random() (string, error) {
	buf := make([]byte, codeLen)
	for i := range buf {
		n, err := rand.Int(rand.Reader, bigBase)
		if err != nil {
			return "", err
		}
		buf[i] = alphabet[n.Int64()]
	}
	return string(buf), nil
}

// IsValid reports whether s is a safe custom alias:
// 4–32 characters drawn from [0-9 A-Z a-z - _].
func IsValid(s string) bool {
	if len(s) < 4 || len(s) > 32 {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune(validChars, c) {
			return false
		}
	}
	return true
}
