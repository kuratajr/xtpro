#!/bin/bash
# xtpro Server Start Script

# Màu sắc
GREEN='\033[0;32m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo -e "${BLUE}🚀 Starting xtpro Server...${NC}"

# Kiểm tra file binary có tồn tại không
if [ ! -f "bin/server/xtpro-server-linux-amd64" ]; then
    echo "❌ Error: Server binary not found!"
    echo "Please run ./build-all.sh first"
    exit 1
fi

# Kiểm tra .env file
if [ ! -f ".env" ]; then
    echo "⚠️  Warning: .env file not found, using defaults"
    echo "💡 Tip: Copy .env.server.example to .env and customize"
fi

# Chạy server
echo -e "${GREEN}✅ Server binary found${NC}"
echo ""
./bin/server/xtpro-server-linux-amd64
