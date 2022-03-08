# Upgrade Notes

## From v0.0.x to v0.1.0

The apiVersion of the cloudflare-operator CRDs changed from `cf.containeroo.ch/v1alpha1` to `cf.containeroo.ch/v1beta1`.  
Please update your CRDs to `cf.containeroo.ch/v1beta1` before upgrading to `v0.1.0`:

```shell
kubectl apply --server-side -f https://github.com/containeroo/cloudflare-operator/releases/download/v0.1.0/crds.yaml
```
