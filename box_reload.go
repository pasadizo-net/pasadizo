package box

import (
	"bytes"
	"context"

	"github.com/pasadizo-net/pasadizo/option"
	"github.com/pasadizo-net/pasadizo/route"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/json"
)

// RulesOnlyChange reports whether two configurations differ in nothing but
// their route rules and DNS rules, and whether they differ at all.
func RulesOnlyChange(ctx context.Context, current option.Options, next option.Options) (rulesOnly bool, changed bool, err error) {
	currentRest, currentRules, err := splitRules(ctx, current)
	if err != nil {
		return false, false, err
	}
	nextRest, nextRules, err := splitRules(ctx, next)
	if err != nil {
		return false, false, err
	}
	if !bytes.Equal(currentRest, nextRest) {
		return false, true, nil
	}
	return true, !bytes.Equal(currentRules, nextRules), nil
}

// splitRules encodes a configuration without its route and DNS rules, and
// those rules on their own.
func splitRules(ctx context.Context, options option.Options) (rest []byte, rules []byte, err error) {
	routeRules, dnsRules := optionRules(options)
	rules, err = json.MarshalContext(ctx, struct {
		Route []option.Rule    `json:"route"`
		DNS   []option.DNSRule `json:"dns"`
	}{routeRules, dnsRules})
	if err != nil {
		return nil, nil, err
	}
	options.RawMessage = nil
	if options.Route != nil {
		routeOptions := *options.Route
		routeOptions.Rules = nil
		options.Route = &routeOptions
	}
	if options.DNS != nil {
		dnsOptions := *options.DNS
		dnsOptions.Rules = nil
		options.DNS = &dnsOptions
	}
	rest, err = json.MarshalContext(ctx, options)
	return rest, rules, err
}

func optionRules(options option.Options) (routeRules []option.Rule, dnsRules []option.DNSRule) {
	if options.Route != nil {
		routeRules = options.Route.Rules
	}
	if options.DNS != nil {
		dnsRules = options.DNS.Rules
	}
	return
}

// ReloadRules replaces the route rules and the DNS rules of a running
// instance with those of next, without closing its connections. The caller
// has checked with RulesOnlyChange that nothing else differs. It returns an
// error, and changes nothing, when these rules need a restart or are not
// valid; current is the configuration the instance runs.
func (s *Box) ReloadRules(current option.Options, next option.Options) error {
	currentRules, currentDNSRules := optionRules(current)
	nextRules, nextDNSRules := optionRules(next)
	err := route.RulesReloadable(currentRules, nextRules, currentDNSRules, nextDNSRules)
	if err != nil {
		return err
	}
	commitRules, abortRules, err := s.router.PrepareRules(nextRules)
	if err != nil {
		return E.Cause(err, "prepare route rules")
	}
	commitDNSRules, _, err := s.dnsRouter.PrepareRules(nextDNSRules)
	if err != nil {
		abortRules()
		return E.Cause(err, "prepare DNS rules")
	}
	commitRules()
	commitDNSRules()
	s.referenceManager.UpdateRules(nextRules, nextDNSRules)
	return nil
}
