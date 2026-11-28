// Package concealedauth implements the Concealed HTTP authentication scheme of
// RFC 9729: the client signs a value exported from the TLS session, so a proof
// is bound to one connection and the server needs to send no challenge.
package concealedauth

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"strconv"
	"strings"

	E "github.com/sagernet/sing/common/exceptions"
)

const (
	// Scheme is the authentication scheme name (RFC 9729, Section 9.1).
	Scheme = "Concealed"
	// ExporterLabel is the TLS exporter label (RFC 9729, Section 3).
	ExporterLabel = "EXPORTER-HTTP-Concealed-Authentication"
	// ExporterLength is the length of the key exporter output (RFC 9729, Section 3.2).
	ExporterLength = 48
	// ExportHeader carries the key exporter output from a frontend to a backend (RFC 9729, Section 6.2).
	ExportHeader = "Concealed-Auth-Export"

	// SignatureSchemeECDSAP256 is ecdsa_secp256r1_sha256 from the TLS SignatureScheme registry.
	SignatureSchemeECDSAP256 uint16 = 0x0403
	// SignatureSchemeEd25519 is ed25519 from the TLS SignatureScheme registry.
	SignatureSchemeEd25519 uint16 = 0x0807

	signatureInputLength = 32
	// The prose of Section 3.3; verified erratum 8807 corrects Figure 3 to match.
	signatureContext = "HTTP Concealed Authentication"
)

// Exporter returns TLS keying material, as tls.ConnectionState.ExportKeyingMaterial does.
type Exporter func(label string, context []byte, length int) ([]byte, error)

// Origin is the scheme, host and port of the request's target URI (RFC 9729, Section 3.1).
type Origin struct {
	Scheme string
	Host   string
	Port   uint16
}

// Credentials are the parsed parameters of a Concealed Authorization header field.
type Credentials struct {
	KeyID           []byte
	PublicKey       []byte
	Proof           []byte
	SignatureScheme uint16
	Verification    []byte
	Realm           string
}

type publicKey struct {
	scheme  uint16
	encoded []byte
	verify  func(message []byte, signature []byte) bool
}

// ParsePublicKey reads a public key as PEM ("PUBLIC KEY"), as base64 of its DER
// SubjectPublicKeyInfo, or as base64 of the RFC 9729 encoding (32 bytes for
// Ed25519, a 65-byte uncompressed point for P-256). Standard and URL-safe
// base64 are accepted, with or without padding.
func ParsePublicKey(content string) (uint16, []byte, error) {
	key, err := parsePublicKey(content)
	if err != nil {
		return 0, nil, err
	}
	return key.scheme, key.encoded, nil
}

func parsePublicKey(content string) (*publicKey, error) {
	content = strings.TrimSpace(content)
	var der []byte
	if block, _ := pem.Decode([]byte(content)); block != nil {
		if block.Type != "PUBLIC KEY" {
			return nil, E.New("unexpected PEM block: ", block.Type)
		}
		der = block.Bytes
	} else {
		decoded, err := decodeBase64(content)
		if err != nil {
			return nil, E.Cause(err, "decode public key")
		}
		switch {
		case len(decoded) == ed25519.PublicKeySize:
			return newEd25519PublicKey(ed25519.PublicKey(decoded)), nil
		case len(decoded) == 65 && decoded[0] == 4:
			ecdsaKey, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), decoded)
			if err != nil {
				return nil, E.Cause(err, "parse P-256 public key")
			}
			return newECDSAPublicKey(ecdsaKey)
		}
		der = decoded
	}
	parsed, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, E.Cause(err, "parse public key")
	}
	switch key := parsed.(type) {
	case ed25519.PublicKey:
		return newEd25519PublicKey(key), nil
	case *ecdsa.PublicKey:
		return newECDSAPublicKey(key)
	default:
		return nil, E.New("unsupported public key type: only Ed25519 and ECDSA P-256 are supported")
	}
}

func newEd25519PublicKey(key ed25519.PublicKey) *publicKey {
	return &publicKey{
		scheme:  SignatureSchemeEd25519,
		encoded: bytes.Clone(key),
		verify: func(message []byte, signature []byte) bool {
			return ed25519.Verify(key, message, signature)
		},
	}
}

func newECDSAPublicKey(key *ecdsa.PublicKey) (*publicKey, error) {
	if key.Curve != elliptic.P256() {
		return nil, E.New("unsupported ECDSA curve: only P-256 is supported")
	}
	encoded, err := key.Bytes()
	if err != nil {
		return nil, err
	}
	return &publicKey{
		scheme:  SignatureSchemeECDSAP256,
		encoded: encoded,
		verify: func(message []byte, signature []byte) bool {
			digest := sha256.Sum256(message)
			return ecdsa.VerifyASN1(key, digest[:], signature)
		},
	}, nil
}

// ParsePrivateKey reads a private key as PEM ("PRIVATE KEY" or "EC PRIVATE
// KEY"), as base64 of its DER PKCS #8 form, or as base64 of a 32-byte Ed25519
// seed.
func ParsePrivateKey(content string) (crypto.Signer, error) {
	content = strings.TrimSpace(content)
	var der []byte
	if block, _ := pem.Decode([]byte(content)); block != nil {
		switch block.Type {
		case "PRIVATE KEY":
			der = block.Bytes
		case "EC PRIVATE KEY":
			key, err := x509.ParseECPrivateKey(block.Bytes)
			if err != nil {
				return nil, E.Cause(err, "parse private key")
			}
			return checkPrivateKey(key)
		default:
			return nil, E.New("unexpected PEM block: ", block.Type)
		}
	} else {
		decoded, err := decodeBase64(content)
		if err != nil {
			return nil, E.Cause(err, "decode private key")
		}
		if len(decoded) == ed25519.SeedSize {
			return ed25519.NewKeyFromSeed(decoded), nil
		}
		der = decoded
	}
	parsed, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, E.Cause(err, "parse private key")
	}
	return checkPrivateKey(parsed)
}

func checkPrivateKey(key any) (crypto.Signer, error) {
	switch privateKey := key.(type) {
	case ed25519.PrivateKey:
		return privateKey, nil
	case *ecdsa.PrivateKey:
		if privateKey.Curve != elliptic.P256() {
			return nil, E.New("unsupported ECDSA curve: only P-256 is supported")
		}
		return privateKey, nil
	default:
		return nil, E.New("unsupported private key type: only Ed25519 and ECDSA P-256 are supported")
	}
}

func decodeBase64(content string) ([]byte, error) {
	content = strings.TrimRight(content, "=")
	if strings.ContainsAny(content, "-_") {
		return base64.RawURLEncoding.DecodeString(content)
	}
	return base64.RawStdEncoding.DecodeString(content)
}

// exporterContext builds the key exporter context of RFC 9729, Section 3.1.
func exporterContext(scheme uint16, keyID []byte, publicKey []byte, origin Origin, realm string) []byte {
	context := binary.BigEndian.AppendUint16(nil, scheme)
	context = appendLengthPrefixed(context, keyID)
	context = appendLengthPrefixed(context, publicKey)
	context = appendLengthPrefixed(context, []byte(origin.Scheme))
	context = appendLengthPrefixed(context, []byte(origin.Host))
	context = binary.BigEndian.AppendUint16(context, origin.Port)
	context = appendLengthPrefixed(context, []byte(realm))
	return context
}

// appendLengthPrefixed writes the length as a QUIC variable-length integer in
// the fewest bytes (RFC 9000, Section 16), then the value.
func appendLengthPrefixed(b []byte, value []byte) []byte {
	length := uint64(len(value))
	switch {
	case length < 1<<6:
		b = append(b, byte(length))
	case length < 1<<14:
		b = binary.BigEndian.AppendUint16(b, uint16(length)|0x4000)
	case length < 1<<30:
		b = binary.BigEndian.AppendUint32(b, uint32(length)|0x80000000)
	default:
		b = binary.BigEndian.AppendUint64(b, length|0xc000000000000000)
	}
	return append(b, value...)
}

// signedContent is the content covered by the signature (RFC 9729, Section 3.3).
func signedContent(exporterOutput []byte) []byte {
	content := bytes.Repeat([]byte{0x20}, 64)
	content = append(content, signatureContext...)
	content = append(content, 0)
	return append(content, exporterOutput[:signatureInputLength]...)
}

// Signer produces Concealed Authorization header field values for one key.
type Signer struct {
	keyID      []byte
	scheme     uint16
	publicKey  []byte
	privateKey crypto.Signer
	realm      string
}

// NewSigner returns a Signer for the key ID and private key (see ParsePrivateKey).
// realm is sent in the realm parameter when it is not empty.
func NewSigner(keyID string, privateKey string, realm string) (*Signer, error) {
	if keyID == "" {
		return nil, E.New("missing key ID")
	}
	key, err := ParsePrivateKey(privateKey)
	if err != nil {
		return nil, err
	}
	signer := &Signer{keyID: []byte(keyID), privateKey: key, realm: realm}
	switch publicKey := key.Public().(type) {
	case ed25519.PublicKey:
		signer.scheme = SignatureSchemeEd25519
		signer.publicKey = bytes.Clone(publicKey)
	case *ecdsa.PublicKey:
		signer.scheme = SignatureSchemeECDSAP256
		signer.publicKey, err = publicKey.Bytes()
		if err != nil {
			return nil, err
		}
	}
	return signer, nil
}

// Authorization returns the header field value proving the key on the
// connection that exporter belongs to, for a request to origin.
func (s *Signer) Authorization(exporter Exporter, origin Origin) (string, error) {
	output, err := exporter(ExporterLabel, exporterContext(s.scheme, s.keyID, s.publicKey, origin, s.realm), ExporterLength)
	if err != nil {
		return "", E.Cause(err, "export keying material")
	}
	if len(output) != ExporterLength {
		return "", E.New("unexpected keying material length: ", len(output))
	}
	content := signedContent(output)
	var proof []byte
	switch s.scheme {
	case SignatureSchemeEd25519:
		proof, err = s.privateKey.Sign(nil, content, crypto.Hash(0))
	default:
		digest := sha256.Sum256(content)
		proof, err = s.privateKey.Sign(rand.Reader, digest[:], crypto.SHA256)
	}
	if err != nil {
		return "", E.Cause(err, "sign")
	}
	var builder strings.Builder
	builder.WriteString(Scheme)
	builder.WriteString(" k=")
	builder.WriteString(base64.RawURLEncoding.EncodeToString(s.keyID))
	builder.WriteString(", a=")
	builder.WriteString(base64.RawURLEncoding.EncodeToString(s.publicKey))
	builder.WriteString(", s=")
	builder.WriteString(strconv.FormatUint(uint64(s.scheme), 10))
	builder.WriteString(", v=")
	builder.WriteString(base64.RawURLEncoding.EncodeToString(output[signatureInputLength:]))
	builder.WriteString(", p=")
	builder.WriteString(base64.RawURLEncoding.EncodeToString(proof))
	if s.realm != "" {
		builder.WriteString(`, realm="`)
		builder.WriteString(strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s.realm))
		builder.WriteString(`"`)
	}
	return builder.String(), nil
}

// User is one key the server accepts.
type User struct {
	Name      string
	KeyID     string
	PublicKey string
}

// Verifier checks Concealed Authorization header fields against known keys.
type Verifier struct {
	keys map[string]verifierKey
}

type verifierKey struct {
	name string
	key  *publicKey
}

// NewVerifier returns a Verifier for users. Key IDs must be unique.
func NewVerifier(users []User) (*Verifier, error) {
	verifier := &Verifier{keys: make(map[string]verifierKey, len(users))}
	for index, user := range users {
		if user.KeyID == "" {
			return nil, E.New("user[", index, "]: missing key_id")
		}
		if _, loaded := verifier.keys[user.KeyID]; loaded {
			return nil, E.New("user[", index, "]: duplicate key_id: ", user.KeyID)
		}
		key, err := parsePublicKey(user.PublicKey)
		if err != nil {
			return nil, E.Cause(err, "user[", index, "]")
		}
		name := user.Name
		if name == "" {
			name = user.KeyID
		}
		verifier.keys[user.KeyID] = verifierKey{name: name, key: key}
	}
	return verifier, nil
}

// Verify performs the checks of RFC 9729, Section 6.3 on a parsed header field
// and returns the name of the user it proves. exporter returns the key exporter
// output of the connection the request arrived on.
func (v *Verifier) Verify(credentials *Credentials, origin Origin, exporter Exporter) (string, error) {
	entry, loaded := v.keys[string(credentials.KeyID)]
	if !loaded {
		return "", E.New("unknown key ID")
	}
	key := entry.key
	if credentials.SignatureScheme != key.scheme {
		return "", E.New("signature scheme mismatch")
	}
	if subtle.ConstantTimeCompare(credentials.PublicKey, key.encoded) != 1 {
		return "", E.New("public key mismatch")
	}
	output, err := exporter(ExporterLabel, exporterContext(credentials.SignatureScheme, credentials.KeyID, credentials.PublicKey, origin, credentials.Realm), ExporterLength)
	if err != nil {
		return "", E.Cause(err, "export keying material")
	}
	if len(output) != ExporterLength {
		return "", E.New("unexpected keying material length: ", len(output))
	}
	if subtle.ConstantTimeCompare(credentials.Verification, output[signatureInputLength:]) != 1 {
		return "", E.New("verification mismatch")
	}
	if !key.verify(signedContent(output), credentials.Proof) {
		return "", E.New("bad signature")
	}
	return entry.name, nil
}

// ParseAuthorization parses a Concealed header field value. It reports false
// when the scheme is not Concealed, or when a required parameter is missing
// or does not parse (RFC 9729, Section 6.1), and the field must then be
// treated as absent.
func ParseAuthorization(value string) (*Credentials, bool) {
	schemeName, rest, _ := strings.Cut(strings.TrimLeft(value, " \t"), " ")
	if !strings.EqualFold(schemeName, Scheme) {
		return nil, false
	}
	params, valid := parseAuthParams(rest)
	if !valid {
		return nil, false
	}
	var credentials Credentials
	for _, name := range []string{"k", "a", "p", "v"} {
		raw, loaded := params[name]
		if !loaded || !isByteSequence(raw) {
			return nil, false
		}
		decoded, err := base64.RawURLEncoding.DecodeString(raw)
		if err != nil {
			return nil, false
		}
		switch name {
		case "k":
			credentials.KeyID = decoded
		case "a":
			credentials.PublicKey = decoded
		case "p":
			credentials.Proof = decoded
		case "v":
			credentials.Verification = decoded
		}
	}
	signatureScheme, loaded := params["s"]
	if !loaded || !isInteger(signatureScheme) {
		return nil, false
	}
	parsedScheme, err := strconv.ParseUint(signatureScheme, 10, 16)
	if err != nil {
		return nil, false
	}
	credentials.SignatureScheme = uint16(parsedScheme)
	credentials.Realm = params["realm"]
	return &credentials, true
}

// parseAuthParams parses #auth-param (RFC 9110, Section 11.2): token BWS "="
// BWS ( token / quoted-string ), separated by commas. Names are case-insensitive
// and may appear only once.
func parseAuthParams(input string) (map[string]string, bool) {
	params := make(map[string]string)
	position := 0
	skip := func(characters string) {
		for position < len(input) && strings.IndexByte(characters, input[position]) >= 0 {
			position++
		}
	}
	for {
		skip(" \t,")
		if position == len(input) {
			return params, len(params) > 0
		}
		start := position
		for position < len(input) && isTokenChar(input[position]) {
			position++
		}
		if position == start {
			return nil, false
		}
		name := strings.ToLower(input[start:position])
		skip(" \t")
		if position == len(input) || input[position] != '=' {
			return nil, false
		}
		position++
		skip(" \t")
		var value string
		if position < len(input) && input[position] == '"' {
			position++
			var builder strings.Builder
			closed := false
			for position < len(input) {
				character := input[position]
				position++
				if character == '"' {
					closed = true
					break
				}
				if character == '\\' {
					if position == len(input) {
						return nil, false
					}
					character = input[position]
					position++
				}
				builder.WriteByte(character)
			}
			if !closed {
				return nil, false
			}
			value = builder.String()
		} else {
			start = position
			for position < len(input) && isTokenChar(input[position]) {
				position++
			}
			if position == start {
				return nil, false
			}
			value = input[start:position]
		}
		if _, loaded := params[name]; loaded {
			return nil, false
		}
		params[name] = value
		skip(" \t")
		if position < len(input) && input[position] != ',' {
			return nil, false
		}
	}
}

func isTokenChar(character byte) bool {
	return character > ' ' && character < 0x7f && !strings.ContainsRune(`"(),/:;<=>?@[\]{}`, rune(character))
}

func isByteSequence(value string) bool {
	for index := 0; index < len(value); index++ {
		character := value[index]
		if !(character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '-' || character == '_') {
			return false
		}
	}
	return true
}

func isInteger(value string) bool {
	if value == "" || len(value) > 5 || (len(value) > 1 && value[0] == '0') {
		return false
	}
	for index := 0; index < len(value); index++ {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	return true
}

// ParseExportHeader reads a Concealed-Auth-Export value, a Structured Field
// Byte Sequence (RFC 8941, Section 3.3.5) of the 48-byte key exporter output.
func ParseExportHeader(value string) ([]byte, bool) {
	value = strings.Trim(value, " \t")
	if len(value) < 2 || value[0] != ':' || value[len(value)-1] != ':' {
		return nil, false
	}
	decoded, err := base64.StdEncoding.DecodeString(value[1 : len(value)-1])
	if err != nil || len(decoded) != ExporterLength {
		return nil, false
	}
	return decoded, true
}

// FormatExportHeader writes the key exporter output as a Concealed-Auth-Export value.
func FormatExportHeader(output []byte) string {
	return ":" + base64.StdEncoding.EncodeToString(output) + ":"
}
