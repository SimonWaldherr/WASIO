package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
)

type Payload struct {
	Params map[string]string `json:"params"`
}

// privateRanges lists all RFC-private / special-purpose IPv4 ranges.
var privateRanges = []string{
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"127.0.0.0/8",       // loopback
	"169.254.0.0/16",    // link-local
	"100.64.0.0/10",     // shared address space (RFC 6598)
	"192.0.0.0/24",      // IETF protocol
	"198.18.0.0/15",     // benchmarking
	"198.51.100.0/24",   // documentation
	"203.0.113.0/24",    // documentation
	"240.0.0.0/4",       // reserved
	"255.255.255.255/32", // broadcast
}

// privateRangesV6 lists special-purpose IPv6 ranges.
var privateRangesV6 = []string{
	"::1/128",        // loopback
	"fc00::/7",       // unique local
	"fe80::/10",      // link-local
	"::ffff:0:0/96",  // IPv4-mapped
	"2001:db8::/32",  // documentation
	"100::/64",       // discard
}

func isPrivate(ip net.IP) bool {
	ranges := privateRanges
	if ip.To4() == nil {
		ranges = privateRangesV6
	}
	for _, cidr := range ranges {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func ipVersion(ip net.IP) string {
	if ip.To4() != nil {
		return "IPv4"
	}
	return "IPv6"
}

func ipToInt(ip net.IP) uint32 {
	ip4 := ip.To4()
	if ip4 == nil {
		return 0
	}
	return binary.BigEndian.Uint32(ip4)
}

func main() {
	var payload Payload
	if err := json.NewDecoder(os.Stdin).Decode(&payload); err != nil {
		fmt.Println("Error: invalid payload")
		return
	}

	operation := strings.ToLower(payload.Params["op"])
	input := strings.TrimSpace(payload.Params["input"])

	if operation == "" {
		operation = "info"
	}

	switch operation {
	case "info":
		if input == "" {
			fmt.Println("Usage: /ip_utils?op=info&input=192.168.1.1")
			fmt.Println("Operations: info, validate, private, cidr, range")
			return
		}
		ip := net.ParseIP(input)
		if ip == nil {
			fmt.Printf("Invalid IP address: %s\n", input)
			return
		}
		ver := ipVersion(ip)
		priv := isPrivate(ip)
		fmt.Printf("Address:   %s\n", ip.String())
		fmt.Printf("Version:   %s\n", ver)
		fmt.Printf("Private:   %v\n", priv)
		if ver == "IPv4" {
			fmt.Printf("Decimal:   %d\n", ipToInt(ip))
			fmt.Printf("Hex:       %08X\n", ipToInt(ip))
			// Reverse DNS form (arpa)
			parts := strings.Split(ip.To4().String(), ".")
			rev := fmt.Sprintf("%s.%s.%s.%s.in-addr.arpa",
				parts[3], parts[2], parts[1], parts[0])
			fmt.Printf("PTR zone:  %s\n", rev)
		} else {
			fmt.Printf("Expanded:  %s\n", ip.String())
		}
		if priv {
			// Identify the matching range
			for _, cidr := range append(privateRanges, privateRangesV6...) {
				_, network, _ := net.ParseCIDR(cidr)
				if network != nil && network.Contains(ip) {
					fmt.Printf("In range:  %s\n", network.String())
					break
				}
			}
		}

	case "validate":
		if input == "" {
			fmt.Println("Error: 'input' parameter required")
			return
		}
		ip := net.ParseIP(input)
		if ip != nil {
			fmt.Printf("Valid %s address: %s\n", ipVersion(ip), ip.String())
		} else {
			fmt.Printf("Invalid IP address: %s\n", input)
		}

	case "private":
		if input == "" {
			fmt.Println("Error: 'input' parameter required")
			return
		}
		ip := net.ParseIP(input)
		if ip == nil {
			fmt.Printf("Invalid IP address: %s\n", input)
			return
		}
		if isPrivate(ip) {
			fmt.Printf("%s is a private/reserved address\n", ip.String())
		} else {
			fmt.Printf("%s is a public address\n", ip.String())
		}

	case "cidr":
		if input == "" {
			fmt.Println("Error: 'input' parameter required (e.g. 192.168.1.0/24)")
			return
		}
		ipAddr, network, err := net.ParseCIDR(input)
		if err != nil {
			fmt.Printf("Invalid CIDR: %v\n", err)
			return
		}
		ones, bits := network.Mask.Size()
		hosts := uint64(1) << uint(bits-ones)
		fmt.Printf("Network:   %s\n", network.Network())
		fmt.Printf("Address:   %s\n", ipAddr.String())
		fmt.Printf("Mask bits: %d/%d\n", ones, bits)
		fmt.Printf("Netmask:   %s\n", net.IP(network.Mask).String())
		fmt.Printf("Broadcast: %s\n", broadcastAddr(network))
		if hosts > 2 {
			fmt.Printf("Usable:    %d hosts\n", hosts-2)
		} else {
			fmt.Printf("Hosts:     %d\n", hosts)
		}
		checkIP := payload.Params["check"]
		if checkIP != "" {
			pip := net.ParseIP(strings.TrimSpace(checkIP))
			if pip == nil {
				fmt.Printf("Check:     invalid IP %q\n", checkIP)
			} else if network.Contains(pip) {
				fmt.Printf("Check:     %s IS in %s\n", pip, network)
			} else {
				fmt.Printf("Check:     %s is NOT in %s\n", pip, network)
			}
		}

	case "range":
		start := strings.TrimSpace(payload.Params["start"])
		end := strings.TrimSpace(payload.Params["end"])
		if start == "" || end == "" {
			fmt.Println("Error: 'start' and 'end' parameters required")
			return
		}
		sIP := net.ParseIP(start).To4()
		eIP := net.ParseIP(end).To4()
		if sIP == nil || eIP == nil {
			fmt.Println("Error: start/end must be valid IPv4 addresses")
			return
		}
		sInt := ipToInt(net.IP(sIP))
		eInt := ipToInt(net.IP(eIP))
		if sInt > eInt {
			fmt.Println("Error: start must be <= end")
			return
		}
		count := eInt - sInt + 1
		fmt.Printf("Start:  %s (%d)\n", sIP.String(), sInt)
		fmt.Printf("End:    %s (%d)\n", eIP.String(), eInt)
		fmt.Printf("Count:  %d addresses\n", count)
		// Print first 5 and last 5 if large range
		if count <= 10 {
			for i := sInt; i <= eInt; i++ {
				b := make([]byte, 4)
				binary.BigEndian.PutUint32(b, i)
				fmt.Printf("  %s\n", net.IP(b).String())
			}
		} else {
			for i := sInt; i < sInt+5; i++ {
				b := make([]byte, 4)
				binary.BigEndian.PutUint32(b, i)
				fmt.Printf("  %s\n", net.IP(b).String())
			}
			fmt.Printf("  ... (%d more) ...\n", count-10)
			for i := eInt - 4; i <= eInt; i++ {
				b := make([]byte, 4)
				binary.BigEndian.PutUint32(b, i)
				fmt.Printf("  %s\n", net.IP(b).String())
			}
		}

	default:
		fmt.Printf("Unknown operation: %s\n", operation)
		fmt.Println("Operations: info, validate, private, cidr, range")
	}
}

func broadcastAddr(n *net.IPNet) string {
	ip := n.IP.To4()
	if ip == nil {
		return "n/a (IPv6)"
	}
	mask := n.Mask
	broadcast := make(net.IP, 4)
	for i := 0; i < 4; i++ {
		broadcast[i] = ip[i] | ^mask[i]
	}
	return broadcast.String()
}
