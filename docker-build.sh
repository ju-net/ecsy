#!/bin/bash

# Build binaries using Docker  
docker build --target builder -t ecsy-builder .

# Create dist directory
mkdir -p dist

# Extract binaries from builder stage
docker create --name ecsy-temp ecsy-builder
docker cp ecsy-temp:/build/dist/. dist/
docker rm ecsy-temp

echo "Binaries created in dist/"
ls -la dist/