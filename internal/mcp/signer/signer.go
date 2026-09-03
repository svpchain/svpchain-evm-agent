// Package signer contains the key parsing helpers shared by the local owner
// registration commands.
package signer

import (
	"encoding/hex"
	"fmt"
	"strings"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/evm/crypto/ethsecp256k1"
	appconfig "github.com/dydxprotocol/v4-chain/protocol/app/config"
)

func init() {
	appconfig.SetAddressPrefixes()
}

// ParsePrivKey decodes a 32-byte eth_secp256k1 private key from a hex string.
func ParsePrivKey(value string) (*ethsecp256k1.PrivKey, error) {
	value = strings.TrimPrefix(strings.TrimSpace(value), "0x")
	bytes, err := hex.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("hex decode: %w", err)
	}
	if len(bytes) != 32 {
		return nil, fmt.Errorf("private key must be 32 bytes (got %d)", len(bytes))
	}
	return &ethsecp256k1.PrivKey{Key: bytes}, nil
}

// DeriveAddress returns the svp bech32 address derived from priv.
func DeriveAddress(priv *ethsecp256k1.PrivKey) string {
	return sdk.AccAddress(priv.PubKey().Address()).String()
}
