package main

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"os"

	"github.com/pasadizo-net/pasadizo/log"
	E "github.com/sagernet/sing/common/exceptions"

	"github.com/spf13/cobra"
)

var commandGenerateConcealedAuthKeyPair = &cobra.Command{
	Use:       "concealed-auth-keypair [ed25519|p256]",
	Short:     "Generate a key pair for Concealed HTTP authentication (RFC 9729)",
	Args:      cobra.MaximumNArgs(1),
	ValidArgs: []string{"ed25519", "p256"},
	Run: func(cmd *cobra.Command, args []string) {
		algorithm := "ed25519"
		if len(args) > 0 {
			algorithm = args[0]
		}
		err := generateConcealedAuthKeyPair(algorithm)
		if err != nil {
			log.Fatal(err)
		}
	},
}

func init() {
	commandGenerate.AddCommand(commandGenerateConcealedAuthKeyPair)
}

// generateConcealedAuthKeyPair prints the private key as base64 PKCS #8 and
// the public key in the encoding of RFC 9729, Section 3.1.1, as sent in the a
// parameter.
func generateConcealedAuthKeyPair(algorithm string) error {
	var (
		privateKey crypto.Signer
		publicKey  []byte
		err        error
	)
	switch algorithm {
	case "ed25519":
		var key ed25519.PrivateKey
		_, key, err = ed25519.GenerateKey(rand.Reader)
		privateKey = key
		publicKey = key.Public().(ed25519.PublicKey)
	case "p256":
		var key *ecdsa.PrivateKey
		key, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err == nil {
			privateKey = key
			publicKey, err = key.PublicKey.Bytes()
		}
	default:
		return E.New("unknown algorithm: ", algorithm)
	}
	if err != nil {
		return err
	}
	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return err
	}
	os.Stdout.WriteString("PrivateKey: " + base64.StdEncoding.EncodeToString(privateKeyDER) + "\n")
	os.Stdout.WriteString("PublicKey: " + base64.RawURLEncoding.EncodeToString(publicKey) + "\n")
	return nil
}
