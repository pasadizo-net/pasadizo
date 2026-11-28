package tls

import (
	"bytes"
	"encoding/pem"
	"strings"
	"testing"
)

// The example file of RFC 9934, Figure 1.
const rfc9934Example = `-----BEGIN PRIVATE KEY-----
MC4CAQAwBQYDK2VuBCIEICjd4yGRdsoP9gU7YT7My8DHx1Tjme8GYDXrOMCi8v1V
-----END PRIVATE KEY-----
-----BEGIN ECHCONFIG-----
AD7+DQA65wAgACA8wVN2BtscOl3vQheUzHeIkVmKIiydUhDCliA4iyQRCwAEAAEA
AQALZXhhbXBsZS5jb20AAA==
-----END ECHCONFIG-----
`

func TestParseRFC9934Example(t *testing.T) {
	t.Parallel()
	keys, err := ParseECHKeysPEM([]byte(rfc9934Example))
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || len(keys[0].PrivateKey) != 32 {
		t.Fatalf("unexpected keys: %+v", keys)
	}
	configPem := rfc9934Example[strings.Index(rfc9934Example, "-----BEGIN ECHCONFIG"):]
	configList, err := ParseECHConfigListPEM([]byte(configPem))
	if err != nil {
		t.Fatal(err)
	}
	configs, err := splitECHConfigList(configList)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(configs[0], keys[0].Config) {
		t.Fatal("the server key does not carry the config of the file")
	}
}

func TestECHKeygenRFC9934RoundTrip(t *testing.T) {
	t.Parallel()
	configPem, keyPem, err := ECHKeygenRFC9934("public.example.org")
	if err != nil {
		t.Fatal(err)
	}
	configList, err := ParseECHConfigListPEM([]byte(configPem))
	if err != nil {
		t.Fatal(err)
	}
	keys, err := ParseECHKeysPEM([]byte(keyPem))
	if err != nil {
		t.Fatal(err)
	}
	configs, err := splitECHConfigList(configList)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || !bytes.Equal(keys[0].Config, configs[0]) {
		t.Fatal("the key file and the config file disagree")
	}
	if !strings.Contains(string(configs[0]), "public.example.org") {
		t.Fatal("public name missing from the config")
	}
}

func TestECHConfigPEMFormatsAreTheSameBytes(t *testing.T) {
	t.Parallel()
	configPem, _, err := ECHKeygenDefault("public.example.org")
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode([]byte(configPem))
	relabeled := pem.EncodeToMemory(&pem.Block{Type: echConfigRFC9934PEMType, Bytes: block.Bytes})
	sing, err := ParseECHConfigListPEM([]byte(configPem))
	if err != nil {
		t.Fatal(err)
	}
	rfc, err := ParseECHConfigListPEM(relabeled)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(sing, rfc) {
		t.Fatal("ECH CONFIGS and ECHCONFIG differ")
	}
}

func TestParseECHConfigListPEMRefusesPrivateKey(t *testing.T) {
	t.Parallel()
	_, keyPem, err := ECHKeygenRFC9934("public.example.org")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseECHConfigListPEM([]byte(keyPem)); err == nil {
		t.Fatal("a client config with the server's private key was accepted")
	}
}

func TestParseECHConfigListPEMRefusesTrailingPrivateKey(t *testing.T) {
	t.Parallel()
	configPem, keyPem, err := ECHKeygenRFC9934("public.example.org")
	if err != nil {
		t.Fatal(err)
	}
	// The whole output of generate ech-keypair --format rfc9934.
	if _, err := ParseECHConfigListPEM([]byte(configPem + keyPem)); err == nil {
		t.Fatal("a client config followed by the server's private key was accepted")
	}
	if _, err := ParseECHConfigListPEM([]byte(configPem + "trailing text\n")); err != nil {
		t.Fatal("content after the ECHConfigList must be ignored: ", err)
	}
}

func TestParseECHKeysPEMRejectsForeignConfig(t *testing.T) {
	t.Parallel()
	_, keyPem, err := ECHKeygenRFC9934("public.example.org")
	if err != nil {
		t.Fatal(err)
	}
	otherConfig, _, err := ECHKeygenRFC9934("public.example.org")
	if err != nil {
		t.Fatal(err)
	}
	privateKey := keyPem[:strings.Index(keyPem, "-----BEGIN ECHCONFIG")]
	if _, err := ParseECHKeysPEM([]byte(privateKey + otherConfig)); err == nil {
		t.Fatal("a config of another key was accepted")
	}
}

func TestParseECHKeysPEMLegacy(t *testing.T) {
	t.Parallel()
	_, keyPem, err := ECHKeygenDefault("public.example.org")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseECHKeysPEM([]byte(keyPem)); err != nil {
		t.Fatal(err)
	}
}
