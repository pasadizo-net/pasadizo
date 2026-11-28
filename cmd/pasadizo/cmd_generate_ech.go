package main

import (
	"os"

	"github.com/pasadizo-net/pasadizo/common/tls"
	"github.com/pasadizo-net/pasadizo/log"
	E "github.com/sagernet/sing/common/exceptions"

	"github.com/spf13/cobra"
)

var commandGenerateECHKeyPairFormat string

var commandGenerateECHKeyPair = &cobra.Command{
	Use:   "ech-keypair <plain_server_name>",
	Short: "Generate TLS ECH key pair",
	Long: `Generate TLS ECH key pair.

The default format prints the "ECH CONFIGS" block for the client and the
"ECH KEYS" block for the server, which sing-box reads too.

--format rfc9934 prints the files of RFC 9934: first the client's ECHCONFIG
block, then the server's file, a PKCS #8 PRIVATE KEY followed by the same
ECHCONFIG block.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		err := generateECHKeyPair(args[0], commandGenerateECHKeyPairFormat)
		if err != nil {
			log.Fatal(err)
		}
	},
}

func init() {
	commandGenerateECHKeyPair.Flags().StringVar(&commandGenerateECHKeyPairFormat, "format", "sing-box", "output format: sing-box or rfc9934")
	commandGenerate.AddCommand(commandGenerateECHKeyPair)
}

func generateECHKeyPair(serverName string, format string) error {
	var (
		configPem string
		keyPem    string
		err       error
	)
	switch format {
	case "sing-box":
		configPem, keyPem, err = tls.ECHKeygenDefault(serverName)
	case "rfc9934":
		configPem, keyPem, err = tls.ECHKeygenRFC9934(serverName)
	default:
		return E.New("unknown ECH key pair format: ", format)
	}
	if err != nil {
		return err
	}
	os.Stdout.WriteString(configPem)
	os.Stdout.WriteString("\n")
	os.Stdout.WriteString(keyPem)
	return nil
}
