package indexing

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"
)

// Gateway URLs can originate in onchain data. Check the IP at the socket,
// including on redirects, so DNS changes cannot switch to a local destination.
var publicGatewayClient = &http.Client{
	Timeout: 15 * time.Second,
	Transport: &http.Transport{
		DialContext: (&net.Dialer{
			Timeout: 5 * time.Second,
			ControlContext: func(_ context.Context, _, addr string, _ syscall.RawConn) error {
				endpoint, err := netip.ParseAddrPort(addr)
				if err != nil {
					return err
				}
				if !publicGatewayIP(endpoint.Addr()) {
					return fmt.Errorf("gateway address is not public")
				}
				return nil
			},
		}).DialContext,
		ForceAttemptHTTP2:   true,
		TLSHandshakeTimeout: 5 * time.Second,
		IdleConnTimeout:     30 * time.Second,
		MaxIdleConns:        32,
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 || req.URL.User != nil || (req.URL.Scheme != "https" && req.URL.Scheme != "http") {
			return fmt.Errorf("invalid gateway redirect")
		}
		return nil
	},
}

// Some special-use addresses are classified as global unicast by netip.
var globalIPv6 = netip.MustParsePrefix("2000::/3")

var specialGatewayRanges = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("240.0.0.0/4"), netip.MustParsePrefix("2001::/32"),
	netip.MustParsePrefix("2002::/16"),
}

func publicGatewayIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.Zone() != "" {
		return false
	}
	if ip.Is6() && !globalIPv6.Contains(ip) {
		return false
	}
	for _, prefix := range specialGatewayRanges {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}
