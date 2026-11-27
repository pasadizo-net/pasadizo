package route

import (
	"slices"

	"github.com/pasadizo-net/pasadizo/adapter"
	C "github.com/pasadizo-net/pasadizo/constant"
	"github.com/pasadizo-net/pasadizo/option"
	R "github.com/pasadizo-net/pasadizo/route/rule"
	E "github.com/sagernet/sing/common/exceptions"
)

// Route and DNS rules can be replaced while the instance runs, so that a
// change of rules does not close every connection the way a restart does.
// The rest of the configuration is not reloaded in place.

// currentRules returns the rules in use: those of the last reload, or the
// ones the router started with.
func (r *Router) currentRules() []adapter.Rule {
	if reloaded := r.reloadedRules.Load(); reloaded != nil {
		return *reloaded
	}
	return r.rules
}

func (r *Router) closeReloadedRules() error {
	if reloaded := r.reloadedRules.Load(); reloaded != nil {
		closeRules(*reloaded)
	}
	return nil
}

func closeRules(rules []adapter.Rule) {
	for _, rule := range rules {
		_ = rule.Close()
	}
}

// PrepareRules builds and starts a new set of route rules beside the running
// one. Nothing changes until commit is called; abort drops the new rules.
// Connections that are open keep the outbound they were routed to.
func (r *Router) PrepareRules(rules []option.Rule) (commit func(), abort func(), err error) {
	newRules := make([]adapter.Rule, 0, len(rules))
	for i, options := range rules {
		err = R.ValidateNoNestedRuleActions(options)
		if err != nil {
			closeRules(newRules)
			return nil, nil, E.Cause(err, "parse rule[", i, "]")
		}
		var rule adapter.Rule
		rule, err = R.NewRule(r.ctx, r.logger, options, true)
		if err != nil {
			closeRules(newRules)
			return nil, nil, E.Cause(err, "parse rule[", i, "]")
		}
		newRules = append(newRules, rule)
	}
	err = r.validateRuleActions(newRules)
	if err != nil {
		closeRules(newRules)
		return nil, nil, err
	}
	for i, rule := range newRules {
		err = rule.Start()
		if err != nil {
			closeRules(newRules)
			return nil, nil, E.Cause(err, "initialize rule[", i, "]")
		}
	}
	commit = func() {
		oldRules := r.currentRules()
		r.reloadedRules.Store(&newRules)
		// the new rules hold their rule-sets by now, so releasing the old
		// ones cannot leave a rule-set that is still used without a holder
		closeRules(oldRules)
	}
	abort = func() {
		closeRules(newRules)
	}
	return commit, abort, nil
}

// RulesReloadable reports why a change from the current rules to the next
// ones needs a restart, or nil when they can be replaced in place.
//
// Three things are decided from the rules when the instance starts and are
// not decided again by a reload: whether processes are looked up, whether
// neighbors are resolved and whether the Wi-Fi state is watched. And a
// rule-set that no rule used at start has been emptied to save memory, so the
// next rules may use only rule-sets that the current ones use.
func RulesReloadable(current []option.Rule, next []option.Rule, currentDNS []option.DNSRule, nextDNS []option.DNSRule) error {
	type need struct {
		name          string
		current, next bool
	}
	for _, it := range []need{
		{"process", hasRule(current, isProcessRule) || hasDNSRule(currentDNS, isProcessDNSRule), hasRule(next, isProcessRule) || hasDNSRule(nextDNS, isProcessDNSRule)},
		{"neighbor", hasRule(current, isNeighborRule) || hasDNSRule(currentDNS, isNeighborDNSRule), hasRule(next, isNeighborRule) || hasDNSRule(nextDNS, isNeighborDNSRule)},
		{"Wi-Fi", hasRule(current, isWIFIRule) || hasDNSRule(currentDNS, isWIFIDNSRule), hasRule(next, isWIFIRule) || hasDNSRule(nextDNS, isWIFIDNSRule)},
	} {
		if it.current != it.next {
			return E.New("the use of ", it.name, " rules changed")
		}
	}
	var currentSets, nextSets []string
	collectRuleSets(current, &currentSets)
	collectDNSRuleSets(currentDNS, &currentSets)
	collectRuleSets(next, &nextSets)
	collectDNSRuleSets(nextDNS, &nextSets)
	for _, tag := range nextSets {
		if !slices.Contains(currentSets, tag) {
			return E.New("rule-set ", tag, " was not in use")
		}
	}
	return nil
}

func collectRuleSets(rules []option.Rule, tags *[]string) {
	for _, rule := range rules {
		switch rule.Type {
		case C.RuleTypeDefault:
			*tags = append(*tags, rule.DefaultOptions.RuleSet...)
		case C.RuleTypeLogical:
			collectRuleSets(rule.LogicalOptions.Rules, tags)
		}
	}
}

func collectDNSRuleSets(rules []option.DNSRule, tags *[]string) {
	for _, rule := range rules {
		switch rule.Type {
		case C.RuleTypeDefault:
			*tags = append(*tags, rule.DefaultOptions.RuleSet...)
		case C.RuleTypeLogical:
			collectDNSRuleSets(rule.LogicalOptions.Rules, tags)
		}
	}
}

// UpdateRules gives the reference manager the rules of a reload, so that the
// outbounds and DNS servers they name count as referenced.
func (m *ReferenceManager) UpdateRules(rules []option.Rule, dnsRules []option.DNSRule) {
	m.rulesAccess.Lock()
	m.rules = rules
	m.dnsRules = dnsRules
	m.rulesAccess.Unlock()
	if m.subscriber != nil {
		m.subscriber.Emit(struct{}{})
	}
}
