# HTTP Tunneling 🌐

HTTP mode giúp bạn public web app (localhost) ra Internet.

## Ví dụ

```bash
./bin/client/xtpro-linux-amd64 --proto http 3000
```

Nếu server có cấu hình `HTTP_DOMAIN`, bạn sẽ nhận được Public URL dạng `https://<subdomain>.<domain>`.

