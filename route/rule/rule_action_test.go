package rule

import (
	"testing"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"

	"github.com/stretchr/testify/require"
)

func TestRuleActionFairShare(t *testing.T) {
	t.Parallel()

	logger := log.NewNOPFactory().NewLogger("router")
	action, err := NewRuleAction(logger, option.RuleAction{
		Action: C.RuleActionTypeRouteOptions,
		RouteOptionsOptions: option.RouteOptionsActionOptions{
			FairShare: &option.FairShareOptions{DownMbps: 100},
		},
	})
	require.NoError(t, err)
	routeOptions := action.(*RuleActionRouteOptions)
	require.NotNil(t, routeOptions.FairShare)
	require.Equal(t, "route-options(fair-share)", routeOptions.String())

	_, err = NewRuleAction(logger, option.RuleAction{
		Action: C.RuleActionTypeRoute,
		RouteOptions: option.RouteActionOptions{
			Outbound: "direct",
			RawRouteOptionsActionOptions: option.RawRouteOptionsActionOptions{
				FairShare: &option.FairShareOptions{},
			},
		},
	})
	require.ErrorContains(t, err, "fair_share")
}
