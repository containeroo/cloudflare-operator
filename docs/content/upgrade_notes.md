# Upgrade Notes

## From v0.1.3 to v0.2.0

Properties of the cloudflare-operator CRD `ips.cf.containeroo.ch` has BREAKING changes.  
You have to delete all `IP` objects before updating and re-create it according the new specification:

```bash
kubectl delete ip --all
```

New `ip.containeroo.ch` CRD specification can be found [here](/docs/content/core_concept.md#IP).

If you have create a `IP` object to sync your external IPv4 address and not set `dynamicIPSources`, you have to delete your object and re-create a new `IP` object.

Example:

```bash
kubectl apply -f - << EOF
apiVersion: cf.containeroo.ch/v1beta1
kind: IP
metadata:
  name: dynamic-external-ipv4-address
spec:
  type: dynamic
  interval: 5m
  sources:
    - url: https://ifconfig.me/ip
    - url: https://ipecho.net/plain
    - url: https://myip.is/ip/
    - url: https://checkip.amazonaws.com
    - url: https://api.ipify.org
EOF
```

## From v0.0.x to v0.1.0

The apiVersion of the cloudflare-operator CRDs changed from `cf.containeroo.ch/v1alpha1` to `cf.containeroo.ch/v1beta1`.  
Please update your CRDs to `cf.containeroo.ch/v1beta1` before upgrading to `v0.1.0`:

```shell
kubectl apply --server-side -f https://github.com/containeroo/cloudflare-operator/releases/download/v0.1.0/crds.yaml
```
