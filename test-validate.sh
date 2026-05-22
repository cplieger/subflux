#!/bin/bash
# Test the validate-path endpoint

# Create account if needed
curl -s -X POST -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"testtesttesttest"}' \
  http://localhost:8374/api/auth/setup

# Login and capture cookie
RESP=$(curl -s -c /tmp/subflux-cookie.txt -X POST \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"testtesttesttest"}' \
  http://localhost:8374/api/auth/login)
echo "Login: $RESP"

# Test valid path
echo "--- Testing /media ---"
curl -s -b /tmp/subflux-cookie.txt -X POST \
  -H "Content-Type: application/json" \
  -d '{"path":"/media"}' \
  http://localhost:8374/api/config/validate-path
echo ""

# Test invalid path
echo "--- Testing /sdfjhsdgf ---"
curl -s -b /tmp/subflux-cookie.txt -X POST \
  -H "Content-Type: application/json" \
  -d '{"path":"/sdfjhsdgf"}' \
  http://localhost:8374/api/config/validate-path
echo ""

# Test /config (exists in container)
echo "--- Testing /config ---"
curl -s -b /tmp/subflux-cookie.txt -X POST \
  -H "Content-Type: application/json" \
  -d '{"path":"/config"}' \
  http://localhost:8374/api/config/validate-path
echo ""

rm -f /tmp/subflux-cookie.txt
