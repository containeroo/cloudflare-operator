## Update IP from service.status.loadBalancer.ingress.[0].ip
Add kubectl sidecar container to be able to send requests into kubernetes API.
```yaml
sidecars:
  - name: proxy
    image: bitnami/kubectl
    args: ["proxy","--port=8858"]
```

Add additional permissions for this sidecar to be able to get service resource.

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

Now add IP object

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
