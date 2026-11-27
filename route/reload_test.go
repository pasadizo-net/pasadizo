package route

import (
	"testing"

	C "github.com/pasadizo-net/pasadizo/constant"
	"github.com/pasadizo-net/pasadizo/option"

	"github.com/stretchr/testify/require"
)

func defaultRule(edit func(rule *option.DefaultRule)) option.Rule {
	rule := option.Rule{Type: C.RuleTypeDefault}
	edit(&rule.DefaultOptions)
	return rule
}

func defaultDNSRule(edit func(rule *option.DefaultDNSRule)) option.DNSRule {
	rule := option.DNSRule{Type: C.RuleTypeDefault}
	edit(&rule.DefaultOptions)
	return rule
}

func TestRulesReloadable(t *testing.T) {
	t.Parallel()
	domain := defaultRule(func(rule *option.DefaultRule) { rule.DomainSuffix = []string{"example.org"} })
	otherDomain := defaultRule(func(rule *option.DefaultRule) { rule.DomainSuffix = []string{"example.com"} })
	withSet := defaultRule(func(rule *option.DefaultRule) { rule.RuleSet = []string{"geoip-ir"} })
	withOtherSet := defaultRule(func(rule *option.DefaultRule) { rule.RuleSet = []string{"geosite-ads"} })
	process := defaultRule(func(rule *option.DefaultRule) { rule.ProcessName = []string{"curl"} })
	logical := option.Rule{Type: C.RuleTypeLogical}
	logical.LogicalOptions.Rules = []option.Rule{withOtherSet}
	dnsDomain := defaultDNSRule(func(rule *option.DefaultDNSRule) { rule.DomainSuffix = []string{"example.org"} })
	dnsWithSet := defaultDNSRule(func(rule *option.DefaultDNSRule) { rule.RuleSet = []string{"geoip-ir"} })
	dnsWIFI := defaultDNSRule(func(rule *option.DefaultDNSRule) { rule.WIFISSID = []string{"home"} })

	// other matchers, another order, fewer rules
	require.NoError(t, RulesReloadable([]option.Rule{domain, withSet}, []option.Rule{withSet, otherDomain}, nil, nil))
	require.NoError(t, RulesReloadable([]option.Rule{domain, withSet}, []option.Rule{domain}, []option.DNSRule{dnsDomain}, nil))
	// a rule-set that a route rule holds may be used by a DNS rule
	require.NoError(t, RulesReloadable([]option.Rule{withSet}, []option.Rule{withSet}, nil, []option.DNSRule{dnsWithSet}))

	// a rule-set that no rule held has no content any more
	require.ErrorContains(t, RulesReloadable([]option.Rule{withSet}, []option.Rule{withSet, withOtherSet}, nil, nil), "geosite-ads")
	require.ErrorContains(t, RulesReloadable([]option.Rule{withSet}, []option.Rule{withSet, logical}, nil, nil), "geosite-ads")
	require.ErrorContains(t, RulesReloadable(nil, nil, nil, []option.DNSRule{dnsWithSet}), "geoip-ir")
	// what the instance decided at start from the rules
	require.ErrorContains(t, RulesReloadable([]option.Rule{domain}, []option.Rule{domain, process}, nil, nil), "process")
	require.ErrorContains(t, RulesReloadable([]option.Rule{process}, []option.Rule{domain}, nil, nil), "process")
	require.ErrorContains(t, RulesReloadable(nil, nil, []option.DNSRule{dnsDomain}, []option.DNSRule{dnsWIFI}), "Wi-Fi")
}
