package ping

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

// BenchmarkProcessPacket-8   		 5000000	       271 ns/op // regular map[string]packetInfo
// BenchmarkProcessPacket-8		 5000000	       309 ns/op // with custom thread safe map
// BenchmarkProcessPacket-8   	 	 3000000	       458 ns/op // with sync.Map

func BenchmarkProcessPacket(b *testing.B) {
	p := &Pinger{
		size:        64,
		proto:       ProtocolICMP,
		wg:          &sync.WaitGroup{},
		packetsSent: newPktMap(),
		packetsRcvd: newPktMap(),
	}

	body := &icmp.Echo{
		ID:   1,
		Seq:  1,
		Data: bytes.Repeat([]byte{1}, int(p.size)),
	}

	message := &icmp.Message{
		Type: ipv4.ICMPTypeEchoReply,
		Code: 0,
		Body: body,
	}

	msgBytes, _ := message.Marshal(nil)

	pkt := &msg{
		data:   msgBytes,
		nbytes: len(msgBytes),
		ipAddr: "127.0.0.1",
		ttl:    24,
	}

	p.packetsSent.Store(pkt.ipAddr, packetInfo{
		sentAt:      map[uint]time.Time{1: time.Now().Add(-time.Second)},
		packetsSent: 1,
	})

	p.wg.Add(b.N)
	for k := 0; k < b.N; k++ {
		p.processPing(pkt)
	}
}

func TestPktMap(t *testing.T) {
	wg := &sync.WaitGroup{}
	pkts := []string{"0", "1", "2", "3", "4", "5", "6", "7", "8", "9"}
	pkt := newPktMap()
	wg.Add(len(pkts))
	data := map[string]packetInfo{}

	// test store
	for _, p := range pkts {
		go func(s string) {
			pkt.Store(s, packetInfo{ip: s, packetsRcvd: 1, packetsSent: 1})
			wg.Done()
		}(p)
	}

	wg.Wait()
	for _, p := range pkts {
		data[p] = packetInfo{ip: p, packetsRcvd: 1, packetsSent: 1}
	}

	// test len
	assert.Equal(t, 10, pkt.Len())

	// test copy
	assert.Equal(t, data, pkt.Copy())

	// test delete
	pkt.Delete("0")

	// test load
	_, ok := pkt.Load("0")
	assert.False(t, ok)

	// test packets
	sent, rcvd := pkt.Packets()
	assert.Equal(t, 9, sent)
	assert.Equal(t, 9, rcvd)
}

func TestNewPinger(t *testing.T) {
	_, err := NewPinger(nil)
	require.Error(t, err)

	_, err = NewPinger(
		&icmp.PacketConn{},
		WithOnRecieve(func(*Packet) {}),
		WithOnFinish(func(*Statistics) {}),
		WithContext(nil),
		WithSize(2000),
		WithCount(3),
		WithInterval(time.Nanosecond),
		WithTimeout(time.Second*2),
		WithDeadline(-time.Second),
	)
	require.NoError(t, err)
}

func TestListen(t *testing.T) {
	_, err := Listen("udp4", "")
	require.NoError(t, err)
}

func TestE2E(t *testing.T) {
	c, err := Listen("udp4", "")
	require.NoError(t, err)

	stats := []*Statistics{}

	p, err := NewPinger(c,
		WithCount(2),
		WithDeadline(time.Second*3),
		WithTimeout(time.Second),
		WithOnRecieve(func(p *Packet) { fmt.Printf("%#v\n", p) }),
		WithOnFinish(func(s *Statistics) {
			stats = append(stats, s)
		}))
	require.NoError(t, err)

	addr, err := net.ResolveIPAddr("ip4", "127.0.0.1")
	require.NoError(t, err)

	err = p.Send(&net.UDPAddr{IP: addr.IP, Zone: addr.Zone})
	require.NoError(t, err)

	fmt.Printf("%#v\n", stats)
}

func TestE2EContextTimeout(t *testing.T) {
	c, err := Listen("udp4", "")
	require.NoError(t, err)

	stats := []*Statistics{}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*2)
	defer cancel()
	p, err := NewPinger(c,
		WithCount(3),
		WithDeadline(time.Second*3),
		WithTimeout(time.Second),
		WithInterval(time.Second),
		WithContext(ctx),
		WithOnRecieve(func(p *Packet) { fmt.Printf("%#v\n", p) }),
		WithOnFinish(func(s *Statistics) {
			stats = append(stats, s)
		}))
	require.NoError(t, err)

	addr, err := net.ResolveIPAddr("ip4", "127.0.0.2")
	require.NoError(t, err)

	err = p.Send(&net.UDPAddr{IP: addr.IP, Zone: addr.Zone})
	require.NoError(t, err)

	fmt.Printf("%#v\n", stats)
}

func TestE2EBadAddr(t *testing.T) {
	c, err := Listen("udp4", "")
	require.NoError(t, err)

	p, err := NewPinger(c)
	require.NoError(t, err)

	addr, err := net.ResolveIPAddr("ip4", "127.0.0.3")
	require.NoError(t, err)

	err = p.Send(addr)
	require.Error(t, err)
}
