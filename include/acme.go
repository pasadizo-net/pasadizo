//go:build with_acme

package include

import (
	"github.com/pasadizo-net/pasadizo/adapter/certificate"
	"github.com/pasadizo-net/pasadizo/service/acme"
)

func registerACMECertificateProvider(registry *certificate.Registry) {
	acme.RegisterCertificateProvider(registry)
}
