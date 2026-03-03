package netutil

import "net"

// LocalIP returns the preferred outbound IP address of the machine.
// It falls back to "localhost" if the IP cannot be determined.
func LocalIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "localhost"
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}
