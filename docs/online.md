# Playing over the internet

The joiner has to reach the host's port (TCP 25565
unless the host passed `--port`). Either forward that TCP port on the
host's router to the host's PC and have the joiner use the host's public
IP, or put both machines on a VPN such as [Tailscale](https://tailscale.com)
and join the host's Tailscale IP (`100.x.y.z`) — no port-forwarding
needed. A Windows host should allow `ww-multiplayer.exe` through the
firewall when Windows asks.

---

[← Known issues](05-known-issues.md) · [Docs home](README.md) · [Self-hosting a dedicated server →](11-self-hosting.md)
