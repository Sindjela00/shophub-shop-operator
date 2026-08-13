// Package ethaddr validates Ethereum addresses without pulling in a full go-ethereum
// dependency — the operator only needs to check that a wallet's payout address is
// well-formed, not sign or broadcast anything.
package ethaddr

import (
	"encoding/hex"
	"errors"
	"regexp"
	"strings"

	"golang.org/x/crypto/sha3"
)

var hexAddrPattern = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)

// Validate checks that addr is a syntactically valid Ethereum address (0x followed by 40 hex
// characters). If the address uses mixed case, its EIP-55 checksum must also be correct;
// all-lowercase or all-uppercase addresses are valid but unchecksummed, per EIP-55.
func Validate(addr string) error {
	if !hexAddrPattern.MatchString(addr) {
		return errors.New("not a valid Ethereum address: expected 0x followed by 40 hex characters")
	}

	body := addr[2:]
	lower := strings.ToLower(body)
	upper := strings.ToUpper(body)
	if body == lower || body == upper {
		return nil
	}

	if body != checksum(lower) {
		return errors.New("invalid EIP-55 checksum")
	}
	return nil
}

// checksum applies EIP-55: hash the lowercase address, then uppercase each letter whose
// corresponding hash nibble is >= 8.
func checksum(lowerBody string) string {
	hash := sha3.NewLegacyKeccak256()
	hash.Write([]byte(lowerBody))
	hashHex := hex.EncodeToString(hash.Sum(nil))

	var b strings.Builder
	for i, c := range lowerBody {
		if c >= '0' && c <= '9' {
			b.WriteRune(c)
			continue
		}
		if hashHex[i] >= '8' {
			b.WriteRune(c - 32) // ASCII lower-to-upper
		} else {
			b.WriteRune(c)
		}
	}
	return b.String()
}
