package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"math"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/glinton/ping"
)

func main() {
	ipv4 := flag.Bool("-4", false, "")
	ipv6 := flag.Bool("-6", false, "")
	count := flag.Int("c", 0, "")
	interval := flag.Float64("i", 1, "")
	iface := flag.String("I", "", "")
	size := flag.Int("-s", 0, "")
	deadline := flag.Float64("w", 0, "")
	timeout := flag.Float64("W", 1, "")

	flag.Usage = func() {
		fmt.Print(usage)
	}
	flag.Parse()

	if flag.NArg() == 0 {
		flag.Usage()
		return
	}

	destination := flag.Arg(0)

	network := "ip4"
	if *ipv6 && !*ipv4 {
		network = "ip6"
	}

	data := bytes.Repeat([]byte{1}, 56)
	if *size > 0 && *size < 1024 {
		data = bytes.Repeat([]byte{1}, *size)
	}

	host, err := net.ResolveIPAddr(network, destination)
	if err != nil {
		fmt.Printf("can't resolve host: %s\n", err.Error())
		return
	}

	fmt.Printf("PING %s (%s) %d bytes of data.\n", destination, host.String(), len(data))

	ctx := term(context.Background())

	wg := &sync.WaitGroup{}
	var cancel context.CancelFunc
	if *deadline > 0 {
		ctx, cancel = context.WithTimeout(ctx, time.Duration(*deadline*float64(time.Second)))
		defer cancel()
	}

	if *timeout <= 0 {
		*timeout = 2
	}

	if *interval < .2 {
		fmt.Println("ping: cannot flood; minimal interval allowed for user is 200ms")
		return
	}

	tick := time.NewTicker(time.Duration(*interval * float64(time.Second)))
	defer tick.Stop()

	chanLength := 100
	if *count > 0 {
		chanLength = *count
	}

	resps := make(chan *ping.Response, chanLength)
	packetsSent := 0
	c := &ping.Client{}

	name := ""
	if names, err := net.LookupAddr(host.String()); len(names) > 0 && err == nil {
		name = strings.TrimSuffix(names[0], ".")
	}

	req := ping.Request{
		Dst:  net.ParseIP(host.String()),
		Src:  net.ParseIP(getAddr(*iface)),
		Data: data,
	}
	for *count == 0 || packetsSent < *count {
		select {
		case <-ctx.Done():
			goto finish
		case <-tick.C:
			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*timeout*float64(time.Second)))
			defer cancel()

			packetsSent++
			wg.Add(1)
			go func(req ping.Request, seq int) {
				defer wg.Done()
				req.Seq = seq
				resp, err := c.Do(ctx, &req)
				if err != nil {
					fmt.Println("failed to ping:", err)
					return
				}

				resps <- resp
				onRcv(resp, name)
			}(req, packetsSent)
		}
	}

finish:
	wg.Wait()
	close(resps)

	rsps := []*ping.Response{}
	for res := range resps {
		rsps = append(rsps, res)
	}

	onFin(packetsSent, rsps, destination)
}

func onRcv(res *ping.Response, name string) {
	if name != "" {
		fmt.Printf("%d bytes from %s (%s): icmp_seq=%d ttl=%v time=%v\n",
			res.TotalLength, name, res.Src.String(), res.Seq, res.TTL, res.RTT.Truncate(time.Microsecond*100))
		return
	}
	fmt.Printf("%d bytes from %s: icmp_seq=%d ttl=%v time=%v\n",
		res.TotalLength, res.Src.String(), res.Seq, res.TTL, res.RTT.Truncate(time.Microsecond*100))
}

func onFin(packetsSent int, resps []*ping.Response, destination string) {
	if packetsSent == 0 {
		fmt.Println("No packets were sent")
		return
	}

	packetsRcvd := len(resps)
	if packetsRcvd == 0 {
		fmt.Println("Sent:", packetsSent, "Received: 0")
		return
	}

	var min, max, avg, total time.Duration
	min = resps[0].RTT
	max = resps[0].RTT

	for _, res := range resps {
		if res.RTT < min {
			min = res.RTT
		}
		if res.RTT > max {
			max = res.RTT
		}
		total += res.RTT
	}

	avg = total / time.Duration(packetsRcvd)
	var sumsquares time.Duration
	for _, res := range resps {
		sumsquares += (res.RTT - avg) * (res.RTT - avg)
	}
	stdDev := time.Duration(math.Sqrt(float64(sumsquares / time.Duration(packetsRcvd))))
	loss := float64(packetsSent-packetsRcvd) / float64(packetsSent) * 100

	fmt.Printf("\n--- %s ping statistics ---\n", destination)
	if loss > 0 {
		fmt.Printf("%d packets transmitted, %d received, %.2f%% packet loss\n",
			packetsSent, packetsRcvd, loss)
	} else {
		fmt.Printf("%d packets transmitted, %d received, %.0f%% packet loss\n",
			packetsSent, packetsRcvd, loss)
	}
	fmt.Printf("rtt min/avg/max/mdev = %v/%v/%v/%v\n",
		min.Truncate(time.Microsecond), avg.Truncate(time.Microsecond), max.Truncate(time.Microsecond), stdDev.Truncate(time.Microsecond))
}

// getAddr returns the IP, if provided, or the first IP address of the specified interface.
func getAddr(ipOrIface string) string {
	if addr := net.ParseIP(ipOrIface); addr != nil {
		return addr.String()
	}

	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}

	var ip net.IP
	for i := range ifaces {
		if ifaces[i].Name == ipOrIface {
			addrs, err := ifaces[i].Addrs()
			if err != nil {
				return ""
			}
			if len(addrs) > 0 {
				switch v := addrs[0].(type) {
				case *net.IPNet:
					ip = v.IP
				case *net.IPAddr:
					ip = v.IP
				}
				if len(ip) == 0 {
					return ""
				}
				return ip.String()
			}
		}
	}

	return ""
}

// Handle signals in the fanciest way. Thanks to:
// https://github.com/influxdata/influxdb/blob/v2.0.0-alpha.14/kit/signals/context.go
func term(ctx context.Context) context.Context {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	ctx, cancel := context.WithCancel(ctx)
	go func() {
		defer cancel()
		select {
		case <-ctx.Done():
			return
		case <-sigCh:
			return
		}
	}()
	return ctx
}

var usage = `
NAME
       ping - send ICMP ECHO_REQUEST to network hosts

SYNOPSIS
       ping [-h] [-4] [-6] [-c count] [-i interval] [-I interface]
            [-w deadline] [-W timeout] [-s packetsize] destination

DESCRIPTION
       ping uses the ICMP protocol's mandatory ECHO_REQUEST datagram to elicit
       an ICMP ECHO_RESPONSE from a host or gateway. ECHO_REQUEST datagrams
			 (pings) have an IP and ICMP header, followed by an arbitrary number
			 of padbytes used to fill out the packet.

       ping works with both IPv4 and IPv6. Using only one of them explicitly
       can be enforced by specifying -4 or -6.

OPTIONS
       -4
           Use IPv4 only.

       -6
           Use IPv6 only.

       -c count
           Stop after sending count ECHO_REQUEST packets. With deadline
           option, ping waits for count ECHO_REPLY packets, until the timeout
           expires.

       -h
           Show help.

       -i interval
           Wait interval seconds between sending each packet. The default is
           to wait for one second between each packet normally. Interval values
           less than 200 milliseconds (200ms) are not allowed.

       -I interface
           interface is either an address, or an interface name. If interface
           is an address, it sets source address to specified interface
           address. If interface in an interface name, it sets source
           interface to specified interface.

       -s packetsize
           Specifies the number of data bytes to be sent. The default is 56,
           which translates into 64 ICMP data bytes when combined with the 8
           bytes of ICMP header data.

       -w deadline
           Specify a timeout, in seconds, before ping exits regardless of how
           many packets have been sent or received. In this case ping does not
           stop after count packet are sent, it waits either for deadline
           expire or until count probes are answered or for some error
           notification from network.

       -W timeout
           Time to wait for a response, in seconds. The option affects only
           timeout in absence of any responses.

EXAMPLES
       # ping google continuously
       ping www.google.com

       # ping google 5 times
       ping -c 5 www.google.com

       # ping google 5 times at 500ms intervals
       ping -c 5 -i .5 www.google.com

       # ping google for 10 seconds
       ping -w 10 www.google.com
`
