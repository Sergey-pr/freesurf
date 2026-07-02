# FreeSurf

A minimalistic, multi-platform (macOS / Linux / Windows) VPN client.

> **Status: working tunnel on macOS and Windows (VLESS).** The UI, data model,
> local storage, and a real two-core TUN engine are in place. macOS is the primary
> tested platform; Windows is implemented (native service + Wintun) but less
> exercised. Linux TUN and more protocols are next (see [Roadmap](#roadmap)).

## What works today

- Main window with a large **Start / Stop** button (with a *connecting* state).
- Server list below it. A server (a subscription) collapses to show its child
  nodes; single-node servers render flat.
- Top-right **+** button → dropdown → **Paste from clipboard**: reads the system
  clipboard and imports a subscription URL or one-or-more share URIs into the list.
- Select a node, hit **Start**: the app downloads & pins both cores (Xray +
  sing-box), builds their configs, validates with `sing-box check`, installs a
  privileged helper on first connect (one auth/UAC prompt), and brings the tunnel
  up. **Stop** tears it down.
- **Ping** an individual node or a whole subscription to see reachability/latency.
  Names are resolved over DoH (bypassing local DNS interference) and each node is
  probed the way it connects — TCP, or QUIC/UDP for HTTP/3 (`H3`) nodes.
- Everything is persisted in SQLite.

### Tunnel engine (two cores)

A connection runs **two cores**, split by privilege:

- **Xray** — runs *unprivileged* as the proxy backend, exposing a local SOCKS port.
- **sing-box** — runs *privileged* (needs the TUN device); owns the full-tunnel TUN
  inbound (`auto_route`) and routes traffic into Xray's SOCKS.

- **Cores:** both binaries are downloaded from GitHub and pinned to fixed versions
  into `<data>/bin/` on first connect; versions are verified before use.
- **Parser:** `vless://` share URIs → Xray outbound (TLS / Reality / uTLS / ALPN /
  flow / tcp・xhttp・ws・grpc・httpupgrade transports). VLESS only for now.
- **Config:** full-tunnel TUN inbound (`auto_route`), DNS + route, proxy + direct
  outbounds; a direct rule pins the server IP so the core's own traffic to the
  server doesn't loop back into the TUN.
- **Privileges:** the TUN core needs root / the TUN device, so it runs via a helper
  installed once — the only prompt (password / UAC). macOS uses a launchd
  LaunchDaemon; Windows a native Go service (LocalSystem) + Wintun. After that,
  Start/Stop is just a sentinel file, so it won't re-prompt.

## Stack

| Layer         | Tech                                                  |
|---------------|------------------------------------------------------|
| App framework | [Wails v3](https://v3.wails.io) (Go + native WebView) |
| Language      | Go 1.25+                                              |
| UI            | Vue 3 (Composition API) + Pinia + Vite               |
| Database      | SQLite via `github.com/doug-martin/goqu`             |

The visual design and project layout follow the `timespan` app; the VPN concepts
(servers = subscriptions, sub-servers = nodes; later, the sing-box core) follow
`singbox-launcher`.

## Prerequisites

- **Go** 1.25+
- **Node.js** 18+ and npm
- **Wails v3 CLI**: `go install github.com/wailsapp/wails/v3/cmd/wails3@latest`
  (ensure `~/go/bin` is on your `PATH`)

## Install (macOS)

Release builds are ad-hoc signed but not notarized, so macOS blocks the first
launch. Allow it through **System Settings**:

1. Double-click `freesurf.app` — macOS refuses to open it (*"cannot be opened
   because the developer cannot be verified"*).
2. Open **System Settings → Privacy & Security**.
3. Scroll to the **Security** section — you'll see *"FreeSurf was blocked to
   protect your Mac."* Click **Open Anyway**.
4. Confirm with your password / Touch ID, then click **Open** in the final dialog.

The **Open Anyway** button appears only after you've tried (and failed) to open the
app once, and it stays available for about an hour.

## Development

```sh
cd frontend && npm install && cd ..        # first time only
wails3 dev -config ./build/config.yml      # hot-reload Go + Vue
```

## Build

```sh
wails3 build
```

## Regenerate JS bindings

After changing exported methods on `App` in `app.go`:

```sh
wails3 generate bindings -d frontend/bindings ./...
```

## Data

State is stored in SQLite at:

| Platform | Path                                                  |
|----------|-------------------------------------------------------|
| macOS    | `~/Library/Application Support/FreeSurf/freesurf.db`  |
| Linux    | `~/.config/FreeSurf/freesurf.db`                      |
| Windows  | `%APPDATA%\FreeSurf\freesurf.db`                      |

## Roadmap

1. ~~**Two-core engine**~~ - done (download/pin Xray + sing-box, generate configs,
   validate, run with privileges, real connect/disconnect).
2. ~~**Windows TUN**~~ - done (native Go service + Wintun). **Linux TUN** is next -
   privilege elevation (systemd unit / pkexec, pure Go); currently returns a clear
   "not yet supported" error.
3. ~~**Subscription fetching**~~ - done. Paste a subscription URL or a Happ
   `happ://crypt5/…` deep link (decrypted locally, then fetched with the Happ
   `User-Agent` + a stable `X-Hwid`); servers are imported as collapsible nodes.
4. **More protocols** - decode VMess / Trojan / Shadowsocks / Hysteria2 / TUIC /
   WireGuard (currently VLESS only) and Happ crypt1–4 links (crypt5 only so far).

## License

MIT - see [LICENSE](LICENSE).
