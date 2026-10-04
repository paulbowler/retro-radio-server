# DNS setup for the first Pure trial

Give the server a stable LAN IPv4 address (DHCP reservation is sufficient). Publish container port 8080 as host port **80**. The radio must use your home DNS resolver. A DNS override only changes IP resolution; it cannot change port 80 into 8080.

## AdGuard Home

In **Filters → DNS rewrites**, add `pure.wifiradiofrontier.com` with the server's LAN IPv4 address as the answer. Ensure DHCP hands out AdGuard's DNS address, or configure that DNS address on the radio. UI wording may vary by release.

## Pi-hole

Under **Local DNS → DNS Records**, add domain `pure.wifiradiofrontier.com` and the server LAN IPv4 address. On versions with a different layout, find local DNS records in settings. Use the Pi-hole DNS address on the radio or via DHCP.

## dnsmasq

Add a local hosts-style entry, replacing the example address:

```ini
host-record=pure.wifiradiofrontier.com,192.168.1.20
```

Restart/reload dnsmasq using your installation's service manager. `host-record` is deliberately an exact hostname mapping. Do not redirect all Internet traffic or all broadcaster hosts.

## Verify

Query your configured DNS resolver from another LAN computer:

```sh
nslookup pure.wifiradiofrontier.com <DNS-server-IP>
```

The answer should be the Retro Radio server's LAN IPv4 address. The setup dashboard at `http://<server-IP>/` should change after the radio opens Internet Radio. If it does not, verify the radio's DNS choice, port mapping, host firewall and cached DNS. Some manufacturers use an additional fallback hostname: inspect redacted requests before adding a rewrite; do not assume Pure uses one from a Hama trace.

Do not redirect NTP or firmware update domains. An HTTP response from the setup page is not evidence of radio model detection or audible playback; follow the hardware checklist.
