package main

import (
	"fmt"
	"net"

	"github.com/glinton/go-ping"
)

func printRcvd(pkt *ping.Packet) {
	fmt.Printf("%d bytes from %s: icmp_seq=%d time=%v ttl=%v\n",
		pkt.Nbytes, pkt.IPAddr, pkt.Seq, pkt.RTT, pkt.TTL)
}

func printStat(stats *ping.Statistics) {
	fmt.Printf("\n--- %s ping statistics ---\n", stats.Addr)
	fmt.Printf("%d packets transmitted, %d packets received, %v%% packet loss\n",
		stats.PacketsSent, stats.PacketsRecv, stats.PacketLoss)
	fmt.Printf("round-trip min/avg/max/stddev = %v/%v/%v/%v\n",
		stats.MinRTT, stats.AvgRTT, stats.MaxRTT, stats.StdDevRTT)
}

func main() {
	conn, err := ping.Listen("ip4:icmp", "")
	defer conn.Close()
	if err != nil {
		fmt.Println(err)
		return
	}

	pinger, err := ping.NewPinger(
		conn,
		ping.WithCount(5),
		ping.WithOnRecieve(printRcvd),
		ping.WithOnFinish(printStat),
	)
	if err != nil {
		fmt.Println(err)
		return
	}

	go pinger.Read()

	localhost, err := net.ResolveIPAddr("ip", "localhost")
	if err != nil {
		fmt.Println(err)
		return
	}
	google, err := net.ResolveIPAddr("ip", "google.com")
	if err != nil {
		fmt.Println(err)
		return
	}

	pinger.Send(localhost, google)
}