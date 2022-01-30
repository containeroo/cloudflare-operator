# Operation

## Account

To view all `Account` objects use the command:

```bash
kubectl get accounts.cf.containeroo.ch
```

output:

```console
NAME   EMAIL                  PHASE
main   admin@example.com      Active
```

## Zone

To view all `Zone` objects use the command:

```bash
kubectl get zones.cf.containeroo.ch
```

output:

```console
NAME         ZONE NAME    ID                                 PHASE
example-com  example.com  69                                 Active
other-com    other.com    420                                Active
```

## IP

To view all `IP` objects use the command:

```bash
kubectl get ips.cf.containeroo.ch
```

output:

```console
NAME            ADDRESS         TYPE      PHASE
external-ipv4   178.4.20.69     dynamic   Ready
static-address  142.251.36.35   static    Ready
```

## DNSRecord

To view all `DNSRecord` objects use the command:

```bash
kubectl get dnsrecords.cf.containeroo.ch --all-namespaces
```

output:

```console
NAMESPACE             NAME                   RECORD NAME            TYPE    CONTENT         PROXIED   TTL   STATUS
blog                  blog-example-com       blog.example.com          A   142.251.36.35    true      1     Created
www                   www-exmaple-com        www.example.com       CNAME   example.com      true      1     Created
cloudflare-operator   vpn-example-com        vpn.example.com       CNAME   example.com      false     120   Created
cloudflare-operator   example-com            example.com               A   178.4.20.69      true      1     Created
```

## Troubleshooting

describe here possible errors
