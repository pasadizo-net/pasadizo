package box_test

import (
	"context"
	"testing"

	box "github.com/pasadizo-net/pasadizo"
	"github.com/pasadizo-net/pasadizo/include"
	"github.com/pasadizo-net/pasadizo/option"
	"github.com/sagernet/sing/common/json"

	"github.com/stretchr/testify/require"
)

func reloadTestOptions(t *testing.T, ctx context.Context, level string, suffix string, port string) option.Options {
	t.Helper()
	options, err := json.UnmarshalExtendedContext[option.Options](ctx, []byte(`{
		"log": {"level": "`+level+`"},
		"dns": {
			"servers": [{"type": "local", "tag": "local"}],
			"rules": [{"domain_suffix": ["`+suffix+`"], "server": "local"}]
		},
		"inbounds": [{"type": "mixed", "tag": "in", "listen": "127.0.0.1", "listen_port": 2080}],
		"outbounds": [{"type": "direct", "tag": "direct"}],
		"route": {"rules": [{"port": [`+port+`], "outbound": "direct"}], "final": "direct"}
	}`))
	require.NoError(t, err)
	return options
}

func TestRulesOnlyChange(t *testing.T) {
	t.Parallel()
	ctx := include.Context(context.Background())
	current := reloadTestOptions(t, ctx, "info", "example.org", "80")

	rulesOnly, changed, err := box.RulesOnlyChange(ctx, current, reloadTestOptions(t, ctx, "info", "example.org", "80"))
	require.NoError(t, err)
	require.True(t, rulesOnly)
	require.False(t, changed)

	// a route rule, a DNS rule, or both
	for _, next := range []option.Options{
		reloadTestOptions(t, ctx, "info", "example.org", "443"),
		reloadTestOptions(t, ctx, "info", "example.com", "80"),
		reloadTestOptions(t, ctx, "info", "example.com", "443"),
	} {
		rulesOnly, changed, err = box.RulesOnlyChange(ctx, current, next)
		require.NoError(t, err)
		require.True(t, rulesOnly)
		require.True(t, changed)
	}

	// anything else, with or without a change of rules
	for _, next := range []option.Options{
		reloadTestOptions(t, ctx, "debug", "example.org", "80"),
		reloadTestOptions(t, ctx, "debug", "example.com", "443"),
	} {
		rulesOnly, _, err = box.RulesOnlyChange(ctx, current, next)
		require.NoError(t, err)
		require.False(t, rulesOnly)
	}
}
