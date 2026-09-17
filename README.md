# FreeSurf

FreeSurf is a small VPN client for macOS and Windows. You paste in a subscription or a server link, pick a node and press Start. All of your traffic then goes through the proxy, except for the addresses you choose to send directly.

It currently supports VLESS servers (TLS, Reality, and the tcp, xhttp, ws, grpc and httpupgrade transports). Other protocols are not supported yet.

## Download

Get the latest build from the [Releases](https://github.com/Sergey-pr/free-surf/releases) page:

| Platform                | File                         |
|-------------------------|------------------------------|
| macOS (Apple Silicon)   | `freesurf-macos-arm64.zip`   |
| Windows (64-bit)        | `freesurf-windows-amd64.zip` |

### macOS

The app is signed ad hoc but not notarized, so macOS blocks it the first time you open it. To allow it:

1. Unzip the archive and double-click `freesurf.app`. macOS will say it can't verify the developer.
2. Open System Settings, then Privacy & Security.
3. In the Security section you'll see "FreeSurf was blocked to protect your Mac." Click Open Anyway.
4. Confirm with your password or Touch ID, then click Open.

The Open Anyway button only shows up after a failed launch, and it goes away after about an hour.

### Windows

Unzip the archive and run `freesurf.exe`. If SmartScreen warns you, click More info, then Run anyway.

## Using FreeSurf

### Adding servers

Copy a subscription URL, one or more `vless://` links, or a Happ `happ://crypt5/...` link. In FreeSurf, click + in the top right corner and choose Paste from clipboard.

A subscription shows up as a group you can expand to see its nodes. You can rename a subscription, refresh it by hand, or delete it. FreeSurf also refreshes subscriptions in the background, every 30 minutes by default. You can change the interval in Settings.

### Connecting

Select a node and press Start. Press Stop to disconnect.

The first time you connect, FreeSurf asks for your password on macOS, or shows a UAC prompt on Windows. It needs this once to install a small helper that is allowed to create the VPN network interface. After that, connecting and disconnecting won't prompt you again. You'll see the prompt one more time after an app update.

### Checking servers

Use ping on a node or on a whole subscription to see which servers respond and how fast. FreeSurf checks each node the same way it would connect to it, so a node that answers the ping should also connect.

If a connection fails, open the logs window to see what happened.

### Sending some traffic around the VPN

In Settings, the Bypass VPN field lists what should skip the proxy and go out directly. Write one rule per line. Anything after `#` is a comment.

```
10.0.0.0/8
100.64.0.0/10
172.16.0.0/12
192.168.0.0/16
169.254.0.0/16
224.0.0.0/4
255.255.255.255
geoip:ru
geoip:private
domain:ru
domain:su
domain:рф
geosite:category-ru
geosite:reddit
domain:nalog.ru
domain:nalog.gov.ru
domain:gosuslugi.ru
```

Supported geo lists are `geoip:ru`, `geoip:private`, `geosite:category-ru` and `geosite:reddit`. They ship inside the app, so they only update when you update FreeSurf.

FreeSurf checks the list when you click Save and tells you which line is wrong. Changes take effect the next time you connect.

Domains that match a bypass rule are also resolved by your regular DNS, so you get the addresses a direct connection should use. Domain rules may not catch apps that use their own encrypted DNS. For those, add the IP ranges as well.

## Your data

FreeSurf keeps servers and settings on your computer only:

| Platform | Location                                             |
|----------|------------------------------------------------------|
| macOS    | `~/Library/Application Support/FreeSurf/`            |
| Windows  | `%APPDATA%\FreeSurf\`                                |

## Uninstalling

Delete the app and the data folder above. The helper is installed separately, so remove it too.

On macOS, run this in Terminal:

```sh
sudo launchctl bootout system /Library/LaunchDaemons/com.freesurf.helper.plist
sudo rm -f /Library/LaunchDaemons/com.freesurf.helper.plist
sudo rm -rf "/Library/Application Support/FreeSurf"
```

On Windows, run this in an administrator Command Prompt:

```bat
sc stop FreeSurfTunnel
sc delete FreeSurfTunnel
rmdir /s /q "%ProgramData%\FreeSurf"
```

## Building from source

You need Go 1.25 or newer, Node.js 20.19+ or 22.12+, and the Wails v3 CLI:

```sh
go install github.com/wailsapp/wails/v3/cmd/wails3@latest
```

Then:

```sh
cd frontend && npm install && cd ..
wails3 build                                # release build
wails3 dev -config ./build/config.yml       # development with hot reload
```

The build downloads the pinned sing-box and Xray binaries and the geo lists, checks their SHA-256 digests and embeds them into the app. A plain `go build` compiles without them, but that binary can't connect.

## How it works

Each connection runs two programs. Xray runs as your user and talks to the VPN server. sing-box runs through the helper with administrator rights, owns the virtual network interface and passes traffic to Xray. Bypass rules are applied in sing-box.

The helper only ever runs its own copies of these programs, stored in a folder that only administrators can write to. The app talks to it through a single request file. The helper checks that file strictly and builds the sing-box configuration itself.

## License

MIT. See [LICENSE](LICENSE).
