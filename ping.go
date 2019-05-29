package ping

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

var (
	id int32
)

const (
	ProtocolICMP     = 1  // Internet Control Message for IPv4
	ProtocolIPv6ICMP = 58 // ICMP for IPv6
)

// pktMap is a threadsafe map for storing and loading packetInfo.
type pktMap struct {
	data map[string]packetInfo
	tex  *sync.Mutex
}

// Store stores packetInfo into pktMap in a thread safe manner.
func (p *pktMap) Store(key string, val packetInfo) {
	p.tex.Lock()
	p.data[key] = val
	p.tex.Unlock()
}

// Load returns packetInfo from pktMap in a thread safe manner.
func (p *pktMap) Load(key string) (packetInfo, bool) {
	p.tex.Lock()
	val, ok := p.data[key]
	p.tex.Unlock()
	return val, ok
}

// Pinger is a thing that sends pings. You may have one per host you wish to ping, with
// unique settings per each, or you may have one Pinger for multiple hosts you desire
// to ping, with the same settings for each host.
type Pinger struct {
	wg  *sync.WaitGroup
	ctx context.Context

	proto       int    // iana icmp protocol
	packetsRcvd pktMap // packetsRcvd contains the aggregated stats for packets received.
	packetsSent pktMap // packetsSent contains information for calculating packet stats on receipt.
	queuedSends int32  // number of pings queued for sending

	onRecv   func(*Packet)     // onRecv is called when Pinger receives and processes a packet
	onFinish func(*Statistics) // onFinish is called when Pinger exits

	conn *icmp.PacketConn // conn is the connection to send the pings over

	size     uint          // size is the size in bytes of a ping to send. Default is 64 bytes.
	count    uint          // count is how many pings to send. 0 is no limit.
	interval time.Duration // interval defines the interval at which pings are sent. Must be 200ms or greater.
	timeout  time.Duration // timeout defines the per-ping timeout. 0 means no timeout.
	deadline time.Duration // deadline defines the overall ping timeout. 0 means no timeout.
}

// Statistics defines the aggregated statistics of received ping packets.
type Statistics struct {
	PacketsSent uint            // PacketsSent is the number of packets sent.
	PacketsRecv uint            // PacketsRecv is the number of packets received.
	PacketLoss  float64         // PacketLoss is the percentage of packets lost.
	Addr        string          // Addr is the string address of the host being pinged.
	RTTs        []time.Duration // RTTs is all of the round-trip times sent via this pinger.
	MinRTT      time.Duration   // MinRTT is the minimum round-trip time sent via this pinger.
	MaxRTT      time.Duration   // MaxRTT is the maximum round-trip time sent via this pinger.
	AvgRTT      time.Duration   // AvgRTT is the average round-trip time sent via this pinger.
	StdDevRTT   time.Duration   // StdDevRTT is the standard deviation of the round-trip times sent via this pinger.
}

// Packet is defines ping packet statistics.
type Packet struct {
	Nbytes int           // NBytes is the number of bytes in the message.
	IPAddr string        // IPAddr is the address of the host being pinged.
	Seq    int           // Seq is the ICMP sequence number.
	TTL    int           // TTL is the Time to Live on the packet.
	RTT    time.Duration // RTT is the round-trip time it took to ping.
}

type packetInfo struct {
	sentAt      map[uint]time.Time // records the time the ping was sent
	packetsSent uint               // tallies the number of packet's sent
	packetsRcvd uint               // tallies the number of packet's received
	rtts        []time.Duration    // combined round-trip-times for all pings
}

type msg struct {
	data   []byte // ping payload
	ttl    int    // ttl (part of a control message)
	nbytes int    // nubmer of bytes received in echo-reply
	ipAddr string // ip address of remote host
}

// WithOnRecieve allows setting a callback for when an echo response has been recieved.
func WithOnRecieve(fn func(*Packet)) func(*Pinger) {
	return func(p *Pinger) {
		p.onRecv = fn
	}
}

// WithOnFinish allows setting a callback for when pinging a host has completed.
func WithOnFinish(fn func(*Statistics)) func(*Pinger) {
	return func(p *Pinger) {
		p.onFinish = fn
	}
}

// WithContext allows setting the context used.
func WithContext(ctx context.Context) func(*Pinger) {
	return func(p *Pinger) {
		p.ctx = ctx
	}
}

// WithSize allows setting the size (in bytes) of a ping to be sent. Default is 64 bytes.
func WithSize(i uint) func(*Pinger) {
	return func(p *Pinger) {
		p.size = i
	}
}

// WithCount allows setting the number of pings to send. Default is is no limit (0).
func WithCount(i uint) func(*Pinger) {
	return func(p *Pinger) {
		p.count = i
	}
}

// WithInterval allows setting the interval at which pings are sent. Must be 200ms or greater.
func WithInterval(d time.Duration) func(*Pinger) {
	return func(p *Pinger) {
		p.interval = d
	}
}

// WithTimeout allows setting the per-ping timeout. Default is 5 seconds.
func WithTimeout(d time.Duration) func(*Pinger) {
	return func(p *Pinger) {
		p.timeout = d
	}
}

// WithDeadline allows setting the total ping timeout per host. Default is no timeout (0).
func WithDeadline(d time.Duration) func(*Pinger) {
	return func(p *Pinger) {
		p.deadline = d
	}
}

// NewPinger returns a new pinger with configured options.
func NewPinger(conn *icmp.PacketConn, opts ...func(*Pinger)) (*Pinger, error) {
	if conn == nil {
		return nil, errors.New("connection must not be nil")
	}

	p := &Pinger{
		conn:        conn,
		size:        64,
		interval:    time.Second,
		timeout:     time.Second * 5,
		wg:          &sync.WaitGroup{},
		packetsSent: pktMap{tex: &sync.Mutex{}, data: make(map[string]packetInfo)},
		packetsRcvd: pktMap{tex: &sync.Mutex{}, data: make(map[string]packetInfo)},
	}

	for i := range opts {
		opts[i](p)
	}

	if p.interval < time.Millisecond*200 {
		p.interval = time.Millisecond * 200
	}

	if p.size > 1024 {
		p.size = 1024
	}

	if p.ctx == nil {
		p.ctx = context.Background()
	}

	return p, nil
}

// Listen listens on a network ('ip4:icmp', 'ip6:ipv6-icmp', 'udp4', or 'udp6')
// and optional address (blank string for any address/interface).
func Listen(network, addr string) (*icmp.PacketConn, error) {
	conn, err := icmp.ListenPacket(network, addr)
	if err != nil {
		return nil, fmt.Errorf("error listening for ICMP packets: %s", err.Error())
	}
	return conn, nil
}

// Read reads data (blocking) from the connection and processes it.
func (p *Pinger) Read() error {
	if p.conn == nil {
		return errors.New("connection must not be nil")
	}

	msgs := make(chan *msg, 100)

	if c4 := p.conn.IPv4PacketConn(); c4 != nil {
		go read4(p.ctx, c4, msgs)
		p.proto = ProtocolICMP
	} else if c6 := p.conn.IPv6PacketConn(); c6 != nil {
		go read6(p.ctx, c6, msgs)
		p.proto = ProtocolIPv6ICMP
	}

	var cancel context.CancelFunc
	if p.deadline > 0 {
		p.ctx, cancel = context.WithTimeout(p.ctx, p.deadline)
		defer cancel()
	}

	for {
		select {
		case <-p.ctx.Done():
			return p.ctx.Err()
		case msg := <-msgs:
			if msg == nil {
				continue
			}
			err := p.processPing(msg)
			if err != nil {
				return err
			}
		}
	}
}

func read4(ctx context.Context, conn *ipv4.PacketConn, recv chan<- *msg) error {
	defer close(recv)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			conn.SetReadDeadline(time.Now().Add(time.Millisecond * 300))
			bytesReceived := make([]byte, 1500)

			n, cm, src, err := conn.ReadFrom(bytesReceived)
			if err != nil {
				if neterr, ok := err.(*net.OpError); ok {
					if neterr.Timeout() {
						continue
					} else {
						return err
					}
				}
				return err
			}

			if n <= 0 {
				continue
			}

			var ttl int
			if cm != nil {
				ttl = cm.TTL
			}

			recv <- &msg{data: bytesReceived[:n], nbytes: n, ipAddr: src.String(), ttl: ttl}
		}
	}
}

func read6(ctx context.Context, conn *ipv6.PacketConn, recv chan *msg) error {
	defer close(recv)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			conn.SetReadDeadline(time.Now().Add(time.Millisecond * 300))
			bytesReceived := make([]byte, 1500)

			n, cm, src, err := conn.ReadFrom(bytesReceived)
			if err != nil {
				if neterr, ok := err.(*net.OpError); ok {
					if neterr.Timeout() {
						continue
					} else {
						return err
					}
				}
				return err
			}

			if n <= 0 {
				continue
			}

			var ttl int
			if cm != nil {
				ttl = cm.HopLimit
			}

			recv <- &msg{data: bytesReceived[:n], nbytes: n, ipAddr: src.String(), ttl: ttl}

			bytesReceived = bytesReceived[:0:0]
		}
	}
}

func (p *Pinger) processPing(msg *msg) error {
	receivedAt := time.Now()

	m, err := icmp.ParseMessage(p.proto, msg.data)
	if err != nil {
		return err
	}

	if m.Type != ipv4.ICMPTypeEchoReply && m.Type != ipv6.ICMPTypeEchoReply {
		// Likely an `ICMPTypeDestinationUnreachable`, ignore it.
		return nil
	}

	outPkt := &Packet{
		Nbytes: msg.nbytes,
		IPAddr: msg.ipAddr,
		TTL:    msg.ttl,
	}

	rcvd := packetInfo{}

	switch pkt := m.Body.(type) {
	case *icmp.Echo:
		sent, ok := p.packetsSent.Load(msg.ipAddr)
		if !ok {
			return fmt.Errorf("received unsolicited response from '%s'", msg.ipAddr)
		}
		outPkt.RTT = receivedAt.Sub(sent.sentAt[uint(pkt.Seq)])
		outPkt.Seq = pkt.Seq

		rcvd, ok = p.packetsRcvd.Load(msg.ipAddr)
		if !ok {
			rcvd = packetInfo{}
		}

		rcvd.packetsSent = sent.packetsSent
		rcvd.packetsRcvd++
		rcvd.rtts = append(rcvd.rtts, outPkt.RTT)
		p.packetsRcvd.Store(msg.ipAddr, rcvd)
	default:
		return fmt.Errorf("invalid ICMP echo reply; type: '%T', '%v'", pkt, pkt)
	}

	handler := p.onRecv
	if handler != nil {
		handler(outPkt)
	}

	if p.count > 0 && rcvd.packetsRcvd >= p.count {
		p.sendFinish(msg.ipAddr, rcvd)
	}

	return nil
}

// Send sends count number of pings to each destination, respecting timeouts.
func (p *Pinger) Send(dest net.Addr, dests ...net.Addr) error {
	p.wg.Add(len(dests) + 1)

	atomic.AddInt32(&p.queuedSends, int32(len(dests)+1))
	if p.conn == nil {
		p.wg.Add(-int(atomic.LoadInt32(&p.queuedSends)))
		return errors.New("connection must not be nil")
	}

	dests = append([]net.Addr{dest}, dests...)

	for _, destIP := range dests {
		go func(dest net.Addr) {
			err := p.send(int(atomic.LoadInt32(&id)), dest)
			atomic.AddInt32(&p.queuedSends, int32(-1))
			if err != nil {
				p.wg.Done()
				return
			}
		}(destIP)

		if atomic.LoadInt32(&id) >= math.MaxInt32 {
			atomic.StoreInt32(&id, 0)
		} else {
			atomic.AddInt32(&id, 1)
		}
	}

	p.wg.Wait()
	return nil
}

func (p *Pinger) send(id int, destIP net.Addr) error {
	ctx := p.ctx
	var cancel context.CancelFunc
	if p.deadline > 0 {
		ctx, cancel = context.WithTimeout(p.ctx, p.deadline)
		defer cancel()
	}

	tick := time.NewTicker(p.interval)
	defer tick.Stop()

	var (
		sequence    uint
		packetsSent uint
		sentMap     = make(map[uint]time.Time, p.count)
	)

	for packetsSent < p.count {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
			sequence++

			body := &icmp.Echo{
				ID:   id,
				Seq:  int(sequence),
				Data: bytes.Repeat([]byte{1}, int(p.size)),
			}

			msg := &icmp.Message{
				Type: ipv6.ICMPTypeEchoRequest,
				Code: 0,
				Body: body,
			}

			if c4 := p.conn.IPv4PacketConn(); c4 != nil {
				msg.Type = ipv4.ICMPTypeEcho
				p.conn.IPv4PacketConn().SetControlMessage(ipv4.FlagTTL, true)
			} else {
				p.conn.IPv6PacketConn().SetControlMessage(ipv6.FlagHopLimit, true)
			}

			msgBytes, err := msg.Marshal(nil)
			if err != nil {
				return err
			}

			if p.timeout > 0 {
				if err := p.conn.SetWriteDeadline(time.Now().Add(p.timeout)); err != nil {
					return err
				}
			}

			sentAt := time.Now()
			if _, err := p.conn.WriteTo(msgBytes, destIP); err != nil {
				return err
			}
			packetsSent++

			sentMap[sequence] = sentAt

			p.packetsSent.Store(destIP.String(), packetInfo{
				sentAt:      sentMap,
				packetsSent: packetsSent,
			})
		}
	}

	return nil
}

func (p *Pinger) sendFinish(ip string, v packetInfo) {
	handler := p.onFinish
	if handler != nil {
		handler(v.statistics(ip))
	}
	p.wg.Done()
}

func (v packetInfo) statistics(k string) *Statistics {
	loss := float64(v.packetsSent-v.packetsRcvd) / float64(v.packetsSent) * 100

	var min, max, total time.Duration
	if len(v.rtts) > 0 {
		min = v.rtts[0]
		max = v.rtts[0]
	}
	for _, rtt := range v.rtts {
		if rtt < min {
			min = rtt
		}
		if rtt > max {
			max = rtt
		}
		total += rtt
	}

	s := &Statistics{
		PacketsSent: v.packetsSent,
		PacketsRecv: v.packetsRcvd,
		PacketLoss:  loss,
		RTTs:        v.rtts,
		Addr:        k,
		MaxRTT:      max,
		MinRTT:      min,
	}

	if len(v.rtts) > 0 {
		s.AvgRTT = total / time.Duration(len(v.rtts))
		var sumsquares time.Duration
		for _, rtt := range v.rtts {
			sumsquares += (rtt - s.AvgRTT) * (rtt - s.AvgRTT)
		}
		s.StdDevRTT = time.Duration(math.Sqrt(float64(sumsquares / time.Duration(len(v.rtts)))))
	}

	return s
}