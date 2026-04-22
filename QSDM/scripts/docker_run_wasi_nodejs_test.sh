#!/bin/bash
# Script to build and run the Node.js WASI test environment Docker container

IMAGE_NAME=qsdm-wasi-nodejs-test

# Build the Docker image using patched Dockerfile
docker build -t $IMAGE_NAME -f Dockerfile-wasi-nodejs-test-patched .

# Run the container interactively
docker run --rm -it $IMAGE_NAME
