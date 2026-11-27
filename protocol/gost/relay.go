package gost

import (
	"context"
	"net"
	"strconv"
	"sync"
	"time"

	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/go-gost/relay"
)

// connectRelay sends the CONNECT request of gost's relay connector over
// stream. A gost relay handler without noDelay sends its answer together with
// the first bytes from the destination, so the answer is read on the first
// Read of the returned conn, as gost's relay connector does. Waiting for it
// here would hang every protocol in which the client speaks first.
func (h *Outbound) connectRelay(ctx context.Context, stream net.Conn, destination M.Socksaddr) (net.Conn, error) {
	request := relay.Request{
		Version: relay.Version1,
		Cmd:     relay.CmdConnect,
	}
	if h.username != "" || h.password != "" {
		request.Features = append(request.Features, &relay.UserAuthFeature{
			Username: h.username,
			Password: h.password,
		})
	}
	address := &relay.AddrFeature{}
	err := address.ParseFrom(destination.String())
	if err != nil {
		return nil, err
	}
	request.Features = append(request.Features, address)

	deadline := time.Now().Add(h.openTimeout)
	if ctxDeadline, loaded := ctx.Deadline(); loaded && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	stream.SetWriteDeadline(deadline)
	_, err = request.WriteTo(stream)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, E.Cause(err, "write relay request")
	}
	stream.SetWriteDeadline(time.Time{})
	return &relayConn{Conn: stream, destination: destination}, nil
}

// relayConn reads the answer of the relay handler before the first byte of
// the destination.
type relayConn struct {
	net.Conn
	destination M.Socksaddr
	once        sync.Once
	err         error
}

func (c *relayConn) Read(p []byte) (int, error) {
	c.once.Do(c.readResponse)
	if c.err != nil {
		return 0, c.err
	}
	return c.Conn.Read(p)
}

func (c *relayConn) readResponse() {
	var response relay.Response
	_, err := response.ReadFrom(c.Conn)
	if err != nil {
		c.err = E.Cause(err, "read relay response")
		return
	}
	if response.Status != relay.StatusOK {
		c.err = E.New("relay CONNECT ", c.destination, ": ", relayStatusText(response.Status))
	}
}

func relayStatusText(status uint8) string {
	switch status {
	case relay.StatusBadRequest:
		return "bad request"
	case relay.StatusUnauthorized:
		return "unauthorized"
	case relay.StatusForbidden:
		return "forbidden"
	case relay.StatusTimeout:
		return "timeout"
	case relay.StatusServiceUnavailable:
		return "service unavailable"
	case relay.StatusHostUnreachable:
		return "host unreachable"
	case relay.StatusNetworkUnreachable:
		return "network unreachable"
	case relay.StatusInternalServerError:
		return "internal server error"
	default:
		return "status " + strconv.Itoa(int(status))
	}
}
