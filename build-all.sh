#!/bin/bash
# xtpro Build Script - Build all platforms
# Usage: ./build-all.sh

set -e

echo "🚀 xtpro Build Script v7.0.0"
echo "================================"
echo ""

# Colors
GREEN='\033[0;32m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Directories
SRC_DIR="src/backend"
BIN_DIR="bin"
CLIENT_DIR="$BIN_DIR/client"
SERVER_DIR="$BIN_DIR/server"

# Clean old builds
echo -e "${BLUE}🧹 Cleaning old builds...${NC}"
rm -rf $CLIENT_DIR $SERVER_DIR
mkdir -p $CLIENT_DIR $SERVER_DIR

# Build info
VERSION="7.0.0"
BUILD_TIME=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
GIT_COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")

# Go to source directory
cd $SRC_DIR

# Embed UI: copy src/frontend into backend embed folder
echo -e "${BLUE}🎨 Preparing embedded UI assets...${NC}"
UI_SRC="../frontend"
UI_DST="internal/uiembed/static"
rm -rf "$UI_DST"
mkdir -p "$UI_DST"
cp -R "$UI_SRC"/* "$UI_DST"/

echo -e "${BLUE}📦 Running go mod tidy...${NC}"
go mod tidy

echo ""
echo -e "${GREEN}🔨 Building Client Binaries${NC}"
echo "================================"

# Client platforms
declare -A CLIENT_PLATFORMS=(
    ["windows/amd64"]="xtpro-windows-amd64.exe"
    ["linux/amd64"]="xtpro-linux-amd64"
    ["linux/arm64"]="xtpro-linux-arm64"
    ["darwin/amd64"]="xtpro-darwin-amd64"
    ["darwin/arm64"]="xtpro-darwin-arm64"
    ["android/arm64"]="xtpro-android-arm64"
)

for platform in "${!CLIENT_PLATFORMS[@]}"; do
    IFS='/' read -r GOOS GOARCH <<< "$platform"
    OUTPUT="${CLIENT_PLATFORMS[$platform]}"
    
    echo -e "${BLUE}  → Building $GOOS/$GOARCH...${NC}"
    
    GOOS=$GOOS GOARCH=$GOARCH go build \
        -ldflags="-s -w -X 'main.Version=$VERSION' -X 'main.BuildTime=$BUILD_TIME' -X 'main.GitCommit=$GIT_COMMIT'" \
        -o "../../$CLIENT_DIR/$OUTPUT" \
        ./cmd/client
    
    echo -e "${GREEN}    ✓ $OUTPUT${NC}"
done

echo ""
echo -e "${GREEN}🔨 Building Server Binaries${NC}"
echo "================================"

# Server platforms
declare -A SERVER_PLATFORMS=(
    ["windows/amd64"]="xtpro-server-windows-amd64.exe"
    ["linux/amd64"]="xtpro-server-linux-amd64"
    ["linux/arm64"]="xtpro-server-linux-arm64"
    ["darwin/amd64"]="xtpro-server-darwin-amd64"
    ["darwin/arm64"]="xtpro-server-darwin-arm64"
)

for platform in "${!SERVER_PLATFORMS[@]}"; do
    IFS='/' read -r GOOS GOARCH <<< "$platform"
    OUTPUT="${SERVER_PLATFORMS[$platform]}"
    
    echo -e "${BLUE}  → Building $GOOS/$GOARCH...${NC}"
    
    GOOS=$GOOS GOARCH=$GOARCH go build \
        -ldflags="-s -w -X 'main.Version=$VERSION' -X 'main.BuildTime=$BUILD_TIME' -X 'main.GitCommit=$GIT_COMMIT'" \
        -o "../../$SERVER_DIR/$OUTPUT" \
        ./cmd/server
    
    echo -e "${GREEN}    ✓ $OUTPUT${NC}"
done

# Back to root
cd ../..

echo ""
echo -e "${BLUE}🔐 Generating checksums...${NC}"
cd $CLIENT_DIR && sha256sum * > ../SHA256SUMS-client.txt && cd ../..
cd $SERVER_DIR && sha256sum * > ../SHA256SUMS-server.txt && cd ../..

echo ""
echo -e "${GREEN}✅ Build completed successfully!${NC}"
echo ""
echo "📊 Build Summary:"
echo "  Version: $VERSION"
echo "  Build Time: $BUILD_TIME"
echo "  Git Commit: $GIT_COMMIT"
echo "  Client Binaries: $(ls $CLIENT_DIR | wc -l)"
echo "  Server Binaries: $(ls $SERVER_DIR | wc -l)"
echo ""
echo "📁 Output directories:"
echo "  Clients: $CLIENT_DIR/"
echo "  Servers: $SERVER_DIR/"
echo "  Checksums: $BIN_DIR/SHA256SUMS-*.txt"
echo ""
