package ethaddr

import "testing"

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		addr    string
		wantErr bool
	}{
		// Canonical EIP-55 test vectors from the spec (https://eips.ethereum.org/EIPS/eip-55).
		{"valid checksum 1", "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed", false},
		{"valid checksum 2", "0xfB6916095ca1df60bB79Ce92cE3Ea74c37c5d359", false},
		{"valid checksum 3", "0xdbF03B407c01E7cD3CBea99509d93f8DDDC8C6FB", false},
		{"valid checksum 4", "0xD1220A0cf47c7B9Be7A2E6BA89F429762e7b9aDb", false},
		{"all lowercase is valid (unchecksummed)", "0x5aaeb6053f3e94c9b9a09f33669435e7ef1beaed", false},
		{"all uppercase body is valid (unchecksummed)", "0x5AAEB6053F3E94C9B9A09F33669435E7EF1BEAED", false},
		{"uppercase 0X prefix is not accepted", "0X5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed", true},
		{"bad checksum (one flipped case)", "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAeD", true},
		{"too short", "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeA", true},
		{"too long", "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAedFF", true},
		{"missing 0x prefix", "5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed", true},
		{"non-hex characters", "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAZZ", true},
		{"empty", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(tt.addr)
			if tt.wantErr && err == nil {
				t.Errorf("Validate(%q) = nil, want an error", tt.addr)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("Validate(%q) = %v, want nil", tt.addr, err)
			}
		})
	}
}
