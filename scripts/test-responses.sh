#!/usr/bin/env bash
# Sends one request per case and checks the HTTP status against api.yaml.
# Usage: ./test-responses.sh [BASE_URL]      (default http://localhost:8081)
#        SKIP_500=1 ./test-responses.sh ...  (when fault injection is off)
BASE="${1:-http://localhost:8081}"
PASS=0
FAIL=0
J=(-H 'Content-Type: application/json')

# body <product_id> <weight> [category_id]
body() {
  printf '{"product_id":%s,"sku":"ABC-123-XYZ","manufacturer":"Acme Corporation","category_id":%s,"weight":%s,"some_other_id":789}' \
    "$1" "${3:-456}" "$2"
}

# check <expected_status> <label> <curl args...>
check() {
  local expected="$1" label="$2"
  shift 2
  local tmp code
  tmp=$(mktemp)
  code=$(curl -s -o "$tmp" -w '%{http_code}' "$@")
  if [ "$code" = "$expected" ]; then
    PASS=$((PASS + 1)); mark="PASS"
  else
    FAIL=$((FAIL + 1)); mark="FAIL"
  fi
  printf '%-4s %-45s expected %s, got %s\n' "$mark" "$label" "$expected" "$code"
  if [ -s "$tmp" ]; then printf '     %s\n' "$(cat "$tmp")"; fi
  rm -f "$tmp"
}

echo "Target: $BASE"
echo "== GET /products/{productId} =="
check 200 "GET existing product" "$BASE/products/1"
check 200 "GET via /v1 prefix" "$BASE/v1/products/2"
check 404 "GET product that does not exist" "$BASE/products/999999"
check 404 "GET invalid ID (not an integer)" "$BASE/products/abc"
check 404 "GET invalid ID (below minimum of 1)" "$BASE/products/0"
if [ "$SKIP_500" != "1" ]; then
  check 500 "GET with simulated internal error" -H 'X-Simulate-Error: 500' "$BASE/products/1"
fi

echo "== POST /products/{productId}/details =="
check 204 "POST valid details" -X POST "${J[@]}" -d "$(body 1 1250)" "$BASE/products/1/details"
check 200 "GET shows the update (weight 1250)" "$BASE/products/1"
check 204 "POST weight 0 (minimum is 0)" -X POST "${J[@]}" -d "$(body 2 0)" "$BASE/products/2/details"
check 400 "POST malformed JSON" -X POST "${J[@]}" -d '{"product_id": 1,' "$BASE/products/1/details"
check 400 "POST empty body" -X POST "${J[@]}" "$BASE/products/1/details"
check 400 "POST missing required field (weight)" -X POST "${J[@]}" \
  -d '{"product_id":1,"sku":"A","manufacturer":"M","category_id":1,"some_other_id":1}' "$BASE/products/1/details"
check 400 "POST wrong type (sku is a number)" -X POST "${J[@]}" \
  -d '{"product_id":1,"sku":123,"manufacturer":"M","category_id":1,"weight":1,"some_other_id":1}' "$BASE/products/1/details"
check 400 "POST out of range (category_id 0)" -X POST "${J[@]}" -d "$(body 1 100 0)" "$BASE/products/1/details"
LONG_SKU=$(printf 'A%.0s' {1..101})
check 400 "POST sku too long (101 characters)" -X POST "${J[@]}" \
  -d "{\"product_id\":1,\"sku\":\"$LONG_SKU\",\"manufacturer\":\"M\",\"category_id\":1,\"weight\":1,\"some_other_id\":1}" "$BASE/products/1/details"
check 400 "POST body product_id differs from path" -X POST "${J[@]}" -d "$(body 2 100)" "$BASE/products/1/details"
check 400 "POST invalid path ID" -X POST "${J[@]}" -d "$(body 1 100)" "$BASE/products/abc/details"
check 404 "POST product that does not exist" -X POST "${J[@]}" -d "$(body 999999 100)" "$BASE/products/999999/details"
if [ "$SKIP_500" != "1" ]; then
  check 500 "POST with simulated internal error" -X POST "${J[@]}" -H 'X-Simulate-Error: 500' \
    -d "$(body 1 100)" "$BASE/products/1/details"
fi

echo "== $PASS passed, $FAIL failed =="
[ "$FAIL" -eq 0 ]
