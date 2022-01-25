# core concept

The goal of cloudflare-operator is to manage Cloudflare DNS Records inside of Kubernetes.

## Account

The `Account` object contains your Cloudflare credentials (eMail & global API key)

## Zone

In the `Zone` object the Cloudflare `Zone ID` is stored. The cloudflare-operator will automatically create for each Cloudflare Zone a `Zone` object.
The zones will be automatic populated and updated in the related `Account` object.

The cloudflare-operator checks if a `DNSRecord.spec.name` ends with `Zone.spec.name` to know where to create Cloudflare DNS Records.

The cloudflare-operator will fetch all Cloudflare DNS Records for each `Zone` object.

## IP

The `IP` Object has two purposes:

1. laziness

You have multiple `DNSRecord` with type `A` who points to the same IP. Link this object with a `DNSRecord` (`spec.ipRef.name`) and the cloudflare-operator will update all `DNSRecord`'s with this IP instead of manually update all `DNSRecord`'s (`spec.content`) or ingress annotations (`metadata.annotations.cf\.containeroo\.ch/content`).

2. "DynDNS"

IF the `type` is set to `dynamic`, the cloudflare-operator will fetch in the given interval ('spec.interval') your external IPv4 address. To get the external IPV4 Address, the cloudflare-operator will random use a list of services.

If no `dynamicIpSources` is specified, the cloudflare-operator will use a hardcoded set of sources.  
If you prefer other sources, you can add them as a list in `dynamicIpSources`.
**Attention:** The source must return only the external IPV4 Address.

good:

```bash
curl https://api.ipify.org

# output:
178.4.20.69
```

bad:

```bash
curl "https://api.ipify.org?format=json"

# output:
{"ip":"178.4.20.69"}
```

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

## DNSRecord

Represents the Cloudflare DNS Record within Kubernetes. The cloudflare-operator checks if a `DNSRecord.spec.name` ends with `Zone.spec.name` to know where to create Cloudflare DNS Records.

The `interval` is the interval in witch the cloudflare-operator will fetch the Cloudflare DNS Records and compare them with the cloudflare-operator DNSRecord properties (`proxied`, `ttl`, `type`, `content`). If the Cloudflare DNS Record does not match with the DNSRecord, the Cloudflare DNS Record will be updated.

Set `spec.ipRef` to the name of a `IP` Object to automatic update the `content` with the address (`spec.address`) of the linked IP object.
