# Core Concept

The goal of cloudflare-operator is to manage Cloudflare DNS records using Kubernetes objects.

## Preamble Cloudflare DNS Record

A Cloudflare DNS record has the following fields:

### Type

Cloudflare can handle multiple types. At the moment cloudflare-operator only supports `A` and `CNAME` records.

**A**

An `A` record must point to a valid IPv4 address, eg `172.4.20.69`.

!!! info "cloudflare-operator use case"
    Let cloudflare-operator create an `A` record for your root domain, eg. `example.com`.

**CNAME***

A `CNAME` record must point to a valid domain, eg `example.com`.
Cloudflare has the ability to point a `CNAME` record to an `A` record. `Proxy Status` and `TTL` of the `CNAME` record will be passed to the `A` record.

!!! info "cloudflare-operator use case"
    Let cloudflare-operator create `CNAME` records for all your subdomains that point to your `A` record root domain.  
    This is not mandatory, but doing so cloudflare-operator only has to change one DNS record if your external IPv4 address changes.

### Name

Must be a valid domain or subdomain, eg `example.com` or `blog.example.com`

### Content

If of type `A` record must be a valid IPv4 address.  
If of type `CNAME` record must be a valid domain.

!!! info "cloudflare-operator use case"
    Our recommendation is to point all your subdomains to your root domain, eg `example.com`.

### Proxy Status

Not proxied means all traffic goes directly to the `content` (IPv4 address or domain) without Cloudflare being a safety net in front.

### TTL

TTL (time to live) is a setting that tells the DNS resolver how long to cache a query before requesting a new one.

### Example

| Type  | Name        | Content       | Proxy Status | TTL  |
|:------|:------------|:--------------|:-------------|:-----|
| A     | example.com | 178.4.20.69   | true         | Auto |
| CNAME | www         | example.com   | true         | Auto |
| A     | blog        | 142.251.36.35 | true         | Auto |
| CNAME | vpn         | example.com   | false        | 120  |

`www.example.com` is hosted on your server at home with your external IPv4 address `178.4.20.69`.  
`blog.example.com` is hosted on a cloud provider instance with the IPv4 address `142.251.36.35`

## Account

The `Account` object contains your Cloudflare credentials (email & global API key)

example:

```yaml
apiVersion: cf.containeroo.ch/v1alpha1
kind: Account
metadata:
  name: account-sample
spec:
  email: mail@example.com
  globalApiKey:
    secretRef:
      name: global-api-key
      namespace: default
```

## Zone

The `Zone` object stores the zone id. This object will be automatically created by the cloudflare-operator based on the zones available in the Cloudflare account.  
The zones will be automatic populated and updated in the related `Account` object.

cloudflare-operator checks if `DNSRecord.spec.name` ends with `Zone.spec.name` to evaluate in which Cloudflare zone the dns record should be created.

cloudflare-operator will fetch all Cloudflare DNS Records for each `Zone` object and deletes them on Cloudflare if they are not present in Kubernetes.

example:

```yaml
apiVersion: cf.containeroo.ch/v1alpha1
kind: Zone
metadata:
  name: example-com
spec:
  id: abcdef123456
  name: example.com
```

## IP

The `IP` object has two purposes:

1. laziness

Let's say you have multiple `DNSRecords` pointing to the same IP, you can use the `IP` object to avoid repeating the IP address in the `DNSRecord.spec.content` field.  
If you change the `IP` object, cloudflare-operator will automatically update the `DNSRecord.spec.content` fields.

example:

```yaml
apiVersion: cf.containeroo.ch/v1alpha1
kind: IP
metadata:
  name: static-address
spec:
  type: static
  address: 142.251.36.35
```

2. "DynDNS"

If the `type` is set to `dynamic`, cloudflare-operator will fetch your external IPv4 address in a specified interval (`spec.interval`).

example:

```yaml
apiVersion: cf.containeroo.ch/v1alpha1
kind: IP
metadata:
  name: external-ipv4
spec:
  type: dynamic
  interval: 5m
```

If no `dynamicIpSources` are specified, cloudflare-operator will use a hardcoded set of sources.  
If you prefer other sources, you can add them as a list in `dynamicIpSources`.

example:

```yaml
apiVersion: cf.containeroo.ch/v1alpha1
kind: IP
metadata:
  name: external-ipv4
spec:
  type: dynamic
  dynamicIpSources:
    - https://api.ipify.org
  interval: 5m
```

!!! warning
    The source must return only the external IPv4 address.

good:

```bash
curl https://api.ipify.org

# output:
142.251.36.35
```

bad:

```bash
curl "https://api.ipify.org?format=json"

# output:
{"ip":"142.251.36.35"}
```

!!! tip
    To minimize the amount of traffic to each IP source, make sure to add more than one `dynamicIpSources`. cloudflare-operator will randomly choose a source on every `interval`.

## Ingress

cloudflare-operator creates a `DNSRecord` for each host specified in an `Ingress` object.
You must set either the annotation `cf.containeroo.ch/content` or `cf.containeroo.ch/ip-ref`.
To skip the creation of a `DNSRecord`, add the annotation `cf.containeroo.ch/skip=true`.

The following annotations are supported:

| annotation                   | value                  | description                                                                                                     |
|:-----------------------------|:-----------------------|:----------------------------------------------------------------------------------------------------------------|
| `cf.containeroo.ch/content`  | IPv4 address or domain | IPv4 address or domain to set as Cloudflare DNS record content                                                  |
| `cf.containeroo.ch/ttl`      | `1` or `60`-`86400`    | Time to live, in seconds, of the Cloudflare DNS record. Must be between 60 and 86400, or 1 for 'automatic'      |
| `cf.containeroo.ch/type`     | `A` or `CNAME`         | Cloudflare DNS record type                                                                                      |
| `cf.containeroo.ch/interval` | `5m`                   | Interval at which cloudflare-operator will compare Cloudflare DNS records with cloudflare-operator `DNSRecords` |
| `cf.containeroo.ch/skip`     | `true` or `false`      | Do not create a Cloudflare DNS record                                                                           |

example:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  annotations:
    cf.containeroo.ch/content: example.com  # root domain
  name: blog
  namespace: blog
spec:
  rules:
  - host: blog.example.com
    http:
      paths:
      - backend:
          service:
            name: blog
            port:
              name: http
        path: /
        pathType: Prefix
```

## DNSRecord

DNSRecords represent a Cloudflare DNS record within Kubernetes.

`interval` specifies, at which interval cloudflare-operator will fetch the Cloudflare DNS records and compare them with `DNSRecord` object spec (`proxied`, `ttl`, `type`, `content`). If the Cloudflare DNS record does not match the `DNSRecord`, Cloudflare DNS record will be updated.

If a `DNSRecord` is deleted, cloudflare-operator will also delete the corresponding Cloudflare DNS record.

Set `spec.ipRef` to the name of an `IP` object to automatically update the `content` with the address (`spec.address`) of the linked `IP` object.

example:

```yaml
apiVersion: cf.containeroo.ch/v1alpha1
kind: DNSRecord
metadata:
  name: blog-example-com
spec:
  name: blog.example.com
  content: # will be automatically populated
  type: CNAME
  proxied: true
  ttl: 1
  interval: 5m
```
