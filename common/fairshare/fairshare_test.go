package fairshare

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sagernet/sing/common/bufio"

	"github.com/stretchr/testify/require"
)

const (
	testRate      = 8 * 1000 * 1000
	testChunk     = 16 * 1024
	testDuration  = 2 * time.Second
	testTolerance = 0.05
)

var (
	sourceA = netip.MustParseAddr("192.168.80.10")
	sourceB = netip.MustParseAddr("192.168.80.11")
	sourceC = netip.MustParseAddr("192.168.80.12")
)

// flood reports chunks for source from workers goroutines until ctx ends and
// returns the bytes released to it before then.
func flood(ctx context.Context, wg *sync.WaitGroup, s *shaper, source netip.Addr, workers int) *atomic.Int64 {
	total := new(atomic.Int64)
	counter := s.counters(source)[0]
	for range workers {
		wg.Go(func() {
			for ctx.Err() == nil {
				counter(testChunk)
				if ctx.Err() == nil {
					total.Add(testChunk)
				}
			}
		})
	}
	return total
}

func run(t *testing.T, sources map[netip.Addr]int) map[netip.Addr]int64 {
	result := make(map[netip.Addr]int64)
	synctest.Test(t, func(t *testing.T) {
		s := newShaper(testRate)
		ctx, cancel := context.WithTimeout(context.Background(), testDuration)
		defer cancel()
		var wg sync.WaitGroup
		totals := make(map[netip.Addr]*atomic.Int64)
		for source, workers := range sources {
			totals[source] = flood(ctx, &wg, s, source, workers)
		}
		wg.Wait()
		for source, total := range totals {
			result[source] = total.Load()
		}
		synctest.Wait()
		s.access.Lock()
		defer s.access.Unlock()
		require.False(t, s.running)
		require.Empty(t, s.sources)
	})
	return result
}

func TestUnlimited(t *testing.T) {
	t.Parallel()
	require.Nil(t, newShaper(0).counters(sourceA))
}

func TestRate(t *testing.T) {
	t.Parallel()
	result := run(t, map[netip.Addr]int{sourceA: 4})
	expected := float64(testRate) * testDuration.Seconds()
	require.InEpsilon(t, expected, float64(result[sourceA]), testTolerance)
}

func TestFairShare(t *testing.T) {
	t.Parallel()
	result := run(t, map[netip.Addr]int{
		sourceA: 32,
		sourceB: 1,
		sourceC: 4,
	})
	expected := float64(testRate) * testDuration.Seconds() / 3
	for source, total := range result {
		require.InEpsilon(t, expected, float64(total), testTolerance, source.String())
	}
}

func TestIdleShareGoesToOthers(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s := newShaper(testRate)
		ctx, cancel := context.WithTimeout(context.Background(), testDuration)
		defer cancel()
		var wg sync.WaitGroup
		bulk := flood(ctx, &wg, s, sourceA, 8)
		light := new(atomic.Int64)
		lightCounter := s.counters(sourceB)[0]
		wg.Go(func() {
			for ctx.Err() == nil {
				lightCounter(1024)
				if ctx.Err() == nil {
					light.Add(1024)
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
		wg.Wait()
		expected := float64(testRate) * testDuration.Seconds()
		require.InEpsilon(t, expected, float64(bulk.Load()+light.Load()), testTolerance)
		require.InEpsilon(t, float64(1024*testDuration/(10*time.Millisecond)), float64(light.Load()), testTolerance)
	})
}

func TestConnCopyIsShaped(t *testing.T) {
	t.Parallel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		io.Copy(io.Discard, conn)
	}()
	conn, err := net.Dial("tcp", listener.Addr().String())
	require.NoError(t, err)
	defer conn.Close()
	limited := NewLimiter(0, testRate).NewConn(conn, sourceA)
	payload := bytes.NewReader(make([]byte, testRate/4))
	start := time.Now()
	_, err = bufio.Copy(limited, payload)
	require.NoError(t, err)
	require.GreaterOrEqual(t, time.Since(start), 200*time.Millisecond)
}
