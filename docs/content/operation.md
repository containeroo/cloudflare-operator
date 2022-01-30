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

Usually, cloudflare-operator will store errors in the corresponding CRD in the `status.message` field and set the object phase (`stattus.phase`) to `Failed`.

### example

create a new invalid `DNSRecord`:

```bash
kubectl apply -f - << EOF
apiVersion: cf.containeroo.ch/v1alpha1
kind: DNSRecord
metadata:
  name: www-example-com
  namespace: cloudflare-operator
spec:
  name: www.example.com
  type: CNAME
  proxied: true
  ttl: 1
  interval: 5m
EOF
```

!!! info
    The problem here is, that the `type` is set to `CNAME`, but no `content` is set.

list `DNSRecords`:

```bash
kubectl get dnsrecords --namespace cloudflare-operator www-example-com
```

output:

```console
NAME                   RECORD NAME            TYPE    CONTENT         PROXIED   TTL   STATUS
blog-example-com       blog.example.com          A   142.251.36.35    true      1     Created
www-exmaple-com        www.example.com       CNAME                    true      1     Failed
vpn-example-com        vpn.example.com       CNAME   example.com      false     120   Created
example-com            example.com               A   178.4.20.69      true      1     Created
```

As you can see, the `STATUS` is set to `Failed`.

Show the new created `DNSRecord` to console:

```bash
kubectl get dnsrecords --namespace cloudflare-operator www-example-com -oyaml
```

output:

```yaml
apiVersion: cf.containeroo.ch/v1alpha1
kind: DNSRecord
metadata:
  annotations:
    kubectl.kubernetes.io/last-applied-configuration: |
      {"apiVersion":"cf.containeroo.ch/v1alpha1","kind":"DNSRecord","metadata":{"annotations":{},"name":"www-example-com","namespace":"cloudflare-operator"},"spec":{"content":null,"interval":"5m","name":"www.example.com","proxied":true,"ttl":1,"type":"CNAME"}}
  creationTimestamp: "2022-01-30T18:55:09Z"
  generation: 1
  name: www-example-com
  namespace: cloudflare-operator
  resourceVersion: "23470117"
  uid: a832b157-7482-4d0a-be85-89b0748306a3
spec:
  interval: 5m
  name: www.example.com
  proxied: true
  ttl: 1
  type: CNAME
status:
  message: DNSRecord content is empty. Either content or ipRef must be set
  phase: Failed
  recordId: ""
```

In the `status.message` you can see the error. The `Phase` is also set to `Failed`.
