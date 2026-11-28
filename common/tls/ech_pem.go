package tls

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"

	E "github.com/sagernet/sing/common/exceptions"

	"golang.org/x/crypto/cryptobyte"
)

// PEM labels of ECH keys and configurations. "ECH CONFIGS" and "ECH KEYS" are
// sing-box's own; "PRIVATE KEY" (PKCS #8) followed by "ECHCONFIG" is the file
// of RFC 9934, which OpenSSL and other TLS stacks read too.
const (
	echConfigsPEMType       = "ECH CONFIGS"
	echKeysPEMType          = "ECH KEYS"
	echConfigRFC9934PEMType = "ECHCONFIG"
	privateKeyPEMType       = "PRIVATE KEY"
)

const (
	echConfigVersion       = 0xfe0d
	echKEMX25519HKDFSHA256 = 0x0020
)

// ParseECHConfigListPEM returns the ECHConfigList of an ECH configuration in
// PEM: sing-box's "ECH CONFIGS" block, or the "ECHCONFIG" block of RFC 9934.
// An RFC 9934 file that also holds the private key is refused, because a
// client needs only the public half and must not carry the server's key.
func ParseECHConfigListPEM(content []byte) ([]byte, error) {
	block, rest := pem.Decode(content)
	if block == nil {
		return nil, E.New("invalid ECH configs pem")
	}
	switch block.Type {
	case echConfigsPEMType:
		if len(bytes.TrimSpace(rest)) > 0 {
			return nil, E.New("invalid ECH configs pem: trailing data")
		}
	case echConfigRFC9934PEMType:
		// RFC 9934: content after the ECHConfigList SHOULD be ignored.
	case privateKeyPEMType:
		return nil, E.New("ECH config pem holds a private key: give the client only the ECHCONFIG block")
	default:
		return nil, E.New("invalid ECH configs pem: unexpected block ", block.Type)
	}
	if _, err := splitECHConfigList(block.Bytes); err != nil {
		return nil, E.Cause(err, "parse ECH config list")
	}
	return block.Bytes, nil
}

// ParseECHKeysPEM returns the server keys of an ECH key file in PEM:
// sing-box's "ECH KEYS" block, or an RFC 9934 file, which is a PKCS #8
// X25519 private key followed by the ECHConfigList it serves. Of an RFC 9934
// list, only the configurations that belong to the private key are used.
func ParseECHKeysPEM(content []byte) ([]tls.EncryptedClientHelloKey, error) {
	block, rest := pem.Decode(content)
	if block == nil {
		return nil, E.New("invalid ECH keys pem")
	}
	switch block.Type {
	case echKeysPEMType:
		echKeys, err := UnmarshalECHKeys(block.Bytes)
		if err != nil {
			return nil, E.Cause(err, "parse ECH keys")
		}
		return echKeys, nil
	case privateKeyPEMType:
		return parseRFC9934ECHKeys(block.Bytes, rest)
	default:
		return nil, E.New("invalid ECH keys pem: unexpected block ", block.Type)
	}
}

func parseRFC9934ECHKeys(privateKeyDER []byte, rest []byte) ([]tls.EncryptedClientHelloKey, error) {
	parsedKey, err := x509.ParsePKCS8PrivateKey(privateKeyDER)
	if err != nil {
		return nil, E.Cause(err, "parse ECH private key")
	}
	privateKey, isECDH := parsedKey.(*ecdh.PrivateKey)
	if !isECDH || privateKey.Curve() != ecdh.X25519() {
		return nil, E.New("ECH private key is not X25519")
	}
	configBlock, _ := pem.Decode(rest)
	if configBlock == nil || configBlock.Type != echConfigRFC9934PEMType {
		return nil, E.New("invalid ECH keys pem: missing ECHCONFIG after the private key")
	}
	configs, err := splitECHConfigList(configBlock.Bytes)
	if err != nil {
		return nil, E.Cause(err, "parse ECH config list")
	}
	publicKey := privateKey.PublicKey().Bytes()
	var echKeys []tls.EncryptedClientHelloKey
	for _, config := range configs {
		kemID, configPublicKey, err := echConfigPublicKey(config)
		if err != nil {
			return nil, E.Cause(err, "parse ECH config")
		}
		if kemID != echKEMX25519HKDFSHA256 || !bytes.Equal(configPublicKey, publicKey) {
			continue
		}
		echKeys = append(echKeys, tls.EncryptedClientHelloKey{
			Config:     config,
			PrivateKey: privateKey.Bytes(),
		})
	}
	if len(echKeys) == 0 {
		return nil, E.New("no ECH config in the pem matches its private key")
	}
	return echKeys, nil
}

// splitECHConfigList splits an ECHConfigList into its ECHConfig values, each
// with its version and length, as tls.EncryptedClientHelloKey.Config takes it.
// Configurations of an unknown version are kept: a client skips them.
func splitECHConfigList(list []byte) ([][]byte, error) {
	input := cryptobyte.String(list)
	var configList cryptobyte.String
	if !input.ReadUint16LengthPrefixed(&configList) || !input.Empty() {
		return nil, E.New("malformed ECHConfigList")
	}
	var configs [][]byte
	for !configList.Empty() {
		var (
			version  uint16
			contents cryptobyte.String
		)
		if !configList.ReadUint16(&version) || !configList.ReadUint16LengthPrefixed(&contents) {
			return nil, E.New("malformed ECHConfig")
		}
		builder := cryptobyte.NewBuilder(nil)
		builder.AddUint16(version)
		builder.AddUint16LengthPrefixed(func(builder *cryptobyte.Builder) {
			builder.AddBytes(contents)
		})
		configs = append(configs, builder.BytesOrPanic())
	}
	if len(configs) == 0 {
		return nil, E.New("empty ECHConfigList")
	}
	return configs, nil
}

// echConfigPublicKey returns the KEM and the public key of one ECHConfig.
func echConfigPublicKey(config []byte) (uint16, []byte, error) {
	input := cryptobyte.String(config)
	var (
		version   uint16
		contents  cryptobyte.String
		configID  uint8
		kemID     uint16
		publicKey cryptobyte.String
	)
	if !input.ReadUint16(&version) || !input.ReadUint16LengthPrefixed(&contents) {
		return 0, nil, E.New("malformed ECHConfig")
	}
	if version != echConfigVersion {
		return 0, nil, nil
	}
	if !contents.ReadUint8(&configID) || !contents.ReadUint16(&kemID) || !contents.ReadUint16LengthPrefixed(&publicKey) {
		return 0, nil, E.New("malformed ECHConfig contents")
	}
	return kemID, publicKey, nil
}

// ECHKeygenRFC9934 generates an X25519 ECH key and returns two PEM texts in
// the format of RFC 9934: the server's file (the PKCS #8 private key followed
// by the ECHConfigList) and the client's (the ECHConfigList alone).
func ECHKeygenRFC9934(publicName string) (configPem string, keyPem string, err error) {
	echKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	echConfig, err := marshalECHConfig(0, echKey.PublicKey().Bytes(), publicName, 0)
	if err != nil {
		return "", "", err
	}
	configBuilder := cryptobyte.NewBuilder(nil)
	configBuilder.AddUint16LengthPrefixed(func(builder *cryptobyte.Builder) {
		builder.AddBytes(echConfig)
	})
	configList, err := configBuilder.Bytes()
	if err != nil {
		return "", "", err
	}
	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(echKey)
	if err != nil {
		return "", "", err
	}
	configPem = string(pem.EncodeToMemory(&pem.Block{Type: echConfigRFC9934PEMType, Bytes: configList}))
	keyPem = string(pem.EncodeToMemory(&pem.Block{Type: privateKeyPEMType, Bytes: privateKeyDER})) + configPem
	return configPem, keyPem, nil
}
