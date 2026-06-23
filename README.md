# FreeSurf

A minimalistic, multi-platform (macOS / Linux / Windows) VPN client.

> **Status: v1 scaffold.** The full UI, data model and local storage are in place.
> The big **Start** button currently toggles a *mock* connection state — the real
> sing-box tunnel engine is the next milestone (see [Roadmap](#roadmap)).

## What works today

- Main window with a large **Start / Stop** button.
- Server list below it. A server (a subscription) collapses to show its child
  nodes; single-node servers render flat.
- Top-right **+** button → dropdown → **Paste from clipboard**: reads the system
  clipboard and imports a subscription URL or one-or-more share URIs into the list.
- Select a node in the list, then connect/disconnect with the big button.
- Everything is persisted in SQLite.

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

1. **sing-box engine** — download & pin the `sing-box` binary, generate
   `config.json`, launch/supervise it as a subprocess (TUN / system proxy), and
   replace the mock connection state with real connect/disconnect.
2. **Subscription fetching & full protocol parsing** — fetch subscription URLs and
   decode VLESS / VMess / Trojan / Shadowsocks / Hysteria2 / TUIC / WireGuard …
   into real nodes (currently a shallow best-effort parser).

## License

MIT — see [LICENSE](LICENSE).
