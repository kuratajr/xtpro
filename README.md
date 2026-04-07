# xtpro

Nền tảng tunneling đa giao thức (HTTP/TCP/UDP) + File sharing (WebDAV), gồm **client** và **server** viết bằng Go.

## Quick start

### Build

```bash
./build-all.sh
```

### Client

```bash
./bin/client/xtpro-linux-amd64 --proto http 3000
./bin/client/xtpro-linux-amd64 --proto tcp 22
./bin/client/xtpro-linux-amd64 --proto udp 19132
```

### Server

```bash
./bin/server/xtpro-server-linux-amd64
./bin/server/xtpro-server-linux-amd64 -port 9000
```

### Docker

```bash
docker compose up --build
```

## Artifacts (theo `build-all.sh`)

- **Client**: `xtpro-<os>-<arch>` (Windows có `.exe`)
- **Server**: `xtpro-server-<os>-<arch>` (Windows có `.exe`)


