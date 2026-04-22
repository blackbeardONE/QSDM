#!/bin/bash
# Simple HTTP server to serve the tests directory on port 8000

echo "Serving tests directory at http://localhost:8000"
cd tests
python3 -m http.server 8000
