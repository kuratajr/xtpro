#!/bin/bash
# xtpro Build Script for Linux/macOS
# Usage: ./scripts/build.sh

set -e

# Switch to project root
cd "$(dirname "$0")/.."

# Colors
GREEN='\033[0;32m'
BLUE='\033[0;34m'
NC='\033[0m'

echo -e "${BLUE}🚀 Building xtpro Client & Server...${NC}"

# Ensure bin directory exists
mkdir -p bin/client bin/server

# Build Client
echo -e "${BLUE}📦 Building Client...${NC}"
cd src/backend
go build -ldflags="-s -w" -o ../../bin/client/xtpro ./cmd/client/
echo -e "${GREEN}✅ Client built: bin/client/xtpro${NC}"

# Build Server
echo -e "${BLUE}📦 Building Server...${NC}"
go build -ldflags="-s -w" -o ../../bin/server/xtpro-server ./cmd/server/
echo -e "${GREEN}✅ Server built: bin/server/xtpro-server${NC}"

cd ../..

echo -e "${GREEN}🎉 All builds completed successfully!${NC}"
