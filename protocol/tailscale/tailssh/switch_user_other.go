//go:build with_tailscale && !windows && !linux

package tailssh

import "github.com/pasadizo-net/pasadizo/adapter"

func checkSwitchUserPrivilege(_ *adapter.PlatformUser) error {
	return nil
}
