//go:build !linux

package main

import "github.com/pasadizo-net/pasadizo/option"

func runInUserNamespaceIfNeeded(options option.Options, optionsList []*OptionsEntry) error {
	return nil
}
