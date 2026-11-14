package conf

import (
	"bytes"
	"crypto/ecdh"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"os"
)

// Both supported cores accept the length-prefixed ECH KEYS payload.
// Keep private key material in a file, not in loggable panel configuration.
func (c *CertConfig) ECHKeys() ([]byte, error) {
	if c == nil || c.ECHKeyFile == "" {
		return nil, nil
	}
	content, err := os.ReadFile(c.ECHKeyFile)
	if err != nil {
		return nil, fmt.Errorf("read ECH key file: %w", err)
	}
	block, rest := pem.Decode(content)
	if block == nil || block.Type != "ECH KEYS" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, fmt.Errorf("expected one ECH KEYS PEM block")
	}
	payload := block.Bytes
	remaining := payload
	count := 0
	for len(remaining) > 0 {
		if len(remaining) < 2 {
			return nil, fmt.Errorf("invalid ECH key framing")
		}
		keyLength := int(binary.BigEndian.Uint16(remaining))
		remaining = remaining[2:]
		if keyLength != 32 || len(remaining) < keyLength+2 {
			return nil, fmt.Errorf("invalid ECH X25519 private key")
		}
		privateKey := remaining[:keyLength]
		remaining = remaining[keyLength:]
		configLength := int(binary.BigEndian.Uint16(remaining))
		remaining = remaining[2:]
		if configLength < 4 || len(remaining) < configLength {
			return nil, fmt.Errorf("invalid ECH config framing")
		}
		config := remaining[:configLength]
		if binary.BigEndian.Uint16(config) != 0xfe0d || int(binary.BigEndian.Uint16(config[2:])) != configLength-4 {
			return nil, fmt.Errorf("invalid ECH config version or size")
		}
		if len(config) < 41 || binary.BigEndian.Uint16(config[5:7]) != 0x20 || binary.BigEndian.Uint16(config[7:9]) != 32 {
			return nil, fmt.Errorf("ECH requires an X25519 config")
		}
		key, err := ecdh.X25519().NewPrivateKey(privateKey)
		if err != nil || !bytes.Equal(key.PublicKey().Bytes(), config[9:41]) {
			return nil, fmt.Errorf("ECH private key does not match public config")
		}
		remaining = remaining[configLength:]
		count++
	}
	if count == 0 {
		return nil, fmt.Errorf("empty ECH keys")
	}
	return payload, nil
}
