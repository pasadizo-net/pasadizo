package masque

// settingH3Datagram00 is SETTINGS_H3_DATAGRAM from draft-ietf-masque-h3-datagram-00,
// which Cloudflare WARP still requires next to the RFC 9297 setting.
const settingH3Datagram00 = 0x276

// WARPHTTP3Settings returns the HTTP/3 settings a client must send in addition
// to the standard ones to open a CONNECT-IP tunnel to Cloudflare WARP.
func WARPHTTP3Settings() map[uint64]uint64 {
	return map[uint64]uint64{settingH3Datagram00: 1}
}
