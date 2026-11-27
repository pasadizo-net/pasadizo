//go:build !generate_completions

package main

import "github.com/pasadizo-net/pasadizo/log"

func main() {
	if err := mainCommand.Execute(); err != nil {
		log.Fatal(err)
	}
}
