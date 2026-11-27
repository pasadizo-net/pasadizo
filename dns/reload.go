package dns

import (
	"slices"

	"github.com/pasadizo-net/pasadizo/adapter"
	"github.com/pasadizo-net/pasadizo/option"
	E "github.com/sagernet/sing/common/exceptions"
)

// PrepareRules builds and starts a new set of DNS rules beside the running
// one. Nothing that answers a query changes until commit is called; abort
// drops the new rules. The commit also clears the DNS cache, since an answer
// cached under the old rules may come from a server the new rules no longer
// choose for that name.
func (r *Router) PrepareRules(rules []option.DNSRule) (commit func(), abort func(), err error) {
	r.rulesAccess.RLock()
	running := r.started && !r.closing
	r.rulesAccess.RUnlock()
	if !running {
		return nil, nil, E.New("DNS router is not running")
	}
	oldRawRules := r.rawRules
	r.rawRules = slices.Clone(rules)
	newRules, legacyDNSMode, _, err := r.buildRules(true)
	if err != nil {
		r.rawRules = oldRawRules
		return nil, nil, err
	}
	commit = func() {
		r.rulesAccess.Lock()
		if r.closing {
			r.rulesAccess.Unlock()
			closeRules(newRules)
			return
		}
		var oldRules []adapter.DNSRule
		oldRules, r.rules = r.rules, newRules
		r.legacyDNSMode = legacyDNSMode
		r.rulesAccess.Unlock()
		closeRules(oldRules)
		r.ClearCache()
	}
	abort = func() {
		r.rawRules = oldRawRules
		closeRules(newRules)
	}
	return commit, abort, nil
}
