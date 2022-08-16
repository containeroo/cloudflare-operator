#!/usr/bin/env bash

set -o errexit

command -v jq >/dev/null 2>&1 || { echo >&2 "'yq' is not installed"; exit 1; }

[[ -z "${CLOUDFLARE_ZONE_ID}" ]] && { echo >&2 "CLOUDFLARE_ZONE_ID is not set!"; exit 1; }
[[ -z "${CLOUDFLARE_BEARER_TOKEN}" ]] && { echo >&2 "CLOUDFLARE_BEARER_TOKEN is not set!"; exit 1; }

NAMESPACE=${NAMESPACE:=cloudflare-operator}
INTERVAL="${INTERVAL:=5m}"

ALLOWED_RECORD_TYPES=(A AAAA CNAME)

cf_response=$(curl \
              -sS \
              -X GET "https://api.cloudflare.com/client/v4/zones/${CLOUDFLARE_ZONE_ID}/dns_records" \
              -H "Content-Type:application/json" \
              -H "Authorization: Bearer ${CLOUDFLARE_BEARER_TOKEN}")

[ -n "$(jq '.errors[]?' <<< "${cf_response}")" ] && \
  echo "ERROR: cannot get Cloudflare DNS records. $(jq '.errors[]' <<< "${cf_response}")" && \
  exit 1

readarray -t dns_records < <(jq -c '.result[]' <<< "${cf_response}")

for dns_record in "${dns_records[@]}"; do
  type=$(jq -r .type <<< "$dns_record")

  [[ ! " ${ALLOWED_RECORD_TYPES[*]} " =~ " ${type} " ]] && \
    continue

  continue
  name=$(jq -r .name <<< "$dns_record")
  content=$(jq -r .content <<< "$dns_record")
  proxyied=$(jq -r .proxied <<< "$dns_record")
  ttl=$(jq -r .ttl <<< "$dns_record")

  echo """---
apiVersion: cf.containeroo.ch/v1beta1
kind: DNSRecord
metadata:
  name: ${name//./-}
  namespace: ${NAMESPACE}
spec:
  content: ${content}
  name: ${name}
  type: ${type}
  proxied: ${proxyied}
  ttl: ${ttl}
  interval: ${INTERVAL}
"""

done
