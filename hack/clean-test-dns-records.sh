#!/usr/bin/env bash
set -euo pipefail

if [[ -z "${CF_API_TOKEN:-}" ]]; then
  echo "❌ CF_API_TOKEN is not set"
  exit 1
fi

if [[ -z "${CF_ZONE_ID:-}" ]]; then
  echo "❌ CF_ZONE_ID is not set"
  exit 1
fi

echo "🔍 Fetching DNS records for zone ${CF_ZONE_ID}..."

records=$(curl -s -X GET "https://api.cloudflare.com/client/v4/zones/${CF_ZONE_ID}/dns_records?per_page=1000" \
  -H "Authorization: Bearer ${CF_API_TOKEN}" \
  -H "Content-Type: application/json")

record_ids=$(echo "$records" | jq -r '.result[].id')

if [[ -z "$record_ids" ]]; then
  echo "✅ No DNS records found to delete."
  exit 0
fi

echo "⚠️ Deleting DNS records..."
for rid in $record_ids; do
  name=$(echo "$records" | jq -r ".result[] | select(.id==\"$rid\") | .name")
  type=$(echo "$records" | jq -r ".result[] | select(.id==\"$rid\") | .type")

  echo "  ➜ Deleting [$type] $name"
  curl -s -X DELETE "https://api.cloudflare.com/client/v4/zones/${CF_ZONE_ID}/dns_records/${rid}" \
    -H "Authorization: Bearer ${CF_API_TOKEN}" \
    -H "Content-Type: application/json" >/dev/null
done

echo "✅ All DNS records deleted."
