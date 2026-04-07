# Installation Guide 📥

Hướng dẫn cài đặt **xtpro** trên các nền tảng phổ biến.

## Build từ source

Yêu cầu: Go (theo `src/backend/go.mod`)

```bash
./build-all.sh
```

Artifacts được tạo trong:

- `bin/client/xtpro-<os>-<arch>` (Windows có `.exe`)
- `bin/server/xtpro-server-<os>-<arch>` (Windows có `.exe`)

## Chạy nhanh

### Windows

```powershell
.\bin\client\xtpro-windows-amd64.exe --proto http 3000
.\bin\server\xtpro-server-windows-amd64.exe
```

### Linux/macOS

```bash
./bin/client/xtpro-linux-amd64 --proto http 3000
./bin/server/xtpro-server-linux-amd64
```

