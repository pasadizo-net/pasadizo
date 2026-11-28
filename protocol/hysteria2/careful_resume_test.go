package hysteria2

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pasadizo-net/pasadizo/option"
	"github.com/sagernet/sing/common/json/badoption"

	"github.com/stretchr/testify/require"
)

func TestNewCarefulResumeStoreOff(t *testing.T) {
	t.Parallel()
	for _, options := range []*option.Hysteria2CarefulResumeOptions{nil, {}, {Lifetime: badoption.Duration(time.Hour)}} {
		store, err := newCarefulResumeStore(context.Background(), options)
		require.NoError(t, err)
		require.Nil(t, store)
	}
}

func TestNewCarefulResumeStore(t *testing.T) {
	t.Parallel()
	store, err := newCarefulResumeStore(context.Background(), &option.Hysteria2CarefulResumeOptions{Enabled: true})
	require.NoError(t, err)
	require.NotNil(t, store)
	require.NoError(t, store.Close())

	_, err = newCarefulResumeStore(context.Background(), &option.Hysteria2CarefulResumeOptions{
		Enabled:  true,
		Lifetime: badoption.Duration(-time.Second),
	})
	require.Error(t, err)
}

func TestNewCarefulResumeStoreStateFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "careful-resume.json")
	store, err := newCarefulResumeStore(context.Background(), &option.Hysteria2CarefulResumeOptions{
		Enabled:   true,
		StateFile: path,
	})
	require.NoError(t, err)
	store.Flush() // a change, so that Close writes the file
	require.NoError(t, store.Close())
	_, err = os.Stat(path)
	require.NoError(t, err)

	// A state file that cannot be read stops the start.
	_, err = newCarefulResumeStore(context.Background(), &option.Hysteria2CarefulResumeOptions{
		Enabled:   true,
		StateFile: t.TempDir(),
	})
	require.Error(t, err)
}
