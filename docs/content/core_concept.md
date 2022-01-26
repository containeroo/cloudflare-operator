# core concept

The goal of cloudflare-operator is to manage Cloudflare DNS Records inside of Kubernetes.

## preamble Cloudflare DNS Record

A Cloudflare DNS Record has the following fields:

### Type

Cloudflare can handle multiple types. At the moment cloudflare-operator only can handle the types `A` and `CNAME`.

**A**

A `A` record must point to a valid IPv4 address, eg `172.4.20.69`.

!!! info "cloudflare-operator use case"
    Let the cloudflare-operator create a `A` record for your root domain, eg. `example.com`.

**CNAME***

A `CNAME` record must point to a valid domain, eg `example.com`.
Cloudflare has the ability to point a `CNAME` record to a `A` record. `Proxy Status` and `TTL` of the `CNAME` record will be passed to the `A` record.

!!! info "cloudflare-operator use case"
    Let the cloudflare-operator create for all your subdomains `CNAME` records that point to your `A` record root domain.  
    This is not mandatory, but so the cloudflare-operator has to change only one DNS record if your external IPv4 address changes.

### Name

Must be a valid domain or subdomain, eg `example.com` or `blog.example.com`

### Content

If of type `A` record must be a valid IPv4 address.  
If of type `CNAME` record must be a valid domain.

!!! info "cloudflare-operator use case"
    Our recommendation is to point all your subdomains to your root domain.

### Proxy Status

Non proxied means all traffic goes directly to your own IP without Cloudflare being a safety net in front.

### TTL

TTL (time to live) is a setting that tells the DNS resolver how long to cache a query before requesting a new one.

### Example

| Type  | Name        | Content       | Proxy Status | TTL  |
| :---- | :---------- | :------------ | :----------- | :--- |
| A     | example.com | 178.4.20.69   | true         | Auto |
| CNAME | www         | example.com   | true         | Auto |
| A     | blog        | 142.251.36.35 | true         | Auto |
| CNAME | vpn         | example.com   | false        | 120  |

`www.example.com` is hosted on your server at home with your external IPv4 address `178.4.20.69`.  
`blog.example.com` is hosted on a cloud provider instance with the IPv4 address `142.251.36.35`

## Account

The `Account` object contains your Cloudflare credentials (eMail & global API key)

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

In the `Zone` object the Cloudflare `Zone ID` is stored. This object will be automatically created by the cloudflare-operator based on the zones available in the Cloudflare account.  
The zones will be automatic populated and updated in the related `Account` object.

The cloudflare-operator checks if a `DNSRecord.spec.name` ends with `Zone.spec.name` to evaluate in which Cloudflare Zone the Cloudflare DNS Records should be created.

The cloudflare-operator will fetch all Cloudflare DNS Records for each `Zone` object.

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

The `IP` Object has two purposes:

1. laziness

You have multiple `DNSRecord` with type `A` who points to the same IP. Link this object with a `DNSRecord` (`spec.ipRef.name`) and the cloudflare-operator will update all `DNSRecord`'s with this IP instead of manually update all `DNSRecord`'s (`spec.content`) or ingress annotations (`metadata.annotations.cf\.containeroo\.ch/content`).

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

IF the `type` is set to `dynamic`, the cloudflare-operator will fetch in the given interval (`spec.interval`) your external IPv4 address. To get the external IPV4 address, the cloudflare-operator will random use a list of services.

example:

```yaml
apiVersion: cf.containeroo.ch/v1alpha1
kind: IP
metadata:
  name: external-ipv4
spec:
  type: dynamic
  address: # will be automatically populated
  interval: 5m
```

If no `dynamicIpSources` is specified, the cloudflare-operator will use a hardcoded set of sources.  
If you prefer other sources, you can add them as a list in `dynamicIpSources`.

example:

```yaml
apiVersion: cf.containeroo.ch/v1alpha1
kind: IP
metadata:
  name: external-ipv4
spec:
  type: dynamic
  address: # will be automatically populated
  dynamicIpSources:
    - https://api.ipify.org
  interval: 5m
```

!!! warning
    The source must return only the external IPV4 address.

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
    To not stress the IP-provider add more than one `dynamicIpSources`. The cloudflare-operator will randomly choose a IP-provider every `interval`

## Ingress

The cloudflare-operator creates a `DNSRecord` for each host specified in a ingress.
You must set as minimum the annotation `cf.containeroo.ch/content` or `cf.containeroo.ch/ip-ref`.
To skip the creation of a `DNSRecord`, add the annotation `cf.containeroo.ch/skip=true`.

The following annotation are possible:

| annotation                   | value                | description                                                                                                     |
| :--------------------------- | :------------------- | :-------------------------------------------------------------------------------------------------------------- |
| `cf.containeroo.ch/content`  | Ip address or domain | Ip address or domain to set as Cloudflare DNS record content                                                    |
| `cf.containeroo.ch/ttl`      | `1` or `60`-`86400`  | Time to live, in seconds, of the Cloudflare DNS record. Must be between 60 and 86400, or 1 for 'automatic'      |
| `cf.containeroo.ch/type`     | `A` or `CNAME`       | Cloudflare DNS record type                                                                                      |
| `cf.containeroo.ch/interval` | `5m`                 | Interval in which cloudflare-operator will compare Cloudflare DNS Records with cloudflare-operator `DNSRecords` |
| `cf.containeroo.ch/skip`     | `true` or `false`    | Do not create a cloudflare-operator `DNSRecord`                                                                 |

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

Represents the Cloudflare DNS Record within Kubernetes. The cloudflare-operator checks if a `DNSRecord.spec.name` ends with `Zone.spec.name` to evaluate in which Cloudflare Zone the Cloudflare DNS Records should be created.

The `interval` is the interval in witch the cloudflare-operator will fetch the Cloudflare DNS Records and compare them with the cloudflare-operator `DNSRecord` properties (`proxied`, `ttl`, `type`, `content`). If the Cloudflare DNS Record does not match with the `DNSRecord`, the Cloudflare DNS Record will be updated.

If a `DNSRecord` is deleted, the cloudflare-operator will also delete the corresponding Cloudflare DNS Record.

Set `spec.ipRef` to the name of a `IP` Object to automatic update the `content` with the address (`spec.address`) of the linked `IP` object.

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
