// SPDX-License-Identifier: GPL-3.0-only
package upnp

import "testing"

func TestDiscoveryParsing(t *testing.T) {
	raw := "HTTP/1.1 200 OK\r\nLOCATION: https://music.home.paulbowler.co.uk/description.xml\r\nST: urn:schemas-upnp-org:service:ContentDirectory:1\r\nUSN: uuid:music\r\n\r\n"
	server, ok := parseDiscovery(raw, "192.168.1.10")
	if !ok || server.USN != "uuid:music" || server.Location != "https://music.home.paulbowler.co.uk/description.xml" {
		t.Fatal(server, ok)
	}
	for _, raw := range []string{"garbage", "HTTP/1.1 200 OK\r\nLOCATION: file:///etc/passwd\r\n\r\n", "HTTP/1.1 200 OK\r\nLOCATION: http://music.home/\r\nST: not-a-music-server\r\n\r\n"} {
		if _, ok := parseDiscovery(raw, "192.168.1.1"); ok {
			t.Fatal(raw)
		}
	}
}
