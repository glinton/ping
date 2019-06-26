package ping

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

const (
	// ProtocolIPv4ICMP defines ICMP for IPv4.
	ProtocolIPv4ICMP = 1
	// ProtocolIPv6ICMP defines ICMP for IPv6.
	ProtocolIPv6ICMP = 58
)

// A Request represents an icmp echo request to be sent by a client.
type Request struct {
	reqTex  *sync.Mutex // request lock
	sentAt  time.Time   // time at which echo request was sent
	dst     net.Addr    // useable destination address
	proto   int         // icmp protocol (4 or 6)
	network string      // one of 'ip4:icmp', 'ip6:ipv6-icmp', 'udp4', or 'udp6' for privileged/non-privileged datagrams

	ID   int    // icmp.Echo.ID - for differentiating pin. if not set manually, do automatically. an identifier to aid in matching echos and replies, may be zero.
	Seq  int    // Seq is the ICMP sequence number. a sequence number to aid in matching echos and replies, may be zero.
	Data []byte // maybe don't want to do this, too much freedom. if so, limit size. used to set icmp.Echo.Data

	Dst net.IP // The address of the host to which the message should be sent.
	Src net.IP // The address of the host that composes the ICMP message.
}

// Response represents an icmp echo response received by a client.
type Response struct {
	rcvdAt time.Time // time at which echo response was received

	TotalLength int           // Length of internet header and data in octets.
	TTL         int           // Time to live in seconds; as this field is decremented at each machine in which the datagram is processed, the value in this field should be at least as great as the number of gateways which this datagram will traverse. Maximum possible value of this field is 255.
	Src         net.IP        // The address of the host that composed the ICMP message.
	Dst         net.IP        // The address of the host to which the message was received from.
	RTT         time.Duration // RTT is the round-trip time it took to ping.

	ID   int    // icmp.Echo.ID - for differentiating pin. if not set manually, do automatically. an identifier to aid in matching echos and replies, may be zero.
	Seq  uint   // Seq is the ICMP sequence number. a sequence number to aid in matching echos and replies, may be zero.
	Data []byte // maybe don't want to do this, too much freedom. if so, limit size. used to set icmp.Echo.Data

	Req *Request // Req is the request that elicited this response.
}

// Client is a ping client.
type Client struct{}

// Do sends a ping request and returns a ping response.
func (c *Client) Do(ctx context.Context, req Request) (*Response, error) {
	req.reqTex = &sync.Mutex{}

	conn, err := c.listen(&req)
	if err != nil {
		return nil, err
	}

	var addr net.Addr
	if req.isIPv6() {
		addr, err = net.ResolveIPAddr("ip6", req.Dst.String())
	} else {
		addr, err = net.ResolveIPAddr("ip4", req.Dst.String())
	}
	if err != nil {
		return nil, err
	}

	switch req.network {
	case "udp4", "udp6":
		if a, ok := addr.(*net.IPAddr); ok {
			req.dst = &net.UDPAddr{IP: a.IP, Zone: a.Zone}
		}
	}

	var (
		resp    *Response
		readErr error

		wg = &sync.WaitGroup{}
	)

	wg.Add(1)
	go func() {
		defer wg.Done()
		resp, readErr = read(ctx, conn)
		if readErr != nil {
			return
		}

		req.reqTex.Lock()
		resp.Req = &req
		resp.RTT = resp.rcvdAt.Sub(req.sentAt)
		req.reqTex.Unlock()
	}()

	err = send(ctx, conn, &req)
	if err != nil {
		return nil, err
	}

	wg.Wait()
	if readErr != nil {
		return nil, readErr
	}

	return resp, nil
}

// listen tries first to create a privileged datagram-oriented ICMP endpoint then
// attempts to create a non-privileged one. If both fail, it returns an error.
func (c *Client) listen(req *Request) (*icmp.PacketConn, error) {
	req.network = "ip4:icmp"
	req.proto = ProtocolIPv4ICMP

	if req.isIPv6() {
		req.proto = ProtocolIPv6ICMP
		req.network = "ip6:ipv6-icmp"
	}

	srcIP := req.Src.String()
	if srcIP == "<nil>" {
		srcIP = ""
	}

	conn, err := icmp.ListenPacket(req.network, srcIP)
	if err != nil {
		req.network = "udp4"
		if req.isIPv6() {
			req.network = "udp6"
		}

		var err2 error
		conn, err2 = icmp.ListenPacket(req.network, srcIP)
		if err2 != nil {
			return nil, fmt.Errorf("error listening for ICMP packets: %s: %s", err.Error(), err2.Error())
		}
	}

	return conn, nil
}

func (req *Request) isIPv6() bool {
	if p4 := req.Dst.To4(); len(p4) == net.IPv4len {
		return false
	}
	return true
}

func read(ctx context.Context, conn *icmp.PacketConn) (*Response, error) {
	if c4 := conn.IPv4PacketConn(); c4 != nil {
		return read4(ctx, c4)
	}
	c6 := conn.IPv6PacketConn()
	if c6 == nil {
		return nil, errors.New("bad icmp connection type")
	}
	return read6(ctx, c6)
}

func read4(ctx context.Context, conn *ipv4.PacketConn) (*Response, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			conn.SetReadDeadline(time.Now().Add(time.Millisecond * 300))
			bytesReceived := make([]byte, 1500)

			n, cm, src, err := conn.ReadFrom(bytesReceived)
			rcv := time.Now()
			if err != nil {
				if neterr, ok := err.(*net.OpError); ok {
					if neterr.Timeout() {
						continue
					} else {
						return nil, err
					}
				}
				return nil, err
			}

			if n <= 0 {
				continue
			}

			m, err := icmp.ParseMessage(ProtocolIPv4ICMP, bytesReceived[:n])
			if err != nil {
				return nil, err
			}

			if m.Type != ipv4.ICMPTypeEchoReply {
				// Likely an `ICMPTypeDestinationUnreachable`, ignore it.
				continue
			}

			var seq uint
			var id int
			b, ok := m.Body.(*icmp.Echo)
			if ok {
				seq = uint(b.Seq)
				id = b.ID
			}

			var ttl int
			if cm != nil {
				ttl = cm.TTL
			}

			srcHost, _, _ := net.SplitHostPort(src.String())
			dstHost, _, _ := net.SplitHostPort(conn.LocalAddr().String())
			return &Response{
				ID:          id,
				Seq:         seq,
				Data:        bytesReceived[:n],
				TotalLength: n,
				Src:         net.ParseIP(srcHost),
				Dst:         net.ParseIP(dstHost),
				TTL:         ttl,
				rcvdAt:      rcv,
			}, nil
		}
	}
}

func read6(ctx context.Context, conn *ipv6.PacketConn) (*Response, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			conn.SetReadDeadline(time.Now().Add(time.Millisecond * 300))
			bytesReceived := make([]byte, 1500)

			n, cm, src, err := conn.ReadFrom(bytesReceived)
			rcv := time.Now()
			if err != nil {
				if neterr, ok := err.(*net.OpError); ok {
					if neterr.Timeout() {
						continue
					} else {
						return nil, err
					}
				}
				return nil, err
			}

			if n <= 0 {
				continue
			}

			m, err := icmp.ParseMessage(ProtocolIPv6ICMP, bytesReceived[:n])
			if err != nil {
				return nil, err
			}

			if m.Type != ipv6.ICMPTypeEchoReply {
				// Likely an `ICMPTypeDestinationUnreachable`, ignore it.
				continue
			}

			var seq uint
			var id int
			b, ok := m.Body.(*icmp.Echo)
			if ok {
				seq = uint(b.Seq)
				id = b.ID
			}

			var ttl int
			if cm != nil {
				ttl = cm.HopLimit
			}

			srcHost, _, _ := net.SplitHostPort(src.String())
			dstHost, _, _ := net.SplitHostPort(conn.LocalAddr().String())
			return &Response{
				ID:          id,
				Seq:         seq,
				Data:        bytesReceived[:n],
				TotalLength: n,
				Src:         net.ParseIP(srcHost),
				Dst:         net.ParseIP(dstHost),
				TTL:         ttl,
				rcvdAt:      rcv,
			}, nil
		}
	}
}

func (req *Request) data() []byte {
	if len(req.Data) == 0 {
		return bytes.Repeat([]byte{1}, 56)
	}
	return req.Data
}

func send(ctx context.Context, conn *icmp.PacketConn, req *Request) error {
	select {
	case <-ctx.Done():
		return nil
	default:
		body := &icmp.Echo{
			ID:   req.ID,
			Seq:  int(req.Seq),
			Data: req.data(),
		}

		msg := &icmp.Message{
			Type: ipv6.ICMPTypeEchoRequest,
			Code: 0,
			Body: body,
		}

		if req.proto == ProtocolIPv4ICMP {
			msg.Type = ipv4.ICMPTypeEcho
			conn.IPv4PacketConn().SetControlMessage(ipv4.FlagTTL, true)
		} else {
			conn.IPv6PacketConn().SetControlMessage(ipv6.FlagHopLimit, true)
		}

		msgBytes, err := msg.Marshal(nil)
		if err != nil {
			return err
		}

		if timeout, ok := ctx.Deadline(); ok {
			if err := conn.SetWriteDeadline(timeout); err != nil {
				return err
			}
		}

		if _, err := conn.WriteTo(msgBytes, req.dst); err != nil {
			return err
		}
		req.reqTex.Lock()
		req.sentAt = time.Now()
		req.reqTex.Unlock()
	}

	return nil
}
