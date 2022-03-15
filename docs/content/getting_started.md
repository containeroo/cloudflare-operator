# Getting Started

This tutorial shows you how to start using cloudflare-operator.

## Preparation

Create a secret with your Cloudflare global API Key. The key containing the API key must be named `apiKey`.

```bash hl_lines="9"
kubectl apply -f - << EOF
apiVersion: v1
kind: Secret
type: Opaque
metadata:
  name: cloudflare-global-api-key
  namespace: cloudflare-operator
stringData:
  apiKey: 1234
EOF
```

Create an `Account` object:

```bash
kubectl apply -f - << EOF
apiVersion: cf.containeroo.ch/v1beta1
kind: Account
metadata:
  name: account-sample
spec:
  email: mail@example.com
  globalAPIKey:
    secretRef:
      name: cloudflare-global-api-key
      namespace: cloudflare-operator
EOF
```

Create an `IP` object for your root domain to automatically update your external IPv4 address:

```bash
kubectl apply -f - << EOF
apiVersion: cf.containeroo.ch/v1beta1
kind: IP
metadata:
  name: dynamic-external-ipv4-address
spec:
  type: dynamic
  interval: 5m
  ipSources:
    - url: https://ifconfig.me/ip
    - url: https://ipecho.net/plain
    - url: https://myip.is/ip/
    - url: https://checkip.amazonaws.com
    - url: https://api.ipify.org
EOF
```

Create a `DNSRecord` with type `A` for your root domain:

```bash
kubectl apply -f - << EOF
apiVersion: cf.containeroo.ch/v1beta1
kind: DNSRecord
metadata:
  name: root-domain
  namespace: cloudflare-operator
spec:
  name: example.com
  type: A
  ipRef:
    name: dynamic-external-ipv4-address
  proxied: true
  ttl: 1
  interval: 5m
EOF
```

Annotate all ingresses with your root domain, so cloudflare-operator can create `DNSRecords` for all hosts specified in all ingresses:

```bash hl_lines="4"
kubectl annotate ingress \
      --all-namespaces \
      --all \
      "cf.containeroo.ch/content=example.com"
```

!!! info
    If you do not want to expose some ingresses, delete the annotation `kubectl annotate ingress --namespace <NAMESPACE> <INGRESS-NAME> cf.containeroo.ch/content-` and add the annotation `kubectl annotate ingress --namespace <NAMESPACE> <INGRESS-NAME> cf.containeroo.ch/ignore=true` to skip the creation of the `DNSRecord`.

## Additional DNSRecord

### VPN

Let's say you have a vpn-server on a Raspberry Pi. In order to manage the Cloudflare DNS record for you, create a `DNSRecord` object:

```bash
kubectl apply -f - << EOF
apiVersion: cf.containeroo.ch/v1beta1
kind: DNSRecord
metadata:
  name: vpn
  namespace: cloudflare-operator
spec:
  name: vpn.example.com
  type: A
  ipRef:
    name: dynamic-external-ipv4-address
  proxied: false
  ttl: 120
  interval: 5m
EOF
```

Because you linked the `DNSRecord` to your IP object (`spec.ipRef.name`), cloudflare-operator will also update the content of the Cloudflare DNS record for you, if your external IPv4 address changes.

Set the `ttl` to `120` to shorten waiting time if your external IPv4 address has changed.
Set `proxied` to `false` because Cloudflare cannot proxy vpn traffic.

### External Service

Let's say you have an external website `blog.example.com` hosted on a cloud VPC and your external cloud instance IP address is `178.4.20.69`.

```bash
kubectl apply -f - << EOF
apiVersion: cf.containeroo.ch/v1beta1
kind: DNSRecord
metadata:
  name: blog
  namespace: cloudflare-operator
spec:
  name: blob.example.com
  content: 178.4.20.69
  type: A
  proxied: true
  ttl: 1
  interval: 5m
EOF
```

Now your blog will be routed through Cloudflare.

!!! note
    Do not forget to change the `content` of the `DNSRecord` if the external IPv4 address of your cloud instance has changed!

!!! tip "Bonus tip"
    If you have multiple cloud services accessible with the same IP, you can also create an `IP` object and link this with `ipRef.name`, so you only have to change the IP address of your cloud instance once in the `IP` object.

If your cloud provider has an API returning the public IPv4 address of your instance, you can also create an `IP` object with type `dynamic` and reference it in the `DNSRecord`.

Create a `secret` with your provider API credentials:

```bash
kubectl apply -f - << EOF
apiVersion: v1
kind: Secret
metadata:
  name: hetzner-bearer-token
  namespace: default
type: Opaque
stringData:
  Authorization: Bearer TOKEN123
EOF
```

Create an `IP` object with a reference to the `secret` created above:

```bash hl_lines="12"
kubectl apply -f - << EOF
apiVersion: cf.containeroo.ch/v1beta1
kind: IP
metadata:
  name: hetzner-ipv4
spec:
  type: dynamic
  ipSources:
    - url: https://api.hetzner.cloud/v1/servers
      responseJSONPath: '{.servers[0].public_net.ipv4.ip}'
      requestHeadersSecretRef:
        name: hetzner-bearer-token
        namespace: default
  interval: 5m
EOF
```

Create a `DNSRecord` with a reference to the `IP` object created above:

```bash
kubectl apply -f - << EOF
apiVersion: cf.containeroo.ch/v1beta1
kind: DNSRecord
metadata:
  name: blog
  namespace: cloudflare-operator
spec:
  name: blob.example.com
  ipRef:
    name: hetzner-ipv4
  type: A
  proxied: true
  ttl: 1
  interval: 5m
EOF
```
