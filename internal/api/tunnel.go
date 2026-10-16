package api

// TunnelPort is the TCP port of the API on the Edge's Tunnel address,
// 100.64.0.1. The listener exists only inside the Tunnel's netstack, so
// nothing listens on a host address. The data port is 7000.
const TunnelPort = 7001
