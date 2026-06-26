# FreeSurf

A minimalistic, multi-platform (macOS / Linux / Windows) VPN client.

> **Status: working tunnel on macOS (VLESS).** The UI, data model, local storage,
> and a real sing-box TUN engine are in place for macOS. Linux/Windows TUN and more
> protocols are next (see [Roadmap](#roadmap)).

## What works today

- Main window with a large **Start / Stop** button (with a *connecting* state).
- Server list below it. A server (a subscription) collapses to show its child
  nodes; single-node servers render flat.
- Top-right **+** button → dropdown → **Paste from clipboard**: reads the system
  clipboard and imports a subscription URL or one-or-more share URIs into the list.
- Select a node, hit **Start**: the app downloads & pins the sing-box core, builds
  a TUN `config.json`, validates it with `sing-box check`, and launches the core
  with elevated privileges (a macOS auth prompt). **Stop** tears it down.
- Everything is persisted in SQLite.

### sing-box engine

- **Core:** the `sing-box-lx` fork is downloaded from GitHub (pinned version) into
  `<data>/bin/sing-box` on first connect; the version is verified before use.
- **Parser:** `vless://` share URIs → sing-box outbound (TLS / Reality / uTLS /
  ALPN / flow / ws・grpc・http(upgrade) transports). VLESS only for now.
- **Config:** full-tunnel TUN inbound (`auto_route`), DNS + route, proxy + direct
  outbounds (sing-box 1.13.x schema).
- **Privileges:** TUN needs root; on macOS the core is started via an `osascript`
  admin prompt (macOS caches the right for ~5 min, so Stop usually won't re-prompt).

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

Release builds are ad-hoc signed but not notarized. After downloading and
unzipping, macOS quarantines the app and refuses to open it with *"can't be
opened because it may be damaged or incomplete"*. Strip the quarantine
attribute once and it will launch:

```sh
xattr -dr com.apple.quarantine freesurf.app
```

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

1. ~~**sing-box engine**~~ - done for macOS (download/pin core, generate TUN
   `config.json`, validate, run with privileges, real connect/disconnect).
2. **Linux & Windows TUN** - privilege elevation (pkexec/sudo on Linux; runas +
   `wintun.dll` on Windows). Currently macOS-only; other platforms return a clear
   "not yet supported" error.
3. ~~**Subscription fetching**~~ - done. Paste a subscription URL or a Happ
   `happ://crypt5/…` deep link (decrypted locally, then fetched with the Happ
   `User-Agent` + a stable `X-Hwid`); servers are imported as collapsible nodes.
4. **More protocols** - decode VMess / Trojan / Shadowsocks / Hysteria2 / TUIC /
   WireGuard (currently VLESS only) and Happ crypt1–4 links (crypt5 only so far).

## License

MIT - see [LICENSE](LICENSE).
