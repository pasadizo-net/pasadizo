package concealedauth

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeExporter stands in for a TLS session: HMAC-free, but it depends on the
// session secret, the label and the context, as a real exporter does.
func fakeExporter(secret string) Exporter {
	return func(label string, context []byte, length int) ([]byte, error) {
		var output []byte
		for counter := byte(0); len(output) < length; counter++ {
			digest := sha256.Sum256(append(append([]byte(secret+label), context...), counter))
			output = append(output, digest[:]...)
		}
		return output[:length], nil
	}
}

func TestSignedContentMatchesRFC(t *testing.T) {
	// RFC 9729, Figure 3, as corrected by verified erratum 8807: the printed
	// hex spells "HTTP Signature Authentication", the draft's old name.
	expected, err := hex.DecodeString(strings.Repeat("20", 64) +
		"4854545020436F6E6365616C65642041757468656E7469636174696F6E" + "00" +
		strings.Repeat("01", 32))
	require.NoError(t, err)
	require.Equal(t, expected, signedContent(bytes.Repeat([]byte{1}, ExporterLength)))
}

func TestExporterContext(t *testing.T) {
	context := exporterContext(SignatureSchemeEd25519, []byte("basement"), bytes.Repeat([]byte{7}, 32), Origin{Scheme: "https", Host: "example.com", Port: 443}, "")
	expected := []byte{0x08, 0x07, 8}
	expected = append(expected, "basement"...)
	expected = append(expected, 32)
	expected = append(expected, bytes.Repeat([]byte{7}, 32)...)
	expected = append(expected, 5)
	expected = append(expected, "https"...)
	expected = append(expected, 11)
	expected = append(expected, "example.com"...)
	expected = append(expected, 0x01, 0xbb, 0)
	require.Equal(t, expected, context)
	// A length of 64 or more takes the two-byte form.
	require.Equal(t, []byte{0x40, 64}, appendLengthPrefixed(nil, make([]byte, 64))[:2])
}

func TestParseAuthorizationRFCExample(t *testing.T) {
	// RFC 9729, Figure 5, unwrapped.
	credentials, valid := ParseAuthorization("Concealed k=YmFzZW1lbnQ, a=VGhpcyBpcyBh-HB1YmxpYyBrZXkgaW4gdXNl_GhlcmU, s=2055, v=dmVyaWZpY2F0aW9u_zE2Qg, p=QzpcV2luZG93c_xTeXN0ZW0zMlxkcml2ZXJz-ENyb3dkU3RyaWtlXEMtMDAwMDAwMDAyOTEtMD-wMC0w_DAwLnN5cw")
	require.True(t, valid)
	require.Equal(t, []byte("basement"), credentials.KeyID)
	require.Equal(t, SignatureSchemeEd25519, credentials.SignatureScheme)
	require.Empty(t, credentials.Realm)
}

func TestParseAuthorizationRejects(t *testing.T) {
	const valid = "k=YQ, a=YQ, s=2055, v=YQ, p=YQ"
	for _, value := range []string{
		"Basic dXNlcjpwYXNz",
		"Concealed",
		"Concealed a=YQ, s=2055, v=YQ, p=YQ",             // no k
		"Concealed k=YQ, a=YQ, v=YQ, p=YQ",               // no s
		"Concealed k=YQ==, a=YQ, s=2055, v=YQ, p=YQ",     // padding
		"Concealed k=Y+Q, a=YQ, s=2055, v=YQ, p=YQ",      // not base64url
		"Concealed k=YQ, a=YQ, s=02055, v=YQ, p=YQ",      // leading zero
		"Concealed k=YQ, a=YQ, s=65536, v=YQ, p=YQ",      // out of range
		"Concealed k=YQ, k=YQ, a=YQ, s=2055, v=YQ, p=YQ", // duplicate
		"Concealed " + valid + `, realm="open`,           // unterminated
	} {
		_, parsed := ParseAuthorization(value)
		require.False(t, parsed, value)
	}
	credentials, parsed := ParseAuthorization("concealed " + valid + `, Realm="a \"b\", c", extra=1`)
	require.True(t, parsed)
	require.Equal(t, `a "b", c`, credentials.Realm)
}

func newKeys(t *testing.T) (ed25519Private string, ed25519Public string, p256Private string, p256Public string) {
	_, edKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	edDER, err := x509.MarshalPKCS8PrivateKey(edKey)
	require.NoError(t, err)
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	ecDER, err := x509.MarshalPKCS8PrivateKey(ecKey)
	require.NoError(t, err)
	ecPublicDER, err := x509.MarshalPKIXPublicKey(&ecKey.PublicKey)
	require.NoError(t, err)
	// One of each accepted form: base64 PKCS #8, a raw base64url key, PEM.
	return base64.StdEncoding.EncodeToString(edDER),
		base64.RawURLEncoding.EncodeToString(edKey.Public().(ed25519.PublicKey)),
		string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: ecDER})),
		string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: ecPublicDER}))
}

func TestSignAndVerify(t *testing.T) {
	edPrivate, edPublic, ecPrivate, ecPublic := newKeys(t)
	verifier, err := NewVerifier([]User{
		{Name: "ed", KeyID: "ed-key", PublicKey: edPublic},
		{Name: "ec", KeyID: "ec-key", PublicKey: ecPublic},
	})
	require.NoError(t, err)
	origin := Origin{Scheme: "https", Host: "proxy.example", Port: 443}
	for _, testCase := range []struct{ name, keyID, privateKey, realm string }{
		{"ed", "ed-key", edPrivate, ""},
		{"ec", "ec-key", ecPrivate, "house"},
	} {
		signer, err := NewSigner(testCase.keyID, testCase.privateKey, testCase.realm)
		require.NoError(t, err)
		header, err := signer.Authorization(fakeExporter("session-1"), origin)
		require.NoError(t, err)
		credentials, valid := ParseAuthorization(header)
		require.True(t, valid, header)

		user, err := verifier.Verify(credentials, origin, fakeExporter("session-1"))
		require.NoError(t, err)
		require.Equal(t, testCase.name, user)

		// Another session, another origin: the same header proves nothing.
		_, err = verifier.Verify(credentials, origin, fakeExporter("session-2"))
		require.Error(t, err)
		_, err = verifier.Verify(credentials, Origin{Scheme: "https", Host: "proxy.example", Port: 8443}, fakeExporter("session-1"))
		require.Error(t, err)
	}
}

func TestVerifyRejectsWrongKey(t *testing.T) {
	_, edPublic, _, _ := newKeys(t)
	otherPrivate, _, _, _ := newKeys(t)
	verifier, err := NewVerifier([]User{{KeyID: "parham", PublicKey: edPublic}})
	require.NoError(t, err)
	origin := Origin{Scheme: "https", Host: "proxy.example", Port: 443}
	signer, err := NewSigner("parham", otherPrivate, "")
	require.NoError(t, err)
	header, err := signer.Authorization(fakeExporter("session"), origin)
	require.NoError(t, err)
	credentials, valid := ParseAuthorization(header)
	require.True(t, valid)
	_, err = verifier.Verify(credentials, origin, fakeExporter("session"))
	require.ErrorContains(t, err, "public key mismatch")

	// The right public key in a, but a signature from another key.
	credentials.PublicKey, err = base64.RawURLEncoding.DecodeString(edPublic)
	require.NoError(t, err)
	_, err = verifier.Verify(credentials, origin, fakeExporter("session"))
	require.Error(t, err)

	credentials.KeyID = []byte("nobody")
	_, err = verifier.Verify(credentials, origin, fakeExporter("session"))
	require.ErrorContains(t, err, "unknown key ID")
}

func TestExportHeader(t *testing.T) {
	output := bytes.Repeat([]byte{0xfe}, ExporterLength)
	parsed, valid := ParseExportHeader(FormatExportHeader(output))
	require.True(t, valid)
	require.Equal(t, output, parsed)
	_, valid = ParseExportHeader(":" + base64.StdEncoding.EncodeToString(output[:47]) + ":")
	require.False(t, valid)
	_, valid = ParseExportHeader(base64.StdEncoding.EncodeToString(output))
	require.False(t, valid)
}
