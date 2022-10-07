# Fetch IP From Kubernetes Object

To fetch IP addresses from other Kubernetes objects, such as services or Istio gateways, you can add a kubectl sidecar to the operator pod.

Enable the kubectl sidecar in your helm values:

```yaml
sidecars:
  - name: proxy
    image: bitnami/kubectl
    args: ["proxy","--port=8858"]
```

Also in the helm values, add additional permissions for this sidecar to be able to get service resource.

```yaml
clusterRole:
  extraRules:
  - apiGroups:
    - ""
    resources:
    - services
    verbs:
    - get
    - list
```

Now add the IP object:

```yaml
---
apiVersion: cf.containeroo.ch/v1beta1
kind: IP
metadata:
  name: ingress-ip
spec:
  type: dynamic
  ipSources:
    - url: localhost:8858/api/v1/namespaces/ingress-nginx/services/ingress-nginx
      responseJSONPath: '{.status.loadBalancer.ingress.[0].ip}'
  interval: 5m
```
